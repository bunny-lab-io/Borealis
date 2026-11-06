//go:build windows

package config

import (
	"errors"
	"io"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func readConfigFile(path string) ([]byte, error) {
	f, err := openConfigRead(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// openConfigRead permits the atomic replacement used by Save while retaining
// read access to the opened snapshot. It neither creates files nor requests
// write/delete access, and nil security attributes keep the handle private.
func openConfigRead(path string) (*os.File, error) {
	if path == "" {
		return nil, &os.PathError{Op: "open", Path: path, Err: syscall.ENOENT}
	}
	open := func(name string) (windows.Handle, error) {
		p, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return windows.InvalidHandle, err
		}
		return windows.CreateFile(p, windows.GENERIC_READ,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil, windows.OPEN_EXISTING,
			windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	}
	h, err := open(path)
	if errors.Is(err, windows.ERROR_FILENAME_EXCED_RANGE) {
		// Go 1.22 enables native long paths on recent Windows versions. Keep
		// native normalization there; only older length-limited systems need
		// this bounded absolute-drive conversion. Never retry sharing errors.
		if extended := legacyConfigReadPath(path); extended != path {
			h, err = open(extended)
		}
	}
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(h), path)
	if f == nil {
		windows.CloseHandle(h)
		return nil, &os.PathError{Op: "open", Path: path, Err: syscall.EINVAL}
	}
	return f, nil
}

// legacyConfigReadPath follows the conservative conversion boundaries of Go
// 1.22's os.fixLongPath: short, relative, UNC, extended and parent-traversing
// paths stay untouched. Do not use filepath.Clean: it changes their meaning.
func legacyConfigReadPath(path string) string {
	if len(path) < 248 || path[1] != ':' ||
		!strings.ContainsRune("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz", rune(path[0])) ||
		(path[2] != '\\' && path[2] != '/') {
		return path
	}
	parts := strings.Split(strings.ReplaceAll(path[3:], "/", `\`), `\`)
	components := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "..":
			return path
		case "", ".":
		default:
			components = append(components, part)
		}
	}
	return `\\?\` + path[:2] + `\` + strings.Join(components, `\`)
}
