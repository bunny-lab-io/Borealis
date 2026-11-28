package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
)

func schedulerWorkerPolicySnapshot(site int64, guid string) map[string]any {
	return map[string]any{"site_id": site, "worker_guid": guid, "name": "site-worker-" + guid,
		"configured_image": "borealis-engine/site-worker:local", "kubernetes_phase": "Running", "ready": true,
		"network_mode": "cluster-ip", "service_cluster_ip": "10.43.0.20", "labels": map[string]any{}}
}

func TestSchedulerWorkerInventoryPreservesAgentRotation(t *testing.T) {
	for _, mode := range []string{"empty", "ordinary", "completed rotation", "candidate", "false candidate", "null candidate", "unknown labels", "terminating", "duplicate site", "duplicate GUID", "missing site", "missing GUID"} {
		t.Run(mode, func(t *testing.T) {
			worker := schedulerWorkerPolicySnapshot(1, schedulerK3sSiteWorkerGUID(1))
			workers := []map[string]any{worker}
			valid := false
			switch mode {
			case "empty":
				workers, valid = nil, true
			case "ordinary":
				valid = true
			case "completed rotation":
				worker["name"] = "site-worker-promoted-build"
				worker["labels"] = map[string]any{"borealis.io/redeploy-agent-revision": "retained-revision"}
				valid = true
			case "candidate":
				worker["labels"] = map[string]any{"borealis.io/redeploy-agent-candidate": "true"}
			case "false candidate":
				worker["labels"] = map[string]any{"borealis.io/redeploy-agent-candidate": "false"}
			case "null candidate":
				worker["labels"] = map[string]any{"borealis.io/redeploy-agent-candidate": nil}
			case "unknown labels":
				worker["labels"] = nil
			case "terminating":
				worker["deletion_timestamp"] = "2026-11-02T12:00:00Z"
			case "duplicate site":
				workers = append(workers, schedulerWorkerPolicySnapshot(1, "another-guid"))
			case "duplicate GUID":
				workers = append(workers, schedulerWorkerPolicySnapshot(2, cleanText(worker["worker_guid"])))
			case "missing site":
				delete(worker, "site_id")
			case "missing GUID":
				delete(worker, "worker_guid")
			}
			if err := validateSchedulerWorkerInventory(workers); (err == nil) != valid {
				t.Fatalf("inventory validity=%v: %v", valid, err)
			}
		})
	}
	// Failure remains sticky between periodic inventories and performs no SQL or
	// network work. A zero manager would panic if any worker maintenance escaped.
	if observed, err := (&goSchedulerManager{}).reconcileSiteWorkerLifecycle(context.Background(), false, false, 300); err != nil || observed {
		t.Fatal("unknown inventory escaped lifecycle gate", err)
	}
}

