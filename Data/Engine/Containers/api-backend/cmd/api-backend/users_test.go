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

	createUsername    string
	createDisplayName string
	createRole        string
	createPassword    string
	createPayload     map[string]any
	createStatus      int
	createErr         error

	deleteProfile  operatorProfile
	deleteUsername string
	deletePayload  map[string]any
	deleteStatus   int
	deleteErr      error

	passwordResetUsername string
	passwordResetHash     string
	passwordResetPayload  map[string]any
	passwordResetStatus   int
	passwordResetErr      error

	ownPasswordUsername string
	ownPasswordCurrent  string
	ownPasswordNew      string
	ownPasswordPayload  map[string]any
	ownPasswordStatus   int
	ownPasswordErr      error

	roleProfile  operatorProfile
	roleUsername string
	roleValue    string
	rolePayload  map[string]any
	roleStatus   int
	roleErr      error

	mfaUsername    string
	mfaEnabled     bool
	mfaResetSecret bool
	mfaPayload     map[string]any
	mfaStatus      int
	mfaErr         error

	resetUsername string
	resetPayload  map[string]any
	resetStatus   int
	resetErr      error
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

func (s *fakeUserMutationStore) createUser(_ context.Context, _ authSecretService, username string, displayName string, role string, passwordSHA512 string) (map[string]any, int, error) {
	s.createUsername = username
	s.createDisplayName = displayName
	s.createRole = role
	s.createPassword = passwordSHA512
	if s.createPayload == nil {
		s.createPayload = map[string]any{"status": "ok"}
	}
	status := s.createStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.createPayload, status, s.createErr
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

func (s *fakeUserMutationStore) resetUserPassword(_ context.Context, _ authSecretService, username string, passwordSHA512 string) (map[string]any, int, error) {
	s.passwordResetUsername = username
	s.passwordResetHash = passwordSHA512
	if s.passwordResetPayload == nil {
		s.passwordResetPayload = map[string]any{"status": "ok"}
	}
	status := s.passwordResetStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.passwordResetPayload, status, s.passwordResetErr
}

func (s *fakeUserMutationStore) resetOwnPassword(_ context.Context, _ authSecretService, username string, currentPasswordSHA512 string, newPasswordSHA512 string) (map[string]any, int, error) {
	s.ownPasswordUsername = username
	s.ownPasswordCurrent = currentPasswordSHA512
	s.ownPasswordNew = newPasswordSHA512
	if s.ownPasswordPayload == nil {
		s.ownPasswordPayload = map[string]any{"status": "ok"}
	}
	status := s.ownPasswordStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.ownPasswordPayload, status, s.ownPasswordErr
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

func (s *fakeUserMutationStore) updateUserMFA(_ context.Context, username string, enabled bool, resetSecret bool) (map[string]any, int, error) {
	s.mfaUsername = username
	s.mfaEnabled = enabled
	s.mfaResetSecret = resetSecret
	if s.mfaPayload == nil {
		s.mfaPayload = map[string]any{"status": "ok"}
	}
	status := s.mfaStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.mfaPayload, status, s.mfaErr
}

func (s *fakeUserMutationStore) resetOwnMFA(_ context.Context, username string) (map[string]any, int, error) {
	s.resetUsername = username
	if s.resetPayload == nil {
		s.resetPayload = map[string]any{
			"status":                       "ok",
			"username":                     username,
			"mfa_enabled":                  true,
			"setup_required_on_next_login": true,
		}
	}
	status := s.resetStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.resetPayload, status, s.resetErr
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
		aegis:   &authLoginTestAegis{},
		timeout: time.Second,
	}
}

func TestUsersPostCreateDispatchesToGoStore(t *testing.T) {
	store := &fakeUserMutationStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth := userMutationAuthService(store)
	mux := http.NewServeMux()
	registerUserRoutes(mux, auth, http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, userMutationRequest(http.MethodPost, "/api/users", `{"username":"created_user","display_name":"Created User","role":"admin","password_sha512":"abc123"}`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.createUsername != "created_user" || store.createDisplayName != "Created User" || store.createRole != "Admin" || store.createPassword != "abc123" {
		t.Fatalf("unexpected create dispatch username=%q display=%q role=%q password=%q", store.createUsername, store.createDisplayName, store.createRole, store.createPassword)
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

func TestUserSubtreeMFADispatchesToGoStore(t *testing.T) {
	store := &fakeUserMutationStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth := userMutationAuthService(store)

	recorder := httptest.NewRecorder()
	userSubtreeHandler(auth, http.NotFoundHandler()).ServeHTTP(recorder, userMutationRequest(http.MethodPost, "/api/users/example_user/mfa", `{"enabled":true,"reset_secret":true}`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.mfaUsername != "example_user" || !store.mfaEnabled || !store.mfaResetSecret {
		t.Fatalf("expected MFA update example_user true true, got %q %v %v", store.mfaUsername, store.mfaEnabled, store.mfaResetSecret)
	}
}

func TestOwnMFAResetDispatchesCurrentUser(t *testing.T) {
	store := &fakeUserMutationStore{profile: operatorProfile{Username: "operator", Role: "User"}}
	auth := userMutationAuthService(store)

	recorder := httptest.NewRecorder()
	ownMFAResetHandler(auth).ServeHTTP(recorder, userMutationRequest(http.MethodPost, "/api/auth/mfa/reset", `{}`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.resetUsername != "operator" {
		t.Fatalf("expected reset username operator, got %q", store.resetUsername)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["setup_required_on_next_login"] != true {
		t.Fatalf("expected setup_required_on_next_login true, got %+v", payload)
	}
}

func TestUserSubtreeResetPasswordDispatchesToGoStore(t *testing.T) {
	store := &fakeUserMutationStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth := userMutationAuthService(store)
	passwordHash := strings.Repeat("a", 128)

	recorder := httptest.NewRecorder()
	userSubtreeHandler(auth, http.NotFoundHandler()).ServeHTTP(recorder, userMutationRequest(http.MethodPost, "/api/users/example_user/reset_password", `{"password_sha512":"`+passwordHash+`"}`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.passwordResetUsername != "example_user" || store.passwordResetHash != passwordHash {
		t.Fatalf("expected reset dispatch example_user/hash, got %q/%q", store.passwordResetUsername, store.passwordResetHash)
	}
}

func TestOwnPasswordResetHashesPlainPasswordPayloads(t *testing.T) {
	store := &fakeUserMutationStore{profile: operatorProfile{Username: "operator", Role: "User"}}
	auth := userMutationAuthService(store)

	recorder := httptest.NewRecorder()
	ownPasswordResetHandler(auth).ServeHTTP(recorder, userMutationRequest(http.MethodPost, "/api/auth/password/reset", `{"current_password":"old-password","new_password":"new-password"}`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.ownPasswordUsername != "operator" {
		t.Fatalf("expected own reset username operator, got %q", store.ownPasswordUsername)
	}
	if store.ownPasswordCurrent != sha512Hex("old-password") || store.ownPasswordNew != sha512Hex("new-password") {
		t.Fatalf("expected hashed own password payload, got current=%q new=%q", store.ownPasswordCurrent, store.ownPasswordNew)
	}
}
