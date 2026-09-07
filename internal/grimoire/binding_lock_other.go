//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package grimoire

import "sync"

var bindingsMutex sync.Mutex

func lockBindings(_ Paths) (func(), error) {
	bindingsMutex.Lock()
	return bindingsMutex.Unlock, nil
}
