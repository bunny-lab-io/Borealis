package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type agentIngestTestStore struct {
	deviceAuthRecord deviceBearerAuthRecord
	deviceAuthFound  bool
	requiredVersion  *int

	heartbeatGUID    string
	heartbeatPayload map[string]any
	heartbeatStatus  int
	heartbeatErr     error

	statusGUID    string
	statusPayload map[string]any
	statusResult  agentStatusUpdateResult
	statusCode    int
	statusErr     error
	statusCalls   int

	detailsGUID    string
	detailsPayload map[string]any
	detailsResult  agentDetailsUpdateResult
	detailsCode    int
	detailsErr     error
	detailsCalls   int
}

func (s *agentIngestTestStore) requiredDeviceTokenVersion(_ context.Context, _ string) (*int, error) {
	return s.requiredVersion, nil
}

func (s *agentIngestTestStore) deviceBearerAuthRecord(_ context.Context, _ string) (deviceBearerAuthRecord, bool, error) {
	if !s.deviceAuthFound {
		return deviceBearerAuthRecord{}, false, nil
	}
	return s.deviceAuthRecord, true, nil
}

func (s *agentIngestTestStore) updateAgentHeartbeat(_ context.Context, deviceCtx deviceBearerAuthContext, payload map[string]any) (map[string]any, int, error) {
	s.heartbeatGUID = deviceCtx.GUID
	s.heartbeatPayload = copyMap(payload)
	status := s.heartbeatStatus
	if status == 0 {
		status = http.StatusOK
	}
	if s.heartbeatErr != nil {
		return nil, status, s.heartbeatErr
	}
	return map[string]any{
		"status":              "ok",
		"poll_after_ms":       int64(15000),
		"metadata_field_acks": []string{"field_009"},
	}, status, nil
}

func (s *agentIngestTestStore) updateAgentStatus(_ context.Context, deviceCtx deviceBearerAuthContext, payload map[string]any) (agentStatusUpdateResult, int, error) {
	s.statusCalls++
	s.statusGUID = deviceCtx.GUID
	s.statusPayload = copyMap(payload)
	status := s.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	if s.statusErr != nil {
		return agentStatusUpdateResult{}, status, s.statusErr
	}
	return s.statusResult, status, nil
}

func (s *agentIngestTestStore) updateAgentDetails(_ context.Context, deviceCtx deviceBearerAuthContext, payload map[string]any) (agentDetailsUpdateResult, int, error) {
	s.detailsCalls++
	s.detailsGUID = deviceCtx.GUID
	s.detailsPayload = copyMap(payload)
	status := s.detailsCode
	if status == 0 {
		status = http.StatusOK
	}
	if s.detailsErr != nil {
		return agentDetailsUpdateResult{}, status, s.detailsErr
	}
	if s.detailsResult.Payload == nil {
		s.detailsResult.Payload = map[string]any{"status": "ok"}
	}
	return s.detailsResult, status, nil
}

type fakeAgentStatusBroadcaster struct {
	payload map[string]any
	calls   int
	err     error

	deviceEvents []map[string]any
}

func (s *agentIngestTestStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	return operatorProfile{Username: username, Role: fallbackRole}, nil
}

func (b *fakeAgentStatusBroadcaster) broadcastAgentStatus(_ context.Context, payload map[string]any) error {
	b.calls++
	b.payload = copyMap(payload)
	return b.err
}

func (b *fakeAgentStatusBroadcaster) broadcastDeviceEvent(_ context.Context, eventName string, payload map[string]any) error {
	b.deviceEvents = append(b.deviceEvents, map[string]any{"event_name": eventName, "payload": copyMap(payload)})
	return b.err
}

