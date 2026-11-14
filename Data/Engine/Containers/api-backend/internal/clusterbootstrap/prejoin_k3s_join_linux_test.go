package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestPrejoinK3sConfigurationIntentAndIndependentProof(t *testing.T) {
	for _, mode := range []string{"complete", "lost acknowledgement", "partial configuration", "missing installation", "installation drift", "artifact tamper", "foreign claim", "closed material", "fresh proof loss"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			p, j, ledger, parent, install, identity := prejoinHostFixture(t)
			host := PrejoinHostActions{CheckFresh: func(context.Context) error { return nil }, Prepare: func(context.Context, *os.File, *os.File) error { return nil }, Prove: func(context.Context) error { return nil }}
			if _, err := p.preparePrejoinHost(ctx, j, install, identity, host, parent, false); err != nil {
				t.Fatal(err)
			}
			artifacts, _, _ := preparationArtifactsForSourceFixture(t, p.configuration.wire.Expected.Source)
			system := t.TempDir()
			for _, path := range []string{"var/lib", "usr/local/bin"} {
				if err := os.MkdirAll(filepath.Join(system, path), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := p.prejoinArtifactsTransaction(ctx, j, install, identity, artifacts, artifacts.postgresReference, host.Prove, parent, system, false); err != nil {
				t.Fatal(err)
			}
			installed := false
			runtime := PrejoinK3sActions{CheckFresh: func(context.Context) error { return nil }, Install: func(context.Context, *os.File, *os.File) error { installed = true; return nil }, Prove: func(context.Context) error {
				if !installed {
					return ErrMutationUnknown
				}
				return nil
			}}
			if mode != "missing installation" {
				if _, err := p.prejoinArtifactPhaseTransaction(ctx, j, install, identity, artifacts, artifacts.postgresReference, host.Prove, parent, system, false, &runtime); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "installation drift" {
				installed = false
			}
			if mode == "artifact tamper" {
				if os.WriteFile(filepath.Join(system, "usr/local/bin/k3s"), []byte("changed"), 0755) != nil {
					t.Fatal("fixture tamper")
				}
			}
			ca, _, _ := joinCAFixture(t)
			h := sha256.Sum256(ca)
			token := []byte("K10" + hex.EncodeToString(h[:]) + "::server:private journal fixture")
			expected := p.configuration.wire.Expected
			if mode == "foreign claim" {
				expected.Target.Generation++
			}
			material, err := NewK3sJoinMaterial(expected, install.Binding, token, ca)
			if err != nil {
				t.Fatal(err)
			}
			defer material.Close()
			if mode == "closed material" {
				material.Close()
			}
			configured, calls := false, 0
			actions := PrejoinK3sJoinActions{
				Installed: runtime.Prove,
				CheckFresh: func(context.Context) error {
					if mode == "fresh proof loss" {
						return ErrMutationUnknown
					}
					return nil
				},
				Configure: func(_ context.Context, hostLock, identityLock *os.File) error {
					calls++
					if readMutationFixture(t, ledger).Steps["configure_k3s"].State != "intent" {
						t.Fatal("configuration preceded durable intent")
					}
					requirePrejoinHostLock(t, hostLock, j.lock.Name())
					if identityLock == nil {
						t.Fatal("identity exclusion missing")
					}
					identityConsumerBlocked(t, install.Root)
					if mode == "partial configuration" {
						return ErrMutationUnknown
					}
					configured = true
					return nil
				},
				Prove: func(context.Context) error {
					if !configured {
						return ErrMutationUnknown
					}
					return nil
				},
			}
			phase := &prejoinK3sJoinPhase{material, actions}
			check := j.check
			j.check = func(ctx context.Context) error {
				if mode == "lost acknowledgement" && readMutationFixture(t, ledger).Steps["configure_k3s"].State == "applied" {
					return ErrSessionAuthority
				}
				return check(ctx)
			}
			_, err = p.prejoinArtifactPhaseTransactionWithJoin(ctx, j, install, identity, artifacts, artifacts.postgresReference, host.Prove, parent, system, false, nil, phase)
			if (err == nil) != (mode == "complete") {
				t.Fatal("wrong configuration outcome", err)
			}
			j.check = check
			record, exists := readMutationFixture(t, ledger).Steps["configure_k3s"]
			if mode != "complete" && mode != "lost acknowledgement" && mode != "partial configuration" {
				if exists || calls != 0 {
					t.Fatal("invalid prerequisite caused intent/effect")
				}
				return
			}
			if calls != 1 || !exists || (record.State == "applied") != configured {
				t.Fatal("wrong durable outcome", record.State, calls)
			}
			if _, err = p.prejoinArtifactPhaseTransactionWithJoin(ctx, j, install, identity, artifacts, artifacts.postgresReference, host.Prove, parent, system, false, nil, phase); err == nil || calls != 1 {
				t.Fatal("configuration replayed")
			}
			if _, err = p.prejoinArtifactPhaseTransactionWithJoin(ctx, j, install, identity, artifacts, artifacts.postgresReference, host.Prove, parent, system, true, nil, phase); err == nil {
				t.Fatal("producer proved itself")
			}
			j = reopenSourcePublication(t, p, j, ledger)
			if _, err := p.prejoinArtifactPhaseTransactionWithJoin(ctx, j, install, identity, artifacts, artifacts.postgresReference, host.Prove, parent, system, true, nil, phase); err == nil {
				t.Fatal("old claim's private material accepted")
			}
			fresh, err := NewK3sJoinMaterial(p.configuration.wire.Expected, install.Binding, token, ca)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			phase.material = fresh
			result, err := p.prejoinArtifactPhaseTransactionWithJoin(ctx, j, install, identity, artifacts, artifacts.postgresReference, host.Prove, parent, system, true, nil, phase)
			if (err == nil) != configured || configured && result != record.InputSHA256 || calls != 1 {
				t.Fatal("wrong independent configuration proof", err)
			}
			if _, err = p.prejoinArtifactPhaseTransaction(ctx, j, install, identity, artifacts, artifacts.postgresReference, host.Prove, parent, system, true, &runtime); err == nil {
				t.Fatal("earlier installation proof accepted configuration")
			}
			other, err := NewK3sJoinMaterial(p.configuration.wire.Expected, install.Binding, append(bytes.Clone(token), 'x'), ca)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			if _, err = p.prejoinArtifactPhaseTransactionWithJoin(ctx, j, install, identity, artifacts, artifacts.postgresReference, host.Prove, parent, system, true, nil, &prejoinK3sJoinPhase{other, actions}); err == nil {
				t.Fatal("changed source token relabeled retained effect")
			}
			raw, err := os.ReadFile(filepath.Join(ledger, mutationJournalName))
			if err != nil || bytes.Contains(raw, token) {
				t.Fatal("private material entered journal")
			}
			identityConsumerBlocked(t, install.Root)
		})
	}
}
