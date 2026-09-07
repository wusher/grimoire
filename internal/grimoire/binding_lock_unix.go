//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package grimoire

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func lockBindings(paths Paths) (func(), error) {
	if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(paths.ConfigHome, ".bindings.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock bindings: %w", err)
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}, nil
}
