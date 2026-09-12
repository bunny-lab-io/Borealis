package clusterbootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparationInputsBindRealBundleAndRecheckBeforeExport(t *testing.T) {
	e, manifest, members := archiveFixture(t)
	b, err := stageFixture(t, context.Background(), e, manifest, pack(t, members), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	expected, runtime := preparationFixture()
	expected.Source = e
	config, err := NewPreparationConfiguration(expected, runtime)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	check := func(context.Context) error { calls++; return nil }
	p, err := BindPreparationInputs(context.Background(), b, config, check)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("missing authority checks around source verification")
	}
	raw, err := p.Configuration(context.Background(), expected, check)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatal("export reused previous authority")
	}
	if _, err := ImportPreparationConfiguration(raw, expected); err != nil {
		t.Fatal(err)
	}
	if source, err := p.SourceBundle(context.Background(), expected, check); err != nil || source != b || calls != 6 {
		t.Fatal("source export missed authority or changed bundle")
	}
	if _, err := json.Marshal(p); !errors.Is(err, ErrPreparationConfig) {
		t.Fatal("generic input serialization accepted")
	}
	changed := expected
	changed.Target.Generation++
	if _, err := p.Configuration(context.Background(), changed, check); !errors.Is(err, ErrPreparationConfig) {
		t.Fatal("changed claim exported private inputs")
	}
	if _, err := p.Configuration(context.Background(), expected, func(context.Context) error { return errors.New("private-test") }); !errors.Is(err, ErrSessionAuthority) {
		t.Fatal("failed authority exported configuration")
	}
	if _, err := p.SourceBundle(context.Background(), expected, func(context.Context) error { return errors.New("private-test") }); !errors.Is(err, ErrSessionAuthority) {
		t.Fatal("failed authority exported source")
	}
	root := b.root
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owned scratch not closed")
	}
	if _, err := p.Configuration(context.Background(), expected, check); err == nil {
		t.Fatal("closed inputs remained usable")
	}
}

func TestPreparationInputsRejectChangedScratchAndIncompleteProof(t *testing.T) {
	e, manifest, members := archiveFixture(t)
	archive := pack(t, members)
	write := func(t *testing.T, path string, raw []byte) {
		t.Helper()
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, mutate := range map[string]func(*testing.T, *Bundle, *PreparationExpected){
		"source bytes": func(t *testing.T, b *Bundle, _ *PreparationExpected) {
			write(t, filepath.Join(b.SourcePath(), "Engine.sh"), []byte("#!/bin/sh\nexit 9\n"))
		},
		"binary bytes": func(t *testing.T, b *Bundle, _ *PreparationExpected) {
			write(t, b.ManagerPath(), []byte("not-the-verified-binary"))
		},
		"binary mode": func(t *testing.T, b *Bundle, _ *PreparationExpected) {
			if err := os.Chmod(b.ManagerPath(), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"extra Git metadata": func(t *testing.T, b *Bundle, _ *PreparationExpected) {
			write(t, filepath.Join(b.SourcePath(), ".git", "info", "attributes"), []byte("* filter=outside\n"))
		},
		"Git config changed": func(t *testing.T, b *Bundle, _ *PreparationExpected) {
			write(t, filepath.Join(b.SourcePath(), ".git", "config"), []byte("[core]\nfsmonitor=/tmp/private-test\n"))
		},
		"extra source": func(t *testing.T, b *Bundle, _ *PreparationExpected) {
			write(t, filepath.Join(b.SourcePath(), "untracked.sh"), []byte("exit 0\n"))
		},
		"tracked symlink": func(t *testing.T, b *Bundle, _ *PreparationExpected) {
			p := filepath.Join(b.SourcePath(), "Engine.sh")
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/etc/hostname", p); err != nil {
				t.Fatal(err)
			}
		},
		"source directory symlink": func(t *testing.T, b *Bundle, _ *PreparationExpected) {
			p := b.SourcePath()
			if err := os.Rename(p, p+"-old"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("source-old", p); err != nil {
				t.Fatal(err)
			}
		},
		"archive bytes": func(t *testing.T, b *Bundle, _ *PreparationExpected) {
			raw, err := os.ReadFile(b.ArchivePath())
			if err != nil {
				t.Fatal(err)
			}
			raw[len(raw)/2] ^= 1
			write(t, b.ArchivePath(), raw)
		},
		"archive mode": func(t *testing.T, b *Bundle, _ *PreparationExpected) {
			if err := os.Chmod(b.ArchivePath(), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"K3s manifest mismatch": func(_ *testing.T, _ *Bundle, e *PreparationExpected) { e.K3sVersion = "v1.36.4+k3s1" },
		"release mismatch":      func(_ *testing.T, _ *Bundle, e *PreparationExpected) { e.Source.Release = "2026.09.999-rc.2" },
		"commit mismatch":       func(_ *testing.T, _ *Bundle, e *PreparationExpected) { e.Source.SourceSHA = strings.Repeat("e", 40) },
		"zero bundle":           func(_ *testing.T, b *Bundle, _ *PreparationExpected) { b.asset = assetIdentity{} },
	} {
		t.Run(name, func(t *testing.T) {
			b, err := stageFixture(t, context.Background(), e, manifest, archive, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = b.Close() })
			expected, runtime := preparationFixture()
			expected.Source = e
			mutate(t, b, &expected)
			c, err := NewPreparationConfiguration(expected, runtime)
			if err != nil {
				t.Fatal(err)
			}
			p, err := BindPreparationInputs(context.Background(), b, c, func(context.Context) error { return nil })
			if !errors.Is(err, ErrPreparationConfig) || p != nil {
				t.Fatal("changed/unverified preparation inputs accepted")
			}
		})
	}
	for _, when := range []int{1, 2} {
		b, err := stageFixture(t, context.Background(), e, manifest, archive, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = b.Close() })
		expected, runtime := preparationFixture()
		expected.Source = e
		c, _ := NewPreparationConfiguration(expected, runtime)
		calls := 0
		p, err := BindPreparationInputs(context.Background(), b, c, func(context.Context) error {
			calls++
			if calls == when {
				return errors.New("private-test")
			}
			return nil
		})
		if !errors.Is(err, ErrSessionAuthority) || p != nil || calls != when {
			t.Fatal("lost boundary authority accepted")
		}
	}
	// Cancellation observed even if the authority callback accidentally reports success.
	expected, runtime := preparationFixture()
	expected.Source = e
	c, _ := NewPreparationConfiguration(expected, runtime)
	b, err := stageFixture(t, context.Background(), e, manifest, archive, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := BindPreparationInputs(ctx, b, c, func(context.Context) error { cancel(); return nil }); !errors.Is(err, ErrSessionAuthority) {
		t.Fatal("callback cancellation ignored")
	}
	if !bytes.Equal(c.raw, mustPreparationExport(t, c)) {
		t.Fatal("failed binding mutated configuration")
	}
}

func mustPreparationExport(t *testing.T, c *PreparationConfiguration) []byte {
	t.Helper()
	raw, err := c.Export()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
