package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const joinedIdentityRecoveryMarker = ".borealis-identity-recovery.json"

func joinedIdentityMaterial(t *testing.T) *engineidentity.Material {
	t.Helper()
	_, jwt, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, script, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwtDER, err := x509.MarshalPKCS8PrivateKey(jwt)
	if err != nil {
		t.Fatal(err)
	}
	scriptDER, err := x509.MarshalPKCS8PrivateKey(script)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	material, err := engineidentity.ParseFiles(map[string][]byte{
		engineidentity.EngineSecretPath: []byte(hex.EncodeToString(secret)),
		engineidentity.AgentJWTPath:     pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: jwtDER}),
		engineidentity.ScriptKeyPath:    pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: scriptDER}),
		engineidentity.ScriptPublicPath: publicDER,
	})
	if err != nil {
		t.Fatal(err)
	}
	return material
}

func joinedIdentityFixture(t *testing.T) (*MutationJournal, string, engineidentity.Installation, *engineidentity.Material, *engineidentity.Material) {
	t.Helper()
	root, identity := mutationTempDir(t), mutationTempDir(t)
	owner, source := mutationFixture()
	j, err := openTestMutationJournal(context.Background(), root, owner, source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	before, incoming := joinedIdentityMaterial(t), joinedIdentityMaterial(t)
	for name, raw := range before.Files() {
		path := filepath.Join(identity, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	install := engineidentity.Installation{Root: identity, Journal: filepath.Join(root, "identity-attempt"),
		Binding: engineidentity.Binding{ClusterID: owner.ClusterID, SourceID: "66666666-6666-4666-8666-666666666666", SourceUID: "77777777-7777-4777-8777-777777777777"},
		Target:  engineidentity.TargetBinding{ProvisioningID: owner.TargetID, SSHFingerprint: owner.HostKeyFingerprint, NodeUID: "88888888-8888-4888-8888-888888888888"},
		UID:     os.Geteuid(), GID: os.Getegid(), Check: func() error { return nil }, Quiesced: func() error { return nil }}
	return j, root, install, before, incoming
}

func identityJournalState(path string) string {
	raw, _ := os.ReadFile(filepath.Join(path, "journal.json"))
	var record struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(raw, &record)
	return record.State
}

func identityConsumerBlocked(t *testing.T, root string) {
	t.Helper()
	consumer, err := engineidentity.AcquireConsumer(root)
	if err == nil {
		consumer.Close()
		t.Fatal("consumer crossed pending journal boundary")
	}
}

func joinedIdentityCommittedUnknown(t *testing.T, phase string) (*MutationJournal, string, engineidentity.Installation, *engineidentity.Material) {
	t.Helper()
	j, root, install, _, material := joinedIdentityFixture(t)
	check := j.check
	lost := false
	j.check = func(ctx context.Context) error {
		if err := check(ctx); err != nil {
			return err
		}
		innerCommitted := identityJournalState(install.Journal) == "committed"
		outerApplied := j.state.Steps["install_identity"].State == "applied"
		lost = lost || phase == "inner_committed" && innerCommitted || phase == "outer_committed" && outerApplied
		if lost {
			return ErrSessionAuthority
		}
		return nil
	}
	if _, err := j.InstallJoinedIdentity(context.Background(), install, material); err == nil || !lost {
		t.Fatal("fixture did not retain committed uncertain identity")
	}
	identityConsumerBlocked(t, install.Root)
	owner, source := j.state.Owner, j.state.Source
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openTestMutationJournal(context.Background(), root, nextMutationOwner(owner), source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	return reopened, root, install, material
}

func TestJoinedIdentityBothJournalsCommitBeforeConsumerRelease(t *testing.T) {
	j, root, install, _, material := joinedIdentityFixture(t)
	checked := 0
	check := j.check
	j.check = func(ctx context.Context) error {
		if err := check(ctx); err != nil {
			return err
		}
		if identityJournalState(install.Journal) == "committed" {
			checked++
			identityConsumerBlocked(t, install.Root)
		}
		return nil
	}
	install.Check = func() error {
		if identityJournalState(install.Journal) != "" {
			if readMutationFixture(t, root).Steps["install_identity"].State == "" {
				t.Fatal("keys written without durable outer intent")
			}
		}
		return nil
	}
	state, err := j.InstallJoinedIdentity(context.Background(), install, material)
	if err != nil || state != "committed" {
		t.Fatalf("%s %v", state, err)
	}
	if checked == 0 {
		t.Fatal("commit boundary not exercised")
	}
	// The pending-commit assertion applies only while the first call owns the
	// consumer fence, not the subsequent rejected replay after service release.
	j.check = check
	record := readMutationFixture(t, root).Steps["install_identity"]
	if record.State != "applied" || record.ResultSHA256 != material.Digest() {
		t.Fatal("outer outcome not durable")
	}
	identityConsumerBlocked(t, install.Root)
	if _, err := j.InstallJoinedIdentity(context.Background(), install, material); err != ErrMutationApplied {
		t.Fatal("completed attempt replayed")
	}
	identityConsumerBlocked(t, install.Root)
}

func TestJoinedIdentityLostOwnershipAcrossCommitBoundaries(t *testing.T) {
	for _, phase := range []string{"during_write", "inner_committed", "outer_committed", "outer_persist_failed"} {
		t.Run(phase, func(t *testing.T) {
			j, root, install, before, material := joinedIdentityFixture(t)
			check := j.check
			lost := false
			j.check = func(ctx context.Context) error {
				if err := check(ctx); err != nil {
					return err
				}
				inner := identityJournalState(install.Journal)
				outer := readMutationFixture(t, root).Steps["install_identity"].State
				switch phase {
				case "during_write":
					current, _ := os.ReadFile(filepath.Join(install.Root, engineidentity.EngineSecretPath))
					lost = lost || string(current) == string(material.Files()[engineidentity.EngineSecretPath])
				case "inner_committed":
					lost = lost || inner == "committed"
				case "outer_committed":
					lost = lost || outer == "applied"
				case "outer_persist_failed":
					if !lost && j.state.Steps["install_identity"].State == "applied" {
						lost = true
						// Real closed-descriptor IO failure, leaving disk intent intact.
						if err := j.root.Close(); err != nil {
							t.Fatal(err)
						}
					}
					return nil
				}
				if lost {
					return ErrSessionAuthority
				}
				return nil
			}
			if _, err := j.InstallJoinedIdentity(context.Background(), install, material); err == nil || !lost {
				t.Fatal("fault did not fail installation")
			}
			identityConsumerBlocked(t, install.Root)
			wantOuter := "intent"
			if phase == "outer_committed" {
				wantOuter = "applied"
			}
			if got := readMutationFixture(t, root).Steps["install_identity"].State; got != wantOuter {
				t.Fatalf("outer state %s, want %s", got, wantOuter)
			}
			current, err := engineidentity.Load(install.Root)
			if err != nil {
				t.Fatal(err)
			}
			want := material
			if phase == "during_write" {
				want = before
			}
			if !current.Equal(want) {
				t.Fatal("unexpected identity after fault")
			}
			owner, source := j.state.Owner, j.state.Source
			_ = j.Close()
			reopened, err := openTestMutationJournal(context.Background(), root, nextMutationOwner(owner), source, mutationAuthority)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if _, err := reopened.InstallJoinedIdentity(context.Background(), install, material); err == nil {
				t.Fatal("new owner replayed retained attempt")
			}
			identityConsumerBlocked(t, install.Root)
		})
	}
}

func TestJoinedIdentityRejectsUnboundOrUnsettledAttemptBeforeMarker(t *testing.T) {
	for _, kind := range []string{"cluster", "target", "ssh", "fresh", "source", "missing_check", "missing_quiescence", "nil_material", "prior_unknown", "prior_executor"} {
		t.Run(kind, func(t *testing.T) {
			j, _, install, before, material := joinedIdentityFixture(t)
			switch kind {
			case "cluster":
				install.Binding.ClusterID = "99999999-9999-4999-8999-999999999999"
			case "target":
				install.Target.ProvisioningID = "99999999-9999-4999-8999-999999999999"
			case "ssh":
				install.Target.SSHFingerprint = "changed"
			case "fresh":
				install.Target.NodeUID = ""
			case "source":
				install.Check = func() error { return errors.New("original source changed") }
			case "missing_check":
				install.Check = nil
			case "missing_quiescence":
				install.Quiesced = nil
			case "nil_material":
				material = nil
			case "prior_unknown":
				_, _ = j.Apply(context.Background(), "prepare_host", mutationDigest("prior"), func(context.Context, *os.File) (string, error) { return "", errors.New("unknown") })
			case "prior_executor":
				if _, err := j.Apply(context.Background(), "stage_source", mutationDigest("prior"), func(context.Context, *os.File) (string, error) { return mutationDigest("result"), nil }); err != nil {
					t.Fatal(err)
				}
				j.executor = testMutationExecutor(mutationDigest("different executor"))
				j.quiescent = func(context.Context, ExecutorIdentity) error { return ErrExecutorContainment }
			}
			if _, err := j.InstallJoinedIdentity(context.Background(), install, material); err == nil {
				t.Fatal("unqualified attempt accepted")
			}
			consumer, err := engineidentity.AcquireConsumer(install.Root)
			if err != nil {
				t.Fatal("rejected preflight left restart marker")
			}
			consumer.Close()
			current, err := engineidentity.Load(install.Root)
			if err != nil || !current.Equal(before) {
				t.Fatal("rejected preflight changed identity")
			}
		})
	}
}

func TestJoinedIdentityCloseCannotReleaseHostLockDuringCommit(t *testing.T) {
	j, _, install, _, material := joinedIdentityFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	paused := false
	install.Check = func() error {
		if !paused && identityJournalState(install.Journal) == "committed" {
			paused = true
			close(entered)
			<-release
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { _, err := j.InstallJoinedIdentity(context.Background(), install, material); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("transaction did not reach commit boundary")
	}
	identityConsumerBlocked(t, install.Root)
	closed := make(chan error, 1)
	go func() { closed <- j.Close() }()
	select {
	case <-closed:
		t.Fatal("close passed active transaction")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}

func TestJoinedIdentityReconciliationProvesCommitWithoutReleasingConsumers(t *testing.T) {
	for _, phase := range []string{"inner_committed", "outer_committed"} {
		t.Run(phase, func(t *testing.T) {
			j, root, install, material := joinedIdentityCommittedUnknown(t, phase)
			markerPath := filepath.Join(install.Root, joinedIdentityRecoveryMarker)
			markerBefore, err := os.ReadFile(markerPath)
			if err != nil {
				t.Fatal(err)
			}
			journalPath := filepath.Join(install.Journal, "journal.json")
			journalBefore, err := os.ReadFile(journalPath)
			if err != nil {
				t.Fatal(err)
			}
			got, err := j.ReconcileJoinedIdentity(context.Background(), install, material)
			if err != nil || got != material.Digest() {
				t.Fatalf("committed identity reconciliation failed: %s %v", got, err)
			}
			record := readMutationFixture(t, root).Steps["install_identity"]
			if record.State != "applied" || record.ResultSHA256 != material.Digest() {
				t.Fatal("outer result not settled from committed identity proof")
			}
			markerAfter, err := os.ReadFile(markerPath)
			if err != nil || !bytes.Equal(markerBefore, markerAfter) {
				t.Fatal("reconciliation changed or removed restart marker")
			}
			journalAfter, err := os.ReadFile(journalPath)
			if err != nil || !bytes.Equal(journalBefore, journalAfter) {
				t.Fatal("reconciliation changed inner identity journal")
			}
			current, err := engineidentity.Load(install.Root)
			if err != nil || !current.Equal(material) {
				t.Fatal("reconciliation changed committed identity")
			}
			identityConsumerBlocked(t, install.Root)
			if got, err := j.ReconcileJoinedIdentity(context.Background(), install, material); err != nil || got != material.Digest() {
				t.Fatal("repeat proof changed definitive outer result")
			}
			identityConsumerBlocked(t, install.Root)
		})
	}
}

func TestJoinedIdentityReconciliationRejectsIncompleteOrChangedProof(t *testing.T) {
	for _, kind := range []string{"identity", "inner_journal", "marker", "missing_marker", "material", "authority", "quiescence", "producer_live"} {
		t.Run(kind, func(t *testing.T) {
			j, root, install, material := joinedIdentityCommittedUnknown(t, "inner_committed")
			markerPath := filepath.Join(install.Root, joinedIdentityRecoveryMarker)
			switch kind {
			case "identity":
				if err := os.WriteFile(filepath.Join(install.Root, engineidentity.EngineSecretPath), []byte("changed-identity-material-that-is-long-enough\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "inner_journal":
				path := filepath.Join(install.Journal, "journal.json")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var record map[string]any
				if json.Unmarshal(raw, &record) != nil {
					t.Fatal("fixture journal decode failed")
				}
				record["state"] = "prepared"
				raw, _ = json.Marshal(record)
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			case "marker":
				if err := os.WriteFile(markerPath, []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing_marker":
				if err := os.Remove(markerPath); err != nil {
					t.Fatal(err)
				}
			case "material":
				material = joinedIdentityMaterial(t)
			case "authority":
				install.Check = func() error { return errors.New("original source changed") }
			case "quiescence":
				install.Quiesced = func() error { return errors.New("legacy consumer state unknown") }
			case "producer_live":
				j.quiescent = func(context.Context, ExecutorIdentity) error { return ErrExecutorContainment }
			}
			_, err := j.ReconcileJoinedIdentity(context.Background(), install, material)
			want := ErrMutationUnknown
			if kind == "material" {
				want = ErrMutationJournal
			}
			if err != want {
				t.Fatalf("unsafe proof returned %v, want %v", err, want)
			}
			if readMutationFixture(t, root).Steps["install_identity"].State != "intent" {
				t.Fatal("failed proof changed uncertain outer result")
			}
			if kind == "missing_marker" {
				if _, err := os.Stat(markerPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("read-only proof recreated missing marker")
				}
			} else {
				identityConsumerBlocked(t, install.Root)
			}
		})
	}
}

func TestJoinedIdentityReleaseRequiresAppliedProofAndIsIdempotent(t *testing.T) {
	j, root, install, material := joinedIdentityCommittedUnknown(t, "inner_committed")
	markerPath := filepath.Join(install.Root, joinedIdentityRecoveryMarker)
	journalPath := filepath.Join(install.Journal, "journal.json")
	journalBefore, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.ReleaseJoinedIdentity(context.Background(), install, material); err != ErrMutationUnknown {
		t.Fatal("uncertain outer intent authorized restart")
	}
	identityConsumerBlocked(t, install.Root)
	if _, err := j.ReconcileJoinedIdentity(context.Background(), install, material); err != nil {
		t.Fatal(err)
	}
	state, err := j.ReleaseJoinedIdentity(context.Background(), install, material)
	if err != nil || state != "released" {
		t.Fatalf("committed identity release failed: %s %v", state, err)
	}
	if _, err := os.Stat(markerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("successful release retained restart marker")
	}
	consumer, err := engineidentity.AcquireConsumer(install.Root)
	if err != nil {
		t.Fatal("released identity still blocked consumer startup")
	}
	consumer.Close()
	journalAfter, err := os.ReadFile(journalPath)
	if err != nil || !bytes.Equal(journalBefore, journalAfter) {
		t.Fatal("release changed committed inner journal")
	}
	current, err := engineidentity.Load(install.Root)
	if err != nil || !current.Equal(material) {
		t.Fatal("release changed committed identity")
	}
	record := readMutationFixture(t, root).Steps["install_identity"]
	if record.State != "applied" || record.ResultSHA256 != material.Digest() {
		t.Fatal("release changed outer identity result")
	}
	if state, err := j.ReleaseJoinedIdentity(context.Background(), install, material); err != nil || state != "already_released" {
		t.Fatalf("released identity was not idempotent: %s %v", state, err)
	}
}

func TestJoinedIdentityReleaseFailureRetainsOrRestoresMarker(t *testing.T) {
	for _, kind := range []string{"identity", "inner_journal", "marker", "material", "authority", "quiescence", "same_producer", "producer_live", "authority_after_unlink", "quiescence_after_unlink"} {
		t.Run(kind, func(t *testing.T) {
			j, root, install, material := joinedIdentityCommittedUnknown(t, "inner_committed")
			if _, err := j.ReconcileJoinedIdentity(context.Background(), install, material); err != nil {
				t.Fatal(err)
			}
			markerPath := filepath.Join(install.Root, joinedIdentityRecoveryMarker)
			markerBefore, err := os.ReadFile(markerPath)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "identity":
				if err := os.WriteFile(filepath.Join(install.Root, engineidentity.EngineSecretPath), []byte("changed-identity-material-that-is-long-enough\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "inner_journal":
				path := filepath.Join(install.Journal, "journal.json")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var record map[string]any
				if json.Unmarshal(raw, &record) != nil {
					t.Fatal("fixture journal decode failed")
				}
				record["state"] = "prepared"
				raw, _ = json.Marshal(record)
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			case "marker":
				if err := os.WriteFile(markerPath, []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "material":
				material = joinedIdentityMaterial(t)
			case "authority":
				install.Check = func() error { return errors.New("original source changed") }
			case "quiescence":
				install.Quiesced = func() error { return errors.New("legacy consumer state unknown") }
			case "same_producer":
				j.executor = j.state.Steps["install_identity"].Executor
			case "producer_live":
				j.quiescent = func(context.Context, ExecutorIdentity) error { return ErrExecutorContainment }
			case "authority_after_unlink":
				install.Check = func() error {
					if _, err := os.Stat(markerPath); errors.Is(err, os.ErrNotExist) {
						return errors.New("authority lost after marker unlink")
					}
					return nil
				}
			case "quiescence_after_unlink":
				install.Quiesced = func() error {
					if _, err := os.Stat(markerPath); errors.Is(err, os.ErrNotExist) {
						return errors.New("legacy process state changed after marker unlink")
					}
					return nil
				}
			}
			_, err = j.ReleaseJoinedIdentity(context.Background(), install, material)
			want := ErrMutationUnknown
			if kind == "material" {
				want = ErrMutationJournal
			}
			if err != want {
				t.Fatalf("unsafe release returned %v, want %v", err, want)
			}
			if readMutationFixture(t, root).Steps["install_identity"].State != "applied" {
				t.Fatal("failed release changed applied outer result")
			}
			markerAfter, err := os.ReadFile(markerPath)
			if err != nil {
				t.Fatal("failed release left startup unblocked")
			}
			if kind == "authority_after_unlink" || kind == "quiescence_after_unlink" {
				if !bytes.Equal(markerBefore, markerAfter) {
					t.Fatal("failed post-unlink check did not restore exact marker")
				}
			}
			identityConsumerBlocked(t, install.Root)
		})
	}
}

func TestJoinedIdentityCloseCannotReleaseLocksDuringMarkerRelease(t *testing.T) {
	j, _, install, material := joinedIdentityCommittedUnknown(t, "inner_committed")
	if _, err := j.ReconcileJoinedIdentity(context.Background(), install, material); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(install.Root, joinedIdentityRecoveryMarker)
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	paused := false
	install.Check = func() error {
		if _, err := os.Stat(markerPath); errors.Is(err, os.ErrNotExist) && !paused {
			paused = true
			close(entered)
			<-release
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { _, err := j.ReleaseJoinedIdentity(context.Background(), install, material); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("release did not reach post-unlink boundary")
	}
	identityConsumerBlocked(t, install.Root)
	closed := make(chan error, 1)
	go func() { closed <- j.Close() }()
	select {
	case <-closed:
		t.Fatal("close released host lock during marker release")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	consumer, err := engineidentity.AcquireConsumer(install.Root)
	if err != nil {
		t.Fatal(err)
	}
	consumer.Close()
}
