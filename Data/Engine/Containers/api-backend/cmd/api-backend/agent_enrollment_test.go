package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type enrollmentTestStore struct {
	requestInput   agentEnrollmentRequestInput
	requestResult  agentEnrollmentRequestResult
	requestStatus  int
	requestErr     error
	finalizeInput  agentEnrollmentFinalizeInput
	finalizeResult agentEnrollmentFinalizeResult
	finalizeStatus int
	finalizeErr    error
	replayKeySeen  string
	replayTimeSeen time.Time
	replayAllow    bool
}

func (s *enrollmentTestStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	return operatorProfile{Username: username, Role: fallbackRole}, nil
}

func (s *enrollmentTestStore) createAgentEnrollmentRequest(_ context.Context, request agentEnrollmentRequestInput) (agentEnrollmentRequestResult, int, error) {
	s.requestInput = request
	if s.requestErr != nil {
		status := s.requestStatus
		if status == 0 {
			status = http.StatusBadRequest
		}
		return agentEnrollmentRequestResult{}, status, s.requestErr
	}
	status := s.requestStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.requestResult, status, nil
}

func (s *enrollmentTestStore) finalizeAgentEnrollment(_ context.Context, request agentEnrollmentFinalizeInput) (agentEnrollmentFinalizeResult, int, error) {
	s.finalizeInput = request
	if request.ConsumeReplay != nil {
		key := request.ApprovalReference + ":" + base64.StdEncoding.EncodeToString(request.ProofSignature)
		s.replayKeySeen = key
		s.replayTimeSeen = request.Now
		if !request.ConsumeReplay(key, request.Now) {
			return agentEnrollmentFinalizeResult{}, http.StatusConflict, errDPoPReplay
		}
	}
	if s.finalizeErr != nil {
		status := s.finalizeStatus
		if status == 0 {
			status = http.StatusBadRequest
		}
		return agentEnrollmentFinalizeResult{}, status, s.finalizeErr
	}
	status := s.finalizeStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.finalizeResult, status, nil
}

func TestAgentEnrollmentRequestHandlerCreatesPendingResponse(t *testing.T) {
	// Fixed test-only Ed25519 SPKI has an On...= base64 suffix. Generic HTML
	// event-attribute detection previously rejected such randomly generated keys.
	const encodedKey = "MCowBQYDK2VwAyEAZ7y25wZ806ry8XZHh0bRAdSR2tOnfsxQGgQOmMHEQpI="
	publicDER, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x509.ParsePKIXPublicKey(publicDER); err != nil {
		t.Fatal(err)
	}
	store := &enrollmentTestStore{
		requestResult: agentEnrollmentRequestResult{
			Status:            "pending",
			ApprovalReference: "approval-ref",
			ServerNonceB64:    base64.StdEncoding.EncodeToString([]byte("server-nonce-01234567890123456789")),
			PollAfterMS:       3000,
		},
	}
	scriptSigner := testAgentScriptSigner(t)
	auth := &authService{store: store, timeout: time.Second}
	body := `{"hostname":"agent-node-01","enrollment_code":"INSTALL-CODE","agent_pubkey":"` +
		base64.StdEncoding.EncodeToString(publicDER) +
		`","client_nonce":"` + encodedKey + `"}`

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/enroll/request", strings.NewReader(body))
	request.RemoteAddr = "10.0.0.5:12345"
	agentEnrollmentRequestHandler(
		auth,
		scriptSigner,
		&enrollmentRateLimiter{events: map[string][]time.Time{}},
		&enrollmentRateLimiter{events: map[string][]time.Time{}},
	).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.requestInput.Hostname != "agent-node-01" || store.requestInput.EnrollmentCode != "INSTALL-CODE" {
		t.Fatalf("unexpected request input %+v", store.requestInput)
	}
	if store.requestInput.Fingerprint != fingerprintFromSPKIDER(publicDER) {
		t.Fatalf("unexpected fingerprint %s", store.requestInput.Fingerprint)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "pending" || payload["approval_reference"] != "approval-ref" || payload["signing_key"] != scriptSigningKeyB64(scriptSigner) {
		t.Fatalf("unexpected response %+v", payload)
	}
}

func TestAgentEnrollmentPollHandlerIssuesAccessToken(t *testing.T) {
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	store := &enrollmentTestStore{
		finalizeResult: agentEnrollmentFinalizeResult{
			Status:       "approved",
			GUID:         guid,
			Fingerprint:  "fingerprint",
			TokenVersion: 2,
			RefreshToken: "refresh-token",
			SiteID:       int64Ptr(7),
			Route: &agentWorkerRoute{
				WorkerGUID:      "worker-7",
				SiteID:          7,
				RoutePathPrefix: "/_borealis/site-workers/worker-7",
				Generation:      3,
			},
		},
	}
	signer := testAgentJWTSigner(t)
	scriptSigner := testAgentScriptSigner(t)
	auth := &authService{store: store, timeout: time.Second}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/agent/enroll/poll",
		strings.NewReader(`{"approval_reference":"approval-ref","client_nonce":"`+base64.StdEncoding.EncodeToString([]byte("client-nonce"))+`","proof_sig":"`+base64.StdEncoding.EncodeToString([]byte("proof"))+`"}`),
	)
	request.Header.Set("X-Forwarded-Host", "borealis.example.test")
	request.Header.Set("X-Forwarded-Proto", "https")
	agentEnrollmentPollHandler(
		auth,
		signer,
		scriptSigner,
		&enrollmentReplayCache{seen: map[string]time.Time{}, ttl: agentEnrollmentProofTTL},
	).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.finalizeInput.ApprovalReference != "approval-ref" || store.replayKeySeen == "" {
		t.Fatalf("expected finalize with replay consume, got input=%+v replay=%q", store.finalizeInput, store.replayKeySeen)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "approved" || payload["guid"] != guid || payload["refresh_token"] != "refresh-token" || payload["access_token"] == "" {
		t.Fatalf("unexpected response %+v", payload)
	}
	remoteOps := payload["remote_ops_route"].(map[string]any)
	if remoteOps["available"] != true || remoteOps["socket_url"] != "https://borealis.example.test/_borealis/site-workers/worker-7/socket.io/" {
		t.Fatalf("unexpected remote ops %+v", remoteOps)
	}
}

func testAgentScriptSigner(t *testing.T) *agentScriptSigner {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := newAgentScriptSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func int64Ptr(value int64) *int64 {
	return &value
}
