//go:build windows

package config

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

const configReadSharing = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE

func holdWindowsConfigHandle(t *testing.T, path string, access, sharing uint32) windows.Handle {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, access, sharing, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.CloseHandle(h); err != nil {
			t.Errorf("close fixture handle: %v", err)
		}
	})
	return h
}

func TestConfigReadersAllowExistingDeleteHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(`{"agent":{"guid":"delete-sharing-fixture"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// DELETE access is held only by this fixture, without deleting anything.
	// This forces the original reader's sharing failure without scheduling races.
	holdWindowsConfigHandle(t, path, windows.DELETE, configReadSharing)
	if _, err := os.ReadFile(path); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("original reader control = %v, want sharing violation", err)
	}
	for name, load := range map[string]func(string) (AgentConfig, error){"Load": Load, "loadUnlocked": loadUnlocked} {
		t.Run(name, func(t *testing.T) {
			cfg, err := load(path)
			if err != nil || cfg.Agent.GUID != "delete-sharing-fixture" {
				t.Fatalf("read with DELETE handle: guid=%q error=%v", cfg.Agent.GUID, err)
			}
		})
	}
}

func TestConfigReaderAllowsSaveAndKeepsOldSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	old := Default()
	old.Agent.GUID = "old-snapshot"
	old.Identity.PublicKeySPKIB64 = strings.Repeat("o", 65536)
	if err := Save(path, &old); err != nil {
		t.Fatal(err)
	}
	oldBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := openConfigRead(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	next := Default()
	next.Agent.GUID = "new-snapshot"
	next.Identity.PublicKeySPKIB64 = strings.Repeat("n", 32768)
	if err := Save(path, &next); err != nil {
		t.Fatalf("Save while reader held: %v", err)
	}
	gotOld, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(gotOld, oldBytes) {
		t.Fatalf("held reader lost complete old snapshot: bytes=%d want=%d error=%v", len(gotOld), len(oldBytes), err)
	}
	gotNext, err := Load(path)
	if err != nil || !reflect.DeepEqual(gotNext, next) {
		t.Fatalf("fresh reader lost complete new snapshot: guid=%q revision=%d error=%v", gotNext.Agent.GUID, gotNext.Agent.State.Revision, err)
	}
}

func TestConfigReaderRequestsReadAccessOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(`{}`), 0o400); err != nil {
		t.Fatal(err)
	}
	// Any requested write or DELETE access would conflict with this handle.
	holdWindowsConfigHandle(t, path, windows.GENERIC_READ, windows.FILE_SHARE_READ)
	reader, err := openConfigRead(path)
	if err != nil {
		t.Fatalf("reader requested incompatible access: %v", err)
	}
	defer reader.Close()
	if _, err := reader.Write([]byte("x")); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("read handle Write = %v, want access denied", err)
	}
}

func TestConfigReadersCloseHandlesAfterDecode(t *testing.T) {
	for _, content := range []string{`{}`, "", `{"agent":`} {
		t.Run(content, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), FileName)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _ = Load(path)
			_, _ = loadUnlocked(path)
			// No live reader may remain, including after a JSON parse error.
			holdWindowsConfigHandle(t, path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0)
		})
	}
}

func TestConfigReaderClosesHandleAfterReadError(t *testing.T) {
	path := t.TempDir()
	if _, err := readConfigFile(path); err == nil {
		t.Fatal("reading directory succeeded")
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatalf("exclusive open after read error: %v", err)
	}
	if err := windows.CloseHandle(h); err != nil {
		t.Fatal(err)
	}
}

func TestConfigReaderPreservesSharingErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	holdWindowsConfigHandle(t, path, windows.GENERIC_READ, 0)
	_, err := Load(path)
	var pathError *os.PathError
	if !errors.As(err, &pathError) || pathError.Path != path || pathError.Op != "open" ||
		!errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("exclusive handle error = %v, want unchanged open sharing error", err)
	}
}

func TestConfigReaderNativeLocalPathParity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	content := []byte(`{"agent":{"guid":"path-fixture"}}`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	paths := map[string]string{
		"absolute":       path,
		"relative":       `.` + string(os.PathSeparator) + FileName,
		"drive-relative": filepath.VolumeName(dir) + FileName,
		"slash":          strings.ReplaceAll(path, `\`, "/"),
		"extended-drive": `\\?\` + path,
		"dot-component":  dir + `\.\` + FileName,
		"trailing-dot":   path + ".",
		"trailing-space": path + " ",
	}
	longDir := dir
	for len(longDir) < 300 {
		longDir += `\` + strings.Repeat("long", 12)
	}
	if err := os.MkdirAll(`\\?\`+longDir, 0o700); err != nil {
		t.Fatal(err)
	}
	longPath := longDir + `\` + FileName
	if err := os.WriteFile(`\\?\`+longPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	paths["long-native"] = longPath
	paths["long-extended"] = `\\?\` + longPath
	paths["long-alias-trailing-dot"] = longPath + "."
	paths["long-alias-trailing-space"] = longPath + " "
	paths["long-alias-slash-dot"] = strings.ReplaceAll(longDir, `\`, "/") + "/./" + FileName
	for name, candidate := range paths {
		t.Run(name, func(t *testing.T) {
			want, wantErr := os.ReadFile(candidate)
			got, err := readConfigFile(candidate)
			// Older Go/Windows path fixups can reject these aliases. Preserve
			// that result as well as native normalization on current Windows.
			if wantErr != nil && strings.HasPrefix(name, "long-alias-") {
				var wantPath, gotPath *os.PathError
				if !errors.As(wantErr, &wantPath) || !errors.As(err, &gotPath) ||
					gotPath.Op != wantPath.Op || gotPath.Path != wantPath.Path || !errors.Is(gotPath.Err, wantPath.Err) {
					t.Fatalf("native alias error = %v, want %v", err, wantErr)
				}
				return
			}
			if wantErr != nil {
				t.Fatalf("native os.ReadFile fixture: %v", wantErr)
			}
			if err != nil || !bytes.Equal(got, want) || !bytes.Equal(got, content) {
				t.Fatalf("native read parity: got=%q want=%q error=%v", got, want, err)
			}
		})
	}
}

func TestLegacyConfigReadPathLeavesNonDrivePathsUntouched(t *testing.T) {
	long := strings.Repeat("component\\", 30)
	for _, path := range []string{
		"", `C:\agent.json`, long + FileName, `C:` + long + FileName,
		`\` + long + FileName, `\\server\share\` + long + FileName,
		`//server/share/` + long + FileName, `\\?\C:\` + long + FileName,
		`\\?\UNC\server\share\` + long + FileName, `\\.\C:\` + long + FileName,
		`C:\` + long + `..\` + FileName,
	} {
		if got := legacyConfigReadPath(path); got != path {
			t.Errorf("path changed: %q -> %q", path, got)
		}
	}
}

func TestLegacyConfigReadPathLocalFixture(t *testing.T) {
	dir := t.TempDir()
	for len(dir) < 300 {
		dir += `\` + strings.Repeat("legacy", 8)
	}
	if err := os.MkdirAll(`\\?\`+dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := dir + `\` + FileName
	want := []byte(`{"agent":{"guid":"legacy-path-fixture"}}`)
	if err := os.WriteFile(`\\?\`+path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, strings.ReplaceAll(dir, `\`, "/") + "/.//" + FileName} {
		extended := legacyConfigReadPath(candidate)
		if extended != `\\?\`+path {
			t.Fatalf("legacy conversion = %q, want %q", extended, `\\?\`+path)
		}
		got, err := readConfigFile(extended)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("legacy local path fixture: got=%q error=%v", got, err)
		}
	}
}
