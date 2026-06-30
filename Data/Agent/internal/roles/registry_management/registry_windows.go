//go:build windows

package registrymanagement

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	registryAdvapi32        = windows.NewLazySystemDLL("advapi32.dll")
	registryProcSetValueExW = registryAdvapi32.NewProc("RegSetValueExW")
	registryProcDeleteTreeW = registryAdvapi32.NewProc("RegDeleteTreeW")
)

func registryRoots(ctx context.Context) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries := []map[string]any{}
	for _, alias := range []string{"HKCR", "HKCU", "HKLM", "HKU", "HKCC"} {
		entry := map[string]any{
			"path":         alias,
			"parent_path":  "",
			"name":         hiveDisplayName(alias),
			"short_name":   alias,
			"kind":         "hive",
			"has_children": true,
			"is_hive":      true,
			"editable":     false,
		}
		if key, closeKey, err := openRegistryPath(registryPath{Hive: alias, Path: alias}, registry.READ); err == nil {
			if info, statErr := key.Stat(); statErr == nil {
				entry["subkey_count"] = int64(info.SubKeyCount)
				entry["value_count"] = int64(info.ValueCount)
				entry["modified_at"] = info.ModTime().Unix()
				entry["has_children"] = info.SubKeyCount > 0
			}
			closeKey()
		}
		entries = append(entries, entry)
	}
	return map[string]any{
		"ok":            true,
		"platform":      "windows",
		"context_label": "SYSTEM",
		"current_path":  "",
		"entries":       entries,
	}, nil
}

func registryChildren(ctx context.Context, pathValue registryPath) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, closeKey, err := openRegistryPath(pathValue, registry.READ)
	if err != nil {
		return nil, mapRegistryError(err, pathValue.Path)
	}
	defer closeKey()

	subkeyNames, err := key.ReadSubKeyNames(-1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, mapRegistryError(err, pathValue.Path)
	}
	entries := make([]map[string]any, 0, len(subkeyNames))
	for _, name := range subkeyNames {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		entry, entryErr := registryKeyEntry(pathValue, name)
		if entryErr == nil {
			entries = append(entries, entry)
		}
	}

	valueNames, err := key.ReadValueNames(-1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, mapRegistryError(err, pathValue.Path)
	}
	values := make([]map[string]any, 0, len(valueNames))
	for _, name := range valueNames {
		value, valueErr := registryValueEntry(key, name)
		if valueErr == nil {
			values = append(values, value)
		}
	}

	return map[string]any{
		"ok":            true,
		"platform":      "windows",
		"context_label": "SYSTEM",
		"current_path":  pathValue.Path,
		"entries":       sortKeyEntries(entries),
		"values":        sortRegistryValues(values),
	}, nil
}

