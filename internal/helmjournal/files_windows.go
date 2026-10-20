//go:build windows

package helmjournal

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const fileAllAccess = 0x001f01ff // Win32 FILE_ALL_ACCESS, as produced by SDDL FA.

func journalUser() (*windows.SID, error) {
	u, err := windows.GetCurrentThreadEffectiveToken().GetTokenUser()
	if err != nil || u.User.Sid == nil || !u.User.Sid.IsValid() {
		return nil, ErrUnavailable
	}
	return u.User.Sid, nil
}

func privateSecurity(directory bool) (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := journalUser()
	if err != nil {
		return nil, err
	}
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	// Owner-only protected DACL. Existing permissions are never silently
	// repaired. The descriptor applies at creation, without a public-file gap.
	sd, err := windows.SecurityDescriptorFromString("O:" + user.String() + "D:P(A;" + inherit + ";FA;;;" + user.String() + ")")
	if err != nil {
		return nil, ErrUnavailable
	}
	return sd, nil
}

func openPrivateRoot(path string) (*privateRoot, error) {
	return openPrivateRootMode(path, true)
}

func openExistingPrivateRoot(path string) (*privateRoot, error) {
	return openPrivateRootMode(path, false)
}

func openPrivateRootMode(path string, create bool) (*privateRoot, error) {
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' {
		return nil, ErrUnavailable
	}
	v, err := windows.UTF16PtrFromString(volume + "\\")
	if err != nil || windows.GetDriveType(v) != windows.DRIVE_FIXED {
		return nil, ErrUnavailable
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, ErrInvalid
	}
	if create {
		sd, err := privateSecurity(true)
		if err != nil {
			return nil, err
		}
		sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
		if err := windows.CreateDirectory(p, &sa); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return nil, ErrUnavailable
		}
		runtime.KeepAlive(sd)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		if !create && errors.Is(err, os.ErrNotExist) {
			return nil, errStoreNotInitialized
		}
		return nil, ErrUnavailable
	}
	dir := os.NewFile(uintptr(h), path)
	if dir == nil {
		windows.CloseHandle(h)
		return nil, ErrUnavailable
	}
	if validatePrivateFile(dir, true) != nil {
		dir.Close()
		return nil, ErrUnavailable
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		dir.Close()
		return nil, ErrUnavailable
	}
	r := &privateRoot{path: path, dir: dir, root: root}
	if r.check() != nil {
		r.close()
		return nil, ErrUnavailable
	}
	return r, nil
}

func validatePrivateFile(f *os.File, directory bool) error {
	if f == nil {
		return ErrUnavailable
	}
	h := windows.Handle(f.Fd())
	kind, err := windows.GetFileType(h)
	if err != nil || kind != windows.FILE_TYPE_DISK {
		return ErrUnavailable
	}
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		(info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory || !directory && info.NumberOfLinks != 1 {
		return ErrUnavailable
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || !sd.IsValid() {
		return ErrUnavailable
	}
	owner, _, err := sd.Owner()
	user, userErr := journalUser()
	if err != nil || userErr != nil || owner == nil || !windows.EqualSid(owner, user) {
		return ErrUnavailable
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return ErrUnavailable
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount != 1 {
		return ErrUnavailable
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if windows.GetAce(acl, 0, &ace) != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
		ace.Header.AceSize < 12 || uint32(ace.Mask) != fileAllAccess {
		return ErrUnavailable
	}
	wantFlags := uint8(0)
	if directory {
		wantFlags = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	}
	if ace.Header.AceFlags != wantFlags {
		return ErrUnavailable
	}
	aceUser := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !aceUser.IsValid() || !windows.EqualSid(aceUser, user) {
		return ErrUnavailable
	}
	runtime.KeepAlive(sd)
	return nil
}

func (r *privateRoot) openFile(name string, exclusive, writable bool) (*os.File, error) {
	if r.check() != nil {
		return nil, ErrUnavailable
	}
	p, err := windows.UTF16PtrFromString(filepath.Join(r.path, name))
	if err != nil {
		return nil, ErrInvalid
	}
	access := uint32(windows.GENERIC_READ | windows.READ_CONTROL)
	if writable {
		access |= windows.GENERIC_WRITE
	}
	create := uint32(windows.OPEN_EXISTING)
	if exclusive {
		create = windows.CREATE_NEW
	} else if writable && name == lockName {
		create = windows.OPEN_ALWAYS
	}
	sd, err := privateSecurity(false)
	if err != nil {
		return nil, err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	h, err := windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, &sa, create,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	runtime.KeepAlive(sd)
	if err != nil {
		return nil, ErrUnavailable
	}
	f := os.NewFile(uintptr(h), filepath.Join(r.path, name))
	if f == nil {
		windows.CloseHandle(h)
		return nil, ErrUnavailable
	}
	if r.checkFile(name, f) != nil {
		f.Close()
		return nil, ErrUnavailable
	}
	return f, nil
}

// File.Sync calls FlushFileBuffers for every completed frame. Windows provides
// no corresponding portable directory Sync contract here. Qualification covers
// abrupt process termination on the local fixed-volume filesystem; it does not
// claim arbitrary power-loss or failing-storage durability.
func (*privateRoot) sync() error { return nil }

func acquireLock(f *os.File) error {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
	if err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return ErrBusy
		}
		return ErrUnavailable
	}
	return nil
}

func releaseLock(f *os.File) error {
	var overlapped windows.Overlapped
	if windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped) != nil {
		return ErrUnavailable
	}
	return nil
}
