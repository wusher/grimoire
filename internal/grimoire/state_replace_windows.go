//go:build windows

package grimoire

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func replaceFile(source, destination string) error {
	from, err := windows.UTF16PtrFromString(platformRawPath(source))
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(platformRawPath(destination))
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func syncParentDirectory(string) error {
	// MoveFileEx with MOVEFILE_WRITE_THROUGH supplies the available durability guarantee on Windows.
	return nil
}

func platformRawPath(path string) string {
	path = filepath.FromSlash(path)
	if strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\??\`) || strings.HasPrefix(path, `\\.\`) {
		return path
	}
	if !filepath.IsAbs(path) {
		return path
	}
	path = filepath.Clean(path)
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + path[2:]
	}
	return `\\?\` + path
}
