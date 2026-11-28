package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPrejoinPrivateTransferAtomicHandoff(t *testing.T) {
	p, j, root, parent, install, material := prejoinDirectoryFixture(t)
	ctx := context.Background()
	for _, phase := range []string{"directory", "install", "reconcile"} {
		if phase == "reconcile" {
			j = reopenSourcePublication(t, p, j, root)
		}
		var wire bytes.Buffer
		expected := p.configuration.wire.Expected
		_, helper, _, _ := p.bundle.ManagerProof()
		if p.WritePrejoinPreparation(ctx, expected, install.Binding, material, &wire, mutationAuthority) != nil {
			t.Fatal("transfer generation failed")
		}
		err := ConsumePrejoinPreparation(ctx, mutationTempDir(t), expected, helper, install.Binding, &wire, mutationAuthority, func(ctx context.Context, received *PreparationInputs, keys *engineidentity.Material) error {
			var err error
			switch phase {
			case "directory":
				_, err = received.preparePrejoinIdentityRoot(ctx, j, install, keys, parent)
			case "install":
				_, err = received.installPreparedPrejoinIdentity(ctx, j, install, keys, parent)
			case "reconcile":
				_, err = received.reconcilePreparedPrejoinIdentity(ctx, j, install, keys, parent)
			}
			return err
		})
		if err != nil {
			t.Fatal(phase, err)
		}
		identityConsumerBlocked(t, install.Root)
	}
	if install.ProvePrejoinFencedCommitted(material) != nil {
		t.Fatal("retained shared material proof failed")
	}
}

func TestPreparedPrejoinReconciliationExactEffect(t *testing.T) {
	for _, mode := range []string{"inner committed", "outer committed", "source drift", "key drift", "extra private file", "account receipt", "root receipt", "directory mode", "producer running", "producer self"} {
		t.Run(mode, func(t *testing.T) {
			p, j, root, parent, install, material := prejoinDirectoryFixture(t)
			ctx := context.Background()
			if _, err := p.preparePrejoinIdentityRoot(ctx, j, install, material, parent); err != nil {
				t.Fatal(err)
			}
			check := j.check
			lost := false
			j.check = func(ctx context.Context) error {
				if check(ctx) != nil {
					return ErrSessionAuthority
				}
				lost = lost || mode == "inner committed" && identityJournalState(install.Journal) == "committed" || mode == "outer committed" && j.state.Steps["install_identity"].State == "applied"
				if lost {
					return ErrSessionAuthority
				}
				return nil
			}
			_, err := p.installPreparedPrejoinIdentity(ctx, j, install, material, parent)
			if (err != nil) != (mode == "inner committed" || mode == "outer committed") {
				t.Fatal("unexpected initial result", err)
			}
			j.check = check
			if mode != "producer self" {
				j = reopenSourcePublication(t, p, j, root)
			}
			switch mode {
			case "source drift":
				err = os.WriteFile(filepath.Join(parent, "Borealis", "Engine.sh"), []byte("changed"), 0755)
			case "key drift":
				err = os.WriteFile(filepath.Join(install.Root, engineidentity.EngineSecretPath), []byte("changed"), 0600)
			case "extra private file":
				err = os.WriteFile(filepath.Join(install.Root, "unknown"), []byte("extra"), 0600)
			case "account receipt":
				r := j.state.Steps["prepare_runtime_identity"]
				r.ResultSHA256 = mutationDigest("other")
				j.state.Steps["prepare_runtime_identity"] = r
			case "root receipt":
				r := j.state.Steps["prepare_identity_root"]
				r.ResultSHA256 = mutationDigest("other")
				j.state.Steps["prepare_identity_root"] = r
			case "directory mode":
				err = os.Chmod(filepath.Join(install.Root, "Auth_Tokens"), 0755)
			case "producer running":
				j.quiescent = func(context.Context, ExecutorIdentity) error { return ErrExecutorContainment }
			}
			// Initial uncertain errors were expected; mutation fixture writes are
			// checked separately from that retained error.
			if err != nil && mode != "inner committed" && mode != "outer committed" {
				t.Fatal(err)
			}
			_, err = p.reconcilePreparedPrejoinIdentity(ctx, j, install, material, parent)
			valid := mode == "inner committed" || mode == "outer committed"
			if (err == nil) != valid {
				t.Fatal("unexpected reconciliation", err)
			}
			identityConsumerBlocked(t, install.Root)
		})
	}
}

func TestPreparedPrejoinInstallRejectsPostCommitSourceDrift(t *testing.T) {
	p, j, _, parent, install, material := prejoinDirectoryFixture(t)
	ctx := context.Background()
	if _, err := p.preparePrejoinIdentityRoot(ctx, j, install, material, parent); err != nil {
		t.Fatal(err)
	}
	changed := false
	install.Check = func() error {
		if !changed && identityJournalState(install.Journal) == "committed" {
			if err := os.WriteFile(filepath.Join(parent, "Borealis", "Engine.sh"), []byte("changed during identity commit"), 0755); err != nil {
				return err
			}
			changed = true
		}
		return nil
	}
	if _, err := p.installPreparedPrejoinIdentity(ctx, j, install, material, parent); err != ErrMutationUnknown || !changed {
		t.Fatal("source drift acknowledged", err)
	}
	identityConsumerBlocked(t, install.Root)
	if j.state.Steps["install_identity"].State != "applied" {
		t.Fatal("committed effect erased")
	}
}
