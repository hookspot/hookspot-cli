package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func validateNoParentTraversal(path string) error {
	for _, component := range strings.Split(filepath.ToSlash(path), "/") {
		if component == ".." {
			return errors.New("path must not contain parent traversal")
		}
	}
	return nil
}

func validateExistingDirectoryPath(path string) error {
	if err := validateNoParentTraversal(path); err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs)
	root := volume + string(filepath.Separator)
	remaining := strings.TrimPrefix(abs, root)
	current := root
	if remaining == "" {
		return nil
	}
	for _, component := range strings.Split(remaining, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("directory ancestry contains a symlink or non-directory")
		}
	}
	return nil
}

// disjointPath compares cleaned lexical paths without resolving symlinks. Both
// ancestry directions are rejected, so a writable destination cannot be inside
// or contain a retained tree.
func disjointPath(a, b string) bool {
	absoluteA, err := filepath.Abs(filepath.Clean(a))
	if err != nil {
		return false
	}
	absoluteB, err := filepath.Abs(filepath.Clean(b))
	if err != nil {
		return false
	}
	return !pathContains(absoluteA, absoluteB) && !pathContains(absoluteB, absoluteA)
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

func isFilesystemRoot(path string) bool {
	absolute, err := filepath.Abs(filepath.Clean(path))
	return err == nil && filepath.Dir(absolute) == absolute
}
