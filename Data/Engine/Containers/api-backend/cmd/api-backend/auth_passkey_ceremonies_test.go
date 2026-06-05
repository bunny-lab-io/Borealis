package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type passkeyCeremonyTestStore struct {
	profile operatorProfile
	user    passkeyCeremonyUser
}

func (s *passkeyCeremonyTestStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	if profile.DisplayName == "" {
		profile.DisplayName = profile.Username
	}
	if profile.AuthSource == "" {
		profile.AuthSource = "local"
	}
	return profile, nil
}

func (s *passkeyCeremonyTestStore) loadPasskeyRegistrationUser(_ context.Context, username string) (passkeyCeremonyUser, bool, error) {
	user := s.user
	if user.ID == 0 {
		user.ID = 7
	}
	if user.Username == "" {
		user.Username = username
	}
	if user.DisplayName == "" {
		user.DisplayName = user.Username
	}
	if user.Role == "" {
		user.Role = "Admin"
	}
	if user.AuthSource == "" {
		user.AuthSource = "local"
	}
	return user, true, nil
}

func (s *passkeyCeremonyTestStore) insertUserPasskey(_ context.Context, _ int64, _ string, _ []string, _ string, _ string, _ int64) (int, error) {
	return 1, nil
}

func (s *passkeyCeremonyTestStore) findPasskeyForAssertion(_ context.Context, _ []byte, _ []string, _ []string) (passkeyCeremonyUser, passkeyStoredCredential, bool, error) {
	return passkeyCeremonyUser{}, passkeyStoredCredential{}, false, nil
}

func (s *passkeyCeremonyTestStore) updatePasskeyAssertion(_ context.Context, _ int64, _ string, _ string, _ int64) (int, error) {
	return 1, nil
}

func TestPasskeyRegisterOptionsUsesGoCeremony(t *testing.T) {
	t.Setenv("BOREALIS_PUBLIC_BASE_URL", "https://borealis.example.test")
	store := &passkeyCeremonyTestStore{
		profile: operatorProfile{Username: "operator", Role: "Admin", AuthSource: "local"},
		user:    passkeyCeremonyUser{ID: 7, Username: "operator", DisplayName: "Operator", Role: "Admin", AuthSource: "local"},
	}
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	auth.store = store
	auth.bootstrapGate = &authLoginTestGate{state: map[string]any{"phase": bootstrapPhaseLoginRequired}}
	auth.aegis = &authLoginTestAegis{unlockedCipher: "ready"}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/passkeys/register/options", strings.NewReader(`{"label":"Desk Key"}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	passkeyRegisterOptionsHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "ok" || cleanText(payload["request_id"]) == "" {
		t.Fatalf("unexpected passkey register payload %+v", payload)
	}
	options, _ := payload["options"].(map[string]any)
	rp, _ := options["rp"].(map[string]any)
	user, _ := options["user"].(map[string]any)
	if rp["id"] != "borealis.example.test" || user["name"] != "operator" {
		t.Fatalf("unexpected options rp=%+v user=%+v", rp, user)
	}
	if _, nested := options["publicKey"]; nested {
		t.Fatalf("expected direct SimpleWebAuthn options, got nested publicKey")
	}
}

func TestPasskeyAuthenticateOptionsUsesGoCeremony(t *testing.T) {
	t.Setenv("BOREALIS_PUBLIC_BASE_URL", "https://borealis.example.test")
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	auth.bootstrapGate = &authLoginTestGate{state: map[string]any{"phase": bootstrapPhaseLoginRequired}}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/passkeys/authenticate/options", strings.NewReader(`{}`))
	passkeyAuthenticateOptionsHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	options, _ := payload["options"].(map[string]any)
	if cleanText(options["challenge"]) == "" || options["userVerification"] != "required" {
		t.Fatalf("unexpected authenticate options %+v", options)
	}
}

func TestPasskeyRegisterVerifyRejectsInvalidSignedSession(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	auth.bootstrapGate = &authLoginTestGate{state: map[string]any{"phase": bootstrapPhaseLoginRequired}}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/passkeys/register/verify", strings.NewReader(`{"request_id":"bad","credential":{}}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	passkeyRegisterVerifyHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["error"] != "invalid_session" {
		t.Fatalf("unexpected error payload %+v", payload)
	}
}
