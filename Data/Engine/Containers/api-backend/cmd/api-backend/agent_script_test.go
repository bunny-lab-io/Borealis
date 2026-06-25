package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAgentScriptRequestHandlerReturnsIdleForActiveDevice(t *testing.T) {
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	signer := testAgentJWTSigner(t)
	signer.now = func() time.Time { return time.Unix(1700000000, 0) }
	token, err := signer.issueAccessToken(guid, "fingerprint", 4, agentAccessTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	scriptSigner := testAgentScriptSigner(t)
	store := &agentUpdateTestStore{
		deviceAuthFound: true,
		deviceAuthRecord: deviceBearerAuthRecord{
			GUID:         guid,
			Fingerprint:  "fingerprint",
			TokenVersion: 4,
			Status:       "active",
		},
	}
	auth := &authService{store: store, timeout: time.Second}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/script/request", nil)
	request.Header.Set("Authorization", "Bearer "+token)

	agentScriptRequestHandler(auth, signer, &dpopVerifier{seenJTI: map[string]time.Time{}}, scriptSigner).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "idle" || payload["poll_after_ms"].(float64) != 30000 || payload["sig_alg"] != "ed25519" || payload["signing_key"] != scriptSigningKeyB64(scriptSigner) {
		t.Fatalf("unexpected script response %+v", payload)
	}
}

func TestAgentScriptRequestHandlerQuarantinesNonActiveDevice(t *testing.T) {
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	signer := testAgentJWTSigner(t)
	signer.now = func() time.Time { return time.Unix(1700000000, 0) }
	token, err := signer.issueAccessToken(guid, "fingerprint", 4, agentAccessTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	scriptSigner := testAgentScriptSigner(t)
	store := &agentUpdateTestStore{
		deviceAuthFound: true,
		deviceAuthRecord: deviceBearerAuthRecord{
			GUID:         guid,
			Fingerprint:  "fingerprint",
			TokenVersion: 4,
			Status:       "pending",
		},
	}
	auth := &authService{store: store, timeout: time.Second}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/script/request", nil)
	request.Header.Set("Authorization", "Bearer "+token)

	agentScriptRequestHandler(auth, signer, &dpopVerifier{seenJTI: map[string]time.Time{}}, scriptSigner).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "quarantined" || payload["poll_after_ms"].(float64) != 60000 || payload["sig_alg"] != "ed25519" || payload["signing_key"] != scriptSigningKeyB64(scriptSigner) {
		t.Fatalf("unexpected script response %+v", payload)
	}
}
