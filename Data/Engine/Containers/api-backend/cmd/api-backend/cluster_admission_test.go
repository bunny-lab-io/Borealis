package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func clusterAdmissionFixture(t *testing.T) (*postgresOperatorStore, context.Context, string, func(string) (map[string]any, map[string]any)) {
	t.Helper()
	dsn := os.Getenv("BOREALIS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("BOREALIS_TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	store := &postgresOperatorStore{db: db}
	if err := store.ensureClusterSchema(ctx); err != nil {
		t.Fatal(err)
	}
	clusterID := newClusterUUID()
	var operations []string
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		rows, err := db.QueryContext(cleanupCtx, `SELECT DISTINCT operation_id FROM engine.cluster_operation_events WHERE cluster_id=$1 AND operation_id IS NOT NULL`, clusterID)
		if err == nil {
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					operations = append(operations, id)
				}
			}
			rows.Close()
		}
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM engine.cluster_operation_events WHERE cluster_id=$1`, clusterID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM engine.cluster_audit_events WHERE actor='admission-test'`)
		for _, id := range operations {
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM engine.cluster_operations WHERE id=$1`, id)
		}
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM engine.cluster_admissions WHERE cluster_id=$1`, clusterID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM engine.cluster_invitations WHERE cluster_id=$1`, clusterID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM engine.cluster_state WHERE cluster_id=$1`, clusterID)
	})
	now := time.Now().Unix()
	if _, err := db.ExecContext(ctx, `INSERT INTO engine.cluster_state
		(id,cluster_id,enabled,status,active_size,desired_size,hmr_state,baseline_release,baseline_sha,config_json,created_at,updated_at)
		VALUES(1,$1,1,'Healthy',1,1,'inactive','2026.9.5.1',$2,'{"k3s_version":"v1.36.3+k3s1"}',$3,$3)
		ON CONFLICT(id) DO UPDATE SET cluster_id=EXCLUDED.cluster_id,enabled=1,status='Healthy',active_size=1,desired_size=1,
		hmr_state='inactive',hmr_node_id=NULL,active_operation_id=NULL,baseline_release=EXCLUDED.baseline_release,baseline_sha=EXCLUDED.baseline_sha,config_json=EXCLUDED.config_json`, clusterID, strings.Repeat("a", 40), now); err != nil {
		t.Fatal(err)
	}
	count := 0
	invite := func(name string) (map[string]any, map[string]any) {
		t.Helper()
		count++
		claims := map[string]any{"type": "cluster-invite", "invitation_id": newClusterUUID(), "cluster_id": clusterID,
			"node_name": name, "token": newClusterSecret(32), "expires_at": time.Now().Add(clusterInviteTTL).Unix()}
		if err := store.createClusterInvitation(ctx, "admission-test", map[string]any{"id": claims["invitation_id"], "cluster_id": clusterID,
			"node_name": name, "token_hash": clusterTokenHash(cleanText(claims["token"])), "expires_at": claims["expires_at"]}); err != nil {
			t.Fatal(err)
		}
		request := map[string]any{"id": newClusterUUID(), "invitation_id": claims["invitation_id"], "cluster_id": clusterID,
			"token_hash": clusterTokenHash(cleanText(claims["token"])), "node_name": name, "hostname": name,
			"management_ip": fmt.Sprintf("192.168.90.%d", count+10), "architecture": "amd64", "os_version": "Ubuntu 24.04"}
		return claims, request
	}
	return store, ctx, clusterID, invite
}

func TestClusterAdmissionPostgresScopedHistoryAndResume(t *testing.T) {
	store, ctx, clusterID, invite := clusterAdmissionFixture(t)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO engine.cluster_operation_events
		(cluster_id,event_type,state,message,details_json,created_at)
		SELECT $1,'unrelated','complete','older cluster activity','{}',$2 FROM generate_series(1,750)`, clusterID, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	claimsA, requestA := invite("admission-test-02")
	claimsB, requestB := invite("admission-test-03")
	accepted, err := store.consumeClusterInvitation(ctx, requestA)
	if err != nil {
		t.Fatal(err)
	}
	id := cleanText(accepted["admission_id"])
	retry := copyMap(requestA)
	retry["id"] = newClusterUUID()
	resumed, err := (&postgresOperatorStore{db: store.db}).consumeClusterInvitation(ctx, retry)
	if err != nil || cleanText(resumed["admission_id"]) != id {
		t.Fatalf("lost-response retry changed identity: result=%v err=%v", resumed, err)
	}
	changed := copyMap(retry)
	changed["management_ip"] = "192.168.90.200"
	if _, err := store.consumeClusterInvitation(ctx, changed); !errors.Is(err, errClusterConflict) {
		t.Fatalf("changed target reused consumed invitation: %v", err)
	}
	if _, err := store.consumeClusterInvitation(ctx, requestB); err != nil {
		t.Fatal(err)
	}
	if _, err := store.approveClusterAdmission(ctx, "admission-test", id); err != nil {
		t.Fatal(err)
	}
	resumed, err = store.consumeClusterInvitation(ctx, retry)
	if err != nil || cleanText(resumed["state"]) != "Approved" || cleanText(resumed["admission_id"]) != id {
		t.Fatalf("approved join failed to resume while operation active: %v %v", resumed, err)
	}
	status, err := store.clusterAdmissionStatus(ctx, id, claimsA)
	if err != nil || cleanText(status["state"]) != "Approved" {
		t.Fatalf("scoped status=%v err=%v", status, err)
	}
	events := status["events"].([]map[string]any)
	if len(events) != 2 || cleanText(events[1]["event_type"]) != "admission_pair_approved" {
		t.Fatalf("approval hidden by unrelated history: %v", events)
	}
	if _, err := store.clusterAdmissionStatus(ctx, id, claimsB); !errors.Is(err, errClusterNotFound) {
		t.Fatalf("other invitation read target admission: %v", err)
	}
	for _, field := range []string{"token", "cluster_id", "node_name", "invitation_id"} {
		wrong := copyMap(claimsA)
		wrong[field] = "wrong"
		if _, err := store.clusterAdmissionStatus(ctx, id, wrong); !errors.Is(err, errClusterNotFound) {
			t.Fatalf("mismatched %s authorized admission: %v", field, err)
		}
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.cluster_admissions WHERE invitation_id=$1`, claimsA["invitation_id"]).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate admission rows=%d err=%v", count, err)
	}
}

func TestClusterJoinEventsRejectsWrongTokenPurpose(t *testing.T) {
	store := &clusterTestStore{}
	auth, _ := clusterTestAuth(t, store)
	bundle, err := auth.verifier.signPayload(map[string]any{"type": "other-token", "cluster_id": newClusterUUID()})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/bootstrap/cluster/join/unused/events", nil)
	request.SetPathValue("id", newClusterUUID())
	request.Header.Set("X-Borealis-Cluster-Invite", bundle)
	response := httptest.NewRecorder()
	clusterJoinEventsHandler(auth)(response, request)
	if response.Code != http.StatusUnauthorized || store.admission != nil {
		t.Fatalf("wrong-purpose signature reached store: HTTP %d admission=%v", response.Code, store.admission)
	}
}
