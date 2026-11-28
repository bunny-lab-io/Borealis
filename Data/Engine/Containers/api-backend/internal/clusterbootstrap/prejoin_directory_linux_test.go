package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func prejoinDirectoryFixture(t *testing.T) (*PreparationInputs, *MutationJournal, string, string, engineidentity.Installation, *engineidentity.Material) {
	t.Helper()
	p, j, journal, parent := sourcePublicationFixture(t)
	_, _, install, _, material := joinedIdentityFixture(t)
	install.Root = filepath.Join(parent, "Borealis", prejoinSecretsRelative)
	install.Journal = filepath.Join(journal, "identity-attempt")
	install.Target = engineidentity.TargetBinding{ProvisioningID: j.state.Owner.TargetID, SSHFingerprint: j.state.Owner.HostKeyFingerprint}
	install.Binding.ClusterID = j.state.Owner.ClusterID
	if _, err := p.publishSource(context.Background(), j, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := p.prepareRuntimeIdentity(context.Background(), j, RuntimeIdentityActions{Prepare: func(context.Context, *os.File) error { return nil }, Prove: func(context.Context) error { return nil }}, parent, install.UID, install.GID, false); err != nil {
		t.Fatal(err)
	}
	return p, j, journal, parent, install, material
}

func TestPrejoinDirectoryAtomicFenceAndIdentityHandoff(t *testing.T) {
	p, j, journal, parent, install, material := prejoinDirectoryFixture(t)
	ctx := context.Background()
	if _, err := p.preparePrejoinIdentityRoot(ctx, j, install, material, parent); err != nil {
		t.Fatal(err)
	}
	identityConsumerBlocked(t, install.Root)
	if _, err := engineidentity.Load(install.Root); err == nil {
		t.Fatal("directory publication wrote keys")
	}
	state := readMutationFixture(t, journal)
	if state.Steps["prepare_identity_root"].State != "applied" || len(state.Steps) != 3 {
		t.Fatal("directory effect unrecorded")
	}
	if _, err := j.InstallPrejoinIdentity(ctx, install, material); err == nil {
		t.Fatal("ordinary pre-join bypassed prepared directory")
	}
	if _, err := p.reconcileSource(ctx, j, parent); err == nil {
		t.Fatal("identity directory accepted as source-only state")
	}
	if state, err := p.installPreparedPrejoinIdentity(ctx, j, install, material, parent); err != nil || state != "committed" {
		t.Fatal("handoff failed", state, err)
	}
	identityConsumerBlocked(t, install.Root)
	if install.ProvePrejoinFencedCommitted(material) != nil {
		t.Fatal("prepared identity proof failed")
	}
	if _, err := p.installPreparedPrejoinIdentity(ctx, j, install, material, parent); err == nil {
		t.Fatal("keys replayed")
	}
	if _, err := p.reconcilePrejoinIdentityRoot(ctx, j, install, material, parent); err == nil {
		t.Fatal("installed keys accepted as empty directory")
	}
	if _, err := j.ReleasePrejoinIdentity(ctx, install, material); err == nil {
		t.Fatal("unprepared host released")
	}
	prejoinAppliedFixture(t, j, "prepare_host")
	j = reopenPrejoinFixture(t, j, journal)
	if state, err := j.ReleasePrejoinIdentity(ctx, install, material); err != nil || state != "released" {
		t.Fatal("release failed", state, err)
	}
	consumer, err := engineidentity.AcquireConsumer(install.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	if got, err := consumer.Load(); err != nil || !got.Equal(material) {
		t.Fatal("shared identity changed", err)
	}
}

func TestPrejoinDirectoryRejectsUnprovenCreation(t *testing.T) {
	for _, mode := range []string{"source receipt", "account receipt", "missing account", "source bytes", "claim", "foreign path", "joined UID", "source substitution", "authority", "quiescence", "occupied", "symlink", "later phase"} {
		t.Run(mode, func(t *testing.T) {
			p, j, _, parent, install, material := prejoinDirectoryFixture(t)
			services := filepath.Join(parent, "Borealis", "Engine", "Services")
			switch mode {
			case "source receipt":
				r := j.state.Steps["stage_source"]
				r.ResultSHA256 = mutationDigest("other")
				j.state.Steps["stage_source"] = r
			case "account receipt":
				r := j.state.Steps["prepare_runtime_identity"]
				r.ResultSHA256 = mutationDigest("other")
				j.state.Steps["prepare_runtime_identity"] = r
			case "missing account":
				delete(j.state.Steps, "prepare_runtime_identity")
			case "source bytes":
				if err := os.WriteFile(filepath.Join(parent, "Borealis", "Engine.sh"), []byte("changed"), 0755); err != nil {
					t.Fatal(err)
				}
			case "claim":
				j.state.Owner.Generation++
			case "foreign path":
				install.Root = filepath.Join(t.TempDir(), "keys")
			case "joined UID":
				install.Target.NodeUID = "88888888-8888-4888-8888-888888888888"
			case "source substitution":
				install.Binding.SourceID = install.Target.ProvisioningID
			case "authority":
				install.Check = func() error { return errors.New("lost account custody") }
			case "quiescence":
				install.Quiesced = func() error { return errors.New("consumer present") }
			case "occupied":
				if err := os.Mkdir(services, 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(t.TempDir(), services); err != nil {
					t.Fatal(err)
				}
			case "later phase":
				prejoinAppliedFixture(t, j, "prepare_host")
			}
			if _, err := p.preparePrejoinIdentityRoot(context.Background(), j, install, material, parent); err == nil {
				t.Fatal("unproven directory mutation accepted")
			}
			if _, ok := j.state.Steps["prepare_identity_root"]; ok {
				t.Fatal("invalid creation consumed intent")
			}
		})
	}
}

func TestPrejoinDirectoryUncertainEffectRequiresDistinctExactProof(t *testing.T) {
	p, j, journal, parent, install, material := prejoinDirectoryFixture(t)
	install.Check = func() error {
		if _, err := os.Lstat(install.Root); err == nil {
			return errors.New("lost authority after publication")
		}
		return nil
	}
	if _, err := p.preparePrejoinIdentityRoot(context.Background(), j, install, material, parent); err != ErrMutationUnknown {
		t.Fatal("uncertain effect acknowledged", err)
	}
	identityConsumerBlocked(t, install.Root)
	if readMutationFixture(t, journal).Steps["prepare_identity_root"].State != "intent" {
		t.Fatal("uncertain intent lost")
	}
	install.Check = func() error { return nil }
	if _, err := p.reconcilePrejoinIdentityRoot(context.Background(), j, install, material, parent); err != ErrMutationUnknown {
		t.Fatal("producer reconciled itself", err)
	}
	j = reopenSourcePublication(t, p, j, journal)
	if _, err := p.reconcilePrejoinIdentityRoot(context.Background(), j, install, material, parent); err != nil {
		t.Fatal("exact effect not reconciled", err)
	}
	if _, err := p.installPreparedPrejoinIdentity(context.Background(), j, install, material, parent); err != nil {
		t.Fatal("reconciled handoff failed", err)
	}
}

func TestPrejoinDirectoryRejectsAlteredReconciliation(t *testing.T) {
	for _, mode := range []string{"marker bytes", "marker missing", "extra key", "extra directory", "directory mode", "source bytes", "active executor"} {
		t.Run(mode, func(t *testing.T) {
			p, j, journal, parent, install, material := prejoinDirectoryFixture(t)
			if _, err := p.preparePrejoinIdentityRoot(context.Background(), j, install, material, parent); err != nil {
				t.Fatal(err)
			}
			j = reopenSourcePublication(t, p, j, journal)
			marker := filepath.Join(install.Root, engineidentity.RecoveryMarkerName)
			switch mode {
			case "marker bytes":
				if err := os.WriteFile(marker, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "marker missing":
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
			case "extra key":
				if err := os.WriteFile(filepath.Join(install.Root, engineidentity.EngineSecretPath), material.Files()[engineidentity.EngineSecretPath], 0600); err != nil {
					t.Fatal(err)
				}
			case "extra directory":
				if err := os.Mkdir(filepath.Join(install.Root, "unknown"), 0700); err != nil {
					t.Fatal(err)
				}
			case "directory mode":
				if err := os.Chmod(install.Root, 0755); err != nil {
					t.Fatal(err)
				}
			case "source bytes":
				if err := os.WriteFile(filepath.Join(parent, "Borealis", "Engine.sh"), []byte("changed"), 0755); err != nil {
					t.Fatal(err)
				}
			case "active executor":
				j.quiescent = func(context.Context, ExecutorIdentity) error { return ErrExecutorContainment }
			}
			if _, err := p.reconcilePrejoinIdentityRoot(context.Background(), j, install, material, parent); err == nil {
				t.Fatal("altered directory accepted")
			}
		})
	}
}
