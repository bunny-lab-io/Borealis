package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeviceSecurityStatusHandlerQuarantinesDevice(t *testing.T) {
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.deviceSecurityResult = deviceSecurityResult{
		AgentID: "LAB-01_SYSTEM",
		Payload: map[string]any{
			"status":          "quarantined",
			"guid":            guid,
			"agent_id":        "LAB-01_SYSTEM",
			"token_version":   int64(6),
			"previous_status": "active",
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/devices/"+guid+"/quarantine", strings.NewReader(`{"reason":"lost device"}`))
	request.SetPathValue("guid", guid)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	recorder := httptest.NewRecorder()

	deviceSecurityStatusHandler(auth, devicePurgeRuntime{}, "quarantined").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.deviceSecurityGUID != guid || store.deviceSecurityStatus != "quarantined" || store.deviceSecurityReason != "lost device" {
		t.Fatalf("unexpected store call guid=%q status=%q reason=%q", store.deviceSecurityGUID, store.deviceSecurityStatus, store.deviceSecurityReason)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "quarantined" {
		t.Fatalf("unexpected payload %#v", payload)
	}
	cleanup := payload["runtime_cleanup"].(map[string]any)
	if cleanup["vpn_disconnected"] != false || cleanup["vnc_sessions_revoked"].(float64) != 0 {
		t.Fatalf("unexpected cleanup %#v", cleanup)
	}
}

func TestDeviceSecurityStatusHandlerPropagatesStoreError(t *testing.T) {
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.deviceSecurityCode = http.StatusNotFound
	store.deviceSecurityErr = errors.New("not_found")
	request := httptest.NewRequest(http.MethodPost, "/api/devices/"+guid+"/revoke", strings.NewReader(`{}`))
	request.SetPathValue("guid", guid)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	recorder := httptest.NewRecorder()

	deviceSecurityStatusHandler(auth, devicePurgeRuntime{}, "revoked").ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}