func registryCreateKey(ctx context.Context, parentPath registryPath, name string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parent, closeParent, err := openRegistryPath(parentPath, registry.ALL_ACCESS)
	if err != nil {
		return nil, mapRegistryError(err, parentPath.Path)
	}
	defer closeParent()
	key, openedExisting, err := registry.CreateKey(parent, name, registry.ALL_ACCESS)
	if err != nil {
		return nil, mapRegistryError(err, parentPath.Path+`\`+name)
	}
	defer key.Close()
	if openedExisting {
		return nil, newError("conflict", "Registry key already exists.")
	}
	entry, err := registryKeyEntry(parentPath, name)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "entry": entry}, nil
}

func registryRenameKey(ctx context.Context, pathValue registryPath, newName string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(pathValue.Parts) <= 1 {
		return nil, newError("invalid_path", "Registry hives cannot be renamed.")
	}
	parentPath := registryParentPath(pathValue)
	parent, closeParent, err := openRegistryPath(parentPath, registry.ALL_ACCESS)
	if err != nil {
		return nil, mapRegistryError(err, parentPath.Path)
	}
	defer closeParent()
	oldName := pathValue.Parts[len(pathValue.Parts)-1]
	if keyExists(parent, newName) {
		return nil, newError("conflict", "Destination registry key already exists.")
	}
	source, err := registry.OpenKey(parent, oldName, registry.ALL_ACCESS)
	if err != nil {
		return nil, mapRegistryError(err, pathValue.Path)
	}
	defer source.Close()
	destination, openedExisting, err := registry.CreateKey(parent, newName, registry.ALL_ACCESS)
	if err != nil {
		return nil, mapRegistryError(err, parentPath.Path+`\`+newName)
	}
	if openedExisting {
		destination.Close()
		return nil, newError("conflict", "Destination registry key already exists.")
	}
	copyErr := copyRegistryTree(ctx, source, destination)
	closeErr := destination.Close()
	if copyErr != nil {
		_ = deleteRegistryTree(parent, newName)
		return nil, copyErr
	}
	if closeErr != nil {
		_ = deleteRegistryTree(parent, newName)
		return nil, mapRegistryError(closeErr, parentPath.Path+`\`+newName)
	}
	if err := deleteRegistryTree(parent, oldName); err != nil {
		return nil, mapRegistryError(err, pathValue.Path)
	}
	entry, err := registryKeyEntry(parentPath, newName)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "entry": entry, "old_path": pathValue.Path}, nil
}

func registryDeleteKey(ctx context.Context, pathValue registryPath, recursive bool) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(pathValue.Parts) <= 1 {
		return nil, newError("invalid_path", "Registry hives cannot be deleted.")
	}
	parentPath := registryParentPath(pathValue)
	parent, closeParent, err := openRegistryPath(parentPath, registry.ALL_ACCESS)
	if err != nil {
		return nil, mapRegistryError(err, parentPath.Path)
	}
	defer closeParent()
	name := pathValue.Parts[len(pathValue.Parts)-1]
	target, err := registry.OpenKey(parent, name, registry.READ)
	if err != nil {
		return nil, mapRegistryError(err, pathValue.Path)
	}
	info, statErr := target.Stat()
	_ = target.Close()
	if statErr != nil {
		return nil, mapRegistryError(statErr, pathValue.Path)
	}
	if info.SubKeyCount > 0 && !recursive {
		return nil, newError("key_has_children", "Registry key has subkeys. Set recursive to delete it.")
	}
	if recursive {
		err = deleteRegistryTree(parent, name)
	} else {
		err = registry.DeleteKey(parent, name)
	}
	if err != nil {
		return nil, mapRegistryError(err, pathValue.Path)
	}
	return map[string]any{"ok": true, "deleted_path": pathValue.Path}, nil
}

func registrySetValue(ctx context.Context, pathValue registryPath, input registryValueInput, createOnly bool) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, closeKey, err := openRegistryPath(pathValue, registry.ALL_ACCESS)
	if err != nil {
		return nil, mapRegistryError(err, pathValue.Path)
	}
	defer closeKey()
	if createOnly && registryValueExists(key, input.Name) {
		return nil, newError("conflict", "Registry value already exists.")
	}
	if err := writeRegistryValue(key, input); err != nil {
		return nil, err
	}
	value, err := registryValueEntry(key, input.Name)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "value": value}, nil
}

func registryDeleteValue(ctx context.Context, pathValue registryPath, name string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, closeKey, err := openRegistryPath(pathValue, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return nil, mapRegistryError(err, pathValue.Path)
	}
	defer closeKey()
	if err := key.DeleteValue(name); err != nil {
		return nil, mapRegistryError(err, pathValue.Path)
	}
	return map[string]any{"ok": true, "deleted_value": name, "path": pathValue.Path}, nil
}

func registryParentPath(pathValue registryPath) registryPath {
	if len(pathValue.Parts) <= 1 {
		return registryPath{Hive: pathValue.Hive, Path: pathValue.Hive, Parts: []string{pathValue.Hive}}
	}
	parts := append([]string{}, pathValue.Parts[:len(pathValue.Parts)-1]...)
	subPath := ""
	if len(parts) > 1 {
		subPath = strings.Join(parts[1:], `\`)
	}
	return registryPath{
		Hive:    pathValue.Hive,
		SubPath: subPath,
		Path:    strings.Join(parts, `\`),
		Parts:   parts,
	}
}

func openRegistryPath(pathValue registryPath, access uint32) (registry.Key, func(), error) {
	root, ok := rootRegistryKey(pathValue.Hive)
	if !ok {
		return 0, func() {}, newError("invalid_hive", "Registry hive is invalid.")
	}
	if pathValue.SubPath == "" {
		return root, func() {}, nil
	}
	key, err := registry.OpenKey(root, pathValue.SubPath, access)
	if err != nil {
		return 0, func() {}, err
	}
	return key, func() { _ = key.Close() }, nil
}

func rootRegistryKey(alias string) (registry.Key, bool) {
	switch strings.ToUpper(strings.TrimSpace(alias)) {
	case "HKCR":
		return registry.CLASSES_ROOT, true
	case "HKCU":
		return registry.CURRENT_USER, true
	case "HKLM":
		return registry.LOCAL_MACHINE, true
	case "HKU":
		return registry.USERS, true
	case "HKCC":
		return registry.CURRENT_CONFIG, true
	default:
		return 0, false
	}
}

func registryKeyEntry(parentPath registryPath, name string) (map[string]any, error) {
	parent, closeParent, err := openRegistryPath(parentPath, registry.READ)
	if err != nil {
		return nil, mapRegistryError(err, parentPath.Path)
	}
	defer closeParent()
	key, err := registry.OpenKey(parent, name, registry.READ)
	if err != nil {
		return nil, mapRegistryError(err, parentPath.Path+`\`+name)
	}
	defer key.Close()
	info, err := key.Stat()
	if err != nil {
		return nil, mapRegistryError(err, parentPath.Path+`\`+name)
	}
	return map[string]any{
		"path":         parentPath.Path + `\` + name,
		"parent_path":  parentPath.Path,
		"name":         name,
		"kind":         "key",
		"has_children": info.SubKeyCount > 0,
		"subkey_count": int64(info.SubKeyCount),
		"value_count":  int64(info.ValueCount),
		"modified_at":  info.ModTime().Unix(),
		"editable":     true,
	}, nil
}

func registryValueEntry(key registry.Key, name string) (map[string]any, error) {
	raw, valueType, err := readRawRegistryValue(key, name)
	if err != nil {
		return nil, mapRegistryError(err, name)
	}
	valueTypeName := registryValueTypeName(valueType)
	entry := map[string]any{
		"name":         name,
		"display_name": registryValueDisplayName(name),
		"type":         valueTypeName,
		"raw_type":     int64(valueType),
		"size_bytes":   int64(len(raw)),
		"editable":     isEditableRegistryValueType(valueType),
	}
	switch valueType {
	case registry.SZ, registry.EXPAND_SZ:
		value, _, err := key.GetStringValue(name)
		if err != nil {
			return nil, mapRegistryError(err, name)
		}
		entry["data"] = value
		entry["display_data"] = value
	case registry.MULTI_SZ:
		value, _, err := key.GetStringsValue(name)
		if err != nil {
			return nil, mapRegistryError(err, name)
		}
		entry["data"] = value
		entry["display_data"] = strings.Join(value, "; ")
	case registry.DWORD, registry.QWORD:
		value, _, err := key.GetIntegerValue(name)
		if err != nil {
			return nil, mapRegistryError(err, name)
		}
		entry["data"] = strconv.FormatUint(value, 10)
		entry["display_data"] = strconv.FormatUint(value, 10)
	case registry.BINARY:
		entry["data"] = formatBinaryData(raw)
		entry["display_data"] = formatBinaryData(raw)
		entry["data_base64"] = base64.StdEncoding.EncodeToString(raw)
	default:
		entry["data"] = ""
		entry["display_data"] = fmt.Sprintf("Unsupported type %d", valueType)
		if len(raw) <= maxRegistryBinaryBytes {
			entry["data_base64"] = base64.StdEncoding.EncodeToString(raw)
		}
	}
	return entry, nil
}

func registryValueTypeName(valueType uint32) string {
	switch valueType {
	case registry.NONE:
		return "REG_NONE"
	case registry.SZ:
		return "REG_SZ"
	case registry.EXPAND_SZ:
		return "REG_EXPAND_SZ"
	case registry.BINARY:
		return "REG_BINARY"
	case registry.DWORD:
		return "REG_DWORD"
	case registry.DWORD_BIG_ENDIAN:
		return "REG_DWORD_BIG_ENDIAN"
	case registry.LINK:
		return "REG_LINK"
	case registry.MULTI_SZ:
		return "REG_MULTI_SZ"
	case registry.RESOURCE_LIST:
		return "REG_RESOURCE_LIST"
	case registry.FULL_RESOURCE_DESCRIPTOR:
		return "REG_FULL_RESOURCE_DESCRIPTOR"
	case registry.RESOURCE_REQUIREMENTS_LIST:
		return "REG_RESOURCE_REQUIREMENTS_LIST"
	case registry.QWORD:
		return "REG_QWORD"
	default:
		return fmt.Sprintf("REG_TYPE_%d", valueType)
	}
}

func isEditableRegistryValueType(valueType uint32) bool {
	switch valueType {
	case registry.SZ, registry.EXPAND_SZ, registry.MULTI_SZ, registry.DWORD, registry.QWORD, registry.BINARY:
		return true
	default:
		return false
	}
}

func registryValueExists(key registry.Key, name string) bool {
	_, _, err := key.GetValue(name, nil)
	return err == nil || errors.Is(err, registry.ErrShortBuffer)
}

func writeRegistryValue(key registry.Key, input registryValueInput) error {
	switch input.Type {
	case "REG_SZ":
		if err := key.SetStringValue(input.Name, fmt.Sprint(input.Data)); err != nil {
			return mapRegistryError(err, input.Name)
		}
	case "REG_EXPAND_SZ":
		if err := key.SetExpandStringValue(input.Name, fmt.Sprint(input.Data)); err != nil {
			return mapRegistryError(err, input.Name)
		}
	case "REG_MULTI_SZ":
		if err := key.SetStringsValue(input.Name, normalizeStringList(input.Data)); err != nil {
			return mapRegistryError(err, input.Name)
		}
	case "REG_DWORD":
		value, err := parseUnsignedRegistryInteger(input.Data, 32)
		if err != nil {
			return err
		}
		if err := key.SetDWordValue(input.Name, uint32(value)); err != nil {
			return mapRegistryError(err, input.Name)
		}
	case "REG_QWORD":
		value, err := parseUnsignedRegistryInteger(input.Data, 64)
		if err != nil {
			return err
		}
		if err := key.SetQWordValue(input.Name, value); err != nil {
			return mapRegistryError(err, input.Name)
		}
	case "REG_BINARY":
		value, err := parseBinaryData(input.Data)
		if err != nil {
			return err
		}
		if err := key.SetBinaryValue(input.Name, value); err != nil {
			return mapRegistryError(err, input.Name)
		}
	default:
		return newError("unsupported_type", "Registry value type is unsupported.")
	}
	return nil
}

func copyRegistryTree(ctx context.Context, source registry.Key, destination registry.Key) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	valueNames, err := source.ReadValueNames(-1)
	if err != nil && !errors.Is(err, io.EOF) {
		return mapRegistryError(err, "")
	}
	for _, name := range valueNames {
		raw, valueType, err := readRawRegistryValue(source, name)
		if err != nil {
			return mapRegistryError(err, name)
		}
		if err := setRawRegistryValue(destination, name, valueType, raw); err != nil {
			return mapRegistryError(err, name)
		}
	}
	subkeyNames, err := source.ReadSubKeyNames(-1)
	if err != nil && !errors.Is(err, io.EOF) {
		return mapRegistryError(err, "")
	}
	for _, name := range subkeyNames {
		if err := ctx.Err(); err != nil {
			return err
		}
		sourceChild, err := registry.OpenKey(source, name, registry.ALL_ACCESS)
		if err != nil {
			return mapRegistryError(err, name)
		}
		destinationChild, openedExisting, err := registry.CreateKey(destination, name, registry.ALL_ACCESS)
		if err != nil {
			sourceChild.Close()
			return mapRegistryError(err, name)
		}
		if openedExisting {
			sourceChild.Close()
			destinationChild.Close()
			return newError("conflict", "Destination registry key already exists.")
		}
		copyErr := copyRegistryTree(ctx, sourceChild, destinationChild)
		sourceErr := sourceChild.Close()
		destinationErr := destinationChild.Close()
		if copyErr != nil {
			return copyErr
		}
		if sourceErr != nil {
			return mapRegistryError(sourceErr, name)
		}
		if destinationErr != nil {
			return mapRegistryError(destinationErr, name)
		}
	}
	return nil
}

func deleteRegistryTree(parent registry.Key, name string) error {
	pname, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	if registryProcDeleteTreeW.Find() == nil {
		r1, _, _ := registryProcDeleteTreeW.Call(uintptr(parent), uintptr(unsafe.Pointer(pname)))
		if r1 == 0 {
			return nil
		}
	}
	return deleteRegistryTreeManual(parent, name)
}

func deleteRegistryTreeManual(parent registry.Key, name string) error {
	target, err := registry.OpenKey(parent, name, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	subkeys, readErr := target.ReadSubKeyNames(-1)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		_ = target.Close()
		return readErr
	}
	for _, child := range subkeys {
		if err := deleteRegistryTreeManual(target, child); err != nil {
			_ = target.Close()
			return err
		}
	}
	if closeErr := target.Close(); closeErr != nil {
		return closeErr
	}
	return registry.DeleteKey(parent, name)
}

func keyExists(parent registry.Key, name string) bool {
	key, err := registry.OpenKey(parent, name, registry.READ)
	if err != nil {
		return false
	}
	_ = key.Close()
	return true
}

func readRawRegistryValue(key registry.Key, name string) ([]byte, uint32, error) {
	size, valueType, err := key.GetValue(name, nil)
	if err != nil && !errors.Is(err, registry.ErrShortBuffer) {
		return nil, valueType, err
	}
	if size <= 0 {
		return []byte{}, valueType, nil
	}
	if size > maxRegistryBinaryBytes {
		return nil, valueType, newError("value_too_large", "Registry value is too large.")
	}
	buffer := make([]byte, size)
	size, valueType, err = key.GetValue(name, buffer)
	if err != nil {
		return nil, valueType, err
	}
	return buffer[:size], valueType, nil
}

func setRawRegistryValue(key registry.Key, name string, valueType uint32, data []byte) error {
	pname, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	var dataPtr uintptr
	if len(data) > 0 {
		dataPtr = uintptr(unsafe.Pointer(&data[0]))
	}
	r1, _, _ := registryProcSetValueExW.Call(
		uintptr(key),
		uintptr(unsafe.Pointer(pname)),
		0,
		uintptr(valueType),
		dataPtr,
		uintptr(uint32(len(data))),
	)
	if r1 != 0 {
		return syscall.Errno(r1)
	}
	return nil
}

func mapRegistryError(err error, target string) error {
	if err == nil {
		return nil
	}
	if rerr, ok := err.(registryError); ok {
		return rerr
	}
	code := "registry_error"
	message := err.Error()
	switch {
	case errors.Is(err, registry.ErrNotExist), errors.Is(err, syscall.ERROR_FILE_NOT_FOUND):
		code = "path_not_found"
		message = "Registry path was not found."
	case errors.Is(err, syscall.ERROR_ACCESS_DENIED):
		code = "access_denied"
		message = "Access to the registry path was denied."
	case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
		code = "invalid_request"
		message = "Registry request was invalid."
	}
	if strings.TrimSpace(target) != "" && code != "registry_error" {
		message = fmt.Sprintf("%s Target: %s", message, target)
	}
	return newError(code, message)
}
