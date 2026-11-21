package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func prejoinIdentityFixture(t *testing.T) (*MutationJournal, string, engineidentity.Installation, *engineidentity.Material) {
	t.Helper()
	j, root, install, _, material := joinedIdentityFixture(t)
	install.Root, install.Target.NodeUID = mutationTempDir(t), ""
	prejoinAppliedFixture(t, j, "stage_source")
	return j, root, install, material
}

func prejoinAppliedFixture(t *testing.T, j *MutationJournal, step string) {
	t.Helper()
	if _, err := j.Apply(context.Background(), step, mutationDigest("input:"+step), func(context.Context, *os.File) (string, error) {
		return mutationDigest("result:" + step), nil
	}); err != nil {
		t.Fatal(err)
	}
}

func reopenPrejoinFixture(t *testing.T, j *MutationJournal, root string) *MutationJournal {
	t.Helper()
	owner, source := nextMutationOwner(j.state.Owner), j.state.Source
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openTestMutationJournal(context.Background(), root, owner, source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	return reopened
}

func TestPrejoinIdentityJournalOrdersSourceIdentityAndPreparation(t *testing.T) {
	j, root, install, material := prejoinIdentityFixture(t)
	ctx := context.Background()
	if state, err := j.InstallPrejoinIdentity(ctx, install, material); err != nil || state != "committed" {
		t.Fatal(state, err)
	}
	identityConsumerBlocked(t, install.Root)
	if _, err := j.InstallPrejoinIdentity(ctx, install, material); err != ErrMutationApplied {
		t.Fatal("committed identity replayed", err)
	}
	if _, err := j.ReleasePrejoinIdentity(ctx, install, material); err == nil {
		t.Fatal("unprepared target released")
	}
	prejoinAppliedFixture(t, j, "prepare_host")
	if _, err := j.ReleasePrejoinIdentity(ctx, install, material); err == nil {
		t.Fatal("producing executor released itself")
	}
	j = reopenPrejoinFixture(t, j, root)
	if _, err := j.ReleaseJoinedIdentity(ctx, install, material); err == nil {
		t.Fatal("pre-join journal accepted joined release")
	}
	changed := install
	changed.Target.NodeUID = "88888888-8888-4888-8888-888888888888"
	if _, err := j.ReleasePrejoinIdentity(ctx, changed, material); err == nil {
		t.Fatal("Node UID rebound pre-join journal")
	}
	if _, err := j.ReleaseJoinedIdentity(ctx, changed, material); err == nil {
		t.Fatal("joined release rebound pre-join effect")
	}
	identityConsumerBlocked(t, install.Root)
	for _, want := range []string{"released", "already_released"} {
		if state, err := j.ReleasePrejoinIdentity(ctx, install, material); err != nil || state != want {
			t.Fatal(state, err)
		}
	}
	consumer, err := engineidentity.AcquireConsumer(install.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	if got, err := consumer.Load(); err != nil || !got.Equal(material) {
		t.Fatal("wrong fresh identity", err)
	}
}

func TestPrejoinIdentityJournalRejectsWrongPhase(t *testing.T) {
	for _, mode := range []string{"missing source", "source uncertain", "joined UID", "host prepared", "join recorded", "source changed", "previous executor"} {
		t.Run(mode, func(t *testing.T) {
			j, _, install, material := prejoinIdentityFixture(t)
			switch mode {
			case "missing source":
				delete(j.state.Steps, "stage_source")
			case "source uncertain":
				r := j.state.Steps["stage_source"]
				r.State, r.ResultSHA256 = "intent", ""
				j.state.Steps["stage_source"] = r
			case "joined UID":
				install.Target.NodeUID = "88888888-8888-4888-8888-888888888888"
			case "host prepared":
				prejoinAppliedFixture(t, j, "prepare_host")
			case "join recorded":
				prejoinAppliedFixture(t, j, "join_cluster")
			case "source changed":
				install.Check = func() error { return ErrSessionAuthority }
			case "previous executor":
				j.executor = testMutationExecutor(mutationDigest("different executor"))
				j.quiescent = func(context.Context, ExecutorIdentity) error { return ErrExecutorContainment }
			}
			if _, err := j.InstallPrejoinIdentity(context.Background(), install, material); err == nil {
				t.Fatal("unqualified pre-join identity accepted")
			}
			if _, err := os.Stat(filepath.Join(install.Root, engineidentity.EngineSecretPath)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid phase wrote identity")
			}
		})
	}
}

func TestPrejoinIdentityJournalReconcilesWithoutReplay(t *testing.T) {
	for _, phase := range []string{"inner committed", "outer committed"} {
		t.Run(phase, func(t *testing.T) {
			j, root, install, material := prejoinIdentityFixture(t)
			check := j.check
			lost := false
			j.check = func(ctx context.Context) error {
				if err := check(ctx); err != nil {
					return err
				}
				lost = lost || phase == "inner committed" && identityJournalState(install.Journal) == "committed" ||
					phase == "outer committed" && j.state.Steps["install_identity"].State == "applied"
				if lost {
					return ErrSessionAuthority
				}
				return nil
			}
			if _, err := j.InstallPrejoinIdentity(context.Background(), install, material); err == nil || !lost {
				t.Fatal("uncertain commit accepted")
			}
			identityConsumerBlocked(t, install.Root)
			j = reopenPrejoinFixture(t, j, root)
			if _, err := j.InstallPrejoinIdentity(context.Background(), install, material); err == nil {
				t.Fatal("uncertain transaction replayed")
			}
			if _, err := j.ReconcilePrejoinIdentity(context.Background(), install, material); err != nil {
				t.Fatal("same committed effect not reconciled", err)
			}
			identityConsumerBlocked(t, install.Root)
			if _, err := j.ReleasePrejoinIdentity(context.Background(), install, material); err == nil {
				t.Fatal("reconciliation bypassed host preparation")
			}
			prejoinAppliedFixture(t, j, "prepare_host")
			if _, err := j.ReleasePrejoinIdentity(context.Background(), install, material); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPrejoinIdentityReleaseRejectsUncertainOrJoinedHost(t *testing.T) {
	for _, mode := range []string{"uncertain images", "joined", "live producer", "changed identity"} {
		t.Run(mode, func(t *testing.T) {
			j, root, install, material := prejoinIdentityFixture(t)
			if _, err := j.InstallPrejoinIdentity(context.Background(), install, material); err != nil {
				t.Fatal(err)
			}
			prejoinAppliedFixture(t, j, "prepare_host")
			switch mode {
			case "uncertain images":
				_, _ = j.Apply(context.Background(), "stage_images", mutationDigest("images"), func(context.Context, *os.File) (string, error) { return "", ErrMutationUnknown })
			case "joined":
				prejoinAppliedFixture(t, j, "join_cluster")
			case "changed identity":
				if err := os.WriteFile(filepath.Join(install.Root, engineidentity.EngineSecretPath), []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			j = reopenPrejoinFixture(t, j, root)
			if mode == "live producer" {
				j.quiescent = func(context.Context, ExecutorIdentity) error { return ErrExecutorContainment }
			}
			if _, err := j.ReleasePrejoinIdentity(context.Background(), install, material); err == nil {
				t.Fatal("unsafe target released")
			}
			identityConsumerBlocked(t, install.Root)
		})
	}
}
