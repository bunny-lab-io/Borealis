package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDevicePurgeHandlerDispatchesStore(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	store.devicePurgeResult = devicePurgeResult{
		AgentID: "LAB-OPERATOR-01_SYSTEM",
		Payload: map[string]any{
			"status":                 "purged",
			"device_guid":            guid,
			"hostname":               "LAB-OPERATOR-01",
			"required_token_version": int64(4),
			"scheduled_jobs":         map[string]any{"updated": int64(1), "deleted": int64(0), "targets_removed": int64(1)},
			"deleted_rows":           map[string]any{"devices": int64(1)},
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/devices/"+guid+"/purge", nil)
	request.SetPathValue("guid", guid)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	devicePurgeHandler(auth, devicePurgeRuntime{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.devicePurgeGUID != guid || store.devicePurgeProfile.Username != "operator" {
		t.Fatalf("unexpected purge dispatch guid=%q profile=%+v", store.devicePurgeGUID, store.devicePurgeProfile)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	cleanup := payload["runtime_cleanup"].(map[string]any)
	if cleanup["vpn_disconnected"] != false || cleanup["vnc_sessions_revoked"].(float64) != 0 {
		t.Fatalf("unexpected cleanup payload %#v", cleanup)
	}
}

func TestDevicePurgeHandlerRequiresAdmin(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Technician"})
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/devices/"+guid+"/purge", nil)
	request.SetPathValue("guid", guid)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	devicePurgeHandler(auth, devicePurgeRuntime{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.devicePurgeGUID != "" {
		t.Fatalf("purge store should not be called, got guid=%q", store.devicePurgeGUID)
	}
}

func TestPruneScheduledTargetsForDeviceMatchesPythonRules(t *testing.T) {
	siteID := int64(1)
	purgedGUID := "3BA36DB5-7C82-4B3C-863A-5A7873A4EBF9"
	otherGUID := "54E8C9E2-6B3D-4B51-A456-4ACB94C45F00"
	targets := []any{
		map[string]any{"kind": "device", "device_guid": purgedGUID, "hostname": "test-device", "site_id": float64(1), "site_name": "Main Lab"},
		map[string]any{"kind": "device", "device_guid": otherGUID, "hostname": "survivor-device", "site_id": float64(1), "site_name": "Main Lab"},
		map[string]any{"kind": "filter", "filter_id": float64(7), "name": "Windows Devices"},
		map[string]any{"kind": "all_devices", "name": "All Devices in Scope"},
		"test-device",
	}

	updated, removed := pruneScheduledTargetsForDevice(targets, purgedGUID, "test-device", &siteID)
	if removed != 2 {
		t.Fatalf("expected 2 removed targets, got %d updated=%#v", removed, updated)
	}
	if len(updated) != 3 {
		t.Fatalf("expected 3 targets left, got %#v", updated)
	}
	deviceTarget := updated[0].(map[string]any)
	if deviceTarget["hostname"] != "survivor-device" || deviceTarget["device_guid"] != "54e8c9e2-6b3d-4b51-a456-4acb94c45f00" {
		t.Fatalf("unexpected survivor target %#v", deviceTarget)
	}
	if updated[1].(map[string]any)["kind"] != "filter" || updated[2].(map[string]any)["kind"] != "all_devices" {
		t.Fatalf("filter/all targets should remain, got %#v", updated)
	}
}

func TestVNCRuntimeRevokeAgentRemovesSession(t *testing.T) {
	runtime := newVNCRuntime(nil, nil)
	credential := vncCredential{ControllerPassword: "secret", CredentialRevision: 1}
	session, _, _ := runtime.ensureSession("LAB-OPERATOR-01_SYSTEM", "operator", credential, true)

	if revoked := runtime.revokeAgent("LAB-OPERATOR-01_SYSTEM"); revoked != 1 {
		t.Fatalf("expected one revoked session, got %d", revoked)
	}
	if runtime.sessionByID(session.SessionID) != nil || runtime.sessionByAgent("LAB-OPERATOR-01_SYSTEM") != nil {
		t.Fatalf("session still present after revoke")
	}
}
