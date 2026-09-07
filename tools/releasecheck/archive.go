package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
)

type artifactLimits struct {
	maxCompressedBytes int64
	maxExpandedBytes   int64
	maxMemberBytes     int64
	maxMembers         int
}

var defaultArtifactLimits = artifactLimits{
	maxCompressedBytes: 128 * 1024 * 1024,
	maxExpandedBytes:   256 * 1024 * 1024,
	maxMemberBytes:     128 * 1024 * 1024,
	maxMembers:         16,
}

type archiveMember struct {
	mode fs.FileMode
	data []byte
}

type releaseArchive struct {
	members map[string]archiveMember
	sha256  string
}

var errExpandedArchiveTooLarge = errors.New("expanded archive exceeds limit")

func readReleaseArchive(archivePath, binaryName string, limits artifactLimits) (releaseArchive, error) {
	contents, err := readBoundedRegularFile(archivePath, limits.maxCompressedBytes)
	if err != nil {
		return releaseArchive{}, fmt.Errorf("read archive %s: %w", filepath.Base(archivePath), err)
	}
	if len(contents) == 0 {
		return releaseArchive{}, fmt.Errorf("inspect archive %s: invalid compressed file", filepath.Base(archivePath))
	}

	var members map[string]archiveMember
	switch {
	case strings.HasSuffix(archivePath, ".tar.gz"):
		members, err = readTarGzip(contents, limits)
	case strings.HasSuffix(archivePath, ".zip"):
		members, err = readZIP(contents, limits)
	default:
		return releaseArchive{}, fmt.Errorf("inspect archive %s: unsupported format", filepath.Base(archivePath))
	}
	if err != nil {
		return releaseArchive{}, fmt.Errorf("inspect archive %s: %w", filepath.Base(archivePath), err)
	}
	if err := validateReleaseMembers(members, binaryName); err != nil {
		return releaseArchive{}, fmt.Errorf("inspect archive %s: %w", filepath.Base(archivePath), err)
	}
	return releaseArchive{members: members, sha256: sha256Hex(contents)}, nil
}

func readTarGzip(contents []byte, limits artifactLimits) (map[string]archiveMember, error) {
	compressed := bytes.NewReader(contents)
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return nil, errors.New("invalid gzip header")
	}
	gz.Multistream(false)
	expanded := &boundedStream{reader: gz, max: limits.maxExpandedBytes}
	tape := tar.NewReader(expanded)
	members := make(map[string]archiveMember)
	for {
		header, err := tape.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar header: %w", err)
		}
		if len(members) >= limits.maxMembers {
			return nil, errors.New("archive contains too many members")
		}
		if header.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("member %q is not a regular file", safeArchiveName(header.Name))
		}
		if err := validateRootMemberName(header.Name); err != nil {
			return nil, err
		}
		if _, exists := members[header.Name]; exists {
			return nil, fmt.Errorf("member %q is duplicated", header.Name)
		}
		data, err := readMember(tape, header.Size, limits.maxMemberBytes)
		if err != nil {
			return nil, fmt.Errorf("read member %q: %w", header.Name, err)
		}
		members[header.Name] = archiveMember{mode: header.FileInfo().Mode(), data: data}
	}

	buffer := make([]byte, 32*1024)
	for {
		n, err := expanded.Read(buffer)
		if n > 0 && !allZero(buffer[:n]) {
			return nil, errors.New("tar has nonpadding trailing data")
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("verify gzip trailer: %w", err)
		}
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("close gzip stream: %w", err)
	}
	if compressed.Len() != 0 {
		return nil, errors.New("gzip has trailing compressed data")
	}
	return members, nil
}

