package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func preparedImageVerificationFixture(t *testing.T) (*PreparationInputs, *MutationJournal, string, string, string, engineidentityFixture, *PreparationArtifacts, *K3sJoinMaterial, []byte, []byte, K3sInputPins, []ContainerdImageTarget) {
	t.Helper()
	ctx := context.Background()
	p, j, ledger, parent, install, identity := prejoinHostFixture(t)
	check := func(context.Context) error { return nil }
	host := PrejoinHostActions{CheckFresh: check, Prepare: func(context.Context, *os.File, *os.File) error { return nil }, Prove: check}
	if _, err := p.preparePrejoinHost(ctx, j, install, identity, host, parent, false); err != nil {
		t.Fatal(err)
	}
	artifacts, pins, _ := preparationArtifactsForSourceFixture(t, p.configuration.wire.Expected.Source)
	raw, archivePins, _ := k3sImportArchiveFixture(t, "complete")
	pins.Archive = archivePins.Archive
	if artifacts.k3s.root.WriteFile(K3sArchiveName, raw, 0600) != nil {
		t.Fatal("replace owned fixture")
	}
	artifacts.k3s.assets[K3sArchiveName] = pins.Archive
	proof, err := inspectK3sArchive(ctx, bytes.NewReader(raw), int64(len(raw)), pins)
	if err != nil {
		t.Fatal(err)
	}
	artifacts.k3s.inventory.wire.Archive = proof
	artifacts.k3s.inventory.raw, _ = json.Marshal(artifacts.k3s.inventory.wire)
	targets, err := artifacts.importTargets(ctx, check, pins)
	if err != nil {
		t.Fatal(err)
	}
	system := t.TempDir()
	for _, path := range []string{"var/lib", "usr/local/bin"} {
		if os.MkdirAll(filepath.Join(system, path), 0700) != nil {
			t.Fatal("fixture")
		}
	}
	if _, err := p.prejoinArtifactsTransaction(ctx, j, install, identity, artifacts, artifacts.postgresReference, check, parent, system, false); err != nil {
		t.Fatal(err)
	}
	runtime := PrejoinK3sActions{CheckFresh: check, Install: func(context.Context, *os.File, *os.File) error { return nil }, Prove: check}
	if _, err := p.prejoinArtifactPhaseTransaction(ctx, j, install, identity, artifacts, artifacts.postgresReference, check, parent, system, false, &runtime); err != nil {
		t.Fatal(err)
	}
	ca, _, _ := joinCAFixture(t)
	sum := sha256.Sum256(ca)
	token := []byte("K10" + hex.EncodeToString(sum[:]) + "::server:image-verification-fixture")
	join, err := NewK3sJoinMaterial(p.configuration.wire.Expected, install.Binding, token, ca)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { join.Close() })
	configuration := PrejoinK3sJoinActions{Installed: check, CheckFresh: check, Configure: func(context.Context, *os.File, *os.File) error { return nil }, Prove: check}
	if _, err := p.prejoinArtifactPhaseTransactionWithJoin(ctx, j, install, identity, artifacts, artifacts.postgresReference, check, parent, system, false, nil, &prejoinK3sJoinPhase{join, configuration}); err != nil {
		t.Fatal(err)
	}
	started := startedK3sFixture(p.configuration.wire.Expected)
	start := &prejoinK3sStartPhase{material: join, actions: PreparedK3sStartActions{Configured: check, CheckFresh: check,
		Start: func(context.Context, *os.File, *os.File) error {
			return os.MkdirAll(filepath.Join(system, "var/lib/rancher/k3s/server"), 0700)
		}, Prove: func(context.Context) (K3sStartedObservation, error) { return started, nil }}}
	if _, err := p.prejoinArtifactPhaseTransactionWithStart(ctx, j, install, identity, artifacts, artifacts.postgresReference, check, parent, system, false, nil, nil, start, nil); err != nil {
		t.Fatal(err)
	}
	return p, j, ledger, parent, system, engineidentityFixture{install, identity}, artifacts, join, token, ca, pins, targets
}

type engineidentityFixture struct {
	install  engineidentity.Installation
	material *engineidentity.Material
}

