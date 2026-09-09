//go:build windows

package grimoire

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func repositoryIdentity(repository string) string {
	path, err := windows.UTF16PtrFromString(platformRawPath(filepath.Join(repository, ".git")))
	if err != nil {
		return ""
	}
	handle, err := windows.CreateFile(
		path,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return ""
	}
	return fmt.Sprintf("%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
}
