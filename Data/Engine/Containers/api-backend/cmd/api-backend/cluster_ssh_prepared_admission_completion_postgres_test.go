package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/json"
	"github.com/lib/pq"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func preparedAdmissionCompletionMemberCleanup(t *testing.T, f sshPreparationAuthorityFixture, id string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := f.c.store.db.ExecContext(ctx, `DELETE FROM engine.cluster_nodes WHERE id=$1`, id); err != nil {
			t.Error("remove owned synthetic admission member", err)
		}
	})
}

func preparedAdmissionCompletionFixture(t *testing.T, replacement bool) (sshPreparationAuthorityFixture, clusterSSHPreparationAuthority, clusterSSHPreparedAuthority, map[string]string) {
	t.Helper()
	f, source, targets := newSSHPreparedAuthorityFixture(t, replacement)
	for _, target := range f.targets {
		preparedAdmissionCompletionMemberCleanup(t, f, target.Binding.TargetID)
	}
	original, now, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op)
	if err != nil {
		t.Fatal(err)
	}
	// SQL boundary fixtures start from genuine settled-v2 receipts. Lifecycle
	// positives separately consume actual native quorum Jobs before final commit.
	if err := f.c.beginClusterSSHPreparedAdmission(f.ctx, f.op, original, preparedAdmissionStoreObservation(original, f.op, f.c.holder, now)); err != nil {
		t.Fatal(err)
	}
	f.op, _, err = f.c.claimOperation(f.ctx)
	if err != nil || f.op.CurrentStep != "apply_membership" || !reflect.DeepEqual(f.op.Payload, parseClusterJSON(original.payload)) {
		t.Fatal("read exact admission operation", err)
	}
	return f, source, original, targets
}

func preparedAdmissionCompletionAdvance(t *testing.T, f *sshPreparationAuthorityFixture, original clusterSSHPreparedAuthority) {
	t.Helper()
	if err := f.c.reserveClusterSSHPreparedAdmissionRun(f.ctx, f.op, original); err != nil {
		t.Fatal("reserve exact admission", err)
	}
	if err := f.c.advanceClusterSSHPreparedAdmission(f.ctx, f.op, original); err != nil {
		t.Fatal("advance exact admission", err)
	}
	var found bool
	var err error
	f.op, found, err = f.c.claimOperation(f.ctx)
	if err != nil || !found || f.op.CurrentStep != "verify_quorum" {
		t.Fatal("claim final verification", err)
	}
}

func preparedAdmissionCompletionProof(t *testing.T, ctx context.Context, f sshPreparationAuthorityFixture, source clusterSSHPreparationAuthority, original clusterSSHPreparedAuthority, targets map[string]string) clusterSSHPreparedObservation {
	t.Helper()
	n := newPreparedMembershipNativeForProof(t, source, original, targets, "", clusterbootstrap.MembershipQuorumProofVersion)
	n.before = func(ctx context.Context) error { return recoveryProgressSQLPoolAvailable(ctx, f) }
	n.closeHook = func(ctx context.Context, _ int) error { return recoveryProgressSQLPoolAvailable(ctx, f) }
	base := n.runner.kube.httpClient.Transport
	n.runner.kube.httpClient.Transport = bootstrapRoundTripper(func(request *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(request)
		if err != nil || request.URL.Path != "/api/v1/nodes" {
			return response, err
		}
		raw, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err != nil {
			return nil, err
		}
		for _, item := range anySlice(object["items"]) {
			metadata := clusterSSHStorageMap(item.(map[string]any), "metadata")
			for _, target := range original.receipt.Targets {
				if metadata["uid"] != target.Prejoin.Receipt.Node.NodeUID {
					continue
				}
				labels := clusterSSHStorageMap(metadata, "labels")
				labels["borealis.io/application-state"] = "active"
				labels["borealis.io/hmr-target"] = "false"
				for _, role := range []string{"control-plane", "edge", "scheduler", "postgres-primary"} {
					labels["borealis.io/"+role+"-eligible"] = "true"
				}
			}
		}
		raw, err = json.Marshal(object)
		if err != nil {
			return nil, err
		}
		response.Body, response.ContentLength = io.NopCloser(bytes.NewReader(raw)), int64(len(raw))
		response.Header.Del("Content-Length")
		return response, nil
	})
	f.c.runner = n.runner
	var observed clusterSSHPreparedObservation
	if err := f.c.withClusterSSHPreparedAdmissionProof(ctx, f.op, func(_ context.Context, value clusterSSHPreparedCohortMembershipObservation, _ clusterSSHPreparedMembershipChecks) error {
		observed = value.Prepared
		for _, view := range value.Views {
			if view.Observation.ListenerProofVersion != clusterbootstrap.MembershipQuorumProofVersion || view.Observation.Quorum == nil {
				t.Fatal("final SQL fixture omitted actual quorum proof")
			}
		}
		return nil
	}); err != nil {
		t.Fatal("final native SQL proof", err, "Jobs", n.posts.Load(), "active responses", n.active.Load())
	}
	if n.posts.Load() != 12 || n.active.Load() != 0 || f.c.store.db.Stats().InUse != 0 {
		t.Fatal("proof did not join native/SQL cleanup")
	}
	return observed
}

