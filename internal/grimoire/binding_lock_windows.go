//go:build windows

package grimoire

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func lockBindings(paths Paths) (func(), error) {
	if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(paths.ConfigHome, ".bindings.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	overlapped := &windows.Overlapped{}
	handle := windows.Handle(file.Fd())
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock bindings: %w", err)
	}
	return func() {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		_ = file.Close()
	}, nil
}
