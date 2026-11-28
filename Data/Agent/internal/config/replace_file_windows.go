//go:build windows

package config

import (
	"encoding/binary"
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// configFileRenameInfo is the FILE_RENAME_INFO layout for FileRenameInfoEx.
// RootDirectory follows native pointer alignment; FileNameLength is bytes,
// excluding the UTF-16 terminator. Do not use the padded struct size as the
// filename offset. https://learn.microsoft.com/windows/win32/api/winbase/ns-winbase-file_rename_info
type configFileRenameInfo struct {
	Flags          uint32
	RootDirectory  windows.Handle
	FileNameLength uint32
	FileName       [1]uint16
}

func replaceConfigFile(staged, path string) error {
	return replaceConfigFileWith(staged, path, windows.SetFileInformationByHandle, os.Rename)
}

func replaceConfigFileWith(staged, path string,
	setInfo func(windows.Handle, uint32, *byte, uint32) error,
	legacyRename func(string, string) error,
) error {
	renameError := func(err error) error {
		if err == nil {
			return nil
		}
		return &os.LinkError{Op: "rename", Old: staged, New: path, Err: err}
	}
	info, err := configRenameInfo(path)
	if err != nil {
		return renameError(err)
	}
	open := func(name string) (windows.Handle, error) {
		p, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return windows.InvalidHandle, err
		}
		// Save has already synced and closed this owned staging file. DELETE
		// is the access required for rename; do not follow a replacement link.
		return windows.CreateFile(p, windows.DELETE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil, windows.OPEN_EXISTING,
			windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	}
	h, err := open(staged)
	if errors.Is(err, windows.ERROR_FILENAME_EXCED_RANGE) {
		if extended := legacyConfigReadPath(staged); extended != staged {
			h, err = open(extended)
		}
	}
	if err != nil {
		return renameError(err)
	}
	err = setInfo(h, windows.FileRenameInfoEx, &info[0], uint32(len(info)))
	closeErr := windows.CloseHandle(h)
	if closeErr != nil {
		return renameError(errors.Join(err, closeErr))
	}
	// Only explicit lack of API/filesystem support permits the old operation,
	// after closing our handle. Invalid parameters may indicate a malformed
	// request, so they fail closed along with sharing and access errors.
	if errors.Is(err, windows.ERROR_NOT_SUPPORTED) ||
		errors.Is(err, windows.ERROR_INVALID_FUNCTION) ||
		errors.Is(err, windows.ERROR_CALL_NOT_IMPLEMENTED) {
		return legacyRename(staged, path)
	}
	return renameError(err)
}

func configRenameInfo(path string) ([]byte, error) {
	name, err := windows.UTF16FromString(path)
	if err != nil {
		return nil, err
	}
	var layout configFileRenameInfo
	nameOffset := int(unsafe.Offsetof(layout.FileName))
	nameBytes := uint64(len(name)-1) * 2
	bufferBytes := uint64(nameOffset) + uint64(len(name))*2
	if bufferBytes > uint64(^uint32(0)) || bufferBytes > uint64(^uint(0)>>1) {
		return nil, windows.ERROR_FILENAME_EXCED_RANGE
	}
	if bufferBytes < uint64(unsafe.Sizeof(layout)) {
		bufferBytes = uint64(unsafe.Sizeof(layout))
	}
	info := make([]byte, int(bufferBytes))
	binary.LittleEndian.PutUint32(info, windows.FILE_RENAME_REPLACE_IF_EXISTS|windows.FILE_RENAME_POSIX_SEMANTICS)
	binary.LittleEndian.PutUint32(info[unsafe.Offsetof(layout.FileNameLength):], uint32(nameBytes))
	for i, unit := range name {
		binary.LittleEndian.PutUint16(info[nameOffset+i*2:], unit)
	}
	return info, nil
}
