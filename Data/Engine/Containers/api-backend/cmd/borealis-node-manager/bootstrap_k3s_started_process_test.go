package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bootstrapStartedK3sFixture() string {
	return strings.NewReplacer("ActiveState=inactive", "ActiveState=active", "SubState=dead", "SubState=running", "MainPID=0", "MainPID=42").Replace(bootstrapInertK3sFixture) + "InvocationID=" + strings.Repeat("a", 32) + "\n"
}

func TestBootstrapStartedK3sUnit(t *testing.T) {
	raw := bootstrapStartedK3sFixture()
	got, err := parseBootstrapStartedK3s(raw)
	if err != nil || got.pid != 42 || got.invocation != strings.Repeat("a", 32) {
		t.Fatal("fixed started unit rejected", err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		key, _, _ := strings.Cut(line, "=")
		for _, replacement := range []string{key + "=foreign", key + "=", ""} {
			if replacement == line {
				continue
			}
			if _, err := parseBootstrapStartedK3s(strings.Replace(raw, line+"\n", replacement+"\n", 1)); err == nil {
				t.Fatalf("changed %s admitted", key)
			}
		}
	}
	for _, unsafe := range []string{raw + "MainPID=42\n", strings.TrimSuffix(raw, "\n"), bootstrapInertK3sFixture, strings.Replace(raw, "MainPID=42", "MainPID=042", 1), strings.Replace(raw, "UnitFileState=disabled", "UnitFileState=enabled", 1), strings.Replace(raw, strings.Repeat("a", 32), strings.Repeat("0", 32), 1)} {
		if _, err := parseBootstrapStartedK3s(unsafe); err == nil {
			t.Fatal("ambiguous unit admitted")
		}
	}
}

func TestBootstrapStartedK3sProcessCustody(t *testing.T) {
	for _, mode := range []string{"valid", "service restart", "unit enabled", "pid reused", "process replaced", "UID changed", "mount changed", "host changed", "exe changed", "exe during hash", "hash rejected", "claim lost", "ignored failure", "canceled", "consumer failed"} {
		t.Run(mode, func(t *testing.T) {
			proc := t.TempDir()
			write := func(name, data string) {
				t.Helper()
				path := filepath.Join(proc, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			link := func(name, target string) {
				t.Helper()
				path := filepath.Join(proc, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			replace := func(name, target string) {
				t.Helper()
				if err := os.Remove(filepath.Join(proc, name)); err != nil {
					t.Fatal(err)
				}
				link(name, target)
			}
			write("42/stat", sourceKubeletStatFixture("100"))
			write("42/status", "Uid:\t0\t0\t0\t0\n")
			write("host-net", "net")
			write("host-mnt", "mnt")
			write("other", "other")
			for _, pid := range []string{"1", "42"} {
				link(pid+"/root", proc)
				link(pid+"/ns/net", filepath.Join(proc, "host-net"))
				link(pid+"/ns/mnt", filepath.Join(proc, "host-mnt"))
			}
			link("42/exe", "/usr/bin/true")
			binary, err := os.ReadFile("/usr/bin/true")
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(binary)
			pin := clusterbootstrap.K3sAssetPin{Name: "bin/k3s", Size: int64(len(binary)), SHA256: hex.EncodeToString(digest[:])}
			raw, lost := bootstrapStartedK3sFixture(), false
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			read := func(context.Context) ([]byte, error) { return []byte(raw), nil }
			check := func(context.Context) error {
				if lost {
					return errBootstrapSource
				}
				return nil
			}
			hash := func(ctx context.Context, file *os.File) error {
				if mode == "hash rejected" {
					return errBootstrapSource
				}
				err := hashSourceKubeletExecutable(ctx, file, pin)
				if mode == "exe during hash" {
					replace("42/exe", "/usr/bin/false")
				}
				return err
			}
			var escaped func(context.Context) error
			err = withBootstrapStartedK3sProcess(ctx, proc, read, hash, check, func(refresh func(context.Context) error) error {
				escaped = refresh
				switch mode {
				case "service restart":
					raw = strings.Replace(raw, strings.Repeat("a", 32), strings.Repeat("b", 32), 1)
				case "unit enabled":
					raw = strings.Replace(raw, "UnitFileState=disabled", "UnitFileState=enabled", 1)
				case "pid reused":
					write("42/stat", sourceKubeletStatFixture("101"))
				case "process replaced":
					if err := os.Rename(filepath.Join(proc, "42"), filepath.Join(proc, "old")); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(filepath.Join(proc, "42"), 0700); err != nil {
						t.Fatal(err)
					}
				case "UID changed":
					write("42/status", "Uid:\t1\t1\t1\t1\n")
				case "mount changed":
					replace("42/ns/mnt", filepath.Join(proc, "other"))
				case "host changed":
					replace("42/ns/net", filepath.Join(proc, "other"))
					replace("1/ns/net", filepath.Join(proc, "other"))
				case "exe changed":
					replace("42/exe", "/usr/bin/false")
				case "claim lost":
					lost = true
				case "ignored failure":
					lost = true
					_ = refresh(ctx)
					lost = false
					return nil
				case "canceled":
					cancel()
				case "consumer failed":
					return errBootstrapSource
				}
				return refresh(ctx)
			})
			if mode == "valid" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err != errBootstrapSource {
				t.Fatalf("unsafe custody: %v", err)
			}
			if escaped != nil && escaped(context.Background()) == nil {
				t.Fatal("escaped capability remained active")
			}
		})
	}
}
