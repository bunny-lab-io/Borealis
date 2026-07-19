package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScheduledRunWorkPayloadCarriesSiteScopedTask(t *testing.T) {
	payload := scheduledRunWorkPayload(scheduledRunWorkRow{
		RunID:          sql.NullInt64{Int64: 1200, Valid: true},
		JobID:          sql.NullInt64{Int64: 140, Valid: true},
		SiteID:         sql.NullInt64{Int64: 1, Valid: true},
		TargetHostname: sql.NullString{String: "LAB-OPERATOR-01", Valid: true},
		Status:         sql.NullString{String: scheduledStatusRunning, Valid: true},
		StartedAt:      sql.NullInt64{Int64: 1700000000, Valid: true},
		UpdatedAt:      sql.NullInt64{Int64: 1700000005, Valid: true},
	})

	if payload["id"] != "scheduled-run:1200" || payload["kind"] != schedulerKindScheduledRun {
		t.Fatalf("unexpected identity payload %#v", payload)
	}
	if payload["site_id"].(int64) != 1 || payload["job_id"].(int64) != 140 || payload["run_id"].(int64) != 1200 {
		t.Fatalf("unexpected ids %#v", payload)
	}
	if payload["status"] != scheduledStatusRunning || payload["target_count"].(int64) != 1 {
		t.Fatalf("unexpected status/count %#v", payload)
	}
	link, ok := payload["task_link"].(map[string]any)
	if !ok || link["job_id"].(int64) != 140 || link["run_id"].(int64) != 1200 {
		t.Fatalf("unexpected task link %#v", payload["task_link"])
	}
}

func TestFilterScheduledDispatchWorkPrefersRunState(t *testing.T) {
	workItems := []map[string]any{
		{"id": int64(1026), "kind": schedulerKindScheduledRun, "run_id": int64(1204), "status": workStatusSucceeded},
		{"id": int64(9000), "kind": schedulerKindServiceAction, "run_id": int64(1204), "status": workStatusSucceeded},
		{"id": int64(1027), "kind": schedulerKindScheduledRun, "run_id": int64(1205), "status": workStatusQueued},
	}
	scheduledRuns := []map[string]any{
		{"id": "scheduled-run:1204", "kind": schedulerKindScheduledRun, "run_id": int64(1204), "status": scheduledStatusSuccess},
	}

	filtered := filterScheduledDispatchWork(workItems, scheduledRuns)
	if len(filtered) != 2 {
		t.Fatalf("expected dispatch duplicate removed, got %#v", filtered)
	}
	if filtered[0]["kind"] != schedulerKindServiceAction || filtered[1]["run_id"].(int64) != 1205 {
		t.Fatalf("unexpected filtered rows %#v", filtered)
	}
}

func dockerStatsFixture() map[string]any {
	return map[string]any{
		"read": "2026-06-28T12:00:00Z",
		"cpu_stats": map[string]any{
			"cpu_usage": map[string]any{
				"total_usage": 150000000,
				"percpu_usage": []any{
					10000000,
					20000000,
					30000000,
					40000000,
				},
			},
			"system_cpu_usage": 600000000,
			"online_cpus":      4,
		},
		"precpu_stats": map[string]any{
			"cpu_usage": map[string]any{
				"total_usage": 50000000,
			},
			"system_cpu_usage": 400000000,
		},
		"memory_stats": map[string]any{
			"usage": 104857600,
			"limit": 1073741824,
			"stats": map[string]any{
				"inactive_file": 16777216,
			},
		},
		"networks": map[string]any{
			"eth0": map[string]any{"rx_bytes": 1024, "tx_bytes": 512},
			"wg0":  map[string]any{"rx_bytes": 2048, "tx_bytes": 1536},
		},
		"blkio_stats": map[string]any{
			"io_service_bytes_recursive": []any{
				map[string]any{"op": "Read", "value": 4096},
				map[string]any{"op": "Write", "value": 8192},
			},
		},
		"pids_stats": map[string]any{
			"current": 7,
		},
	}
}

