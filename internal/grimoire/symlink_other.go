//go:build !unix && !windows

package grimoire

import "os"

func symlinkDirectory(string) (bool, error) { return true, nil }

func platformCreateSymlink(target, link string, _ bool) error { return os.Symlink(target, link) }
