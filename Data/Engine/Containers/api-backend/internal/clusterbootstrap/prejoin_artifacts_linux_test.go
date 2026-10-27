package clusterbootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPrejoinArtifactIntentAndIndependentReconciliation(t *testing.T) {
	for _, mode := range []string{"complete", "lost acknowledgement", "partial effect", "source tamper", "host proof loss", "wrong postgres", "missing host"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			p, j, ledger, parent, install, material := prejoinHostFixture(t)
			actions := PrejoinHostActions{CheckFresh: func(context.Context) error { return nil }, Prepare: func(context.Context, *os.File, *os.File) error { return nil }, Prove: func(context.Context) error { return nil }}
			if _, err := p.preparePrejoinHost(ctx, j, install, material, actions, parent, false); err != nil {
				t.Fatal(err)
			}
			artifacts, _, _ := preparationArtifactsForSourceFixture(t, p.configuration.wire.Expected.Source)
			system := t.TempDir()
			for _, path := range []string{"var/lib", "usr/local/bin"} {
				if os.MkdirAll(filepath.Join(system, path), 0700) != nil {
					t.Fatal("system fixture")
				}
			}
			pg := artifacts.postgresReference
			if mode == "wrong postgres" {
				pg = PostgresImageRepository + "@sha256:" + mutationDigest("wrong postgres")
			}
			if mode == "missing host" {
				delete(j.state.Steps, "prepare_host")
			}
			check := j.check
			lost, injected := false, false
			j.check = func(ctx context.Context) error {
				state := readMutationFixture(t, ledger).Steps["stage_images"]
				if mode == "lost acknowledgement" && state.State == "applied" {
					lost = true
				}
				if !injected && (mode == "partial effect" || mode == "source tamper") {
					entries, _ := os.ReadDir(filepath.Join(system, "var/lib/rancher/k3s/agent/images"))
					if len(entries) > 0 {
						injected = true
						if state.State != "intent" {
							t.Fatal("effect preceded durable intent")
						}
						if mode == "partial effect" {
							lost = true
						} else if os.WriteFile(filepath.Join(parent, "Borealis/Engine.sh"), []byte("changed"), 0755) != nil {
							t.Fatal("tamper fixture")
						}
					}
				}
				if lost {
					return ErrSessionAuthority
				}
				return check(ctx)
			}
			hostProof := func(context.Context) error {
				identityConsumerBlocked(t, install.Root)
				requirePrejoinHostLock(t, j.lock, j.lock.Name())
				if mode == "host proof loss" {
					return ErrMutationUnknown
				}
				return nil
			}
			result, err := p.prejoinArtifactsTransaction(ctx, j, install, material, artifacts, pg, hostProof, parent, system, false)
			if (err == nil) != (mode == "complete") {
				t.Fatal("wrong publication outcome", err)
			}
			record, exists := readMutationFixture(t, ledger).Steps["stage_images"]
			if mode == "wrong postgres" || mode == "missing host" || mode == "host proof loss" {
				if exists {
					t.Fatal("failed admission persisted intent")
				}
				if _, err := os.Stat(filepath.Join(system, "var/lib/rancher")); !os.IsNotExist(err) {
					t.Fatal("failed admission mutated target")
				}
				return
			}
			complete := mode == "complete" || mode == "lost acknowledgement"
			if !exists || (record.State == "applied") != complete {
				t.Fatal("wrong durable outcome", record.State)
			}
			j.check = check
			if _, err := p.prejoinArtifactsTransaction(ctx, j, install, material, artifacts, pg, hostProof, parent, system, false); err == nil {
				t.Fatal("write replayed")
			}
			if _, err := p.prejoinArtifactsTransaction(ctx, j, install, material, artifacts, pg, hostProof, parent, system, true); err == nil {
				t.Fatal("producer reconciled itself")
			}
			j = reopenSourcePublication(t, p, j, ledger)
			got, err := p.prejoinArtifactsTransaction(ctx, j, install, material, artifacts, pg, hostProof, parent, system, true)
			if (err == nil) != complete {
				t.Fatal("wrong independent proof", err)
			}
			if complete && (!digestPattern.MatchString(got) || result != "" && result != got) {
				t.Fatal("changed receipt")
			}
			if !complete && readMutationFixture(t, ledger).Steps["stage_images"].State != "intent" {
				t.Fatal("partial state settled")
			}
			if _, err := p.preparePrejoinHost(ctx, j, install, material, actions, parent, true); err == nil {
				t.Fatal("artifact phase admitted by host proof")
			}
			identityConsumerBlocked(t, install.Root)
		})
	}
}
