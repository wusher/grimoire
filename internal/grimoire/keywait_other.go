//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package grimoire

import "os"

func inputWaiting(_ *os.File, _ int) bool { return false }

func inputPollingSupported() bool { return false }
