//go:build windows

package config

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestConfigRenameInfoPreservesNativePathAndABI(t *testing.T) {
	var layout configFileRenameInfo
	wantOffset := uintptr(12)
	if unsafe.Sizeof(windows.Handle(0)) == 8 {
		wantOffset = 20
	}
	if got := unsafe.Offsetof(layout.FileName); got != wantOffset {
		t.Fatalf("filename ABI offset = %d, want %d", got, wantOffset)
	}
	for _, path := range []string{
		`agent.json`, `C:agent.json`, `C:\state\.\agent.json. `,
		`\\server\share\agent.json`, `\\?\UNC\server\share\agent.json`,
		`\\?\C:\state\agent.json`, `C:\` + strings.Repeat(`long\`, 70) + FileName,
		"C:\\state\\config-\U0001f9ea.json",
	} {
		info, err := configRenameInfo(path)
		if err != nil {
			t.Fatal(err)
		}
		if flags := binary.LittleEndian.Uint32(info); flags != 3 {
			t.Fatalf("rename flags = %#x, want replace-existing and POSIX only", flags)
		}
		rootStart := unsafe.Offsetof(layout.RootDirectory)
		rootEnd := rootStart + unsafe.Sizeof(layout.RootDirectory)
		if !bytes.Equal(info[rootStart:rootEnd], make([]byte, rootEnd-rootStart)) {
			t.Fatal("RootDirectory must remain NULL for supplied Win32 paths")
		}
		nameBytes := binary.LittleEndian.Uint32(info[unsafe.Offsetof(layout.FileNameLength):])
		want := utf16.Encode([]rune(path))
		if nameBytes != uint32(len(want)*2) {
			t.Fatalf("UTF-16 byte length = %d, want %d", nameBytes, len(want)*2)
		}
		for i, unit := range append(want, 0) {
			if got := binary.LittleEndian.Uint16(info[int(wantOffset)+i*2:]); got != unit {
				t.Fatalf("path UTF-16 unit %d = %#x, want %#x", i, got, unit)
			}
		}
	}
	if _, err := configRenameInfo("invalid\x00path"); !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("embedded NUL error = %v, want EINVAL", err)
	}
}

func TestConfigReplacementFallbackBoundary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		fallback bool
	}{
		{"success", nil, false},
		{"unsupported", windows.ERROR_NOT_SUPPORTED, true},
		{"invalid-function", windows.ERROR_INVALID_FUNCTION, true},
		{"not-implemented", windows.ERROR_CALL_NOT_IMPLEMENTED, true},
		{"access-denied", windows.ERROR_ACCESS_DENIED, false},
		{"sharing-violation", windows.ERROR_SHARING_VIOLATION, false},
		{"invalid-parameter", windows.ERROR_INVALID_PARAMETER, false},
		{"missing-file", windows.ERROR_FILE_NOT_FOUND, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			staged, path := filepath.Join(dir, "staged.tmp"), filepath.Join(dir, FileName)
			if err := os.WriteFile(staged, []byte("new"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			var handle windows.Handle
			setCalls, legacyCalls := 0, 0
			err := replaceConfigFileWith(staged, path, func(h windows.Handle, class uint32, data *byte, size uint32) error {
				setCalls++
				handle = h
				if class != windows.FileRenameInfoEx || data == nil || size < 16 {
					t.Errorf("invalid rename boundary: class=%d buffer=%p size=%d", class, data, size)
				}
				return tc.err
			}, func(old, next string) error {
				legacyCalls++
				if _, err := windows.GetFileType(handle); !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
					t.Fatalf("legacy rename received live staging handle: %v", err)
				}
				return os.Rename(old, next)
			})
			if setCalls != 1 || (tc.fallback && legacyCalls != 1) || (!tc.fallback && legacyCalls != 0) {
				t.Fatalf("calls: set=%d legacy=%d, fallback=%v", setCalls, legacyCalls, tc.fallback)
			}
			if _, closeErr := windows.GetFileType(handle); !errors.Is(closeErr, windows.ERROR_INVALID_HANDLE) {
				t.Fatalf("staging handle leaked: %v", closeErr)
			}
			if tc.fallback || tc.err == nil {
				if err != nil {
					t.Fatal(err)
				}
				if tc.fallback {
					got, readErr := os.ReadFile(path)
					if readErr != nil || string(got) != "new" {
						t.Fatalf("legacy replacement content = %q, error=%v", got, readErr)
					}
				}
			} else {
				var link *os.LinkError
				if !errors.As(err, &link) || link.Op != "rename" || link.Old != staged || link.New != path || !errors.Is(err, tc.err) {
					t.Fatalf("rename error = %v, want unchanged %v", err, tc.err)
				}
			}
		})
	}
}

func TestConfigReplacementPreservesCloseAndLegacyFailures(t *testing.T) {
	for _, closeInsideSetter := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-failure", true: "close-failure"}[closeInsideSetter], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "staged.tmp")
			if err := os.WriteFile(path, []byte("new"), 0o600); err != nil {
				t.Fatal(err)
			}
			legacyCalls := 0
			legacyErr := &os.LinkError{Op: "rename", Old: path, New: path + ".next", Err: windows.ERROR_ACCESS_DENIED}
			err := replaceConfigFileWith(path, path+".next", func(h windows.Handle, _ uint32, _ *byte, _ uint32) error {
				if closeInsideSetter {
					if err := windows.CloseHandle(h); err != nil {
						t.Fatal(err)
					}
				}
				return windows.ERROR_NOT_SUPPORTED
			}, func(_, _ string) error { legacyCalls++; return legacyErr })
			if closeInsideSetter {
				if legacyCalls != 0 || !errors.Is(err, windows.ERROR_NOT_SUPPORTED) || !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
					t.Fatalf("close failure lost or fallback attempted: calls=%d err=%v", legacyCalls, err)
				}
			} else if legacyCalls != 1 || err != legacyErr {
				t.Fatalf("legacy failure changed: calls=%d err=%v", legacyCalls, err)
			}
		})
	}
}

func TestConfigReplacementRejectsReadonlyDestination(t *testing.T) {
	dir := t.TempDir()
	staged, path := filepath.Join(dir, "staged.tmp"), filepath.Join(dir, FileName)
	if err := os.WriteFile(staged, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := replaceConfigFile(staged, path); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("readonly replacement = %v, want access denied", err)
	}
	for name, want := range map[string]string{path: "old", staged: "new"} {
		got, err := os.ReadFile(name)
		if err != nil || string(got) != want {
			t.Fatalf("failed replacement changed %s: %q, %v", name, got, err)
		}
	}
}

func TestConfigReplacementNativeLocalPaths(t *testing.T) {
	root := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	for _, variant := range []string{"absolute", "relative", "drive-relative", "slash-dot", "trailing-dot", "trailing-space", "extended", "long", "long-extended", "unicode"} {
		t.Run(variant, func(t *testing.T) {
			dir := filepath.Join(root, variant)
			if strings.HasPrefix(variant, "long") {
				for len(dir) < 300 {
					dir += `\` + strings.Repeat("long", 12)
				}
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			name := FileName
			if variant == "unicode" {
				name = "config-\U0001f9ea.json"
			}
			path := filepath.Join(dir, name)
			candidate := path
			switch variant {
			case "relative":
				candidate = variant + `\` + name
			case "drive-relative":
				candidate = filepath.VolumeName(root) + variant + `\` + name
			case "slash-dot":
				candidate = strings.ReplaceAll(dir, `\`, "/") + "/./" + name
			case "trailing-dot":
				candidate += "."
			case "trailing-space":
				candidate += " "
			case "extended", "long-extended":
				candidate = `\\?\` + path
			}
			for _, value := range []string{"first", "replacement"} {
				cfg := Default()
				cfg.Agent.GUID = value
				if err := Save(candidate, &cfg); err != nil {
					t.Fatalf("Save %s: %v", value, err)
				}
				got, err := Load(path)
				if err != nil || got.Agent.GUID != value {
					t.Fatalf("native destination after %s: guid=%q error=%v", value, got.Agent.GUID, err)
				}
			}
		})
	}
}