func TestClusterSSHPreparedAdmissionCompletionPostgresLifecycle(t *testing.T) {
	for _, mode := range []string{"pair", "replacement retained ID"} {
		t.Run(mode, func(t *testing.T) {
			f, source, original, targets := preparedAdmissionCompletionFixture(t, mode != "pair")
			durableID := ""
			if mode != "pair" {
				durableID = newClusterUUID()
				preparedAdmissionCompletionMemberCleanup(t, f, durableID)
				target := original.proof.Cohort.Targets[0]
				f.exec(t, `INSERT INTO engine.cluster_nodes(id,node_name,hostname,management_ip,architecture,os_version,membership_state,application_state,release_tag,release_sha,roles_json,probe_health_json,last_seen_at,created_at,updated_at)
 VALUES($1,$2,$2,$3,'amd64','Ubuntu 24.04','Removed','drained',$4,$5,'{}','{}',1,1,1)`, durableID, target.Report.Hostname, target.Binding.Address, original.baseline.Release, original.baseline.SourceSHA)
			}
			var originalSources string
			if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT json_agg(to_jsonb(n) ORDER BY n.id)::text FROM engine.cluster_nodes n WHERE membership_state='Active'`).Scan(&originalSources); err != nil {
				t.Fatal(err)
			}
			var invitations int
			if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_admissions`).Scan(&invitations); err != nil {
				t.Fatal(err)
			}
			preparedAdmissionCompletionAdvance(t, &f, original)
			ctx, cancel := context.WithTimeout(context.WithoutCancel(f.ctx), 60*time.Second)
			defer cancel()
			f.c.store.db.SetMaxOpenConns(1)
			observed := preparedAdmissionCompletionProof(t, ctx, f, source, original, targets)
			if err := f.c.finishClusterSSHPreparedAdmission(ctx, f.op, original, observed); err != nil {
				t.Fatal("finish exact verified cohort", err)
			}
			var state, step, kind, payload, pointer, status string
			var attempt int64
			var active, desired, members, credentials, pending int
			if err := f.c.store.db.QueryRowContext(ctx, `SELECT o.state,o.current_step,o.kind,o.attempt,o.payload_json,COALESCE(c.active_operation_id,''),c.active_size,c.desired_size,c.status,
 (SELECT count(*) FROM engine.cluster_nodes WHERE membership_state='Active'),
 (SELECT count(*) FROM engine.cluster_onboarding_credentials p JOIN engine.cluster_onboarding_targets t ON t.id=p.target_id WHERE t.operation_id=o.id),
 (SELECT count(*) FROM engine.cluster_admissions) FROM engine.cluster_operations o CROSS JOIN engine.cluster_state c WHERE o.id=$1 AND c.id=1`, f.op.ID).
				Scan(&state, &step, &kind, &attempt, &payload, &pointer, &active, &desired, &status, &members, &credentials, &pending); err != nil {
				t.Fatal(err)
			}
			if state != "succeeded" || step != "complete" || kind != "ssh_onboarding" || attempt != f.op.Attempt || payload != original.payload || pointer != "" || active != 3 || desired != 3 || status != "Healthy" || members != 3 || credentials != 0 || pending != invitations {
				t.Fatal("incorrect final membership/operation/credential settlement")
			}
			var sources string
			ids := []string{}
			for _, member := range original.proof.Source.Members {
				ids = append(ids, member.NodeID)
			}
			// Original members must retain every column; target rows alone join.
			if err := f.c.store.db.QueryRowContext(ctx, `SELECT json_agg(to_jsonb(n) ORDER BY n.id)::text FROM engine.cluster_nodes n WHERE id=ANY($1)`, pq.Array(ids)).Scan(&sources); err != nil {
				t.Fatal(err)
			}
			if sources != originalSources {
				t.Fatal("completion rewrote original source member")
			}
			for i, target := range original.proof.Cohort.Targets {
				var targetState, targetStep, holder, credentialState, nodeID, name, address string
				var generation, expiry int64
				if err := f.c.store.db.QueryRowContext(ctx, `SELECT state,current_step,lease_holder,lease_generation,lease_expires_at,credential_state FROM engine.cluster_onboarding_targets WHERE id=$1`, target.Binding.TargetID).Scan(&targetState, &targetStep, &holder, &generation, &expiry, &credentialState); err != nil {
					t.Fatal(err)
				}
				if targetState != "joined" || targetStep != "complete" || holder != "" || expiry != 0 || generation != original.receipt.Targets[i].Generation || credentialState != "required" {
					t.Fatal("completion lost settled target custody")
				}
				if err := f.c.store.db.QueryRowContext(ctx, `SELECT id,node_name,management_ip FROM engine.cluster_nodes WHERE node_name=$1 AND membership_state='Active' AND application_state='active'`, target.Report.Hostname).Scan(&nodeID, &name, &address); err != nil {
					t.Fatal(err)
				}
				wantID := target.Binding.TargetID
				if durableID != "" {
					wantID = durableID
				}
				if nodeID != wantID || name != target.Report.Hostname || address != target.Binding.Address {
					t.Fatal("replacement lost durable member identity")
				}
			}
			before, err := preparedCohortSQLState(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			if f.c.finishClusterSSHPreparedAdmission(ctx, f.op, original, observed) == nil {
				t.Fatal("completion replay succeeded")
			}
			assertPreparedAdmissionStoreState(t, f, before, false)
		})
	}
}

