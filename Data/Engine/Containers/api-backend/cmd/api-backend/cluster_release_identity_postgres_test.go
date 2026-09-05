package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestClusterReleaseIdentityPostgresQueueAndRetry(t *testing.T) {
	store, ctx, _, _ := clusterAdmissionFixture(t)
	mutation := clusterMutation{Kind: "engine_update", TargetRelease: "2026.9.5.2", TargetSHA: identityReleaseSHA,
		Payload: map[string]any{"scope": "all", "source_release": "2026.9.5.1", "source_sha": strings.Repeat("a", 40),
			"release_immutable": true, "source_k3s_version": "v1.36.3+k3s1", "maintenance_outage_acknowledgement": "ACCEPT OUTAGE",
			"compatibility": map[string]any{"required_k3s_baseline": "v1.36.3+k3s1", "database_migration": "expand-contract", "maximum_version_skew_releases": 1}}}
	// Simulate a K3s operation committing between the API snapshot and queue lock.
	if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_state SET config_json='{"k3s_version":"v1.36.4+k3s1"}' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.createClusterOperation(ctx, "admission-test", mutation); !errors.Is(err, errClusterConflict) || !strings.Contains(err.Error(), "source K3s version changed") {
		t.Fatalf("stale K3s source queued: %v", err)
	}
	mutation.Payload["source_k3s_version"] = "v1.36.4+k3s1"
	if _, err := store.createClusterOperation(ctx, "admission-test", mutation); !errors.Is(err, errClusterConflict) || !strings.Contains(err.Error(), "manifest must match") {
		t.Fatalf("stale manifest queued: %v", err)
	}
	mutation.Payload["compatibility"].(map[string]any)["required_k3s_baseline"] = "v1.36.4+k3s1"
	mutation.Payload["release_immutable"] = false
	if _, err := store.createClusterOperation(ctx, "admission-test", mutation); !errors.Is(err, errClusterConflict) || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("unverified publication queued: %v", err)
	}
	mutation.Payload["release_immutable"] = true
	// API hands the transaction a typed manifest; JSON persistence must retain it.
	mutation.Payload["compatibility"] = clusterReleaseManifest{RequiredK3sBaseline: "v1.36.4+k3s1", DatabaseMigration: "expand-contract", MaximumVersionSkewReleases: 1}
	probes := map[string]any{}
	for _, key := range []string{"startup", "readiness", "liveness", "direct_endpoint", "service", "database", "scheduler", "agent_path", "wireguard"} {
		probes[key] = "passed"
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_nodes SET release_tag='2026.9.5.1',probe_health_json=$1 WHERE node_name='admission-test-01'`, marshalClusterJSON(probes)); err != nil {
		t.Fatal(err)
	}
	result, err := store.createClusterOperation(ctx, "admission-test", mutation)
	if err != nil {
		t.Fatalf("verified identity rejected: %v", err)
	}
	id := cleanText(result["operation_id"])
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM engine.realtime_outbox WHERE payload_json LIKE '%' || $1 || '%'`, id)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operation_events WHERE operation_id=$1`, id)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operations WHERE id=$1`, id)
	})
	// Retries preserve the verified target and manifest without consulting GitHub.
	t.Setenv("BOREALIS_GITHUB_API_BASE_URL", "http://127.0.0.1:1")
	if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_operations SET state='failed',current_step='verify_release' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.retryClusterOperation(ctx, "admission-test", id); err != nil {
		t.Fatal(err)
	}
	var release, sha, payloadJSON, step string
	if err := store.db.QueryRowContext(ctx, `SELECT target_release,target_sha,payload_json,current_step FROM engine.cluster_operations WHERE id=$1`, id).Scan(&release, &sha, &payloadJSON, &step); err != nil {
		t.Fatal(err)
	}
	payload := parseClusterJSON(payloadJSON)
	if release != mutation.TargetRelease || sha != identityReleaseSHA || step != "preflight" || payload["retry_resume_step"] != "verify_release" || payload["release_immutable"] != true || payload["source_k3s_version"] != "v1.36.4+k3s1" {
		t.Fatalf("retry replaced identity: %s %s %s %s", release, sha, step, payloadJSON)
	}
	if err := validateClusterEngineUpdateIdentity(release, sha, payload); err != nil {
		t.Fatalf("persisted manifest changed: %v", err)
	}
}
