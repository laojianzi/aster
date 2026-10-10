package credentialvault

import (
	"errors"
	"golang.org/x/sys/windows"
	"unsafe"
)

type winCredential struct {
	Flags, Type             uint32
	TargetName, Comment     *uint16
	LastWritten             windows.Filetime
	BlobSize                uint32
	Blob                    *byte
	Persist, AttributeCount uint32
	Attributes              unsafe.Pointer
	TargetAlias, Username   *uint16
}

func native(r request) ([]byte, error) {
	dll := windows.NewLazySystemDLL("advapi32.dll")
	name, _ := windows.UTF16PtrFromString(ServiceName + "/" + r.Key)
	check := func(ok uintptr, e error) error {
		if ok != 0 {
			return nil
		}
		if errors.Is(e, windows.ERROR_NOT_FOUND) {
			return ErrNotFound
		}
		return ErrUnavailable
	}
	switch r.Operation {
	case "put":
		user, _ := windows.UTF16PtrFromString("Aster")
		c := winCredential{Type: 1, TargetName: name, BlobSize: uint32(len(r.Value)), Blob: &r.Value[0], Persist: 2, Username: user}
		ok, _, e := dll.NewProc("CredWriteW").Call(uintptr(unsafe.Pointer(&c)), 0)
		return nil, check(ok, e)
	case "get":
		var c *winCredential
		ok, _, e := dll.NewProc("CredReadW").Call(uintptr(unsafe.Pointer(name)), 1, 0, uintptr(unsafe.Pointer(&c)))
		if err := check(ok, e); err != nil {
			return nil, err
		}
		if c == nil {
			return nil, ErrFailed
		}
		defer dll.NewProc("CredFree").Call(uintptr(unsafe.Pointer(c)))
		if c.Type != 1 || c.Blob == nil || c.BlobSize == 0 || c.BlobSize > MaxRecordBytes {
			return nil, ErrInvalid
		}
		return append([]byte(nil), unsafe.Slice(c.Blob, int(c.BlobSize))...), nil
	case "delete":
		ok, _, e := dll.NewProc("CredDeleteW").Call(uintptr(unsafe.Pointer(name)), 1, 0)
		return nil, check(ok, e)
	}
	return nil, ErrInvalid
}