func TestClusterSSHPreparedAdmissionCompletionPostgresReservation(t *testing.T) {
	for _, mode := range []string{"running fence", "blocked fence"} {
		t.Run(mode, func(t *testing.T) {
			f, _, original, _ := preparedAdmissionCompletionFixture(t, false)
			if err := f.c.reserveClusterSSHPreparedAdmissionRun(f.ctx, f.op, original); err != nil {
				t.Fatal(err)
			}
			before, err := preparedCohortSQLState(f.ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			if f.c.reserveClusterSSHPreparedAdmissionRun(f.ctx, f.op, original) == nil {
				t.Fatal("admission effect reservation replayed")
			}
			assertPreparedAdmissionStoreState(t, f, before, false)
			if mode == "blocked fence" {
				if err := f.c.recordSSHPreparedAdmissionBlocked(f.ctx, f.op); err != nil {
					t.Fatal(err)
				}
				before, err = preparedCohortSQLState(f.ctx, f)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.c.recordSSHPreparedAdmissionBlocked(f.ctx, f.op); err != nil {
					t.Fatal(err)
				}
			}
			if operation, found, err := f.c.claimOperation(f.ctx); err != nil || found || operation.ID != "" {
				t.Fatal("durable uncertain admission became selectable", found, err)
			}
			assertPreparedAdmissionStoreState(t, f, before, false)
			if mode == "blocked fence" && f.c.advanceClusterSSHPreparedAdmission(f.ctx, f.op, original) == nil {
				t.Fatal("blocked admission advanced")
			}
			assertPreparedAdmissionStoreState(t, f, before, false)
		})
	}
}

func TestClusterSSHPreparedAdmissionCompletionPostgresRejectsDrift(t *testing.T) {
	for _, phase := range []string{"advance", "finish"} {
		for _, mode := range []string{"target generation", "phase", "payload", "controller", "credential", "source", "fence", "proof expiry"} {
			if phase == "advance" && mode == "proof expiry" {
				continue
			}
			t.Run(phase+"/"+mode, func(t *testing.T) {
				f, _, original, _ := preparedAdmissionCompletionFixture(t, false)
				if phase == "finish" {
					preparedAdmissionCompletionAdvance(t, &f, original)
				} else if err := f.c.reserveClusterSSHPreparedAdmissionRun(f.ctx, f.op, original); err != nil {
					t.Fatal(err)
				}
				_, now, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op)
				if err != nil {
					t.Fatal(err)
				}
				observed := preparedAdmissionStoreObservation(original, f.op, f.c.holder, now)
				switch mode {
				case "target generation":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, f.targets[0].Binding.TargetID)
				case "phase":
					f.exec(t, `UPDATE engine.cluster_operations SET current_step='complete' WHERE id=$1`, f.op.ID)
				case "payload":
					f.exec(t, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID)
				case "controller":
					f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=0 WHERE name=$1`, clusterControllerLeaseName)
				case "credential":
					f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, f.targets[0].Binding.TargetID)
				case "source":
					f.exec(t, `UPDATE engine.cluster_nodes SET application_state='drained' WHERE id=$1`, original.proof.Source.Members[0].NodeID)
				case "fence":
					f.exec(t, `UPDATE engine.cluster_operations SET error_text=$2 WHERE id=$1`, f.op.ID, clusterSSHPreparedAdmissionBlocked)
				case "proof expiry":
					observed.ObservedAt -= 60
				}
				before, err := preparedCohortSQLState(f.ctx, f)
				if err != nil {
					t.Fatal(err)
				}
				if phase == "finish" {
					err = f.c.finishClusterSSHPreparedAdmission(f.ctx, f.op, original, observed)
				} else {
					err = f.c.advanceClusterSSHPreparedAdmission(f.ctx, f.op, original)
				}
				if err == nil {
					t.Fatal("drift advanced or completed cohort")
				}
				assertPreparedAdmissionStoreState(t, f, before, false)
			})
		}
	}
}

func TestClusterSSHPreparedAdmissionCompletionPostgresWaitRollback(t *testing.T) {
	for _, mode := range []string{"event proof expiry", "credential deletion proof expiry", "credential deletion controller expiry", "member insert conflict"} {
		t.Run(mode, func(t *testing.T) {
			f, _, original, _ := preparedAdmissionCompletionFixture(t, false)
			preparedAdmissionCompletionAdvance(t, &f, original)
			_, now, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op)
			if err != nil {
				t.Fatal(err)
			}
			observed := preparedAdmissionStoreObservation(original, f.op, f.c.holder, now)
			if mode == "credential deletion controller expiry" {
				f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=floor(extract(epoch FROM clock_timestamp()))::bigint+2 WHERE name=$1`, clusterControllerLeaseName)
			}
			ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
			defer cancel()
			f.c.store.db.SetMaxOpenConns(4)
			lock, err := f.c.store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Rollback()
			pattern := "%INSERT INTO engine.cluster_operation_events%"
			if mode != "member insert conflict" {
				if mode != "credential deletion controller expiry" {
					observed.ObservedAt -= 58
				}
				if mode == "event proof expiry" {
					_, err = lock.ExecContext(ctx, `LOCK TABLE engine.cluster_operation_events IN SHARE MODE`)
				} else {
					// SELECT/FOR SHARE remains available, but final DELETE (or
					// its up-front write lock) waits until authority has expired.
					_, err = lock.ExecContext(ctx, `LOCK TABLE engine.cluster_onboarding_credentials IN SHARE MODE`)
					pattern = "%engine.cluster_onboarding_credentials%"
				}
			} else {
				// Commit a foreign non-Removed same-name member only after final
				// transaction has begun its own insert. Its first sibling must roll back.
				target := original.proof.Cohort.Targets[len(original.proof.Cohort.Targets)-1]
				conflictID := newClusterUUID()
				preparedAdmissionCompletionMemberCleanup(t, f, conflictID)
				_, err = lock.ExecContext(ctx, `INSERT INTO engine.cluster_nodes(id,node_name,hostname,management_ip,architecture,os_version,membership_state,application_state,release_tag,release_sha,roles_json,probe_health_json,last_seen_at,created_at,updated_at)
 VALUES($1,$2,$2,$3,'amd64','Ubuntu 24.04','Pending','drained',$4,$5,'{}','{}',1,1,1)`, conflictID, target.Report.Hostname, target.Binding.Address, original.baseline.Release, original.baseline.SourceSHA)
				pattern = "%INSERT INTO engine.cluster_nodes%"
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := preparedCohortSQLState(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- f.c.finishClusterSSHPreparedAdmission(ctx, f.op, original, observed) }()
			for {
				var waiting bool
				if err := f.c.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $1)`, pattern).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-done:
					t.Fatal("completion bypassed lock", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(5 * time.Millisecond):
				}
			}
			if mode != "member insert conflict" {
				for {
					var expired bool
					query, args := `SELECT extract(epoch FROM clock_timestamp()) >= $1::bigint+60`, []any{observed.ObservedAt}
					if mode == "credential deletion controller expiry" {
						query, args = `SELECT expires_at<=extract(epoch FROM clock_timestamp()) FROM engine.cluster_application_leases WHERE name=$1`, []any{clusterControllerLeaseName}
					}
					if err := f.c.store.db.QueryRowContext(ctx, query, args...).Scan(&expired); err != nil {
						t.Fatal(err)
					}
					if expired {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(5 * time.Millisecond):
					}
				}
			}
			if err := lock.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("waited-on failure completed membership")
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if mode == "member insert conflict" {
				after, err := preparedCohortSQLState(f.ctx, f)
				if err != nil {
					t.Fatal(err)
				}
				var left, right []json.RawMessage
				_ = json.Unmarshal([]byte(before), &left)
				_ = json.Unmarshal([]byte(after), &right)
				for i := range left {
					if i != 4 && !bytes.Equal(left[i], right[i]) {
						t.Fatal("member conflict changed operation/targets/credentials/reservation/events", i)
					}
				}
				var oldNodes, newNodes []map[string]any
				_ = json.Unmarshal(left[4], &oldNodes)
				_ = json.Unmarshal(right[4], &newNodes)
				if len(newNodes) != len(oldNodes)+1 {
					t.Fatal("partial sibling insert survived rollback")
				}
				for _, old := range oldNodes {
					found := false
					for _, current := range newNodes {
						found = found || reflect.DeepEqual(old, current)
					}
					if !found {
						t.Fatal("conflict rewrote original source")
					}
				}
				for _, node := range newNodes {
					if strings.EqualFold(node["membership_state"].(string), "Active") && node["id"] == original.proof.Cohort.Targets[0].Binding.TargetID {
						t.Fatal("sibling falsely completed")
					}
				}
			} else {
				assertPreparedAdmissionStoreState(t, f, before, false)
			}
		})
	}
}
