package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAgentDetailsHandlerCoalescesAndBroadcastsInventoryEvents(t *testing.T) {
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
		detailsResult: agentDetailsUpdateResult{
			Payload:           map[string]any{"status": "ok"},
			Hostname:          "LAB-OPERATOR-01",
			ServicesChanged:   true,
			SoftwareChanged:   true,
			RememberDuplicate: true,
		},
	}
	auth := &authService{store: store, timeout: time.Second}
	cache := &agentDetailsCache{entries: map[string]agentDetailsCacheEntry{}}
	broadcaster := &fakeAgentStatusBroadcaster{}
	body := `{"hostname":"LAB-OPERATOR-01","service_mode":"system","details":{"summary":{"hostname":"LAB-OPERATOR-01"},"software":[{"name":"Tool","version":"1.0"}]}}`

	first := httptest.NewRecorder()
	firstRequest := httptest.NewRequest(http.MethodPost, "/api/agent/details", strings.NewReader(body))
	firstRequest.Header.Set("Authorization", "Bearer "+token)
	agentDetailsHandler(auth, signer, &dpopVerifier{seenJTI: map[string]time.Time{}}, broadcaster, cache).ServeHTTP(first, firstRequest)

	if first.Code != http.StatusOK || store.detailsCalls != 1 {
		t.Fatalf("expected first details write, code=%d calls=%d body=%s", first.Code, store.detailsCalls, first.Body.String())
	}
	if store.detailsGUID != guid || cleanText(store.detailsPayload["hostname"]) != "LAB-OPERATOR-01" {
		t.Fatalf("unexpected details auth/payload guid=%s payload=%+v", store.detailsGUID, store.detailsPayload)
	}
	if len(broadcaster.deviceEvents) != 2 {
		t.Fatalf("expected service and software event broadcasts, got %+v", broadcaster.deviceEvents)
	}

	second := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodPost, "/api/agent/details", strings.NewReader(body))
	secondRequest.Header.Set("Authorization", "Bearer "+token)
	agentDetailsHandler(auth, signer, &dpopVerifier{seenJTI: map[string]time.Time{}}, broadcaster, cache).ServeHTTP(second, secondRequest)

	if second.Code != http.StatusOK || store.detailsCalls != 1 {
		t.Fatalf("expected duplicate coalesced, code=%d calls=%d body=%s", second.Code, store.detailsCalls, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), `"coalesced":true`) {
		t.Fatalf("expected coalesced response, body=%s", second.Body.String())
	}
}

func TestAgentDetailsNormalizerDedupesSoftwareAndPreservesPendingServiceAction(t *testing.T) {
	software := normalizeAgentSoftwareInventory([]any{
		map[string]any{"name": "Borealis Tool", "version": "1.0", "source": "installed", "publisher": "Bunny"},
		map[string]any{"name": "Borealis Tool", "version": "1.0", "source": "local_installed", "publisher": "Bunny"},
		map[string]any{"name": "11111111-1111-1111-1111-111111111111", "source": "windows_store"},
	})
	if len(software) != 1 || cleanText(software[0]["source"]) != "local_installed" {
		t.Fatalf("unexpected software normalization: %+v", software)
	}

	existing := sql.NullString{Valid: true, String: `{"services":[{"name":"BorealisAgent","status":"Running","pending_action":"restart","pending_requested_at":1700000000,"pending_requested_by":"operator"}]}`}
	incoming := normalizeDeviceServicesAny([]any{
		map[string]any{"name": "BorealisAgent", "status": "Running", "captured_at": 1699999999},
	}, 0)
	merged := mergeDeviceServicesForDetails(existing, incoming)
	services := mapSlice(merged["services"])
	if len(services) != 1 {
		t.Fatalf("expected one merged service, got %+v", merged)
	}
	if cleanText(services[0]["pending_action"]) != "restart" || coerceInt64(services[0]["pending_requested_at"]) != 1700000000 {
		encoded, _ := json.Marshal(services[0])
		t.Fatalf("pending service action lost: %s", encoded)
	}
}

func TestAgentDetailsStoreRejectsMissingDetailsPayload(t *testing.T) {
	store := &postgresOperatorStore{}
	_, status, err := store.updateAgentDetails(context.Background(), deviceBearerAuthContext{GUID: "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"}, map[string]any{"hostname": "LAB"})
	if status != http.StatusBadRequest || err == nil {
		t.Fatalf("expected bad request for missing details, status=%d err=%v", status, err)
	}
}