func TestPreparedImageVerificationReceipt(t *testing.T) {
	expected, _ := preparationFixture()
	start := startedK3sFixture(expected)
	startInput := mutationDigest("startup input")
	startResult, err := start.result(expected, startInput)
	if err != nil {
		t.Fatal(err)
	}
	targets := []ContainerdImageTarget{{Reference: "example.test/image:v1", Digest: "sha256:" + strings.Repeat("a", 64)}}
	for _, mode := range []string{"valid", "before Node", "after member", "image digest", "missing image"} {
		t.Run(mode, func(t *testing.T) {
			observation := PreparedImageObservation{Before: start, Images: slices.Clone(targets), After: start}
			switch mode {
			case "before Node":
				observation.Before.Node.NodeUID = ""
			case "after member":
				observation.After.Membership.LocalID++
			case "image digest":
				observation.Images[0].Digest = "sha256:" + strings.Repeat("b", 64)
			case "missing image":
				observation.Images = nil
			}
			got, err := observation.result(expected, startInput, startResult, mutationDigest("verify input"), targets)
			if (err == nil) != (mode == "valid") || err != nil && got != "" {
				t.Fatal("unsafe image receipt", got, err)
			}
		})
	}
}

func TestPreparedImageVerificationJournal(t *testing.T) {
	for _, mode := range []string{"complete", "lost acknowledgement", "changed image", "changed startup", "missing startup"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			p, j, ledger, parent, system, identity, artifacts, join, token, ca, pins, targets := preparedImageVerificationFixture(t)
			if mode == "missing startup" {
				delete(j.state.Steps, "start_k3s")
			}
			calls := 0
			observe := func(_ context.Context, hostLock, identityLock *os.File, expectedTargets []ContainerdImageTarget) (PreparedImageObservation, error) {
				calls++
				requirePrejoinHostLock(t, hostLock, j.lock.Name())
				if identityLock == nil {
					t.Fatal("missing identity exclusion")
				}
				state := readMutationFixture(t, ledger).Steps["verify_images"].State
				if state != "intent" && state != "applied" {
					t.Fatal("observation before intent")
				}
				started := startedK3sFixture(p.configuration.wire.Expected)
				got := slices.Clone(targets)
				if !slices.Equal(expectedTargets, targets) {
					t.Fatal("core target plan not passed to observer")
				}
				if mode == "changed image" {
					got[0].Digest = "sha256:" + strings.Repeat("f", 64)
				}
				after := started
				if mode == "changed startup" {
					after.Node.NodeUID = ""
				}
				if mode == "lost acknowledgement" && calls == 1 {
					return PreparedImageObservation{}, ErrMutationUnknown
				}
				return PreparedImageObservation{Before: started, Images: got, After: after}, nil
			}
			phase := &prejoinImageVerificationPhase{material: join, actions: PreparedImageVerificationActions{Observe: observe}, pins: pins}
			run := func(reconcile bool) (string, error) {
				return p.prejoinArtifactPhaseTransactionWithStart(ctx, j, identity.install, identity.material, artifacts, artifacts.postgresReference,
					func(context.Context) error { return nil }, parent, system, reconcile, nil, nil, nil, phase)
			}
			result, err := run(false)
			success := mode == "complete"
			if (err == nil) != success {
				t.Fatal("wrong verification outcome", err)
			}
			if mode == "missing startup" {
				if calls != 0 {
					t.Fatal("observation without startup")
				}
				return
			}
			record := readMutationFixture(t, ledger).Steps["verify_images"]
			if (record.State == "applied") != success || success && record.ResultSHA256 != result {
				t.Fatal("wrong durable image result")
			}
			if _, err := run(false); err == nil {
				t.Fatal("image observation replayed")
			}
			if _, err := run(true); err == nil {
				t.Fatal("producer proved itself")
			}
			j = reopenSourcePublication(t, p, j, ledger)
			fresh, freshErr := NewK3sJoinMaterial(p.configuration.wire.Expected, identity.install.Binding, token, ca)
			if freshErr != nil {
				t.Fatal(freshErr)
			}
			defer fresh.Close()
			phase.material = fresh
			result, err = run(true)
			wantReconcile := mode == "complete" || mode == "lost acknowledgement"
			if (err == nil) != wantReconcile {
				t.Fatal("wrong independent image proof", err)
			}
			if wantReconcile && readMutationFixture(t, ledger).Steps["verify_images"].ResultSHA256 != result {
				t.Fatal("image receipt changed")
			}
			identityConsumerBlocked(t, identity.install.Root)
		})
	}
}
