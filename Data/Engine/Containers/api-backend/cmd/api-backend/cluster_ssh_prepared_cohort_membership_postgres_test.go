package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Snapshot every retained SQL row, including credentials and target custody.
// Intentional fault writes replace the oracle immediately; observation itself
// must leave the entire resulting state unchanged, not merely its parent phase.
func preparedCohortSQLState(ctx context.Context, f sshPreparationAuthorityFixture) (string, error) {
	var state string
	err := f.c.store.db.QueryRowContext(ctx, `SELECT json_build_array(to_jsonb(o),to_jsonb(c),
 (SELECT json_agg(to_jsonb(t) ORDER BY t.id) FROM engine.cluster_onboarding_targets t WHERE t.operation_id=o.id),
 (SELECT json_agg(to_jsonb(p) ORDER BY p.target_id) FROM engine.cluster_onboarding_credentials p JOIN engine.cluster_onboarding_targets t ON t.id=p.target_id WHERE t.operation_id=o.id),
 (SELECT json_agg(to_jsonb(n) ORDER BY n.id) FROM engine.cluster_nodes n),
 (SELECT json_agg(to_jsonb(l) ORDER BY l.name) FROM engine.cluster_application_leases l WHERE l.name=$2),
 (SELECT json_agg(to_jsonb(e) ORDER BY e.id) FROM engine.cluster_operation_events e WHERE e.operation_id=o.id))::text
 FROM engine.cluster_operations o CROSS JOIN engine.cluster_state c WHERE o.id=$1 AND c.id=1`, f.op.ID, clusterControllerLeaseName).Scan(&state)
	return state, err
}

func TestClusterSSHPreparedCohortMembershipPostgresCustody(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "final payload drift", "final credential drift", "controller during", "final Node drift", "third view disagreement", "last member failure", "cross-kind Job replay", "late cross-kind Pod replay", "last Close error"} {
		t.Run(mode, func(t *testing.T) { exercisePreparedCohortSQL(t, mode) })
	}
}

func TestClusterSSHPreparedCohortMembershipPostgresJoinsCleanup(t *testing.T) {
	exercisePreparedCohortSQL(t, "cancel during last Close")
}

