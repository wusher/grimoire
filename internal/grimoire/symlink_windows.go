//go:build windows

package grimoire

import (
	"os"

	"golang.org/x/sys/windows"
)

const symbolicLinkAllowUnprivileged = 0x2

func symlinkDirectory(path string) (bool, error) {
	pointer, err := windows.UTF16PtrFromString(platformRawPath(path))
	if err != nil {
		return false, err
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil {
		return false, err
	}
	return attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0, nil
}

func platformCreateSymlink(target, link string, directory bool) error {
	linkPointer, err := windows.UTF16PtrFromString(platformRawPath(link))
	if err != nil {
		return err
	}
	targetPointer, err := windows.UTF16PtrFromString(platformRawPath(target))
	if err != nil {
		return err
	}
	flags := uint32(symbolicLinkAllowUnprivileged)
	if directory {
		flags |= windows.SYMBOLIC_LINK_FLAG_DIRECTORY
	}
	if err := windows.CreateSymbolicLink(linkPointer, targetPointer, flags); err != nil {
		flags &^= symbolicLinkAllowUnprivileged
		if retryErr := windows.CreateSymbolicLink(linkPointer, targetPointer, flags); retryErr != nil {
			return &os.LinkError{Op: "symlink", Old: target, New: link, Err: retryErr}
		}
	}
	return nil
}
