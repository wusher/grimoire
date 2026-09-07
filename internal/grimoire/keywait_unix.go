//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package grimoire

import (
	"os"

	"golang.org/x/sys/unix"
)

func inputWaiting(input *os.File, milliseconds int) bool {
	if input == nil {
		return false
	}
	fds := []unix.PollFd{{Fd: int32(input.Fd()), Events: unix.POLLIN}}
	ready, err := unix.Poll(fds, milliseconds)
	return err == nil && ready > 0
}

func inputPollingSupported() bool { return true }