func exercisePreparedCohortSQL(t *testing.T, mode string) {
	t.Helper()
	// This producer settles real original preparation claims through SQL. No
	// manufactured prepared authority, worker relabel or alternate reader enters
	// the controller API under test.
	f, a, targetUIDs := newSSHPreparedAuthorityFixture(t, mode == "replacement")
	before, _, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op)
	if err != nil {
		t.Fatal("original settled authority", err)
	}
	f.c.store.db.SetMaxOpenConns(1)
	// Fixture setup has its own 15s bound; six observations use a fresh bounded
	// phase without extending any stored controller/credential/target expiry.
	phase, phaseCancel := context.WithTimeout(context.WithoutCancel(f.ctx), 60*time.Second)
	defer phaseCancel()
	ctx, cancel := context.WithCancel(phase)
	defer cancel()
	state, err := preparedCohortSQLState(phase, f)
	if err != nil {
		t.Fatal(err)
	}
	get := func(_ context.Context, path string, out any) error {
		raw, err := json.Marshal(sshMembershipNativeFixture(a, targetUIDs, path))
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, out)
	}
	nodes, targetNodes, err := readClusterSSHPreparedMembershipNodes(phase, get, before)
	if err != nil || len(nodes) != 3 {
		t.Fatal("native fixture", err)
	}
	networks := map[string]clusterbootstrap.SourceNetwork{}
	for _, node := range nodes {
		n := node.Node
		networks[n.Hostname] = sshSourceNetworkFixture(clusterSSHSourceMember{NodeUID: n.NodeUID, Name: n.Hostname, Address: n.Address, MachineID: n.MachineID, BootID: n.BootID}, before.version)
	}
	originalSource := before.receipt.Targets[0].Prejoin.Receipt.Source
	var roster []string
	var prepared []clusterbootstrap.MembershipPreparedTarget
	for _, source := range before.proof.Source.Members {
		roster = append(roster, source.NodeUID)
	}
	for _, target := range before.receipt.Targets {
		prepared = append(prepared, clusterbootstrap.MembershipPreparedTarget{Expected: target.Prejoin.Expected, Receipt: target.Prejoin.Receipt})
	}
	var mu sync.Mutex
	posts, podReads, finalNodes := 0, 0, 0
	closedFinal, injected := false, false
	var invariantFailure error
	var job map[string]any
	var jobUIDs, podUIDs, nonces, hosts []string
	var observations []clusterbootstrap.MembershipObservation
	// Fixture defects must fail independently of expected domain rejection.
	fault := func(format string, args ...any) {
		invariantFailure = errors.Join(invariantFailure, fmt.Errorf(format, args...))
	}
	drift := func(query string, args ...any) {
		result, err := f.c.store.db.ExecContext(phase, query, args...)
		if err != nil {
			fault("fault write: %v", err)
			return
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			fault("fault row count: %d %v", n, err)
			return
		}
		state, err = preparedCohortSQLState(phase, f)
		if err != nil {
			fault("fault snapshot: %v", err)
			return
		}
		injected = true
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if err := recoveryProgressSQLPoolAvailable(phase, f); err != nil {
			fault("HTTP pool release: %v", err)
			w.WriteHeader(500)
			return
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-cohort-sql" {
			fault("controller authentication changed")
		}
		if r.URL.Path == "/api/v1/nodes" || r.URL.Path == "/api/v1/namespaces/kube-system" {
			if r.Method != http.MethodGet || r.URL.RawQuery != "" {
				fault("non-observation Node request")
			}
			object := sshMembershipNativeFixture(a, targetUIDs, r.URL.Path)
			if r.URL.Path == "/api/v1/nodes" && closedFinal {
				finalNodes++
				// First fence belongs to the last Job; second is the final
				// whole-cohort fence after every response body has closed.
				if finalNodes == 2 {
					switch mode {
					case "final payload drift":
						drift(`UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID)
					case "final credential drift":
						drift(`UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, a.Cohort.Targets[0].Binding.TargetID)
					case "final Node drift":
						items := anySlice(object["items"])
						sourceActionMap(items[0].(map[string]any), "metadata")["uid"] = newClusterUUID()
						injected = true
					}
				}
			}
			_ = json.NewEncoder(w).Encode(object)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/apis/batch/v1/namespaces/borealis/jobs" {
			posts++
			job = nil
			if json.NewDecoder(r.Body).Decode(&job) != nil {
				fault("Job body")
				w.WriteHeader(500)
				return
			}
			spec := sourceActionMap(sourceActionMap(sourceActionMap(job, "spec"), "template"), "spec")
			container := anySlice(spec["containers"])[0].(map[string]any)
			command, args := clusterStringSlice(container["command"]), clusterStringSlice(container["args"])
			want := "source-network-client"
			wantArgs := 1
			if posts%2 == 0 {
				want = "source-membership-client"
				wantArgs = 2
			}
			if posts > 6 || len(command) != 2 || command[0] != "/usr/local/bin/borealis-node-manager" || command[1] != want || len(args) != wantArgs || spec["nodeName"] != nodes[(posts-1)/2].Node.Hostname {
				fault("Job escaped fixed command or retained observer")
				w.WriteHeader(500)
				return
			}
			if spec["activeDeadlineSeconds"] != float64(20) || sourceActionMap(job, "spec")["activeDeadlineSeconds"] != float64(25) || sourceActionMap(job, "spec")["ttlSecondsAfterFinished"] != float64(60) {
				fault("remote Job bounds changed")
			}
			nonces, hosts = append(nonces, args[0]), append(hosts, spec["nodeName"].(string))
			jobUID, podUID := newClusterUUID(), newClusterUUID()
			if mode == "cross-kind Job replay" && posts == 2 {
				jobUID, injected = jobUIDs[0], true
			}
			if mode == "late cross-kind Pod replay" && posts == 6 {
				podUID, injected = podUIDs[4], true
			}
			jobUIDs, podUIDs = append(jobUIDs, jobUID), append(podUIDs, podUID)
			sourceActionMap(job, "metadata")["uid"] = jobUID
			job["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
			if mode == "controller during" && posts == 4 {
				drift(`UPDATE engine.cluster_application_leases SET holder='other-controller' WHERE name=$1`, clusterControllerLeaseName)
			}
			if mode == "last member failure" && posts == 6 {
				injected = true
				w.WriteHeader(502)
				return
			}
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(job)
			return
		}
		if r.Method == http.MethodGet && job != nil && r.URL.Path == "/apis/batch/v1/namespaces/borealis/jobs/"+sourceActionMap(job, "metadata")["name"].(string) {
			_ = json.NewEncoder(w).Encode(job)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/namespaces/borealis/pods" || job == nil {
			fault("unexpected native path or mutation: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		podReads++
		network := networks[hosts[posts-1]]
		pod := sourceActionPod(t, job, network, podUIDs[posts-1])
		if posts%2 == 0 {
			container := anySlice(sourceActionMap(sourceActionMap(sourceActionMap(job, "spec"), "template"), "spec")["containers"])[0].(map[string]any)
			request, err := clusterbootstrap.DecodeMembershipObservationRequest(clusterStringSlice(container["args"])[1])
			if err != nil || request.Source != originalSource || request.Observer == nil || request.ObserverUID() != network.NodeUID || request.Network != network || request.KubeSystemUID != before.proof.Source.KubeSystemUID || !reflect.DeepEqual(request.Nodes, nodes) || !reflect.DeepEqual(request.OriginalSourceNodeUIDs, roster) || !reflect.DeepEqual(request.Prepared, prepared) {
				fault("lost exact original source partition, all prepared receipts or observer binding: %v", err)
				w.WriteHeader(500)
				return
			}
			result := sshCohortMembershipResultFixture(request)
			if mode == "third view disagreement" && posts == 6 {
				for i, member := range result.Membership.Members {
					if member.Address == a.Source.Members[0].Address {
						result.Membership.Members[i].ID--
						if result.Membership.LocalID == member.ID {
							result.Membership.LocalID--
						}
					}
				}
				injected = true
			}
			if !result.Valid(request) {
				fault("invalid individual membership fixture")
			}
			observations = append(observations, result)
			pod = sshCohortMembershipPod(t, job, request, result, podUIDs[posts-1])
		}
		if posts == 6 {
			w.Header().Set("X-Test-Final-Pod", "true")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{pod}, "metadata": map[string]any{}})
	}))
	defer server.Close()
	closed, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	var active, closes atomic.Int64
	client := server.Client()
	base := client.Transport
	transport := &cohortMembershipTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(r)
		if response != nil && response.Body != nil {
			active.Add(1)
			last := response.Header.Get("X-Test-Final-Pod") == "true"
			response.Body = &cohortMembershipBody{response.Body, func() error {
				defer active.Add(-1)
				closes.Add(1)
				mu.Lock()
				if err := recoveryProgressSQLPoolAvailable(phase, f); err != nil {
					fault("Close pool release: %v", err)
				}
				if last {
					closedFinal = true
					if mode == "last Close error" || mode == "cancel during last Close" {
						injected = true
					}
				}
				mu.Unlock()
				if last && mode == "cancel during last Close" {
					close(closed)
					<-release
				}
				if last && (mode == "last Close error" || mode == "cancel during last Close") {
					return errors.New("synthetic final response Close error")
				}
				return nil
			}}
		}
		return response, err
	}}
	client.Transport = transport
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: client, token: "synthetic-cohort-sql"}, namespace: "borealis", controllerHolder: f.c.holder, actionImage: "registry.example/api@sha256:" + strings.Repeat("a", 64), jobPollInterval: time.Millisecond}
	f.c.runner = runner
	var got clusterSSHPreparedCohortMembershipObservation
	if mode == "cancel during last Close" {
		done := make(chan struct{})
		go func() { defer close(done); got, err = f.c.observeClusterSSHPreparedCohortMembership(ctx, f.op) }()
		defer func() {
			cancel()
			unblock.Do(func() { close(release) })
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("collector did not join after cleanup release")
			}
		}()
		select {
		case <-closed:
		case <-done:
			t.Fatal("collector returned before final Close gate", err)
		case <-phase.Done():
			t.Fatal("final Close gate timeout")
		}
		cancel()
		select {
		case <-done:
			t.Fatal("cancelled collector escaped owned Close")
		case <-time.After(25 * time.Millisecond):
		}
		unblock.Do(func() { close(release) })
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("cancelled collector did not join")
		}
	} else {
		got, err = f.c.observeClusterSSHPreparedCohortMembership(ctx, f.op)
	}
	mu.Lock()
	defer mu.Unlock()
	if invariantFailure != nil {
		t.Fatal("fixture invariant", invariantFailure)
	}
	success := mode == "pair" || mode == "replacement"
	if (err == nil) != success {
		t.Fatalf("wrong cohort result: mode=%s err=%v posts=%d pods=%d finalNodes=%d", mode, err, posts, podReads, finalNodes)
	}
	if success {
		if posts != 6 || podReads != 6 || finalNodes != 2 || len(got.Views) != 3 || got.Prepared.OperationID != f.op.ID || got.Prepared.Controller != f.c.holder || got.Prepared.Attempt != f.op.Attempt || got.Prepared.PayloadSHA256 != preparationReceiptHash(before.payload) || got.Prepared.ClusterID != a.Source.ClusterID || got.Prepared.KubeSystemUID != a.Source.KubeSystemUID || got.Prepared.ObservedAt <= 0 || !reflect.DeepEqual(got.Prepared.Nodes, targetNodes) {
			t.Fatal("incomplete retained cohort binding")
		}
		for _, identities := range [][]string{nonces, jobUIDs, podUIDs} {
			seen := map[string]bool{}
			for _, identity := range identities {
				if identity == "" || seen[identity] {
					t.Fatal("six fresh Job identities not distinct")
				}
				seen[identity] = true
			}
		}
		for i, view := range got.Views {
			if !reflect.DeepEqual(view.Observation, observations[i]) || view.Nonce != nonces[2*i+1] || view.JobUID != jobUIDs[2*i+1] || view.PodUID != podUIDs[2*i+1] || view.Observation.LocalNode != nodes[i].Node {
				t.Fatal("returned view lost exact member IDs or native receipt custody")
			}
		}
	} else {
		if !injected || !reflect.DeepEqual(got, clusterSSHPreparedCohortMembershipObservation{}) {
			t.Fatal("fault not reached or partial cohort escaped")
		}
		wantPosts := 6
		if mode == "controller during" {
			wantPosts = 4
		} else if mode == "cross-kind Job replay" {
			wantPosts = 2
		}
		if posts != wantPosts {
			t.Fatalf("fault reached wrong native boundary: posts=%d want=%d", posts, wantPosts)
		}
	}
	if active.Load() != 0 || closes.Load() == 0 || transport.idleCalls.Load() != 0 || runner.kube.httpClient != client || client.Transport != transport {
		t.Fatal("owned cleanup escaped or shared transport changed")
	}
	assertCtx, assertCancel := context.WithTimeout(context.WithoutCancel(f.ctx), 5*time.Second)
	defer assertCancel()
	after, stateErr := preparedCohortSQLState(assertCtx, f)
	if stateErr != nil || after != state || f.c.store.db.Stats().InUse != 0 {
		t.Fatal("observation changed retained SQL custody, payload, credential, Node, event or phase", stateErr)
	}
}
