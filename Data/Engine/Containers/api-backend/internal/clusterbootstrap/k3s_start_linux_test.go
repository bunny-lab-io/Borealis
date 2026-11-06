package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func startedK3sFixture(expected PreparationExpected) K3sStartedObservation {
	node := K3sJoinedNode{NodeUID: "99999999-9999-4999-8999-999999999999", Hostname: expected.Target.Hostname, MachineID: expected.Target.MachineID, BootID: expected.TargetBootID, Address: expected.Target.Address, K3sVersion: expected.K3sVersion, Ready: true}
	membership := K3sMembership{Voting: true}
	for i, address := range expected.PeerAddresses {
		name := "peer-0123abc" + string(rune('a'+i))
		if address == expected.Target.Address {
			name = expected.Target.Hostname + "-0123abcd"
			membership.LocalID = uint64(i + 1)
		}
		membership.Members = append(membership.Members, K3sEtcdMember{ID: uint64(i + 1), Name: name, Address: address})
	}
	return K3sStartedObservation{node, membership}
}

func TestPreparedK3sStartReceipt(t *testing.T) {
	expected, _ := preparationFixture()
	for _, mode := range []string{"valid", "not ready", "wrong boot", "wrong Node", "learner", "member ID", "duplicate member", "foreign peer", "missing local", "wrong member name"} {
		t.Run(mode, func(t *testing.T) {
			proof := startedK3sFixture(expected)
			switch mode {
			case "not ready":
				proof.Node.Ready = false
			case "wrong boot":
				proof.Node.BootID = "88888888-8888-4888-8888-888888888888"
			case "wrong Node":
				proof.Node.NodeUID = ""
			case "learner":
				proof.Membership.Voting = false
			case "member ID":
				proof.Membership.LocalID++
			case "duplicate member":
				proof.Membership.Members[1].ID = proof.Membership.Members[0].ID
			case "foreign peer":
				proof.Membership.Members[0].Address = "192.168.50.99"
			case "missing local":
				for i, m := range proof.Membership.Members {
					if m.Address == expected.Target.Address {
						proof.Membership.Members = append(proof.Membership.Members[:i], proof.Membership.Members[i+1:]...)
						break
					}
				}
			case "wrong member name":
				for i, m := range proof.Membership.Members {
					if m.Address == expected.Target.Address {
						proof.Membership.Members[i].Name = "foreign-0123abcd"
					}
				}
			}
			digest, err := proof.result(expected, strings.Repeat("a", 64))
			if (err == nil) != (mode == "valid") || err != nil && digest != "" {
				t.Fatal("invalid startup receipt", err)
			}
		})
	}
}

func TestPreparedK3sStartJournal(t *testing.T) {
	for _, mode := range []string{"complete", "lost acknowledgement", "pending", "missing configuration"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			p, j, ledger, parent, install, identity := prejoinHostFixture(t)
			check := func(context.Context) error { return nil }
			host := PrejoinHostActions{CheckFresh: check, Prepare: func(context.Context, *os.File, *os.File) error { return nil }, Prove: check}
			if _, err := p.preparePrejoinHost(ctx, j, install, identity, host, parent, false); err != nil {
				t.Fatal(err)
			}
			artifacts, _, _ := preparationArtifactsForSourceFixture(t, p.configuration.wire.Expected.Source)
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
			token := []byte("K10" + hex.EncodeToString(sum[:]) + "::server:private-start-fixture")
			join, err := NewK3sJoinMaterial(p.configuration.wire.Expected, install.Binding, token, ca)
			if err != nil {
				t.Fatal(err)
			}
			defer join.Close()
			configuration := PrejoinK3sJoinActions{Installed: check, CheckFresh: check, Configure: func(context.Context, *os.File, *os.File) error { return nil }, Prove: check}
			if mode != "missing configuration" {
				if _, err := p.prejoinArtifactPhaseTransactionWithJoin(ctx, j, install, identity, artifacts, artifacts.postgresReference, check, parent, system, false, nil, &prejoinK3sJoinPhase{join, configuration}); err != nil {
					t.Fatal(err)
				}
			}
			calls, started := 0, false
			phase := &prejoinK3sStartPhase{material: join, actions: PreparedK3sStartActions{Configured: check, CheckFresh: func(context.Context) error {
				if started {
					return ErrMutationUnknown
				}
				return nil
			}}}
			phase.actions.Start = func(_ context.Context, hostLock, identityLock *os.File) error {
				calls++
				if readMutationFixture(t, ledger).Steps["start_k3s"].State != "intent" {
					t.Fatal("start before intent")
				}
				requirePrejoinHostLock(t, hostLock, j.lock.Name())
				if identityLock == nil {
					t.Fatal("missing identity lock")
				}
				identityConsumerBlocked(t, install.Root)
				started = true
				if os.Mkdir(filepath.Join(system, "var/lib/rancher/k3s/server"), 0700) != nil {
					t.Fatal("runtime fixture")
				}
				if mode == "lost acknowledgement" {
					return ErrMutationUnknown
				}
				return nil
			}
			phase.actions.Prove = func(context.Context) (K3sStartedObservation, error) {
				if !started {
					return K3sStartedObservation{}, ErrMutationUnknown
				}
				proof := startedK3sFixture(p.configuration.wire.Expected)
				if mode == "pending" {
					proof.Node.Ready = false
				}
				return proof, nil
			}
			run := func(reconcile bool) (string, error) {
				return p.prejoinArtifactPhaseTransactionWithStart(ctx, j, install, identity, artifacts, artifacts.postgresReference, check, parent, system, reconcile, nil, nil, phase, nil)
			}
			result, err := run(false)
			if (err == nil) != (mode == "complete") {
				t.Fatal("wrong startup outcome", err)
			}
			if mode == "missing configuration" {
				if calls != 0 {
					t.Fatal("start without configuration")
				}
				return
			}
			record := readMutationFixture(t, ledger).Steps["start_k3s"]
			if calls != 1 || (record.State == "applied") != (mode == "complete") || mode == "complete" && record.ResultSHA256 != result {
				t.Fatal("wrong durable result")
			}
			if _, err := run(false); err == nil || calls != 1 {
				t.Fatal("start replay")
			}
			if _, err := run(true); err == nil {
				t.Fatal("producer self-proof")
			}
			j = reopenSourcePublication(t, p, j, ledger)
			fresh, err := NewK3sJoinMaterial(p.configuration.wire.Expected, install.Binding, token, ca)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			phase.material = fresh
			result, err = run(true)
			if (err == nil) != (mode != "pending") || calls != 1 {
				t.Fatal("wrong independent start proof", err)
			}
			if mode != "pending" && readMutationFixture(t, ledger).Steps["start_k3s"].ResultSHA256 != result {
				t.Fatal("receipt changed")
			}
			if _, err := p.prejoinArtifactPhaseTransactionWithJoin(ctx, j, install, identity, artifacts, artifacts.postgresReference, check, parent, system, true, nil, &prejoinK3sJoinPhase{fresh, configuration}); err == nil {
				t.Fatal("old configuration phase accepted runtime")
			}
			identityConsumerBlocked(t, install.Root)
		})
	}
}