func TestNormalizeDockerContainerStats(t *testing.T) {
	stats := normalizeDockerContainerStats(dockerStatsFixture())

	if got := stats["cpu_percent"]; got != float64(200) {
		t.Fatalf("expected cpu percent 200, got %#v", got)
	}
	if got := stats["memory_usage_bytes"]; got != int64(88080384) {
		t.Fatalf("expected cache-adjusted memory usage, got %#v", got)
	}
	if got := stats["memory_percent"]; got != 8.2 {
		t.Fatalf("expected memory percent 8.2, got %#v", got)
	}
	if got := stats["net_input_bytes"]; got != int64(3072) {
		t.Fatalf("expected network input bytes, got %#v", got)
	}
	if got := stats["net_output_bytes"]; got != int64(2048) {
		t.Fatalf("expected network output bytes, got %#v", got)
	}
	if got := stats["block_input_bytes"]; got != int64(4096) {
		t.Fatalf("expected block input bytes, got %#v", got)
	}
	if got := stats["block_output_bytes"]; got != int64(8192) {
		t.Fatalf("expected block output bytes, got %#v", got)
	}
	if got := stats["pids"]; got != int64(7) {
		t.Fatalf("expected pids, got %#v", got)
	}
}

func TestDockerContainerStatsReadsDockerProxyStatsEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/containers/site-worker-1/stats" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("stream"); got != "false" {
			t.Fatalf("expected stream=false, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dockerStatsFixture())
	}))
	defer server.Close()
	t.Setenv("BOREALIS_DOCKER_PROXY_URL", server.URL)

	stats := dockerContainerStats("site-worker-1")
	if got := stats["cpu_percent"]; got != float64(200) {
		t.Fatalf("expected docker proxy stats, got %#v", stats)
	}
}

func TestDockerInspectContainerRequestsSizeMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/containers/site-worker-1/json" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("size"); got != "1" {
			t.Fatalf("expected size=1, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Id":         "abcdef1234567890",
			"SizeRootFs": 576716800,
			"SizeRw":     52428800,
			"HostConfig": map[string]any{
				"StorageOpt": map[string]any{"size": "120G"},
			},
		})
	}))
	defer server.Close()
	t.Setenv("BOREALIS_DOCKER_PROXY_URL", server.URL)

	inspected := dockerInspectContainer("site-worker-1")
	row := map[string]any{}
	applyDockerInspectSizeMetadata(row, inspected)

	if got := row["container_size_rootfs_bytes"]; got != int64(576716800) {
		t.Fatalf("expected rootfs size, got %#v", row)
	}
	if got := row["container_size_rw_bytes"]; got != int64(52428800) {
		t.Fatalf("expected rw size, got %#v", row)
	}
	if got := row["container_storage_limit_bytes"]; got != int64(120*1024*1024*1024) {
		t.Fatalf("expected storage limit, got %#v", row)
	}
	if got := row["container_storage_limit_source"]; got != "HostConfig.StorageOpt.size" {
		t.Fatalf("expected storage source, got %#v", row)
	}
}

func TestDockerInspectStorageLimitPrefersDiskQuota(t *testing.T) {
	limit, source := dockerInspectStorageLimit(map[string]any{
		"HostConfig": map[string]any{
			"DiskQuota": 987654321,
			"StorageOpt": map[string]any{
				"size": "120G",
			},
		},
	})

	if limit != int64(987654321) || source != "HostConfig.DiskQuota" {
		t.Fatalf("expected disk quota storage limit, got limit=%d source=%q", limit, source)
	}
}

func TestMergeK3sSiteWorkerMetadataAttachesMetrics(t *testing.T) {
	row := map[string]any{
		"worker_guid":    "worker-1",
		"container_name": "site-worker-bunny-lab",
		"site_id":        int64(7),
	}
	worker := map[string]any{
		"name":                     "site-worker-bunny-lab",
		"container_name":           "site-worker-bunny-lab",
		"configured_image":         "borealis-engine/site-worker:sha-cccccccccccc",
		"container_metrics_source": "metrics.k8s.io",
		"kubernetes_phase":         "Running",
		"docker_stats": map[string]any{
			"source":             "metrics.k8s.io",
			"cpu_percent":        12.5,
			"memory_usage_bytes": int64(64 * 1024 * 1024),
		},
		"kubernetes_metrics": map[string]any{
			"cpu_usage_millicores": float64(125),
		},
	}

	mergeK3sSiteWorkerMetadata(row, worker)

	if got := row["container_metrics_source"]; got != "metrics.k8s.io" {
		t.Fatalf("expected metrics source, got %#v", row)
	}
	if got := schedulerAnyMap(row["docker_stats"])["cpu_percent"]; got != 12.5 {
		t.Fatalf("expected docker_stats merge, got %#v", row["docker_stats"])
	}
	if got := schedulerAnyMap(row["kubernetes_metrics"])["cpu_usage_millicores"]; got != float64(125) {
		t.Fatalf("expected kubernetes_metrics merge, got %#v", row["kubernetes_metrics"])
	}
	if got := row["container_id"]; got != "site-worker-bunny-lab" {
		t.Fatalf("expected pod name as container id fallback, got %#v", row)
	}
}
