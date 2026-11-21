package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type reissuedCustodyFixture struct {
	value   clusterSSHPreparationRecoveryReissuedProofAuthority
	payload string
	source  clusterSSHPrejoinSource
	nodes   []any
	pod     map[string]any
}

func newReissuedCustodyFixture(t *testing.T, replacement bool, rounds int) *reissuedCustodyFixture {
	t.Helper()
	initial, source, pod := sshPrejoinSourceFixture(t, replacement)
	proof := clusterSSHParentProof{Version: 1, Cohort: initial.Cohort, Source: initial.Source}
	queue := clusterSSHQueueSource{ClusterID: initial.Source.ClusterID, ActiveOperationID: initial.Cohort.OperationID}
	report := newClusterSSHQualificationReport(initial.Cohort.Attempt)
	for i := 0; i < len(report.Checks)-1; i++ {
		report.Checks[i].State = "passed"
	}
	a := sshPreparationAuthorizationFixture(t, proof, queue, &report)
	confirmation := sshPreparationConfirmationFixture(t, &a, initial.Source)
	if confirmation.IdentitySource != source.Binding {
		t.Fatal("fixture changed original selected source")
	}
	plan, err := clusterSSHPreparationPlanDigest(a)
	if err != nil {
		t.Fatal(err)
	}
	var receipts [6]string
	for i := range receipts {
		receipts[i] = strings.Repeat("a", 64)
	}
	payload := marshalClusterJSON(map[string]any{"source_k3s_version": initial.K3sVersion, "ssh_preparation_authorization": a,
		"ssh_qualification": report, "ssh_preparation_confirmation": confirmation, "ssh_preparation_readiness": clusterSSHPreparationReadiness(plan, receipts)})
	base, _, err := clusterSSHPreparationRecoveryPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	record := clusterSSHPreparationRecoveryRecord{Version: 1, ID: newClusterUUID(), OperationID: a.OperationID, Attempt: a.Attempt,
		ControllerHolder: a.ControllerHolder, PlanSHA256: plan, PayloadSHA256: clusterSSHPreparationRecoveryPayloadDigest(base)}
	value := clusterSSHPreparationRecoveryReissuedProofAuthority{Baseline: initial.Baseline, Cohort: initial.Cohort, Source: initial.Source,
		Authorization: a, IdentitySource: source.Binding, ExpiresAt: map[string]int64{}}
	previous, holder := newClusterUUID(), newClusterUUID()
	for _, target := range a.Targets {
		record.Targets = append(record.Targets, clusterSSHPreparationRecoveryTarget{TargetID: target.TargetID, PreviousHolder: previous,
			PreviousGeneration: target.QualificationGeneration + 1, Generation: target.QualificationGeneration + 2, PreviousExpiresAt: 123})
		value.Leases = append(value.Leases, clusterSSHTargetLease{TargetID: target.TargetID, OperationID: record.OperationID, OperationAttempt: record.Attempt,
			ControllerHolder: record.ControllerHolder, OperationKind: "ssh_onboarding", OperationStep: clusterSSHPreparationRecoveryOperationStep,
			Step: clusterSSHPreparationRecoveryClaimStep, Holder: holder, Generation: target.QualificationGeneration + 2})
	}
	value.Lineage = clusterSSHPreparationRecoveryLineage{Version: 2, Original: record}
	payload = sshRecoveryPayload(t, payload, record)
	for range rounds {
		value.Lineage = appendReissueProofFixture(t, value.Lineage, payload)
		entry := &value.Lineage.Requests[len(value.Lineage.Requests)-1]
		for i := range entry.Targets {
			entry.Targets[i].PreviousHolder = value.Leases[i].Holder
			value.Leases[i].Holder, value.Leases[i].Generation = entry.Holder, entry.Targets[i].Generation
			value.ExpiresAt[value.Leases[i].TargetID] = 1000
		}
		payload = reissueProofPayload(t, payload, value.Lineage)
	}
	f := &reissuedCustodyFixture{value: value, payload: payload, source: source, pod: pod}
	for _, member := range value.Source.Members {
		node := sshSourceKubernetesFixture(member)
		node["apiVersion"], node["kind"] = "v1", "Node"
		clusterSSHStorageMap(node, "metadata")["labels"] = map[string]any{"borealis.io/engine-node": "true", "borealis.io/application-state": "active"}
		f.nodes = append(f.nodes, node)
	}
	for _, target := range value.Cohort.Targets {
		f.nodes = append(f.nodes, sshJoinedNodeFixture(target, initial.K3sVersion, newClusterUUID()))
	}
	for name, value := range map[string]string{"BOREALIS_ENGINE_POD_NAME": source.Process.PodName, "BOREALIS_ENGINE_POD_UID": source.Process.PodUID,
		"BOREALIS_ENGINE_NODE_NAME": source.Process.NodeName, "BOREALIS_API_BACKGROUND_LOOPS": "1"} {
		t.Setenv(name, value)
	}
	return f
}

