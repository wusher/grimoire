//go:build unix

package grimoire

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func repositoryIdentity(repository string) string {
	info, err := os.Stat(filepath.Join(repository, ".git"))
	if err != nil {
		return ""
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
}
