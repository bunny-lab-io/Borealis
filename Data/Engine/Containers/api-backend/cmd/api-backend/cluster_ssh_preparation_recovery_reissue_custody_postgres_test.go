package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Reuse actual original claims, confirmation and SQL-issued lineage. Native
// fixture values come from that retained work; no old preparation authority is
// manufactured to make a recovery handle pass an unrelated phase guard.
func recoveryReissuedCustodySQLFixture(t *testing.T, replacement, fourth bool) (sshPreparationAuthorityFixture, clusterSSHPreparationRecoveryReissuedProofClaim, clusterSSHPreparationWork, clusterSSHPrejoinSource, map[string]any, map[string]clusterbootstrap.K3sJoinedNode) {
	t.Helper()
	f, _, claim, _ := recoveryReissuedSourceSQLFixture(t, replacement, fourth)
	f = recoveryTransportSQLExercise(t, f)
	snapshot, err := f.c.readClusterSSHPreparationRecoveryWork(f.ctx, f.op)
	if err != nil {
		t.Fatal("retained reissued work", err)
	}
	work := snapshot.work
	lineage, err := parseClusterSSHPreparationRecoveryLineage(snapshot.payload, work)
	if err != nil || snapshot.payload != claim.payload || !reflect.DeepEqual(lineage, claim.Lineage) {
		t.Fatal("retained exact reissued lineage", err)
	}
	_, process, pod := existingIdentitySourceFixture(t)
	found := false
	for _, member := range work.Proof.Source.Members {
		if member.NodeID == work.IdentitySource.SourceID && member.NodeUID == work.IdentitySource.SourceUID {
			process.NodeName, found = member.Name, true
		}
	}
	if !found {
		t.Fatal("confirmation selected no retained original source")
	}
	metadata := clusterSSHStorageMap(pod, "metadata")
	clusterSSHStorageMap(metadata, "labels")["borealis.io/engine-node"] = process.NodeName
	clusterSSHStorageMap(metadata, "annotations")["borealis.io/revision"] = work.Baseline.SourceSHA
	clusterSSHStorageMap(pod, "spec")["nodeName"] = process.NodeName
	source := clusterSSHPrejoinSource{Binding: work.IdentitySource, Process: process}
	targets := map[string]clusterbootstrap.K3sJoinedNode{}
	for _, target := range work.Proof.Cohort.Targets {
		identity := clusterbootstrap.K3sJoinedNodeIdentity{Hostname: target.Report.Hostname, MachineID: target.Report.MachineID, BootID: target.Report.BootID, Address: target.Binding.Address, K3sVersion: clusterbootstrap.K3sPins().Version}
		raw, err := json.Marshal(sshJoinedNodeFixture(target, identity.K3sVersion, newClusterUUID()))
		if err != nil {
			t.Fatal(err)
		}
		node, err := clusterbootstrap.ParseK3sJoinedNodeIdentity(raw, identity)
		if err != nil || !node.Ready {
			t.Fatal("complete joined native fixture", err)
		}
		targets[target.Binding.TargetID] = node
	}
	return f, claim, work, source, pod, targets
}

// Recovery's existing scope may renew only target expiry. Every other target
// column, sealed credential, source row, event and parent payload stays exact.
func recoveryReissuedCustodySQLState(ctx context.Context, f sshPreparationAuthorityFixture) (string, error) {
	var state string
	err := f.c.store.db.QueryRowContext(ctx, `SELECT json_build_array(to_jsonb(o),to_jsonb(c),
 (SELECT json_agg(to_jsonb(t)-'lease_expires_at' ORDER BY t.id) FROM engine.cluster_onboarding_targets t WHERE t.operation_id=o.id),
 (SELECT json_agg(to_jsonb(p) ORDER BY p.target_id) FROM engine.cluster_onboarding_credentials p JOIN engine.cluster_onboarding_targets t ON t.id=p.target_id WHERE t.operation_id=o.id),
 (SELECT json_agg(to_jsonb(n) ORDER BY n.id) FROM engine.cluster_nodes n),
 (SELECT json_agg(to_jsonb(l) ORDER BY l.name) FROM engine.cluster_application_leases l WHERE l.name=$2),
 (SELECT json_agg(to_jsonb(e) ORDER BY e.id) FROM engine.cluster_operation_events e WHERE e.operation_id=o.id))::text
 FROM engine.cluster_operations o CROSS JOIN engine.cluster_state c WHERE o.id=$1 AND c.id=1`, f.op.ID, clusterControllerLeaseName).Scan(&state)
	return state, err
}

