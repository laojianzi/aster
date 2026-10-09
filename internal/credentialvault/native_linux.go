package credentialvault

import (
	"github.com/ebitengine/purego"
	"unsafe"
)

type nativeList struct {
	data       unsafe.Pointer
	next, prev *nativeList
}

// libsecret negotiates the local Secret Service session. No fallback collection
// is created, no unlock is requested, and searches never use SEARCH_UNLOCK.
// The parent kills this helper if a provider blocks or requires interaction.
func native(r request) ([]byte, error) {
	lib, e := purego.Dlopen("libsecret-1.so.0", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if e != nil {
		return nil, ErrUnavailable
	}
	var symbol func(uintptr, string) unsafe.Pointer
	purego.RegisterLibFunc(&symbol, lib, "dlsym")
	var dup func(string) unsafe.Pointer
	purego.RegisterLibFunc(&dup, lib, "g_strdup")
	var hashNew func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) unsafe.Pointer
	purego.RegisterLibFunc(&hashNew, lib, "g_hash_table_new_full")
	var hashPut func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) int32
	purego.RegisterLibFunc(&hashPut, lib, "g_hash_table_insert")
	var hashFree func(unsafe.Pointer)
	purego.RegisterLibFunc(&hashFree, lib, "g_hash_table_unref")
	var objectFree func(unsafe.Pointer)
	purego.RegisterLibFunc(&objectFree, lib, "g_object_unref")
	var errorFree func(unsafe.Pointer)
	purego.RegisterLibFunc(&errorFree, lib, "g_error_free")
	var schemaNew func(string, int32, unsafe.Pointer) unsafe.Pointer
	purego.RegisterLibFunc(&schemaNew, lib, "secret_schema_newv")
	var schemaFree func(unsafe.Pointer)
	purego.RegisterLibFunc(&schemaFree, lib, "secret_schema_unref")
	var getService func(int32, unsafe.Pointer, *unsafe.Pointer) unsafe.Pointer
	purego.RegisterLibFunc(&getService, lib, "secret_service_get_sync")
	var getCollection func(unsafe.Pointer, string, int32, unsafe.Pointer, *unsafe.Pointer) unsafe.Pointer
	purego.RegisterLibFunc(&getCollection, lib, "secret_collection_for_alias_sync")
	var locked func(unsafe.Pointer) int32
	purego.RegisterLibFunc(&locked, lib, "secret_collection_get_locked")
	var create func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, string, unsafe.Pointer, int32, unsafe.Pointer, *unsafe.Pointer) unsafe.Pointer
	purego.RegisterLibFunc(&create, lib, "secret_item_create_sync")
	var valueNew func(*byte, int64, string) unsafe.Pointer
	purego.RegisterLibFunc(&valueNew, lib, "secret_value_new")
	var valueFree func(unsafe.Pointer)
	purego.RegisterLibFunc(&valueFree, lib, "secret_value_unref")
	var search func(unsafe.Pointer, unsafe.Pointer, unsafe.Pointer, int32, unsafe.Pointer, *unsafe.Pointer) *nativeList
	purego.RegisterLibFunc(&search, lib, "secret_collection_search_sync")
	var listFree func(*nativeList, unsafe.Pointer)
	purego.RegisterLibFunc(&listFree, lib, "g_list_free_full")
	var itemLocked func(unsafe.Pointer) int32
	purego.RegisterLibFunc(&itemLocked, lib, "secret_item_get_locked")
	var load func(unsafe.Pointer, unsafe.Pointer, *unsafe.Pointer) int32
	purego.RegisterLibFunc(&load, lib, "secret_item_load_secret_sync")
	var getValue func(unsafe.Pointer) unsafe.Pointer
	purego.RegisterLibFunc(&getValue, lib, "secret_item_get_secret")
	var getBytes func(unsafe.Pointer, *uint64) *byte
	purego.RegisterLibFunc(&getBytes, lib, "secret_value_get")
	var remove func(unsafe.Pointer, unsafe.Pointer, *unsafe.Pointer) int32
	purego.RegisterLibFunc(&remove, lib, "secret_item_delete_sync")
	var nativeError unsafe.Pointer
	defer func() {
		if nativeError != nil {
			errorFree(nativeError)
		}
	}()
	service := getService(2, nil, &nativeError)
	if service == nil || nativeError != nil {
		return nil, ErrUnavailable
	}
	defer objectFree(service)
	collection := getCollection(service, "default", 0, nil, &nativeError)
	if collection == nil || nativeError != nil {
		return nil, ErrUnavailable
	}
	defer objectFree(collection)
	if locked(collection) != 0 {
		return nil, ErrUnavailable
	}
	names := hashNew(symbol(lib, "g_str_hash"), symbol(lib, "g_str_equal"), symbol(lib, "g_free"), nil)
	defer hashFree(names)
	hashPut(names, dup("binding"), nil) // STRING = 0
	schema := schemaNew(ServiceName, 0, names)
	if schema == nil {
		return nil, ErrUnavailable
	}
	defer schemaFree(schema)
	attrs := hashNew(symbol(lib, "g_str_hash"), symbol(lib, "g_str_equal"), symbol(lib, "g_free"), symbol(lib, "g_free"))
	defer hashFree(attrs)
	hashPut(attrs, dup("binding"), dup(r.Key))
	if r.Operation == "put" {
		value := valueNew(&r.Value[0], int64(len(r.Value)), "application/json")
		if value == nil {
			return nil, ErrFailed
		}
		defer valueFree(value)
		item := create(collection, schema, attrs, "Aster Kubernetes token", value, 2, nil, &nativeError)
		if item == nil || nativeError != nil {
			return nil, ErrFailed
		}
		objectFree(item)
		return nil, nil
	}
	items := search(collection, schema, attrs, 0, nil, &nativeError)
	if nativeError != nil {
		return nil, ErrUnavailable
	}
	if items == nil {
		return nil, ErrNotFound
	}
	defer listFree(items, symbol(lib, "g_object_unref"))
	item := items.data
	if item == nil || itemLocked(item) != 0 {
		return nil, ErrUnavailable
	}
	if r.Operation == "delete" {
		if remove(item, nil, &nativeError) == 0 || nativeError != nil {
			return nil, ErrFailed
		}
		return nil, nil
	}
	if load(item, nil, &nativeError) == 0 || nativeError != nil {
		return nil, ErrUnavailable
	}
	value := getValue(item)
	if value == nil {
		return nil, ErrFailed
	}
	defer valueFree(value)
	var n uint64
	p := getBytes(value, &n)
	if p == nil || n == 0 || n > MaxRecordBytes {
		return nil, ErrInvalid
	}
	return append([]byte(nil), unsafe.Slice(p, int(n))...), nil
}
