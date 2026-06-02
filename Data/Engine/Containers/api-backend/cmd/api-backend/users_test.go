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

type fakeUserMutationStore struct {
	profile operatorProfile
	authErr error

	deleteProfile  operatorProfile
	deleteUsername string
	deletePayload  map[string]any
	deleteStatus   int
	deleteErr      error

	roleProfile  operatorProfile
	roleUsername string
	roleValue    string
	rolePayload  map[string]any
	roleStatus   int
	roleErr      error
}

func (s *fakeUserMutationStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	if s.authErr != nil {
		return operatorProfile{}, s.authErr
	}
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeUserMutationStore) deleteUser(_ context.Context, profile operatorProfile, username string) (map[string]any, int, error) {
	s.deleteProfile = profile
	s.deleteUsername = username
	if s.deletePayload == nil {
		s.deletePayload = map[string]any{"status": "ok"}
	}
	status := s.deleteStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.deletePayload, status, s.deleteErr
}

func (s *fakeUserMutationStore) updateUserRole(_ context.Context, profile operatorProfile, username string, role string) (map[string]any, int, error) {
	s.roleProfile = profile
	s.roleUsername = username
	s.roleValue = role
	if s.rolePayload == nil {
		s.rolePayload = map[string]any{"status": "ok"}
	}
	status := s.roleStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.rolePayload, status, s.roleErr
}

func userMutationRequest(method string, path string, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func userMutationAuthService(store *fakeUserMutationStore) *authService {
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

func TestUserSubtreeDeleteDispatchesToGoStore(t *testing.T) {
	store := &fakeUserMutationStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth := userMutationAuthService(store)
	mux := http.NewServeMux()
	registerUserRoutes(mux, auth, http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, userMutationRequest(http.MethodDelete, "/api/users/example_user", ""))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.deleteUsername != "example_user" {
		t.Fatalf("expected delete username example_user, got %q", store.deleteUsername)
	}
	if store.deleteProfile.Username != "operator" {
		t.Fatalf("expected operator profile, got %+v", store.deleteProfile)
	}
}

func TestUserSubtreeRoleDispatchesToGoStore(t *testing.T) {
	store := &fakeUserMutationStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth := userMutationAuthService(store)
	mux := http.NewServeMux()
	registerUserRoutes(mux, auth, http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, userMutationRequest(http.MethodPost, "/api/users/example_user/role", `{"role":"admin"}`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.roleUsername != "example_user" || store.roleValue != "Admin" {
		t.Fatalf("expected role update example_user/Admin, got %q/%q", store.roleUsername, store.roleValue)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("expected ok payload, got %+v", payload)
	}
}

func TestUserSubtreeRejectsInvalidRole(t *testing.T) {
	store := &fakeUserMutationStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth := userMutationAuthService(store)

	recorder := httptest.NewRecorder()
	userSubtreeHandler(auth, http.NotFoundHandler()).ServeHTTP(recorder, userMutationRequest(http.MethodPost, "/api/users/example_user/role", `{"role":"owner"}`))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.roleUsername != "" {
		t.Fatalf("expected store not to be called, got %q", store.roleUsername)
	}
}

func TestUserSubtreeKeepsPasswordResetOnFallback(t *testing.T) {
	store := &fakeUserMutationStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth := userMutationAuthService(store)
	fallbackHits := 0
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.WriteHeader(http.StatusAccepted)
	})

	recorder := httptest.NewRecorder()
	userSubtreeHandler(auth, fallback).ServeHTTP(recorder, userMutationRequest(http.MethodPost, "/api/users/example_user/reset_password", `{}`))

	if recorder.Code != http.StatusAccepted || fallbackHits != 1 {
		t.Fatalf("expected fallback 202/1, got status=%d hits=%d", recorder.Code, fallbackHits)
	}
}