func TestSchedulerWorkerPolicyPostgresPreservesUncertainOwnership(t *testing.T) {
	for _, mode := range []string{"initial inventory failure", "periodic inventory failure", "legacy retirement failure", "terminal retirement failure", "image retirement failure", "route retirement failure", "missing service retirement failure", "late retirement failure", "delete acknowledgement only", "post-delete inventory failure", "replacement during deletion", "Service cleanup failure", "delayed Service cleanup recovery", "candidate", "unknown candidate", "terminating", "duplicate GUID", "confirmed absence", "completed rotation"} {
		t.Run(mode, func(t *testing.T) {
			m, ctx, insert := schedulerOwnershipFixture(t)
			t.Setenv("BOREALIS_TRAEFIK_DYNAMIC_CONFIG_DIR", t.TempDir())
			t.Setenv("BOREALIS_SITE_WORKER_IMAGE", "borealis-engine/site-worker:local")
			const previousSite, site, missingSite int64 = 8192001, 8192002, 8192003
			guid := schedulerK3sSiteWorkerGUID(site)
			if mode == "legacy retirement failure" {
				guid = newClusterUUID()
			}
			previousGUID, missingGUID := schedulerK3sSiteWorkerGUID(previousSite), schedulerK3sSiteWorkerGUID(missingSite)
			guids := []string{guid, missingGUID, previousGUID}
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = m.store.db.ExecContext(cleanup, `DELETE FROM engine.job_scheduler_worker_routes WHERE worker_guid=ANY($1)`, pq.Array(guids))
				_, _ = m.store.db.ExecContext(cleanup, `DELETE FROM engine.job_scheduler_workers WHERE worker_guid=ANY($1)`, pq.Array(guids))
			})
			for i, id := range []int64{site, missingSite, previousSite} {
				name := "original-" + guids[i]
				metadata := schedulerWorkerRouteMetadataForHost(guids[i], "10.43.0.20", 8123, 8124, "borealis-operator")
				if err := m.upsertWorker(ctx, guids[i], name, id, schedulerWorkerStatusRunning, []string{"scheduled_job"}, []map[string]any{{"preserve": "old work"}}, 8123, metadata); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_workers SET last_seen_at=$1,updated_at=$1 WHERE worker_guid=ANY($2)`, time.Now().Unix()-5000, pq.Array(guids)); err != nil {
				t.Fatal(err)
			}
			workID := insert("policy_fixture", `{"ownership_version":1,"execution_id":"preserved-original-execution"}`)
			if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items SET site_id=$1,worker_guid=$2,status='running',lease_owner='original-owner',lease_expires_at=$3,attempt_count=2 WHERE id=$4`, site, guid, time.Now().Unix()+60, workID); err != nil {
				t.Fatal(err)
			}
			queuedID := insert("policy_fixture", `{"preserve":"queued demand"}`)
			if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items SET site_id=$1 WHERE id=$2`, site, queuedID); err != nil {
				t.Fatal(err)
			}
			workIDs := pq.Array([]int64{workID, queuedID})
			snapshot := func(table string, column string, values any) string {
				t.Helper()
				var value string
				query := fmt.Sprintf("SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY %s)::text,'[]') FROM engine.%s t WHERE %s=ANY($1)", column, table, column)
				if err := m.store.db.QueryRowContext(ctx, query, values).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			preserved := pq.Array([]string{guid, missingGUID})
			beforeWorker := snapshot("job_scheduler_workers", "worker_guid", preserved)
			beforeRoute := snapshot("job_scheduler_worker_routes", "worker_guid", preserved)
			beforeWork := snapshot("job_scheduler_work_items", "id", workIDs)
			var files []string
			for _, id := range []string{guid, missingGUID} {
				var file string
				if err := m.store.db.QueryRowContext(ctx, `SELECT route_file_path FROM engine.job_scheduler_worker_routes WHERE worker_guid=$1`, id).Scan(&file); err != nil {
					t.Fatal(err)
				}
				files = append(files, file)
			}
			var mu sync.Mutex
			var calls []string
			retired := false
			cleanupAllowed := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req borealisOperatorCommandRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					http.Error(w, "invalid fixture", http.StatusBadRequest)
					return
				}
				if m.store.db.Stats().InUse != 0 {
					t.Error("SQL connection retained during operator HTTP")
				}
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, req.Verb)
				result := map[string]any{}
				switch req.Verb {
				case "ListSiteWorkers":
					if strings.Contains(mode, "inventory failure") && (mode != "post-delete inventory failure" || retired) {
						http.Error(w, "fixture inventory unavailable", http.StatusServiceUnavailable)
						return
					}
					worker := schedulerWorkerPolicySnapshot(site, guid)
					switch mode {
					case "terminal retirement failure", "delete acknowledgement only", "post-delete inventory failure", "replacement during deletion", "confirmed absence":
						worker["kubernetes_phase"] = "Failed"
					case "image retirement failure", "late retirement failure":
						worker["configured_image"] = "old-image"
					case "route retirement failure":
						worker["network_mode"] = "host-loopback"
					case "missing service retirement failure":
						delete(worker, "service_cluster_ip")
					case "candidate":
						worker["labels"] = map[string]any{"borealis.io/redeploy-agent-candidate": "true"}
					case "unknown candidate":
						worker["labels"] = map[string]any{"borealis.io/redeploy-agent-candidate": false}
					case "terminating":
						worker["deletion_timestamp"] = "2026-11-02T12:00:00Z"
					case "completed rotation":
						worker["name"] = "site-worker-promoted-build"
					}
					workers := []map[string]any{worker}
					if strings.Contains(mode, "Service cleanup") {
						workers = []map[string]any{}
					}
					if mode == "late retirement failure" {
						workers = append(workers, schedulerWorkerPolicySnapshot(previousSite, previousGUID))
					}
					if mode == "duplicate GUID" {
						workers = append(workers, schedulerWorkerPolicySnapshot(site, guid))
					}
					if mode == "replacement during deletion" && retired {
						workers = []map[string]any{schedulerWorkerPolicySnapshot(site, "different-worker")}
					}
					if mode == "confirmed absence" && retired {
						workers = []map[string]any{}
					}
					result["workers"] = workers
				case "RetireSiteWorker":
					retired = true
					if strings.Contains(mode, "retirement failure") || strings.Contains(mode, "Service cleanup") && !cleanupAllowed {
						http.Error(w, "fixture retirement uncertain", http.StatusServiceUnavailable)
						return
					}
				case "LaunchSiteWorker":
					t.Error("worker replacement launched without complete ownership proof")
					http.Error(w, "unexpected replacement", http.StatusConflict)
					return
				default:
					t.Errorf("unexpected operator verb %s", req.Verb)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "verb": req.Verb, "result": result})
			}))
			defer server.Close()
			m.operator = &borealisOperatorClient{baseURL: server.URL, token: "fixture-token", httpClient: server.Client()}
			if mode == "confirmed absence" || mode == "completed rotation" {
				if err := m.reconcileK3sSiteWorkers(ctx); err != nil {
					t.Fatal("confirmed native inventory rejected", err)
				}
				var status, name string
				if err := m.store.db.QueryRowContext(ctx, `SELECT status,container_name FROM engine.job_scheduler_workers WHERE worker_guid=$1`, guid).Scan(&status, &name); err != nil {
					t.Fatal(err)
				}
				if mode == "confirmed absence" && status != schedulerWorkerStatusLost || mode == "completed rotation" && (status != schedulerWorkerStatusRunning || name != "site-worker-promoted-build") {
					t.Fatalf("confirmed lifecycle state=%s name=%s", status, name)
				}
			} else {
				observed, err := m.reconcileSiteWorkerLifecycle(ctx, true, mode != "initial inventory failure", 300)
				if err == nil || observed {
					t.Fatal("uncertain inventory kept worker lifecycle enabled")
				}
				mu.Lock()
				count := len(calls)
				mu.Unlock()
				if observed, err = m.reconcileSiteWorkerLifecycle(ctx, false, observed, 300); err != nil || observed {
					t.Fatal("uncertainty did not survive between inventory periods", err)
				}
				mu.Lock()
				if len(calls) != count {
					t.Error("blocked lifecycle performed operator action")
				}
				mu.Unlock()
				if snapshot("job_scheduler_workers", "worker_guid", preserved) != beforeWorker || snapshot("job_scheduler_worker_routes", "worker_guid", preserved) != beforeRoute {
					t.Fatal("uncertain or omitted original worker ownership changed")
				}
				for _, file := range files {
					if !strings.HasPrefix(file, filepath.Clean(os.Getenv("BOREALIS_TRAEFIK_DYNAMIC_CONFIG_DIR"))+string(os.PathSeparator)) {
						t.Fatal("fixture route escaped temporary directory")
					}
					if _, err := os.Stat(file); err != nil {
						t.Fatal("original route file removed under uncertainty", err)
					}
				}
				if mode == "delayed Service cleanup recovery" {
					mu.Lock()
					cleanupAllowed = true
					mu.Unlock()
					if err := m.reconcileK3sSiteWorkers(ctx); err != nil {
						t.Fatal("Service-only retirement could not reconcile", err)
					}
					var status string
					if err := m.store.db.QueryRowContext(ctx, `SELECT status FROM engine.job_scheduler_workers WHERE worker_guid=$1`, guid).Scan(&status); err != nil || status != schedulerWorkerStatusLost {
						t.Fatal("proved Service cleanup did not release old worker", err, status)
					}
				}
			}
			if snapshot("job_scheduler_work_items", "id", workIDs) != beforeWork {
				t.Fatal("worker reconciliation rewrote original work/execution evidence")
			}
		})
	}
	t.Run("accepted CREATE lost response", testSchedulerWorkerAcceptedCreateLostResponse)
}

func testSchedulerWorkerAcceptedCreateLostResponse(t *testing.T) {
	m, ctx, insert := schedulerOwnershipFixture(t)
	t.Setenv("BOREALIS_TRAEFIK_DYNAMIC_CONFIG_DIR", t.TempDir())
	t.Setenv("BOREALIS_SITE_WORKER_IMAGE", "borealis-engine/site-worker:local")
	const site int64 = 8192010
	guid := schedulerK3sSiteWorkerGUID(site)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = m.store.db.ExecContext(cleanup, `DELETE FROM engine.job_scheduler_worker_routes WHERE worker_guid=$1`, guid)
		_, _ = m.store.db.ExecContext(cleanup, `DELETE FROM engine.job_scheduler_workers WHERE worker_guid=$1`, guid)
	})
	id := insert("policy_fixture", `{"preserve":"original queued demand"}`)
	if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items SET site_id=$1 WHERE id=$2`, site, id); err != nil {
		t.Fatal(err)
	}
	work := func() string {
		var raw string
		if err := m.store.db.QueryRowContext(ctx, `SELECT to_jsonb(t)::text FROM engine.job_scheduler_work_items t WHERE id=$1`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := work()
	var mu sync.Mutex
	launches, accepted := 0, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req borealisOperatorCommandRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if m.store.db.Stats().InUse != 0 {
			t.Error("SQL connection retained during operator HTTP")
		}
		mu.Lock()
		defer mu.Unlock()
		result := map[string]any{}
		switch req.Verb {
		case "ListSiteWorkers":
			workers := []map[string]any{}
			if accepted {
				workers = append(workers, schedulerWorkerPolicySnapshot(site, guid))
			}
			result["workers"] = workers
		case "LaunchSiteWorker":
			launches++
			accepted = true
			http.Error(w, "accepted CREATE response lost", http.StatusServiceUnavailable)
			return
		default:
			t.Errorf("unexpected operator action %s", req.Verb)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "verb": req.Verb, "result": result})
	}))
	defer server.Close()
	m.operator = &borealisOperatorClient{baseURL: server.URL, token: "fixture-token", httpClient: server.Client()}
	observed, err := m.reconcileSiteWorkerLifecycle(ctx, true, false, 300)
	if err == nil || observed {
		t.Fatal("ambiguous CREATE enabled further worker maintenance")
	}
	var status string
	if err := m.store.db.QueryRowContext(ctx, `SELECT status FROM engine.job_scheduler_workers WHERE worker_guid=$1`, guid).Scan(&status); err != nil || status != schedulerWorkerStatusStarting {
		t.Fatal("ambiguous CREATE discarded original Starting ownership", err, status)
	}
	if observed, err = m.reconcileSiteWorkerLifecycle(ctx, false, observed, 300); err != nil || observed {
		t.Fatal("ambiguous CREATE was retried before fresh inventory", err)
	}
	if observed, err = m.reconcileSiteWorkerLifecycle(ctx, true, observed, 300); err != nil || !observed {
		t.Fatal("fresh accepted Pod did not reconcile original identity", err)
	}
	if err := m.store.db.QueryRowContext(ctx, `SELECT status FROM engine.job_scheduler_workers WHERE worker_guid=$1`, guid).Scan(&status); err != nil || status != schedulerWorkerStatusRunning {
		t.Fatal("accepted Pod did not become observed running worker", err, status)
	}
	mu.Lock()
	defer mu.Unlock()
	if launches != 1 || work() != before {
		t.Fatal("lost response caused replacement or rewrote original work")
	}
}
