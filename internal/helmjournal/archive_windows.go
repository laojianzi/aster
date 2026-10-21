package helmjournal

import (
	"golang.org/x/sys/windows"
	"path/filepath"
)

func (r *privateRoot) renameNoReplace(old, new string) error {
	if r.check() != nil {
		return ErrUnavailable
	}
	from, err := windows.UTF16PtrFromString(filepath.Join(r.path, old))
	if err != nil {
		return ErrUnavailable
	}
	to, err := windows.UTF16PtrFromString(filepath.Join(r.path, new))
	if err != nil {
		return ErrUnavailable
	}
	// The fixed-volume directory is held without FILE_SHARE_DELETE by privateRoot.
	// No REPLACE_EXISTING, COPY_ALLOWED, or delayed/reboot operation is permitted.
	if windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH) != nil {
		return ErrUnavailable
	}
	return nil
}
