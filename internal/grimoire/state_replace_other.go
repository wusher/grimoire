//go:build !windows

package grimoire

import (
	"errors"
	"os"
	"syscall"
)

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}

func platformRawPath(path string) string { return path }

func syncParentDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if errors.Is(syncErr, syscall.EINVAL) || errors.Is(syncErr, errors.ErrUnsupported) {
		syncErr = nil
	}
	return errors.Join(syncErr, closeErr)
}