func TestAgentHeartbeatHandlerPassesAuthenticatedPayload(t *testing.T) {
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	signer := testAgentJWTSigner(t)
	signer.now = func() time.Time { return time.Unix(1700000000, 0) }
	token, err := signer.issueAccessToken(guid, "fingerprint", 4, agentAccessTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	store := &agentIngestTestStore{
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
	request := httptest.NewRequest(http.MethodPost, "/api/agent/heartbeat", strings.NewReader(`{"hostname":"LAB-OPERATOR-01","metadata_fields":{"field_009":{"value":"rack-a-42","modified_at":1234}}}`))
	request.Header.Set("Authorization", "Bearer "+token)

	agentHeartbeatHandler(auth, signer, &dpopVerifier{seenJTI: map[string]time.Time{}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.heartbeatGUID != guid || cleanText(store.heartbeatPayload["hostname"]) != "LAB-OPERATOR-01" {
		t.Fatalf("unexpected heartbeat guid=%s payload=%+v", store.heartbeatGUID, store.heartbeatPayload)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	acks, _ := payload["metadata_field_acks"].([]any)
	if len(acks) != 1 || acks[0] != "field_009" {
		t.Fatalf("unexpected ack payload %+v", payload)
	}
}

func TestAgentStatusHandlerBroadcastsAndCoalescesDuplicates(t *testing.T) {
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	signer := testAgentJWTSigner(t)
	signer.now = func() time.Time { return time.Unix(1700000000, 0) }
	token, err := signer.issueAccessToken(guid, "fingerprint", 4, agentAccessTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"hostname":"LAB-OPERATOR-01","service_mode":"system","phase":"wireguard_online","status":"ok","message":"ready","boot_id":"boot-1","milestones":[]}`
	cache := &agentStatusCache{entries: map[string]agentStatusCacheEntry{}}
	store := &agentIngestTestStore{
		deviceAuthFound: true,
		deviceAuthRecord: deviceBearerAuthRecord{
			GUID:         guid,
			Fingerprint:  "fingerprint",
			TokenVersion: 4,
			Status:       "active",
		},
		statusResult: agentStatusUpdateResult{
			Payload:         map[string]any{"status": "ok", "poll_after_ms": int64(15000), "site_name": "Lab"},
			EmitPayload:     map[string]any{"hostname": "LAB-OPERATOR-01", "guid": guid, "phase": "wireguard_online", "status": "healthy", "changed_at": int64(1700000001)},
			Signature:       normalizedAgentStatusSignature(map[string]any{"hostname": "LAB-OPERATOR-01", "service_mode": "system", "phase": "wireguard_online", "status": "ok", "message": "ready", "boot_id": "boot-1", "milestones": []any{}}),
			CacheKey:        agentStatusCacheKey(guid, "system"),
			SiteName:        "Lab",
			ShouldBroadcast: true,
		},
	}
	broadcaster := &fakeAgentStatusBroadcaster{}
	auth := &authService{store: store, timeout: time.Second}

	first := httptest.NewRecorder()
	firstRequest := httptest.NewRequest(http.MethodPost, "/api/agent/status", strings.NewReader(body))
	firstRequest.Header.Set("Authorization", "Bearer "+token)
	agentStatusHandler(auth, signer, &dpopVerifier{seenJTI: map[string]time.Time{}}, broadcaster, cache).ServeHTTP(first, firstRequest)

	if first.Code != http.StatusOK || broadcaster.calls != 1 || store.statusCalls != 1 {
		t.Fatalf("expected first status write+broadcast, code=%d calls=%d broadcasts=%d body=%s", first.Code, store.statusCalls, broadcaster.calls, first.Body.String())
	}

	second := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodPost, "/api/agent/status", strings.NewReader(body))
	secondRequest.Header.Set("Authorization", "Bearer "+token)
	agentStatusHandler(auth, signer, &dpopVerifier{seenJTI: map[string]time.Time{}}, broadcaster, cache).ServeHTTP(second, secondRequest)

	if second.Code != http.StatusOK || store.statusCalls != 1 || broadcaster.calls != 1 {
		t.Fatalf("expected duplicate coalesced, code=%d calls=%d broadcasts=%d body=%s", second.Code, store.statusCalls, broadcaster.calls, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), `"coalesced":true`) {
		t.Fatalf("expected coalesced response, body=%s", second.Body.String())
	}
}