func readZIP(contents []byte, limits artifactLimits) (map[string]archiveMember, error) {
	reader, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
	if err != nil {
		return nil, errors.New("invalid ZIP structure")
	}
	if len(reader.File) > limits.maxMembers {
		return nil, errors.New("archive contains too many members")
	}
	members := make(map[string]archiveMember, len(reader.File))
	var expanded uint64
	for _, file := range reader.File {
		if err := validateZIPLocalHeader(contents, file); err != nil {
			return nil, err
		}
		if err := validateRootMemberName(file.Name); err != nil {
			return nil, err
		}
		if !file.Mode().IsRegular() {
			return nil, fmt.Errorf("member %q is not a regular file", file.Name)
		}
		if _, exists := members[file.Name]; exists {
			return nil, fmt.Errorf("member %q is duplicated", file.Name)
		}
		if limits.maxMemberBytes < 0 || limits.maxExpandedBytes < 0 ||
			file.UncompressedSize64 > uint64(limits.maxMemberBytes) ||
			file.UncompressedSize64 > uint64(limits.maxExpandedBytes) ||
			expanded > uint64(limits.maxExpandedBytes)-file.UncompressedSize64 {
			return nil, errExpandedArchiveTooLarge
		}
		expanded += file.UncompressedSize64
		memberReader, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open member %q: %w", file.Name, err)
		}
		data, readErr := readMember(memberReader, int64(file.UncompressedSize64), limits.maxMemberBytes)
		closeErr := memberReader.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read member %q: %w", file.Name, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close member %q: %w", file.Name, closeErr)
		}
		members[file.Name] = archiveMember{mode: file.Mode(), data: data}
	}
	return members, nil
}

func validateZIPLocalHeader(contents []byte, file *zip.File) error {
	dataOffset, err := file.DataOffset()
	if err != nil || dataOffset < 30 || dataOffset > int64(len(contents)) {
		return errors.New("ZIP member has an invalid local header")
	}
	start := dataOffset - 30 - 2*int64(^uint16(0))
	if start < 0 {
		start = 0
	}
	// DataOffset is derived from the local header, but archive/zip exposes only
	// the central-directory name. Search the bounded local-header window and
	// require one exact candidate whose calculated data offset agrees.
	matches := 0
	nameMatches := false
	methodMatches := false
	for offset := start; offset+30 <= dataOffset; offset++ {
		header := contents[offset : offset+30]
		if !bytes.Equal(header[:4], []byte{'P', 'K', 3, 4}) {
			continue
		}
		nameLength := int64(binary.LittleEndian.Uint16(header[26:28]))
		extraLength := int64(binary.LittleEndian.Uint16(header[28:30]))
		if offset+30+nameLength+extraLength != dataOffset {
			continue
		}
		matches++
		nameStart := offset + 30
		nameMatches = string(contents[nameStart:nameStart+nameLength]) == file.Name
		methodMatches = binary.LittleEndian.Uint16(header[8:10]) == uint16(file.Method)
	}
	if matches != 1 || !nameMatches || !methodMatches {
		return fmt.Errorf("ZIP member %q local header does not match its directory entry", safeArchiveName(file.Name))
	}
	return nil
}

func validateReleaseMembers(members map[string]archiveMember, binaryName string) error {
	expected := map[string]fs.FileMode{
		binaryName:                0o755,
		"README.md":               0o644,
		"INSTALL.md":              0o644,
		"THIRD_PARTY_NOTICES.txt": 0o644,
		"build-info.json":         0o644,
	}
	if len(members) != len(expected) {
		return fmt.Errorf("archive has %d members, want %d", len(members), len(expected))
	}
	for name, mode := range expected {
		member, ok := members[name]
		if !ok {
			return fmt.Errorf("archive is missing member %q", name)
		}
		if member.mode != mode {
			return fmt.Errorf("member %q has mode %04o, want %04o", name, member.mode, mode)
		}
	}
	return nil
}

func validateRootMemberName(name string) error {
	if name == "" || name == "." || path.Clean(name) != name || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("archive member %q is not a root file", safeArchiveName(name))
	}
	return nil
}

func safeArchiveName(name string) string {
	if len(name) > 80 {
		return "<long-name>"
	}
	for _, char := range name {
		if char < 0x20 || char == 0x7f {
			return "<unsafe-name>"
		}
	}
	return name
}

func readMember(reader io.Reader, declaredSize, maxSize int64) ([]byte, error) {
	if declaredSize < 0 || declaredSize > maxSize {
		return nil, errors.New("member size exceeds limit")
	}
	data, err := io.ReadAll(io.LimitReader(reader, declaredSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != declaredSize {
		return nil, errors.New("member size does not match its header")
	}
	return data, nil
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

type boundedStream struct {
	reader io.Reader
	read   int64
	max    int64
}

func (r *boundedStream) Read(buffer []byte) (int, error) {
	remaining := r.max - r.read
	if remaining <= 0 {
		var probe [1]byte
		n, err := r.reader.Read(probe[:])
		if n > 0 {
			return 0, errExpandedArchiveTooLarge
		}
		return 0, err
	}
	if int64(len(buffer)) > remaining {
		buffer = buffer[:remaining]
	}
	n, err := r.reader.Read(buffer)
	r.read += int64(n)
	return n, err
}