func (f *reissuedCustodyFixture) get(ctx context.Context, path string, out any) error {
	var value any
	switch path {
	case "/api/v1/nodes":
		value = map[string]any{"apiVersion": "v1", "kind": "NodeList", "items": f.nodes}
	case "/api/v1/namespaces/kube-system":
		value = map[string]any{"metadata": map[string]any{"uid": f.value.Source.KubeSystemUID}}
	case "/api/v1/namespaces/borealis/pods/" + f.source.Process.PodName:
		value = f.pod
	default:
		return errors.New("unexpected custody path")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func TestClusterSSHPreparationRecoveryReissuedCustodyExact(t *testing.T) {
	for _, mode := range []string{"Q3 pair", "Q4 pair", "Q3 replacement", "Q4 replacement", "controller role", "frozen input", "owned output"} {
		t.Run(mode, func(t *testing.T) {
			rounds := 1
			if strings.HasPrefix(mode, "Q4") {
				rounds = 2
			}
			f := newReissuedCustodyFixture(t, strings.Contains(mode, "replacement"), rounds)
			if mode == "controller role" {
				t.Setenv("BOREALIS_ENGINE_POD_UID", newClusterUUID())
				t.Setenv("BOREALIS_API_BACKGROUND_LOOPS", "0")
			}
			checks, calls := 0, 0
			check := func(ctx context.Context) error { checks++; return ctx.Err() }
			get := func(ctx context.Context, path string, out any) error {
				if checks != 1+calls*2+1 {
					t.Fatal("native IO lacked preceding authority check", checks, calls)
				}
				if d, ok := ctx.Deadline(); !ok || time.Until(d) > 15*time.Second {
					t.Fatal("unbounded native observation")
				}
				calls++
				return f.get(ctx, path, out)
			}
			read := newClusterSSHPreparationRecoveryReissuedCustodyRead(f.value, f.payload, f.source, check, get)
			if mode == "frozen input" {
				f.value.Cohort.Targets[0].Report.BootID = newClusterUUID()
				f.value.Source.Members[0].BootID = newClusterUUID()
				f.value.Lineage.Original.Targets[0].PreviousGeneration++
				f.value.Lineage.Requests[0].Targets[0].Generation++
				f.value.Authorization.Targets[0].CredentialSHA256 = strings.Repeat("c", 64)
				f.value.ExpiresAt[f.value.Leases[0].TargetID] = 0
			}
			got, err := read(context.Background())
			if err != nil || !got.Original.valid(f.source) || len(got.Targets) != len(f.value.Cohort.Targets) || calls != 8 || checks != 18 {
				t.Fatal("complete typed custody rejected", err, calls, checks)
			}
			want := maps.Clone(got.Targets)
			if mode == "owned output" {
				for id := range got.Targets {
					delete(got.Targets, id)
				}
			}
			checks, calls = 0, 0
			again, err := read(context.Background())
			if err != nil || !maps.Equal(again.Targets, want) {
				t.Fatal("caller mutation changed retained custody", err)
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryReissuedCustodyAuthority(t *testing.T) {
	for _, mode := range []string{"initial phase", "Q2", "old generation", "future generation", "wrong holder", "missing sibling", "inspection", "lineage", "payload", "credential", "source selection", "other survivor", "wrong process", "zero pod", "missing confirmation", "version"} {
		t.Run(mode, func(t *testing.T) {
			f := newReissuedCustodyFixture(t, mode == "other survivor", 2)
			switch mode {
			case "initial phase":
				f.value.Leases[0].OperationStep, f.value.Leases[0].Step = clusterSSHPreparationOperationStep, "stage_source"
			case "Q2":
				f.value.Lineage.Requests = nil
			case "old generation":
				f.value.Leases[0].Generation--
			case "future generation":
				f.value.Leases[0].Generation++
			case "wrong holder":
				f.value.Leases[0].Holder = newClusterUUID()
			case "missing sibling":
				f.value.Leases = f.value.Leases[:1]
			case "inspection":
				f.value.Cohort.Targets[0].Report.BootID = newClusterUUID()
			case "lineage":
				f.value.Lineage.Requests[0].PreviousRecordSHA256 = strings.Repeat("b", 64)
			case "payload":
				f.payload += "x"
			case "credential":
				f.value.Authorization.Targets[0].CredentialSHA256 = strings.Repeat("c", 64)
			case "source selection":
				f.value.IdentitySource.SourceUID = newClusterUUID()
			case "other survivor":
				member := f.value.Source.Members[0]
				f.source.Binding.SourceID, f.source.Binding.SourceUID, f.source.Process.NodeName = member.NodeID, member.NodeUID, member.Name
				f.value.IdentitySource = f.source.Binding
				t.Setenv("BOREALIS_ENGINE_NODE_NAME", member.Name)
			case "wrong process":
				f.source.Process.NodeName = "other-node"
			case "zero pod":
				f.source.Process.PodUID = "00000000-0000-0000-0000-000000000000"
			case "missing confirmation":
				f.payload = strings.Replace(f.payload, "ssh_preparation_confirmation", "unrelated_confirmation", 1)
			case "version":
				f.payload = strings.ReplaceAll(f.payload, clusterbootstrap.K3sPins().Version, "v1.36.4+k3s1")
			}
			calls := 0
			read := newClusterSSHPreparationRecoveryReissuedCustodyRead(f.value, f.payload, f.source, func(ctx context.Context) error { return ctx.Err() },
				func(ctx context.Context, path string, out any) error { calls++; return f.get(ctx, path, out) })
			got, err := read(context.Background())
			if err != clusterbootstrap.ErrSessionAuthority || calls != 0 || got.Original != (clusterSSHPrejoinSourceCustody{}) || got.Targets != nil {
				t.Fatal("foreign authority reached native IO", err, calls)
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryReissuedCustodyDrift(t *testing.T) {
	for _, mode := range []string{"partial pair", "no targets", "unknown node", "duplicate UID", "target pending", "target active", "control-plane eligible", "edge eligible", "scheduler eligible", "postgres eligible", "late target UID", "late target boot", "late source UID", "late namespace", "late Pod UID", "late Pod runtime", "late hostPath", "late authority", "late credential", "later target UID", "private read error", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f := newReissuedCustodyFixture(t, false, 2)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lists, pods, namespaces, checks := 0, 0, 0, 0
			targetNode := f.nodes[len(f.value.Source.Members)].(map[string]any)
			check := func(ctx context.Context) error {
				checks++
				if mode == "late authority" && checks >= 18 || mode == "late credential" && checks >= 17 {
					return errors.New("private SQL or credential diagnostic")
				}
				return ctx.Err()
			}
			get := func(ctx context.Context, path string, out any) error {
				if mode == "private read error" {
					return errors.New("private Kubernetes diagnostic")
				}
				if mode == "cancelled" {
					cancel()
				}
				switch path {
				case "/api/v1/nodes":
					lists++
					node := targetNode
					meta := clusterSSHStorageMap(node, "metadata")
					labels := clusterSSHStorageMap(meta, "labels")
					switch mode {
					case "partial pair":
						f.nodes = f.nodes[:2]
					case "no targets":
						f.nodes = f.nodes[:1]
					case "unknown node":
						meta["name"] = "unknown"
					case "duplicate UID":
						meta["uid"] = f.value.Source.Members[0].NodeUID
					case "target pending":
						clusterSSHStorageMap(node, "status")["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
					case "target active":
						labels["borealis.io/application-state"] = "active"
					case "control-plane eligible", "edge eligible", "scheduler eligible", "postgres eligible":
						name := strings.TrimSuffix(mode, " eligible")
						if name == "postgres" {
							name = "postgres-primary"
						}
						labels["borealis.io/"+name+"-eligible"] = "true"
					case "late target UID":
						if lists == 2 {
							meta["uid"] = newClusterUUID()
						}
					case "later target UID":
						if lists == 3 {
							meta["uid"] = newClusterUUID()
						}
					case "late target boot":
						if lists == 2 {
							clusterSSHStorageMap(clusterSSHStorageMap(node, "status"), "nodeInfo")["bootID"] = newClusterUUID()
						}
					case "late source UID":
						if lists == 2 {
							clusterSSHStorageMap(f.nodes[0].(map[string]any), "metadata")["uid"] = newClusterUUID()
						}
					}
				case "/api/v1/namespaces/kube-system":
					namespaces++
					if mode == "late namespace" && namespaces == 4 {
						f.value.Source.KubeSystemUID = newClusterUUID()
					}
				default:
					pods++
					if pods == 2 {
						switch mode {
						case "late Pod UID":
							clusterSSHStorageMap(f.pod, "metadata")["uid"] = newClusterUUID()
						case "late Pod runtime":
							anySlice(clusterSSHStorageMap(f.pod, "status")["containerStatuses"])[0].(map[string]any)["containerID"] = "containerd://" + strings.Repeat("c", 64)
						case "late hostPath":
							volume := anySlice(clusterSSHStorageMap(f.pod, "spec")["volumes"])[0].(map[string]any)
							clusterSSHStorageMap(volume, "hostPath")["path"] = "/other-secrets"
						}
					}
				}
				return f.get(ctx, path, out)
			}
			read := newClusterSSHPreparationRecoveryReissuedCustodyRead(f.value, f.payload, f.source, check, get)
			got, err := read(ctx)
			if mode == "later target UID" {
				if err != nil {
					t.Fatal("first complete observation", err)
				}
				got, err = read(ctx)
			}
			if err != clusterbootstrap.ErrSessionAuthority || got.Targets != nil || got.Original != (clusterSSHPrejoinSourceCustody{}) {
				t.Fatal("changed or partial custody escaped", err)
			}
			before := checks
			if _, err := read(context.Background()); err != clusterbootstrap.ErrSessionAuthority || checks != before {
				t.Fatal("failed observation did not poison reader")
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryReissuedCustodyMaterial(t *testing.T) {
	for _, mode := range []string{"Q3 pair", "Q4 replacement", "closed consumer", "wrong root", "local pod", "local node", "local loops", "late process", "late keys", "late target", "ignored refresh", "callback error", "escaped refresh", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			rounds := 2
			if mode == "Q3 pair" {
				rounds = 1
			}
			f := newReissuedCustodyFixture(t, strings.Contains(mode, "replacement"), rounds)
			root := t.TempDir()
			want := sshIdentitySourceMaterial(t, root)
			consumer, err := engineidentity.AcquireConsumer(root)
			if err != nil {
				t.Fatal(err)
			}
			defer consumer.Close()
			if mode == "closed consumer" {
				consumer.Close()
			}
			if mode == "wrong root" {
				root = t.TempDir()
			}
			switch mode {
			case "local pod":
				t.Setenv("BOREALIS_ENGINE_POD_UID", newClusterUUID())
			case "local node":
				t.Setenv("BOREALIS_ENGINE_NODE_NAME", "other-node")
			case "local loops":
				t.Setenv("BOREALIS_API_BACKGROUND_LOOPS", "0")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var lost bool
			check := func(ctx context.Context) error {
				if lost {
					return errors.New("private authority diagnostic")
				}
				return ctx.Err()
			}
			read := newClusterSSHPreparationRecoveryReissuedCustodyRead(f.value, f.payload, f.source, check, f.get)
			used := false
			var escaped func(context.Context) error
			err = withClusterSSHPreparationRecoveryReissuedMaterial(ctx, f.value, f.payload, f.source, consumer, root, read,
				func(ctx context.Context, proof clusterSSHPreparationRecoveryReissuedCustody, material *engineidentity.Material, refresh func(context.Context) error) error {
					used = true
					if !material.Equal(want) || len(proof.Targets) != len(f.value.Cohort.Targets) || refresh(ctx) != nil {
						t.Fatal("wrong private material or custody")
					}
					escaped = refresh
					for id := range proof.Targets {
						delete(proof.Targets, id)
					}
					switch mode {
					case "late process":
						t.Setenv("BOREALIS_ENGINE_POD_UID", newClusterUUID())
					case "late keys":
						sshIdentitySourceMaterial(t, root)
					case "late target":
						clusterSSHStorageMap(f.nodes[len(f.value.Source.Members)].(map[string]any), "metadata")["uid"] = newClusterUUID()
					case "ignored refresh":
						lost = true
						_ = refresh(ctx)
						lost = false
					case "callback error":
						return errors.New("private callback diagnostic")
					case "cancelled":
						cancel()
					}
					return nil
				})
			valid := mode == "Q3 pair" || mode == "Q4 replacement" || mode == "escaped refresh"
			if (err == nil) != valid || !valid && err != clusterbootstrap.ErrSessionAuthority {
				t.Fatal("incorrect material scope outcome", err)
			}
			if used != (mode != "closed consumer" && mode != "wrong root" && !strings.HasPrefix(mode, "local ")) {
				t.Fatal("invalid custody reached material consumer")
			}
			if escaped != nil && escaped(context.Background()) != clusterbootstrap.ErrSessionAuthority {
				t.Fatal("escaped refresh retained authority")
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryReissuedCustodyCancellationJoins(t *testing.T) {
	for _, mode := range []string{"parent cancellation", "ignored active refresh"} {
		t.Run(mode, func(t *testing.T) {
			f := newReissuedCustodyFixture(t, false, 2)
			root := t.TempDir()
			sshIdentitySourceMaterial(t, root)
			consumer, err := engineidentity.AcquireConsumer(root)
			if err != nil {
				t.Fatal(err)
			}
			defer consumer.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release, requestDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var unblock sync.Once
			var blocking atomic.Bool
			read := newClusterSSHPreparationRecoveryReissuedCustodyRead(f.value, f.payload, f.source, func(ctx context.Context) error { return ctx.Err() },
				func(ctx context.Context, path string, out any) error {
					if blocking.CompareAndSwap(true, false) {
						close(entered)
						<-release // Deliberately slow owned IO even after cancellation.
						defer close(requestDone)
					}
					return f.get(ctx, path, out)
				})
			done := make(chan error, 1)
			refreshDone := make(chan error, 1)
			go func() {
				done <- withClusterSSHPreparationRecoveryReissuedMaterial(ctx, f.value, f.payload, f.source, consumer, root, read,
					func(ctx context.Context, _ clusterSSHPreparationRecoveryReissuedCustody, _ *engineidentity.Material, refresh func(context.Context) error) error {
						blocking.Store(true)
						go func() { refreshDone <- refresh(ctx) }()
						<-entered
						if mode == "parent cancellation" {
							<-ctx.Done()
						}
						return nil
					})
			}()
			joined := false
			defer func() {
				cancel()
				unblock.Do(func() { close(release) })
				if !joined {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("custody goroutine failed to join")
					}
				}
			}()
			select {
			case <-entered:
			case err := <-done:
				joined = true
				t.Fatal("custody rejected before blocked request", err)
			case <-time.After(5 * time.Second):
				t.Fatal("custody request did not start")
			}
			cancel()
			select {
			case err := <-done:
				joined = true
				t.Fatal("scope returned before owned IO joined", err)
			case <-time.After(25 * time.Millisecond):
			}
			unblock.Do(func() { close(release) })
			select {
			case err := <-done:
				joined = true
				if err != clusterbootstrap.ErrSessionAuthority {
					t.Fatal("cancelled scope acknowledged", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("scope failed to join")
			}
			<-requestDone
			if <-refreshDone != clusterbootstrap.ErrSessionAuthority {
				t.Fatal("cancelled refresh acknowledged")
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryReissuedCustodyWorkerProjection(t *testing.T) {
	for _, mode := range []string{"wrong authority", "wrong source", "partial pair", "extra target", "unknown target", "source UID", "repeated UID", "wrong boot", "not ready", "later UID"} {
		t.Run(mode, func(t *testing.T) {
			f := newReissuedCustodyFixture(t, false, 2)
			root := t.TempDir()
			sshIdentitySourceMaterial(t, root)
			consumer, err := engineidentity.AcquireConsumer(root)
			if err != nil {
				t.Fatal(err)
			}
			defer consumer.Close()
			controller := newClusterSSHPreparationRecoveryReissuedCustodyRead(f.value, f.payload, f.source, func(ctx context.Context) error { return ctx.Err() }, f.get)
			calls := 0
			read := func(ctx context.Context) (clusterSSHPreparationRecoveryReissuedCustody, error) {
				proof, err := controller(ctx)
				if err != nil {
					return proof, err
				}
				calls++
				id := f.value.Cohort.Targets[0].Binding.TargetID
				node := proof.Targets[id]
				switch mode {
				case "wrong authority":
					proof.Original.AuthoritySHA256 = strings.Repeat("b", 64)
				case "wrong source":
					proof.Original.Source.Binding.SourceUID = newClusterUUID()
				case "partial pair":
					delete(proof.Targets, id)
				case "extra target":
					proof.Targets[newClusterUUID()] = node
				case "unknown target":
					delete(proof.Targets, id)
					proof.Targets[newClusterUUID()] = node
				case "source UID":
					node.NodeUID = f.value.Source.Members[0].NodeUID
					proof.Targets[id] = node
				case "repeated UID":
					node.NodeUID = proof.Targets[f.value.Cohort.Targets[1].Binding.TargetID].NodeUID
					proof.Targets[id] = node
				case "wrong boot":
					node.BootID = newClusterUUID()
					proof.Targets[id] = node
				case "not ready":
					node.Ready = false
					proof.Targets[id] = node
				case "later UID":
					if calls == 2 {
						node.NodeUID = newClusterUUID()
						proof.Targets[id] = node
					}
				}
				return proof, nil
			}
			used := false
			err = withClusterSSHPreparationRecoveryReissuedMaterial(context.Background(), f.value, f.payload, f.source, consumer, root, read,
				func(context.Context, clusterSSHPreparationRecoveryReissuedCustody, *engineidentity.Material, func(context.Context) error) error {
					used = true
					return nil
				})
			if err != clusterbootstrap.ErrSessionAuthority || used {
				t.Fatal("unbound controller projection reached material consumer", err)
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryReissuedCustodyPreservesInitialGuards(t *testing.T) {
	f := newReissuedCustodyFixture(t, false, 1)
	initial := clusterSSHPreparationAuthority{Lease: f.value.Leases[0], Cohort: f.value.Cohort, Source: f.value.Source,
		Baseline: f.value.Baseline, IdentitySource: &f.value.IdentitySource, K3sVersion: clusterbootstrap.K3sPins().Version, PayloadSHA256: strings.Repeat("a", 64)}
	if f.source.valid(initial) || validClusterSSHPreparationLease(initial.Lease) {
		t.Fatal("reissue became initial preparation authority")
	}
	got, err := readClusterSSHPrejoinSourceCustody(context.Background(), f.source, func(context.Context) (clusterSSHPreparationAuthority, error) { return initial, nil }, f.get)
	if err == nil || !reflect.DeepEqual(got, clusterSSHPrejoinSourceCustody{}) {
		t.Fatal("old custody accepted reissued lease")
	}
}
