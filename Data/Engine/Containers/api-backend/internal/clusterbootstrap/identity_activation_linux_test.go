package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPrejoinIdentityActivationJournal(t *testing.T) {
	for _, mode := range []string{"complete", "intent lost acknowledgement", "retained marker", "same executor", "same history executor", "active producer", "missing verification", "verification input", "verification result", "prior intent", "source drift", "private configuration drift", "identity drift", "image drift", "Node drift", "member drift", "post-unlink observation", "outer persistence", "post-commit observation"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			p, j, ledger, parent, system, identity, artifacts, join, token, ca, pins, targets := preparedImageVerificationFixture(t)
			started := startedK3sFixture(p.configuration.wire.Expected)
			check := func(context.Context) error { return nil }
			observe := func(context.Context, *os.File, *os.File, []ContainerdImageTarget) (PreparedImageObservation, error) {
				return PreparedImageObservation{Before: started, Images: slices.Clone(targets), After: started}, nil
			}
			phase := &prejoinImageVerificationPhase{join, PreparedImageVerificationActions{Observe: observe}, pins}
			if _, err := p.prejoinArtifactPhaseTransactionWithStart(ctx, j, identity.install, identity.material, artifacts, artifacts.postgresReference, check, parent, system, false, nil, nil, nil, phase); err != nil {
				t.Fatal(err)
			}
			reopen := func() {
				j = reopenSourcePublication(t, p, j, ledger)
				fresh, err := NewK3sJoinMaterial(p.configuration.wire.Expected, identity.install.Binding, token, ca)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { fresh.Close() })
				phase.material = fresh
			}
			if mode != "same executor" {
				reopen()
			}
			marker := filepath.Join(identity.install.Root, engineidentity.RecoveryMarkerName)
			innerBefore, err := os.ReadFile(filepath.Join(identity.install.Journal, "journal.json"))
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "same history executor":
				record := j.state.Steps["stage_source"]
				record.Executor = j.executor
				record.State, record.ResultSHA256 = "not_applied", sourcePublicationAbsenceDigest(record.InputSHA256)
				j.state.History = append(j.state.History, mutationPastAttempt{Step: "stage_source", Record: record})
				if !j.state.valid() || j.persist(ctx) != nil {
					t.Fatal("invalid retained history fixture")
				}
			case "active producer":
				j.quiescent = func(context.Context, ExecutorIdentity) error { return ErrMutationUnknown }
			case "missing verification":
				delete(j.state.Steps, "verify_images")
			case "verification input", "verification result", "prior intent":
				record := j.state.Steps["verify_images"]
				if mode == "verification input" {
					record.InputSHA256 = mutationDigest("different input")
				} else if mode == "verification result" {
					record.ResultSHA256 = mutationDigest("different result")
				} else {
					record.State, record.ResultSHA256 = "intent", ""
				}
				j.state.Steps["verify_images"] = record
			case "source drift":
				if os.WriteFile(filepath.Join(parent, "Borealis", "unexpected"), []byte("changed"), 0600) != nil {
					t.Fatal("fixture source write")
				}
			case "private configuration drift":
				if os.WriteFile(filepath.Join(parent, "Borealis", ".env"), []byte("changed"), 0600) != nil {
					t.Fatal("fixture configuration write")
				}
			case "identity drift":
				if os.WriteFile(filepath.Join(identity.install.Root, engineidentity.EngineSecretPath), []byte(strings.Repeat("a", 64)), 0600) != nil {
					t.Fatal("fixture identity write")
				}
			case "outer persistence":
				original := j.check
				j.check = func(ctx context.Context) error {
					if j.state.Steps["activate_identity"].State == "applied" {
						identityConsumerBlocked(t, identity.install.Root)
						return ErrSessionAuthority
					}
					return original(ctx)
				}
			}
			calls := 0
			phase.actions.Observe = func(ctx context.Context, hostLock, identityLock *os.File, expected []ContainerdImageTarget) (PreparedImageObservation, error) {
				calls++
				requirePrejoinHostLock(t, hostLock, j.lock.Name())
				identityConsumerBlocked(t, identity.install.Root)
				if identityLock == nil || !slices.Equal(expected, targets) {
					t.Fatal("missing exclusions/immutable targets")
				}
				record := readMutationFixture(t, ledger).Steps["activate_identity"]
				if record.State != "intent" && record.State != "applied" {
					t.Fatal("observation preceded durable activation intent")
				}
				_, markerErr := os.Lstat(marker)
				if mode == "post-unlink observation" && errors.Is(markerErr, os.ErrNotExist) || mode == "post-commit observation" && record.State == "applied" || mode == "retained marker" {
					return PreparedImageObservation{}, ErrMutationUnknown
				}
				value, _ := observe(ctx, hostLock, identityLock, expected)
				switch mode {
				case "image drift":
					value.Images[0].Digest = "sha256:" + strings.Repeat("f", 64)
				case "Node drift":
					value.After.Node.NodeUID = "11111111-1111-4111-8111-111111111111"
				case "member drift":
					value.After.Membership.LocalID++
				}
				return value, nil
			}
			var receipt PrejoinPreparationReceipt
			run := func(reconcile bool) (string, error) {
				receipt = PrejoinPreparationReceipt{}
				return p.prejoinArtifactPhaseTransactionWithReceipt(ctx, j, identity.install, identity.material, artifacts, artifacts.postgresReference, check, parent, system, reconcile, nil, nil, nil, phase, true, &receipt)
			}
			result, err := run(false)
			success := mode == "complete" || mode == "intent lost acknowledgement"
			if (err == nil) != success || success && !digestPattern.MatchString(result) {
				t.Fatal("wrong activation outcome", result, err)
			}
			innerAfter, _ := os.ReadFile(filepath.Join(identity.install.Journal, "journal.json"))
			if !bytes.Equal(innerBefore, innerAfter) {
				t.Fatal("activation rewrote original empty-NodeUID journal")
			}
			if success {
				if receipt.Validate(p.configuration.wire.Expected, identity.install.Binding) != nil || receipt.Node != started.Node || receipt.MemberID != started.Membership.LocalID || receipt.Steps[10].ResultSHA256 != result {
					t.Fatal("native completion receipt lost exact observed identity")
				}
			} else if receipt != (PrejoinPreparationReceipt{}) {
				t.Fatal("failed native proof leaked completion")
			}
			if !success {
				if mode == "same history executor" && (calls != 0 || j.state.Steps["activate_identity"].State != "") {
					t.Fatal("activation began before historical producer stopped")
				}
				identityConsumerBlocked(t, identity.install.Root)
				if _, err := os.Lstat(marker); err != nil {
					t.Fatal("uncertain activation lost marker", err)
				}
				if mode == "retained marker" || mode == "post-unlink observation" || mode == "post-commit observation" {
					reopen()
					calls = 0
					if _, err := run(true); err == nil || calls != 0 {
						t.Fatal("reconciliation replayed retained-marker removal")
					}
				}
				return
			}
			consumer, err := engineidentity.AcquireConsumer(identity.install.Root)
			if err != nil {
				t.Fatal("committed activation kept consumer fenced", err)
			}
			consumer.Close()
			if _, err := run(false); err == nil {
				t.Fatal("activation replayed")
			}
			if _, err := run(true); err == nil {
				t.Fatal("producer reconciled itself")
			}
			if mode == "intent lost acknowledgement" {
				record := j.state.Steps["activate_identity"]
				record.State, record.ResultSHA256 = "intent", ""
				j.state.Steps["activate_identity"] = record
				if j.persist(ctx) != nil {
					t.Fatal("lost acknowledgement fixture")
				}
			}
			reopen()
			if value, err := run(true); err != nil || value != result {
				t.Fatal("lost acknowledgement proof failed", value, err)
			}
			if receipt.Validate(p.configuration.wire.Expected, identity.install.Binding) != nil {
				t.Fatal("reconciliation omitted complete receipt")
			}
			if _, err := p.prejoinArtifactPhaseTransactionWithStart(ctx, j, identity.install, identity.material, artifacts, artifacts.postgresReference, check, parent, system, true, nil, nil, nil, phase); err == nil {
				t.Fatal("earlier image phase accepted activated prefix")
			}
		})
	}
}
