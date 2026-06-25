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

type authBootstrapAdminTestStore struct {
	authLoginTestStore
	createdUsername string
	createdPassword string
	createdMFA      string
	recoveryDisplay string
	recoveryReset   bool
	recoveryFound   bool
}

func (s *authBootstrapAdminTestStore) bootstrapAdminRecoveryCandidate(_ context.Context, username string) (string, bool, bool, error) {
	return firstText(s.recoveryDisplay, username), s.recoveryReset, s.recoveryFound, nil
}

func (s *authBootstrapAdminTestStore) createBootstrapAdmin(_ context.Context, username string, _ string, encryptedPassword string, encryptedMFASecret string, _ int64) error {
	s.createdUsername = username
	s.createdPassword = encryptedPassword
	s.createdMFA = encryptedMFASecret
	s.profile = operatorProfile{Username: username, Role: "Admin"}
	s.row = authLoginRow{Username: username, Role: "Admin", AuthSource: "local", MFADisabled: true}
	s.found = true
	return nil
}

func (s *authBootstrapAdminTestStore) recoverBootstrapAdmin(_ context.Context, username string, encryptedPassword string, encryptedMFASecret string, _ int64) error {
	s.createdUsername = username
	s.createdPassword = encryptedPassword
	s.createdMFA = encryptedMFASecret
	return nil
}

func TestBootstrapAdminSetupUsesSignedPendingToken(t *testing.T) {
	passwordHash := strings.Repeat("a", 128)
	store := &authBootstrapAdminTestStore{}
	auth := &authService{
		verifier:      &tokenVerifier{secret: []byte("test-secret"), maxAge: time.Hour, now: time.Now},
		store:         store,
		bootstrapGate: &authLoginTestGate{state: map[string]any{"phase": bootstrapPhaseAdminSetupRequired, "configured": true, "locked": false}},
		aegis:         &authLoginTestAegis{},
		timeout:       time.Second,
	}
	setupRequest := httptest.NewRequest(http.MethodPost, "/api/bootstrap/admin/setup", strings.NewReader(`{"username":"operator","password_sha512":"`+passwordHash+`"}`))
	setupRecorder := httptest.NewRecorder()

	bootstrapAdminSetupHandler(auth).ServeHTTP(setupRecorder, setupRequest)

	if setupRecorder.Code != http.StatusOK {
		t.Fatalf("expected setup 200, got %d body=%s", setupRecorder.Code, setupRecorder.Body.String())
	}
	var setupPayload map[string]any
	if err := json.Unmarshal(setupRecorder.Body.Bytes(), &setupPayload); err != nil {
		t.Fatal(err)
	}
	secret := cleanText(setupPayload["secret"])
	pendingToken := cleanText(setupPayload["pending_token"])
	if secret == "" || pendingToken == "" || cleanText(setupPayload["stage"]) != "setup" {
		t.Fatalf("unexpected setup payload %#v", setupPayload)
	}
	code := hotp(secret, uint64(time.Now().Unix()/30))
	verifyRequest := httptest.NewRequest(http.MethodPost, "/api/bootstrap/admin/mfa/verify", strings.NewReader(`{"pending_token":"`+pendingToken+`","code":"`+code+`"}`))
	verifyRecorder := httptest.NewRecorder()

	bootstrapAdminMFAVerifyHandler(auth).ServeHTTP(verifyRecorder, verifyRequest)

	if verifyRecorder.Code != http.StatusOK {
		t.Fatalf("expected verify 200, got %d body=%s", verifyRecorder.Code, verifyRecorder.Body.String())
	}
	if store.createdUsername != "operator" || store.createdPassword != "enc:"+passwordHash || store.createdMFA != "enc:"+secret {
		t.Fatalf("unexpected created admin user=%q password=%q mfa=%q", store.createdUsername, store.createdPassword, store.createdMFA)
	}
}

func TestAegisRotateHandlerRequiresAdminAndRotates(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	aegis := &authLoginTestAegis{}
	auth.aegis = aegis
	request := httptest.NewRequest(http.MethodPost, "/api/aegis/rotate", strings.NewReader(`{"current_cipher":"old","new_cipher":"new"}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	recorder := httptest.NewRecorder()

	aegisRotateHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected rotate 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if aegis.unlockedCipher != "new" {
		t.Fatalf("expected rotate to update active cipher marker, got %q", aegis.unlockedCipher)
	}
}
