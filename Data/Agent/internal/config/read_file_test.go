package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestConfigReadersPreserveLoadSemantics(t *testing.T) {
	for name, load := range map[string]func(string) (AgentConfig, error){
		"Load": Load, "loadUnlocked": loadUnlocked,
	} {
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				content string
				invalid bool
			}{
				{name: "empty"},
				{name: "defaults", content: `{}`},
				{name: "ordinary", content: `{"server_url":"https://engine.example/","agent":{"guid":"reader-fixture"},"unknown":true}`},
				{name: "invalid", content: `{"agent":`, invalid: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					dir := t.TempDir()
					path := filepath.Join(dir, FileName)
					if err := os.WriteFile(path, []byte(tc.content), 0o400); err != nil {
						t.Fatal(err)
					}
					before, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					cfg, err := load(path)
					if tc.invalid {
						if err == nil || !strings.HasPrefix(err.Error(), "parse config: ") {
							t.Fatalf("invalid JSON error = %v", err)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						want := Default()
						if tc.content != "" {
							want.ApplyDefaults()
						}
						if tc.name == "ordinary" {
							want.ServerURL = "https://engine.example/"
							want.Agent.GUID = "reader-fixture"
						}
						if !reflect.DeepEqual(cfg, want) {
							t.Fatalf("loaded config = %#v, want %#v", cfg, want)
						}
					}
					raw, err := os.ReadFile(path)
					if err != nil || string(raw) != tc.content {
						t.Fatalf("reader changed file: %q, %v", raw, err)
					}
					after, err := os.Stat(path)
					if err != nil || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) {
						t.Fatalf("reader changed file metadata: before=%v after=%v error=%v", before, after, err)
					}
					entries, err := os.ReadDir(dir)
					if err != nil || len(entries) != 1 || entries[0].Name() != FileName {
						t.Fatalf("reader created sibling files: %v, %v", entries, err)
					}
				})
			}
			t.Run("missing-parent", func(t *testing.T) {
				dir := t.TempDir()
				cfg, err := load(filepath.Join(dir, "missing", FileName))
				if err != nil || !reflect.DeepEqual(cfg, Default()) {
					t.Fatalf("missing config = %#v, %v", cfg, err)
				}
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 0 {
					t.Fatalf("reader created parent or lock: %v, %v", entries, err)
				}
			})
			t.Run("readonly-directory", func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, FileName)
				if err := os.WriteFile(path, []byte(`{}`), 0o400); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(dir, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
				if _, err := load(path); err != nil {
					t.Fatalf("read-only directory: %v", err)
				}
				if _, err := load(filepath.Join(dir, "missing.json")); err != nil {
					t.Fatalf("missing config in read-only directory: %v", err)
				}
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 1 {
					t.Fatalf("read-only loads created sidecars: %v, %v", entries, err)
				}
			})
		})
	}
}

func TestReadConfigFilePreservesPathErrors(t *testing.T) {
	for name, path := range map[string]string{
		"empty": "", "nul": "invalid\x00path", "missing": filepath.Join(t.TempDir(), "missing", FileName), "directory": t.TempDir(),
	} {
		t.Run(name, func(t *testing.T) {
			_, want := os.ReadFile(path)
			_, got := readConfigFile(path)
			var wantPath, gotPath *os.PathError
			if !errors.As(want, &wantPath) || !errors.As(got, &gotPath) {
				t.Fatalf("path errors: got %v, want %v", got, want)
			}
			if gotPath.Op != wantPath.Op || gotPath.Path != wantPath.Path || !errors.Is(gotPath.Err, wantPath.Err) {
				t.Fatalf("path error = %#v, want %#v", gotPath, wantPath)
			}
		})
	}
}

func TestConcurrentConfigReadersSeeCompleteAtomicSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	configFor := func(marker string) AgentConfig {
		cfg := Default()
		cfg.Agent.GUID = marker
		cfg.Identity.PublicKeySPKIB64 = strings.Repeat(marker, 8192)
		return cfg
	}
	initial := configFor("old")
	if err := Save(path, &initial); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	failures := make(chan error, 5)
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < 16; i++ {
			cfg := configFor([]string{"new", "old"}[i%2])
			if err := Save(path, &cfg); err != nil {
				failures <- fmt.Errorf("atomic Save: %w", err)
				return
			}
		}
	}()
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for j := 0; j < 32; j++ {
				cfg, err := Load(path)
				if err != nil {
					failures <- fmt.Errorf("concurrent Load: %w", err)
					return
				}
				if (cfg.Agent.GUID != "old" && cfg.Agent.GUID != "new") ||
					cfg.Identity.PublicKeySPKIB64 != strings.Repeat(cfg.Agent.GUID, 8192) {
					failures <- errors.New("reader observed incomplete or mixed configuration")
					return
				}
			}
		}()
	}
	close(start)
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}
