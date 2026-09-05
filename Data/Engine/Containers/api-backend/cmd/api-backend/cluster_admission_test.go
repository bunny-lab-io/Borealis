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
		(id,cluster_id,enabled,status,active_size,desired_size,hmr_state,control_plane_vip,baseline_release,baseline_sha,config_json,created_at,updated_at)
		VALUES(1,$1,1,'Healthy',1,1,'inactive','192.168.90.248','2026.9.5.1',$2,'{"k3s_version":"v1.36.3+k3s1"}',$3,$3)
		ON CONFLICT(id) DO UPDATE SET cluster_id=EXCLUDED.cluster_id,enabled=1,status='Healthy',active_size=1,desired_size=1,
		hmr_state='inactive',hmr_node_id=NULL,active_operation_id=NULL,control_plane_vip=EXCLUDED.control_plane_vip,baseline_release=EXCLUDED.baseline_release,baseline_sha=EXCLUDED.baseline_sha,config_json=EXCLUDED.config_json`, clusterID, strings.Repeat("a", 40), now); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(seedAdmissionPeer(t, store, ctx, "admission-test-01", "192.168.90.10"))
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

func TestClusterAdmissionPostgresCapacityRecovery(t *testing.T) {
	for _, stopState := range []string{"failed", "cancelled"} {
		t.Run(stopState, func(t *testing.T) {
			store, ctx, _, invite := clusterAdmissionFixture(t)
			expiredClaims, expiredRequest := invite("admission-expired")
			expired, err := store.consumeClusterInvitation(ctx, expiredRequest)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_invitations SET expires_at=$1 WHERE id=$2`, time.Now().Unix()-1, expiredClaims["invitation_id"]); err != nil {
				t.Fatal(err)
			}
			_, cancelledRequest := invite("admission-cancelled")
			cancelled, err := store.consumeClusterInvitation(ctx, cancelledRequest)
			if err != nil {
				t.Fatal(err)
			}
			var state string
			if err := store.db.QueryRowContext(ctx, `SELECT state FROM engine.cluster_admissions WHERE id=$1`, expired["admission_id"]).Scan(&state); err != nil || state != "Expired" {
				t.Fatalf("unused expired capacity retained: state=%s err=%v", state, err)
			}
			if _, err := store.cancelClusterAdmission(ctx, "admission-test", cleanText(cancelled["admission_id"])); err != nil {
				t.Fatal(err)
			}
			if _, err := store.cancelClusterAdmission(ctx, "admission-test", cleanText(cancelled["admission_id"])); err != nil {
				t.Fatalf("cancellation retry failed: %v", err)
			}
			if _, err := store.consumeClusterInvitation(ctx, cancelledRequest); !errors.Is(err, errClusterConflict) {
				t.Fatalf("cancelled identity resumed: %v", err)
			}
			_, firstRequest := invite("admission-recovery-02")
			_, secondRequest := invite("admission-recovery-03")
			first, err := store.consumeClusterInvitation(ctx, firstRequest)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.consumeClusterInvitation(ctx, secondRequest); err != nil {
				t.Fatal(err)
			}
			approved, err := store.approveClusterAdmission(ctx, "admission-test", cleanText(first["admission_id"]))
			if err != nil {
				t.Fatal(err)
			}
			opID := cleanText(approved["operation_id"])
			if _, err := store.cancelClusterAdmission(ctx, "admission-test", cleanText(first["admission_id"])); !errors.Is(err, errClusterConflict) {
				t.Fatalf("approved admission released capacity: %v", err)
			}
			if _, err := store.cancelClusterOperation(ctx, "admission-test", opID); !errors.Is(err, errClusterConflict) {
				t.Fatalf("queued membership operation forgot possible joined node: %v", err)
			}
			// Earlier releases allowed queued membership cancellation. A restarted
			// controller must retain both identities even when the second joiner is lost.
			if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_operations SET state=$2,current_step=CASE WHEN $2='cancelled' THEN 'cancelled' ELSE 'apply_membership' END WHERE id=$1`, opID, stopState); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			holder := "admission-recovery-controller"
			if _, err := store.db.ExecContext(ctx, `INSERT INTO engine.cluster_application_leases(name,holder,expires_at,updated_at)
		VALUES($1,$2,$3,$4) ON CONFLICT(name) DO UPDATE SET holder=EXCLUDED.holder,expires_at=EXCLUDED.expires_at,updated_at=EXCLUDED.updated_at`, clusterControllerLeaseName, holder, now.Unix()+60, now.Unix()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = store.db.ExecContext(context.Background(), `DELETE FROM engine.cluster_application_leases WHERE name=$1 AND holder=$2`, clusterControllerLeaseName, holder)
			})
			controller := &clusterController{store: store, holder: "former-owner", now: func() time.Time { return now }}
			if err := controller.reconcileAdmissions(ctx); !errors.Is(err, errClusterControllerLeaseLost) {
				t.Fatalf("former owner reconciled admissions: %v", err)
			}
			controller.holder = holder
			if err := controller.reconcileAdmissions(ctx); err != nil {
				t.Fatal(err)
			}
			if err := store.db.QueryRowContext(ctx, `SELECT state FROM engine.cluster_admissions WHERE id=$1`, first["admission_id"]).Scan(&state); err != nil || state != "Recovery Required" {
				t.Fatalf("uncertain membership not retained: state=%s err=%v", state, err)
			}
			_, replacementRequest := invite("admission-unaccounted-replacement")
			if _, err := store.consumeClusterInvitation(ctx, replacementRequest); !errors.Is(err, errClusterConflict) {
				t.Fatalf("unaccounted members allowed replacement capacity: %v", err)
			}
			if _, err := store.retryClusterOperation(ctx, "admission-test", opID); err != nil {
				t.Fatalf("retained cancelled admission could not resume: %v", err)
			}
			expectedResume := "apply_membership"
			if stopState == "cancelled" {
				expectedResume = ""
			}
			var retryPayload string
			if err := store.db.QueryRowContext(ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, opID).Scan(&retryPayload); err != nil || cleanText(parseClusterJSON(retryPayload)["retry_resume_step"]) != expectedResume {
				t.Fatalf("legacy cancellation retained invalid checkpoint: %s %v", retryPayload, err)
			}
			if err := store.db.QueryRowContext(ctx, `SELECT state FROM engine.cluster_admissions WHERE id=$1`, first["admission_id"]).Scan(&state); err != nil || state != "Approved" {
				t.Fatalf("retry lost reserved admission: state=%s err=%v", state, err)
			}
		})
	}
}

func TestClusterAdmissionCancelBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, role, id, body string
		status               int
	}{
		{"user", "User", newClusterUUID(), `{"confirmation":"CANCEL ADMISSION"}`, http.StatusForbidden},
		{"invalid id", "Admin", "bad-id", `{"confirmation":"CANCEL ADMISSION"}`, http.StatusBadRequest},
		{"unknown field", "Admin", newClusterUUID(), `{"confirmation":"CANCEL ADMISSION","force":true}`, http.StatusBadRequest},
		{"confirmation", "Admin", newClusterUUID(), `{"confirmation":"cancel"}`, http.StatusBadRequest},
		{"valid", "Admin", newClusterUUID(), `{"confirmation":"CANCEL ADMISSION"}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: tc.role}}
			auth, token := clusterTestAuth(t, store)
			request := clusterTestRequest(t, http.MethodPost, "/api/server/cluster/admissions/"+tc.id+"/cancel", tc.body, token)
			request.SetPathValue("id", tc.id)
			response := httptest.NewRecorder()
			clusterAdmissionCancelHandler(auth)(response, request)
			if response.Code != tc.status || (store.cancelledID != "") != (tc.status == http.StatusOK) {
				t.Fatalf("HTTP %d body=%s mutation=%s", response.Code, response.Body, store.cancelledID)
			}
		})
	}
}