func TestClusterSSHPreparationRecoveryReissueCustodyPostgresFences(t *testing.T) {
	for _, mode := range []string{"pair Q3", "replacement Q4", "worker process mismatch", "incomplete cohort", "target UID during read", "source Pod during read", "source Node after callback", "local process after callback", "payload after callback", "sibling credential after callback", "tip holder after callback", "material after callback", "Close failure after callback", "namespace child expiry", "Pod child expiry", "client expiry"} {
		t.Run(mode, func(t *testing.T) { exerciseReissuedCustodySQL(t, mode) })
	}
}

func TestClusterSSHPreparationRecoveryReissueCustodyPostgresJoinsCleanup(t *testing.T) {
	exerciseReissuedCustodySQL(t, "cancel during Close")
}

func exerciseReissuedCustodySQL(t *testing.T, mode string) {
	t.Helper()
	f, claim, work, source, pod, targets := recoveryReissuedCustodySQLFixture(t, mode == "replacement Q4", mode == "replacement Q4")
	state, err := recoveryReissuedCustodySQLState(f.ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	wantPod, err := clusterIdentityPodCustody(pod, work.Baseline.SourceSHA, source.Process)
	if err != nil {
		t.Fatal("original source Pod", err)
	}
	root := t.TempDir()
	wantMaterial := sshIdentitySourceMaterial(t, root)
	consumer, err := engineidentity.AcquireConsumer(root)
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	t.Setenv("BOREALIS_CLUSTER_CONTROLLER_ELIGIBLE", "1")
	t.Setenv("BOREALIS_API_BACKGROUND_LOOPS", "1")
	t.Setenv("BOREALIS_ENGINE_POD_NAME", source.Process.PodName)
	t.Setenv("BOREALIS_ENGINE_POD_UID", source.Process.PodUID)
	t.Setenv("BOREALIS_ENGINE_NODE_NAME", source.Process.NodeName)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	var mu sync.Mutex
	var invariantFailure error
	fault := func(format string, args ...any) {
		invariantFailure = errors.Join(invariantFailure, fmt.Errorf(format, args...))
	}
	reads, nodeReads, podReads, consumed := 0, 0, 0, 0
	afterCallback, injected := false, false
	expiry := strings.Contains(mode, "expiry")
	var proofScope context.Context
	var proofCheck func(context.Context) error
	var readStarted time.Time
	var siblingChecked bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		reads++
		if err := recoveryProgressSQLPoolAvailable(f.ctx, f); err != nil {
			fault("native read retained SQL pool: %v", err)
			w.WriteHeader(500)
			return
		}
		if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer synthetic-reissued-custody" {
			fault("native request escaped original read-only controller transport")
			w.WriteHeader(500)
			return
		}
		var object any
		switch r.URL.Path {
		case "/api/v1/namespaces/kube-system":
			object = map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": "kube-system", "uid": work.Proof.Source.KubeSystemUID, "resourceVersion": "1"}}
		case "/api/v1/nodes":
			nodeReads++
			var items []any
			for _, member := range work.Proof.Source.Members {
				node := sshSourceKubernetesFixture(member)
				node["apiVersion"], node["kind"] = "v1", "Node"
				metadata := sourceActionMap(node, "metadata")
				metadata["resourceVersion"] = "1"
				metadata["labels"] = map[string]any{"borealis.io/engine-node": "true", "borealis.io/application-state": "active"}
				if mode == "source Node after callback" && afterCallback {
					metadata["uid"], injected = newClusterUUID(), true
				}
				items = append(items, node)
			}
			for i, target := range work.Proof.Cohort.Targets {
				uid := targets[target.Binding.TargetID].NodeUID
				if mode == "target UID during read" && nodeReads == 2 && i == len(work.Proof.Cohort.Targets)-1 {
					uid, injected = newClusterUUID(), true
				}
				node := sshJoinedNodeFixture(target, clusterbootstrap.K3sPins().Version, uid)
				sourceActionMap(node, "metadata")["resourceVersion"] = "1"
				if mode == "incomplete cohort" && i == len(work.Proof.Cohort.Targets)-1 {
					sourceActionMap(node, "status")["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
					injected = true
				}
				items = append(items, node)
			}
			object = map[string]any{"apiVersion": "v1", "kind": "NodeList", "items": items, "metadata": map[string]any{}}
		case "/api/v1/namespaces/borealis/pods/" + source.Process.PodName:
			podReads++
			var copy map[string]any
			raw, _ := json.Marshal(pod)
			if json.Unmarshal(raw, &copy) != nil {
				fault("source Pod fixture copy")
			}
			if mode == "source Pod during read" && podReads == 2 {
				sourceActionMap(copy, "metadata")["uid"], injected = newClusterUUID(), true
			}
			object = copy
		default:
			fault("unexpected native surface: %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		if afterCallback {
			w.Header().Set("X-Test-After-Callback", "true")
		}
		_ = json.NewEncoder(w).Encode(object)
	}))
	defer server.Close()
	closed, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	var active, closes atomic.Int64
	client := server.Client()
	if mode == "client expiry" {
		client.Timeout = 500 * time.Millisecond
	}
	borrowedTimeout := client.Timeout
	base := client.Transport
	transport := &cohortMembershipTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(r)
		if response != nil && response.Body != nil {
			active.Add(1)
			late := response.Header.Get("X-Test-After-Callback") == "true"
			response.Body = &cohortMembershipBody{response.Body, func() error {
				defer active.Add(-1)
				closes.Add(1)
				mu.Lock()
				if err := recoveryProgressSQLPoolAvailable(f.ctx, f); err != nil {
					fault("owned Close retained SQL pool: %v", err)
				}
				block := late && mode == "cancel during Close" && !injected
				fail := late && mode == "Close failure after callback" && !injected
				expire := !injected && (mode == "namespace child expiry" && r.URL.Path == "/api/v1/namespaces/kube-system" ||
					mode == "Pod child expiry" && strings.Contains(r.URL.Path, "/pods/") || mode == "client expiry")
				if block || fail || expire {
					injected = true
				}
				mu.Unlock()
				if expire {
					deadline, ok := r.Context().Deadline()
					bound := 5 * time.Second
					if mode == "Pod child expiry" {
						bound = 10 * time.Second
					} else if mode == "client expiry" {
						bound = borrowedTimeout
					}
					remaining := time.Until(deadline)
					if !ok || remaining > bound || remaining < bound/2 || r.Context().Err() != nil {
						t.Error("fixture missed actual live native request deadline")
					}
					<-r.Context().Done()
					// The request deadline expires inside nil-error Close, before
					// the reader's unchanged fifteen-second bound and SQL scope.
					if proofScope.Err() != nil || ctx.Err() != nil || time.Since(readStarted) >= 15*time.Second || proofCheck(proofScope) != nil {
						t.Error("native child expiry lost enclosing original authority")
					}
				}
				if block {
					close(closed)
					<-release
				}
				if block || fail {
					return errors.New("synthetic owned response Close failure")
				}
				return nil
			}}
		}
		return response, err
	}}
	client.Transport = transport
	runner := &kubernetesClusterStepRunner{controllerHolder: f.c.holder, namespace: "borealis", kube: &kubernetesAPIClient{baseURL: server.URL, token: "synthetic-reissued-custody", httpClient: client}}
	f.c.runner = runner
	var escaped func(context.Context) error
	consume := func(scope context.Context, proof clusterSSHPreparationRecoveryReissuedCustody, material *engineidentity.Material, refresh func(context.Context) error) error {
		mu.Lock()
		consumed++
		if proof.Original.Source != source || proof.Original.Pod != wantPod || !validClusterSSHPreparationDigest(proof.Original.AuthoritySHA256) || !maps.Equal(proof.Targets, targets) || !material.Equal(wantMaterial) {
			fault("material callback lost exact original source, Pod or complete target identities")
		}
		if err := recoveryProgressSQLPoolAvailable(scope, f); err != nil {
			fault("material callback retained SQL: %v", err)
		}
		escaped = refresh
		mu.Unlock()
		refreshStarted := time.Now()
		if err := refresh(scope); err != nil {
			mu.Lock()
			fault("valid in-scope refresh failed after %s (outer context %v): %v", time.Since(refreshStarted), scope.Err(), err)
			mu.Unlock()
			return err
		}
		if mode == "pair Q3" || mode == "replacement Q4" {
			t.Logf("material refresh completed in %s inside unchanged 3s challenge", time.Since(refreshStarted))
		}
		mu.Lock()
		defer mu.Unlock()
		afterCallback = true
		var query string
		var args []any
		switch mode {
		case "local process after callback":
			t.Setenv("BOREALIS_ENGINE_POD_UID", newClusterUUID())
			injected = true
		case "payload after callback":
			query, args = `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, []any{f.op.ID}
		case "sibling credential after callback":
			query, args = `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, []any{claim.Claims[len(claim.Claims)-1].Lease.TargetID}
		case "tip holder after callback":
			query, args = `UPDATE engine.cluster_onboarding_targets SET lease_holder=$2 WHERE id=$1`, []any{claim.Claims[len(claim.Claims)-1].Lease.TargetID, newClusterUUID()}
		case "material after callback":
			if err := os.WriteFile(filepath.Join(root, engineidentity.EngineSecretPath), []byte(strings.Repeat("m", 64)+"\n"), 0o600); err != nil {
				fault("material fault fixture: %v", err)
			}
			injected = true
		}
		if query != "" {
			result, err := f.c.store.db.ExecContext(f.ctx, query, args...)
			if err != nil {
				fault("SQL fault fixture: %v", err)
			} else if n, err := result.RowsAffected(); err != nil || n != 1 {
				fault("SQL fault fixture row count: %d %v", n, err)
			} else {
				state, err = recoveryReissuedCustodySQLState(f.ctx, f)
				if err != nil {
					fault("SQL fault snapshot: %v", err)
				}
				injected = true
			}
		}
		return nil
	}
	var controllerRead clusterSSHPreparationRecoveryReissuedCustodyRead
	invoke := func(parent context.Context) error {
		// Component harness only: production controller and API worker have
		// different process roles. No authenticated reissued broker transport
		// exists yet; this direct callback is not production role integration.
		return f.c.withClusterSSHPreparationRecoveryReissuedProof(parent, f.aegis, claim,
			func(scope context.Context, value clusterSSHPreparationRecoveryReissuedProofAuthority, check func(context.Context) error) error {
				var readerChecks atomic.Int64
				trackedCheck := func(ctx context.Context) error { readerChecks.Add(1); return check(ctx) }
				originalRead := f.c.preparationRecoveryReissuedCustodyRead(value, claim.payload, source, trackedCheck)
				controllerRead = originalRead
				if expiry {
					proofScope, proofCheck = scope, check
					controllerRead = func(caller context.Context) (clusterSSHPreparationRecoveryReissuedCustody, error) {
						readStarted = time.Now()
						proof, err := originalRead(caller)
						if injected && err != nil && !siblingChecked {
							siblingChecked = true
							if proofCheck(scope) != nil {
								t.Error("native request failure destroyed original SQL authority")
							}
							// The failed reader must reject before SQL/native IO even
							// after the original request's closure has completed.
							beforeChecks, beforeReads := readerChecks.Load(), reads
							replayCtx, replayCancel := context.WithTimeout(scope, 250*time.Millisecond)
							replayed, replayErr := originalRead(replayCtx)
							if replayErr == nil || !reflect.DeepEqual(replayed, clusterSSHPreparationRecoveryReissuedCustody{}) || replayCtx.Err() != nil ||
								readerChecks.Load() != beforeChecks || reads != beforeReads {
								t.Error("expired reader revived or reached SQL/native IO")
							}
							replayCancel()
							fresh := f.c.preparationRecoveryReissuedCustodyRead(value, claim.payload, source, check)
							sibling, siblingErr := fresh(scope)
							if siblingErr != nil || sibling.Original.Source != source || sibling.Original.Pod != wantPod || !maps.Equal(sibling.Targets, targets) {
								t.Error("failed reader poisoned fresh borrowed-client sibling", siblingErr)
							}
						}
						return proof, err
					}
					// The material helper's refresh has its own three-second
					// challenge bound. Exercise this controller reader directly
					// under its original SQL scope to reach real 5s/10s children
					// inside its unchanged 15s limit, without relaxing either.
					_, err := controllerRead(scope)
					return err
				}
				if mode == "pair Q3" || mode == "replacement Q4" || mode == "worker process mismatch" {
					// Controller proves selected API Pod without requiring its
					// own downward identity to impersonate that API process.
					t.Setenv("BOREALIS_ENGINE_POD_NAME", "cluster-controller-fixture")
					t.Setenv("BOREALIS_ENGINE_POD_UID", newClusterUUID())
					t.Setenv("BOREALIS_ENGINE_NODE_NAME", "controller-fixture")
					t.Setenv("BOREALIS_API_BACKGROUND_LOOPS", "0")
					proof, err := controllerRead(scope)
					if err != nil || proof.Original.Source != source || proof.Original.Pod != wantPod || !maps.Equal(proof.Targets, targets) {
						mu.Lock()
						fault("controller required worker-local identity or lost native custody: %v", err)
						mu.Unlock()
						return errors.New("controller component fixture failed")
					}
					if mode == "worker process mismatch" {
						injected = true
					} else {
						t.Setenv("BOREALIS_ENGINE_POD_NAME", source.Process.PodName)
						t.Setenv("BOREALIS_ENGINE_POD_UID", source.Process.PodUID)
						t.Setenv("BOREALIS_ENGINE_NODE_NAME", source.Process.NodeName)
						t.Setenv("BOREALIS_API_BACKGROUND_LOOPS", "1")
					}
				}
				return withClusterSSHPreparationRecoveryReissuedMaterial(scope, value, claim.payload, source, consumer, root, controllerRead, consume)
			})
	}
	if mode == "cancel during Close" {
		done := make(chan error, 1)
		go func() { done <- invoke(ctx) }()
		joined := false
		defer func() {
			cancel()
			unblock.Do(func() { close(release) })
			if !joined {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("custody did not join owned Close")
				}
			}
		}()
		select {
		case <-closed:
		case err = <-done:
			joined = true
			t.Fatal("custody returned before Close gate", err)
		case <-ctx.Done():
			t.Fatal("custody Close gate timed out")
		}
		cancel()
		select {
		case err = <-done:
			joined = true
			t.Fatal("custody escaped owned Close on cancellation", err)
		case <-time.After(25 * time.Millisecond):
		}
		unblock.Do(func() { close(release) })
		select {
		case err = <-done:
			joined = true
		case <-time.After(5 * time.Second):
			t.Fatal("custody did not join cleanup release")
		}
	} else {
		err = invoke(ctx)
	}
	mu.Lock()
	if invariantFailure != nil {
		mu.Unlock()
		t.Fatal("fixture invariant", invariantFailure)
	}
	success := mode == "pair Q3" || mode == "replacement Q4"
	if (err == nil) != success {
		mu.Unlock()
		t.Fatalf("wrong reissued custody result: %s err=%v native=%d nodes=%d pods=%d consumed=%d", mode, err, reads, nodeReads, podReads, consumed)
	}
	wantConsumed := 1
	if mode == "worker process mismatch" || mode == "incomplete cohort" || mode == "target UID during read" || mode == "source Pod during read" || expiry {
		wantConsumed = 0
	}
	if consumed != wantConsumed || !success && !injected || success && (nodeReads < 12 || podReads < 12) {
		mu.Unlock()
		t.Fatal("incomplete input escaped or late fault never reached", consumed, nodeReads, podReads, injected)
	}
	mu.Unlock()
	if active.Load() != 0 || closes.Load() == 0 || transport.idleCalls.Load() != 0 || client.Transport != transport || runner.kube.httpClient != client || client.Timeout != borrowedTimeout || expiry && !siblingChecked {
		t.Fatal("owned cleanup escaped or shared transport changed")
	}
	assertCtx, assertCancel := context.WithTimeout(context.WithoutCancel(f.ctx), 5*time.Second)
	defer assertCancel()
	// Hold the sole connection after all work joined. Both capabilities must
	// reject before SQL, even when durable authority would independently fail.
	connection, connErr := f.c.store.db.Conn(assertCtx)
	if connErr != nil {
		t.Fatal(connErr)
	}
	beforeWait, beforeReads := f.c.store.db.Stats().WaitCount, reads
	replayCtx, replayCancel := context.WithTimeout(assertCtx, 250*time.Millisecond)
	if success {
		if claim.custody.failed.Load() {
			t.Error("successful scope poisoned before copied-handle replay")
		}
		if invoke(replayCtx) == nil {
			t.Error("copied issued custody regained material scope")
		}
	}
	if escaped != nil && escaped(replayCtx) == nil {
		t.Error("escaped material refresh retained authority")
	}
	if controllerRead != nil {
		proof, err := controllerRead(replayCtx)
		if err == nil || !reflect.DeepEqual(proof, clusterSSHPreparationRecoveryReissuedCustody{}) {
			t.Error("escaped controller reader retained whole or partial custody")
		}
	}
	if replayCtx.Err() != nil || f.c.store.db.Stats().WaitCount != beforeWait || reads != beforeReads {
		t.Error("closed capability reached SQL/native IO before rejection")
	}
	replayCancel()
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	after, stateErr := recoveryReissuedCustodySQLState(assertCtx, f)
	if stateErr != nil || after != state || f.c.store.db.Stats().InUse != 0 {
		t.Fatal("custody changed retained rows, payload, credentials, source, phase or events", stateErr)
	}
	if !success && !claim.custody.failed.Load() {
		t.Fatal("failed custody retained reusable issued proof")
	}
}
