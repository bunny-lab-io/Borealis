package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeWireGuardRecoveryStore struct {
	profile    operatorProfile
	active     int64
	serviceKey string
	action     map[string]any
}

func (s *fakeWireGuardRecoveryStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeWireGuardRecoveryStore) activeWireGuardLeaseCount(_ context.Context) int64 {
	return s.active
}

func (s *fakeWireGuardRecoveryStore) queueServerServiceAction(_ context.Context, serviceKey string, action map[string]any) (int64, error) {
	s.serviceKey = serviceKey
	s.action = action
	return 42, nil
}

func wireGuardRecoveryAuth(store *fakeWireGuardRecoveryStore) *authService {
	return &authService{
		verifier: &tokenVerifier{
			secret: []byte("test-secret"),
			maxAge: time.Hour,
			now:    func() time.Time { return time.Unix(1700000010, 0) },
		},
		store:   store,
		timeout: time.Second,
	}
}

func TestServerWireGuardRecoverQueuesReconcile(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_CONTAINERIZED", "1")
	store := &fakeWireGuardRecoveryStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		active:  1,
	}
	mux := http.NewServeMux()
	registerServerWireGuardRoutes(mux, wireGuardRecoveryAuth(store))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/server/wireguard/recover", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.serviceKey != "wireguard-tunnel" || cleanText(store.action["action"]) != "reconcile" {
		t.Fatalf("unexpected queued service=%q action=%+v", store.serviceKey, store.action)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	wireguard := payload["wireguard"].(map[string]any)
	if wireguard["recovery_in_progress"] != true || wireguard["work_item_id"].(float64) != 42 {
		t.Fatalf("unexpected wireguard payload %+v", wireguard)
	}
}

func TestServerWireGuardRecoverRejectsWithoutActiveLeases(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_CONTAINERIZED", "1")
	store := &fakeWireGuardRecoveryStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		active:  0,
	}
	mux := http.NewServeMux()
	registerServerWireGuardRoutes(mux, wireGuardRecoveryAuth(store))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/server/wireguard/recover", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || store.serviceKey != "" {
		t.Fatalf("unexpected status=%d service=%q body=%s", recorder.Code, store.serviceKey, recorder.Body.String())
	}
}
