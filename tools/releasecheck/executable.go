package main

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
)

func inspectExecutableFormat(data []byte, target buildTarget) error {
	switch target.OS {
	case "linux":
		return inspectELF(data, target.Arch)
	case "darwin":
		return inspectMachO(data, target.Arch)
	case "windows":
		return inspectPE(data, target.Arch)
	default:
		return errors.New("unsupported executable target")
	}
}

func inspectELF(data []byte, arch string) error {
	file, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return errors.New("executable is not ELF")
	}
	defer file.Close()
	if file.Class != elf.ELFCLASS64 {
		return errors.New("ELF executable is not 64-bit")
	}
	if file.Type != elf.ET_EXEC {
		return errors.New("ELF file is not an executable")
	}
	wantMachine := elf.EM_X86_64
	if arch == "arm64" {
		wantMachine = elf.EM_AARCH64
	} else if arch != "amd64" {
		return errors.New("unsupported ELF architecture")
	}
	if file.Machine != wantMachine {
		return errors.New("ELF machine does not match target")
	}
	for _, program := range file.Progs {
		if err := validateFileExtent("ELF segment", program.Off, program.Filesz, uint64(len(data))); err != nil {
			return err
		}
	}
	for _, section := range file.Sections {
		if section.Type == elf.SHT_NOBITS {
			continue
		}
		if err := validateFileExtent("ELF section", section.Offset, section.Size, uint64(len(data))); err != nil {
			return err
		}
	}
	return nil
}

func inspectMachO(data []byte, arch string) error {
	if fat, err := macho.NewFatFile(bytes.NewReader(data)); err == nil {
		_ = fat.Close()
		return errors.New("universal Mach-O executables are not supported")
	} else if !errors.Is(err, macho.ErrNotFat) {
		return errors.New("executable has invalid Mach-O structure")
	}
	file, err := macho.NewFile(bytes.NewReader(data))
	if err != nil {
		return errors.New("executable is not thin Mach-O")
	}
	defer file.Close()
	if file.Magic != macho.Magic64 {
		return errors.New("Mach-O executable is not 64-bit")
	}
	if file.Type != macho.TypeExec {
		return errors.New("Mach-O file is not an executable")
	}
	wantCPU := macho.CpuAmd64
	if arch == "arm64" {
		wantCPU = macho.CpuArm64
	} else if arch != "amd64" {
		return errors.New("unsupported Mach-O architecture")
	}
	if file.Cpu != wantCPU {
		return errors.New("Mach-O CPU does not match target")
	}
	for _, load := range file.Loads {
		segment, ok := load.(*macho.Segment)
		if !ok {
			continue
		}
		if err := validateFileExtent("Mach-O segment", segment.Offset, segment.Filesz, uint64(len(data))); err != nil {
			return err
		}
	}
	for _, section := range file.Sections {
		const (
			sectionTypeMask         = 0xff
			sectionZeroFill         = 0x1
			sectionGBZeroFill       = 0xc
			sectionThreadLocalZeros = 0x12
		)
		sectionType := section.Flags & sectionTypeMask
		if sectionType == sectionZeroFill || sectionType == sectionGBZeroFill || sectionType == sectionThreadLocalZeros {
			continue
		}
		if err := validateFileExtent("Mach-O section", uint64(section.Offset), section.Size, uint64(len(data))); err != nil {
			return err
		}
	}
	return nil
}

func inspectPE(data []byte, arch string) error {
	if len(data) < 64 || !bytes.Equal(data[:2], []byte{'M', 'Z'}) {
		return errors.New("executable is not a PE image")
	}
	signatureOffset := uint64(binary.LittleEndian.Uint32(data[0x3c:0x40]))
	if signatureOffset > uint64(len(data)) || uint64(len(data))-signatureOffset < 4 || !bytes.Equal(data[signatureOffset:signatureOffset+4], []byte{'P', 'E', 0, 0}) {
		return errors.New("executable has an invalid PE image signature")
	}
	file, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return errors.New("executable is not PE")
	}
	defer file.Close()
	if _, ok := file.OptionalHeader.(*pe.OptionalHeader64); !ok {
		return errors.New("PE executable is not 64-bit")
	}
	const (
		peExecutableImage = 0x0002
		peDLL             = 0x2000
	)
	if file.Characteristics&peExecutableImage == 0 || file.Characteristics&peDLL != 0 {
		return errors.New("PE file is not an executable image")
	}
	wantMachine := uint16(pe.IMAGE_FILE_MACHINE_AMD64)
	if arch == "arm64" {
		wantMachine = pe.IMAGE_FILE_MACHINE_ARM64
	} else if arch != "amd64" {
		return errors.New("unsupported PE architecture")
	}
	if file.Machine != wantMachine {
		return errors.New("PE machine does not match target")
	}
	for _, section := range file.Sections {
		if err := validateFileExtent("PE section", uint64(section.Offset), uint64(section.Size), uint64(len(data))); err != nil {
			return err
		}
	}
	return nil
}

func validateFileExtent(label string, offset, size, fileSize uint64) error {
	if size == 0 {
		return nil
	}
	if offset > fileSize || size > fileSize-offset {
		return fmt.Errorf("%s file extent exceeds executable size", label)
	}
	return nil
}
