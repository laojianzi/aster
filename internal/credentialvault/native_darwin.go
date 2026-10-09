package credentialvault

import (
	"github.com/ebitengine/purego"
	"unsafe"
)

// The same-binary helper uses the local macOS file-based Keychain. It does not
// request iCloud synchronisation or relax ACLs. User interaction is disabled in
// this short-lived process; locked or inaccessible items fail closed.
func native(r request) ([]byte, error) {
	cf, e := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if e != nil {
		return nil, ErrUnavailable
	}
	sec, e := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if e != nil {
		return nil, ErrUnavailable
	}
	var symbol func(uintptr, string) unsafe.Pointer
	purego.RegisterLibFunc(&symbol, cf, "dlsym")
	var text func(unsafe.Pointer, string, uint32) unsafe.Pointer
	purego.RegisterLibFunc(&text, cf, "CFStringCreateWithCString")
	var dict func(unsafe.Pointer, int64, unsafe.Pointer, unsafe.Pointer) unsafe.Pointer
	purego.RegisterLibFunc(&dict, cf, "CFDictionaryCreateMutable")
	var set func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer)
	purego.RegisterLibFunc(&set, cf, "CFDictionarySetValue")
	var release func(unsafe.Pointer)
	purego.RegisterLibFunc(&release, cf, "CFRelease")
	var makeData func(unsafe.Pointer, *byte, int64) unsafe.Pointer
	purego.RegisterLibFunc(&makeData, cf, "CFDataCreate")
	var dataLength func(unsafe.Pointer) int64
	purego.RegisterLibFunc(&dataLength, cf, "CFDataGetLength")
	var dataPointer func(unsafe.Pointer) *byte
	purego.RegisterLibFunc(&dataPointer, cf, "CFDataGetBytePtr")
	var typeID func(unsafe.Pointer) uint64
	purego.RegisterLibFunc(&typeID, cf, "CFGetTypeID")
	var dataType func() uint64
	purego.RegisterLibFunc(&dataType, cf, "CFDataGetTypeID")
	var interaction func(uint8) int32
	purego.RegisterLibFunc(&interaction, sec, "SecKeychainSetUserInteractionAllowed")
	if interaction(0) != 0 {
		return nil, ErrUnavailable
	}
	var find func(unsafe.Pointer, *unsafe.Pointer) int32
	purego.RegisterLibFunc(&find, sec, "SecItemCopyMatching")
	var add func(unsafe.Pointer, *unsafe.Pointer) int32
	purego.RegisterLibFunc(&add, sec, "SecItemAdd")
	var update func(unsafe.Pointer, unsafe.Pointer) int32
	purego.RegisterLibFunc(&update, sec, "SecItemUpdate")
	var remove func(unsafe.Pointer) int32
	purego.RegisterLibFunc(&remove, sec, "SecItemDelete")
	constant := func(lib uintptr, name string) unsafe.Pointer {
		p := symbol(lib, name)
		if p == nil {
			panic("missing native symbol")
		}
		return *(*unsafe.Pointer)(p)
	}
	newDict := func() unsafe.Pointer {
		return dict(nil, 0, symbol(cf, "kCFTypeDictionaryKeyCallBacks"), symbol(cf, "kCFTypeDictionaryValueCallBacks"))
	}
	q := newDict()
	if q == nil {
		return nil, ErrUnavailable
	}
	defer release(q)
	service := text(nil, ServiceName, 0x08000100)
	account := text(nil, r.Key, 0x08000100)
	if service == nil || account == nil {
		return nil, ErrFailed
	}
	defer release(service)
	defer release(account)
	set(q, constant(sec, "kSecClass"), constant(sec, "kSecClassGenericPassword"))
	set(q, constant(sec, "kSecAttrService"), service)
	set(q, constant(sec, "kSecAttrAccount"), account)
	status := func(code int32) error {
		if code == 0 {
			return nil
		}
		if code == -25300 {
			return ErrNotFound
		}
		if code == -25308 || code == -25291 {
			return ErrUnavailable
		}
		return ErrFailed
	}
	switch r.Operation {
	case "get":
		set(q, constant(sec, "kSecReturnData"), constant(cf, "kCFBooleanTrue"))
		set(q, constant(sec, "kSecMatchLimit"), constant(sec, "kSecMatchLimitOne"))
		var value unsafe.Pointer
		if err := status(find(q, &value)); err != nil {
			return nil, err
		}
		if value == nil {
			return nil, ErrFailed
		}
		defer release(value)
		if typeID(value) != dataType() {
			return nil, ErrInvalid
		}
		n := dataLength(value)
		if n <= 0 || n > MaxRecordBytes {
			return nil, ErrInvalid
		}
		p := dataPointer(value)
		if p == nil {
			return nil, ErrInvalid
		}
		return append([]byte(nil), unsafe.Slice(p, int(n))...), nil
	case "put":
		value := makeData(nil, &r.Value[0], int64(len(r.Value)))
		if value == nil {
			return nil, ErrFailed
		}
		defer release(value)
		attrs := newDict()
		defer release(attrs)
		set(attrs, constant(sec, "kSecValueData"), value)
		code := update(q, attrs)
		if code == -25300 {
			set(q, constant(sec, "kSecValueData"), value)
			code = add(q, nil)
		}
		return nil, status(code)
	case "delete":
		return nil, status(remove(q))
	}
	return nil, ErrInvalid
}
