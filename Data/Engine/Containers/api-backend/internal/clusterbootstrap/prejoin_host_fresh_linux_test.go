package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"borealis/api-backend/internal/engineidentity"
)

func TestPrejoinHostFreshProofBeforeIntent(t *testing.T) {
	for _, mode := range []string{"valid", "fresh rejection", "source drift", "key drift", "authority loss", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			p, j, path, parent, install, material := prejoinHostFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fresh, writes := 0, 0
			actions := PrejoinHostActions{
				CheckFresh: func(context.Context) error {
					fresh++
					if _, exists := readMutationFixture(t, path).Steps["prepare_host"]; exists {
						t.Fatal("fresh check occurred after intent")
					}
					requirePrejoinHostLock(t, j.lock, j.lock.Name())
					identityConsumerBlocked(t, install.Root)
					switch mode {
					case "fresh rejection":
						return errors.New("foreign Docker installation")
					case "source drift":
						return os.WriteFile(filepath.Join(parent, "Borealis", "Engine.sh"), []byte("changed"), 0755)
					case "key drift":
						return os.WriteFile(filepath.Join(install.Root, engineidentity.EngineSecretPath), []byte("changed"), 0600)
					case "authority loss":
						j.check = func(context.Context) error { return ErrSessionAuthority }
					case "cancel":
						cancel()
					}
					return nil
				},
				Prepare: func(context.Context, *os.File, *os.File) error { writes++; return nil },
				Prove:   func(context.Context) error { return nil },
			}
			_, err := p.preparePrejoinHost(ctx, j, install, material, actions, parent, false)
			_, intent := readMutationFixture(t, path).Steps["prepare_host"]
			if fresh != 1 || (err == nil) != (mode == "valid") || intent != (mode == "valid") || writes != map[bool]int{true: 1}[mode == "valid"] {
				t.Fatal("unproved host consumed intent/effects", err, fresh, writes, intent)
			}
		})
	}
}

func TestPrejoinHostLegacyIntentCannotAttestFreshHost(t *testing.T) {
	p, j, path, parent, install, material := prejoinHostFixture(t)
	ctx := context.Background()
	source, err := p.sourcePublicationBinding(j)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(struct{ Domain, Source, Directory, Identity string }{"prejoin-host-preparation-v1", source, j.state.Steps["prepare_identity_root"].InputSHA256, j.state.Steps["install_identity"].InputSHA256})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	legacy := hex.EncodeToString(sum[:])
	if _, err := j.Apply(ctx, "prepare_host", legacy, func(context.Context, *os.File) (string, error) { return legacy, nil }); err != nil {
		t.Fatal(err)
	}
	j = reopenSourcePublication(t, p, j, path)
	calls := 0
	actions := PrejoinHostActions{CheckFresh: func(context.Context) error { calls++; return nil }, Prepare: func(context.Context, *os.File, *os.File) error { calls++; return nil }, Prove: func(context.Context) error { calls++; return nil }}
	for _, reconcile := range []bool{false, true} {
		if _, err := p.preparePrejoinHost(ctx, j, install, material, actions, parent, reconcile); err == nil || calls != 0 {
			t.Fatal("legacy intent adopted fresh-host contract", err, calls)
		}
	}
	if _, err := p.completePrejoinHost(ctx, j, install, material, PrejoinHostRecoveryActions{ProveConfigured: actions.CheckFresh, Complete: actions.Prepare, Prove: actions.Prove}, parent); err == nil || calls != 0 {
		t.Fatal("legacy host intent entered continuation", err, calls)
	}
	if _, err := p.completePrejoinFirewall(ctx, j, install, material, PrejoinFirewallRecoveryActions{ProvePartial: actions.CheckFresh, Complete: actions.Prepare, Prove: actions.Prove}, parent); err == nil || calls != 0 {
		t.Fatal("legacy host intent entered firewall continuation", err, calls)
	}
	if _, err := p.resumeFreshPrejoinHost(ctx, j, install, material, actions, parent); err == nil || calls != 0 {
		t.Fatal("legacy host intent entered fresh continuation", err, calls)
	}
	if _, err := p.resumeRepositoryPrejoinHost(ctx, j, install, material, actions, parent); err == nil || calls != 0 {
		t.Fatal("legacy host intent entered repository continuation", err, calls)
	}
	if r := readMutationFixture(t, path).Steps["prepare_host"]; r.InputSHA256 != legacy || r.ResultSHA256 != legacy || r.State != "applied" {
		t.Fatal("legacy evidence rewritten")
	}
}
