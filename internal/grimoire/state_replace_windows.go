//go:build windows

package grimoire

import (
	"errors"
	"path/filepath"
	"strings"
	"time"

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
	for attempt := 0; ; attempt++ {
		err = windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		if err == nil || attempt == 99 || !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return err
		}
		time.Sleep(time.Millisecond)
	}
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
