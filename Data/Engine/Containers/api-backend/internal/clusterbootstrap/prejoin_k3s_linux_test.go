package clusterbootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPrejoinK3sIntentAndIndependentReconciliation(t *testing.T) {
	for _, mode := range []string{"complete", "lost acknowledgement", "partial install", "missing artifacts", "artifact tamper", "fresh proof loss"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			p, j, ledger, parent, install, material := prejoinHostFixture(t)
			host := PrejoinHostActions{CheckFresh: func(context.Context) error { return nil }, Prepare: func(context.Context, *os.File, *os.File) error { return nil }, Prove: func(context.Context) error { return nil }}
			if _, err := p.preparePrejoinHost(ctx, j, install, material, host, parent, false); err != nil {
				t.Fatal(err)
			}
			artifacts, _, _ := preparationArtifactsForSourceFixture(t, p.configuration.wire.Expected.Source)
			system := t.TempDir()
			for _, path := range []string{"var/lib", "usr/local/bin"} {
				if err := os.MkdirAll(filepath.Join(system, path), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "missing artifacts" {
				if _, err := p.prejoinArtifactsTransaction(ctx, j, install, material, artifacts, artifacts.postgresReference, host.Prove, parent, system, false); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "artifact tamper" {
				if err := os.WriteFile(filepath.Join(system, "usr/local/bin/k3s"), []byte("changed"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			installed, calls := false, 0
			actions := PrejoinK3sActions{
				CheckFresh: func(context.Context) error {
					if mode == "fresh proof loss" {
						return ErrMutationUnknown
					}
					return nil
				},
				Install: func(_ context.Context, hostLock, identityLock *os.File) error {
					calls++
					if readMutationFixture(t, ledger).Steps["install_k3s"].State != "intent" {
						t.Fatal("installation preceded intent")
					}
					requirePrejoinHostLock(t, hostLock, j.lock.Name())
					if identityLock == nil {
						t.Fatal("missing identity fence")
					}
					identityConsumerBlocked(t, install.Root)
					if mode == "partial install" {
						return ErrMutationUnknown
					}
					installed = true
					return nil
				},
				Prove: func(context.Context) error {
					if !installed {
						return ErrMutationUnknown
					}
					return nil
				},
			}
			check := j.check
			j.check = func(ctx context.Context) error {
				if mode == "lost acknowledgement" && readMutationFixture(t, ledger).Steps["install_k3s"].State == "applied" {
					return ErrSessionAuthority
				}
				return check(ctx)
			}
			_, err := p.prejoinArtifactPhaseTransaction(ctx, j, install, material, artifacts, artifacts.postgresReference, host.Prove, parent, system, false, &actions)
			if (err == nil) != (mode == "complete") {
				t.Fatal("wrong installation outcome", err)
			}
			j.check = check
			record, exists := readMutationFixture(t, ledger).Steps["install_k3s"]
			if mode == "missing artifacts" || mode == "artifact tamper" || mode == "fresh proof loss" {
				if exists || calls != 0 {
					t.Fatal("invalid prerequisite caused effect/intent")
				}
				return
			}
			if calls != 1 || !exists || (record.State == "applied") != installed {
				t.Fatal("wrong durable outcome", record.State, calls)
			}
			if _, err := p.prejoinArtifactPhaseTransaction(ctx, j, install, material, artifacts, artifacts.postgresReference, host.Prove, parent, system, false, &actions); err == nil || calls != 1 {
				t.Fatal("installation replayed")
			}
			if _, err := p.prejoinArtifactPhaseTransaction(ctx, j, install, material, artifacts, artifacts.postgresReference, host.Prove, parent, system, true, &actions); err == nil {
				t.Fatal("producer proved itself")
			}
			j = reopenSourcePublication(t, p, j, ledger)
			result, err := p.prejoinArtifactPhaseTransaction(ctx, j, install, material, artifacts, artifacts.postgresReference, host.Prove, parent, system, true, &actions)
			if (err == nil) != installed || installed && result != record.InputSHA256 || calls != 1 {
				t.Fatal("invalid independent proof", err)
			}
			if _, err := p.prejoinArtifactsTransaction(ctx, j, install, material, artifacts, artifacts.postgresReference, host.Prove, parent, system, true); err == nil {
				t.Fatal("earlier artifact phase accepted runtime effects")
			}
			identityConsumerBlocked(t, install.Root)
		})
	}
}