func seedAdmissionPeer(t *testing.T, store *postgresOperatorStore, ctx context.Context, name, ip string) func() {
	t.Helper()
	id := newClusterUUID()
	now := time.Now().Unix()
	if _, err := store.db.ExecContext(ctx, `INSERT INTO engine.cluster_nodes(id,node_name,hostname,management_ip,architecture,os_version,membership_state,application_state,created_at,updated_at)
 VALUES($1,$2,$2,$3,'amd64','Ubuntu 24.04','Active','active',$4,$4)`, id, name, ip, now); err != nil {
		t.Fatal(err)
	}
	return func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM engine.cluster_nodes WHERE id=$1`, id)
	}
}

func TestClusterAdmissionPostgresAuthorizationRenewal(t *testing.T) {
	store, ctx, _, invite := clusterAdmissionFixture(t)
	claims, request := invite("admission-renew-02")
	_, second := invite("admission-renew-03")
	accepted, err := store.consumeClusterInvitation(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.consumeClusterInvitation(ctx, second); err != nil {
		t.Fatal(err)
	}
	id := cleanText(accepted["admission_id"])
	if _, err := store.approveClusterAdmission(ctx, "admission-test", id); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	unused, unusedRequest := invite("admission-expired-unused")
	if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_invitations SET expires_at=$1 WHERE id=$2`, now-1, unused["invitation_id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.consumeClusterInvitation(ctx, unusedRequest); !errors.Is(err, errClusterConflict) || !strings.Contains(err.Error(), "expired before acceptance") {
		t.Fatalf("expired unused invitation accepted: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_invitations SET created_at=$1,expires_at=$2 WHERE id=$3`, now-3600, now-2700, claims["invitation_id"]); err != nil {
		t.Fatal(err)
	}
	status, err := store.clusterAdmissionStatus(ctx, id, claims)
	if err != nil || coerceInt64(status["expires_at"]) != now-3600+86400 {
		t.Fatalf("approved authorization expired with initial invite: %v %v", status, err)
	}
	config := status["join_config"].(map[string]any)
	if cleanText(config["k3s_server"]) != "https://192.168.90.248:6443" || cleanText(config["k3s_version"]) != "v1.36.3+k3s1" || cleanText(config["peer_cidrs"]) != "192.168.90.10/32,192.168.90.11/32,192.168.90.12/32" {
		t.Fatalf("authority lost exact cluster roster: %v", config)
	}
	if _, err := store.consumeClusterInvitation(ctx, request); err != nil {
		t.Fatalf("accepted resume after initial expiry: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_invitations SET created_at=$1 WHERE id=$2`, now-86401, claims["invitation_id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.clusterAdmissionStatus(ctx, id, claims); !errors.Is(err, errClusterConflict) {
		t.Fatalf("one-day limit bypassed: %v", err)
	}
	if _, err := store.consumeClusterInvitation(ctx, request); !errors.Is(err, errClusterConflict) {
		t.Fatalf("expired accepted invite resumed: %v", err)
	}
	fresh, _ := invite("admission-renew-02")
	renewed := copyMap(request)
	renewed["invitation_id"] = fresh["invitation_id"]
	renewed["token_hash"] = clusterTokenHash(cleanText(fresh["token"]))
	resumed, err := store.consumeClusterInvitation(ctx, renewed)
	if err != nil || cleanText(resumed["admission_id"]) != id {
		t.Fatalf("renewal lost retained target identity: %v %v", resumed, err)
	}
	if _, err := store.clusterAdmissionStatus(ctx, id, claims); !errors.Is(err, errClusterNotFound) {
		t.Fatalf("old invitation survived renewal: %v", err)
	}
	if _, err := store.consumeClusterInvitation(ctx, request); !errors.Is(err, errClusterNotFound) {
		t.Fatalf("revoked invitation resumed: %v", err)
	}
	changed := copyMap(renewed)
	changed["management_ip"] = "192.168.90.99"
	if _, err := store.consumeClusterInvitation(ctx, changed); !errors.Is(err, errClusterConflict) {
		t.Fatalf("renewal authorized replacement target: %v", err)
	}
}
