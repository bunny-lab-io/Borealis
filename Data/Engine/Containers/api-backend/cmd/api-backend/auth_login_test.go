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

type authLoginTestGate struct {
	state        map[string]any
	setupCalled  bool
	unlockCalled bool
}

func (g *authLoginTestGate) operatorAuthAllowed(_ context.Context) (bool, error) {
	return cleanText(g.state["phase"]) == bootstrapPhaseLoginRequired, nil
}

func (g *authLoginTestGate) bootstrapState(_ context.Context) (map[string]any, error) {
	return copyMap(g.state), nil
}

func (g *authLoginTestGate) bootstrapAegisSetup(_ context.Context, _ string) (map[string]any, int, error) {
	g.setupCalled = true
	g.state = map[string]any{"phase": bootstrapPhaseLoginRequired, "configured": true, "locked": false}
	return map[string]any{"status": "ok"}, http.StatusOK, nil
}

func (g *authLoginTestGate) bootstrapAegisUnlock(_ context.Context, _ string) (map[string]any, int, error) {
	g.unlockCalled = true
	g.state = map[string]any{"phase": bootstrapPhaseLoginRequired, "configured": true, "locked": false}
	return map[string]any{"status": "ok"}, http.StatusOK, nil
}

type authLoginTestAegis struct {
	unlockedCipher string
}

func (a *authLoginTestAegis) status(_ context.Context) (map[string]any, error) {
	return map[string]any{"configured": true, "locked": a.unlockedCipher == ""}, nil
}

func (a *authLoginTestAegis) unlockWithCipher(_ context.Context, cipherText string) (map[string]any, error) {
	a.unlockedCipher = cipherText
	return map[string]any{"configured": true, "locked": false}, nil
}

func (a *authLoginTestAegis) decryptSecretText(_ context.Context, value any) (string, error) {
	return strings.TrimPrefix(cleanText(value), "enc:"), nil
}

func (a *authLoginTestAegis) encryptSecretText(_ context.Context, value string) (string, error) {
	return "enc:" + value, nil
}

type authLoginTestStore struct {
	profile          operatorProfile
	row              authLoginRow
	found            bool
	lastLoginUser    string
	updatedMFAUser   string
	updatedMFASecret string
}

func (s *authLoginTestStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *authLoginTestStore) loadLoginRow(_ context.Context, _ string) (authLoginRow, bool, error) {
	return s.row, s.found, nil
}

func (s *authLoginTestStore) updateLastLogin(_ context.Context, username string, _ int64) error {
	s.lastLoginUser = username
	return nil
}

func (s *authLoginTestStore) updateUserMFASecret(_ context.Context, username string, encryptedSecret string, _ int64) error {
	s.updatedMFAUser = username
	s.updatedMFASecret = encryptedSecret
	s.row.MFASecret = encryptedSecret
	return nil
}

func newAuthLoginTestService(store *authLoginTestStore, gate *authLoginTestGate, aegis *authLoginTestAegis) *authService {
	return &authService{
		verifier:      &tokenVerifier{secret: []byte("test-secret"), maxAge: time.Hour, now: time.Now},
		store:         store,
		bootstrapGate: gate,
		aegis:         aegis,
		timeout:       time.Second,
	}
}

func TestAuthLoginLocalMFADisabledIssuesToken(t *testing.T) {
	passwordHash := sha512Hex("correct-password")
	store := &authLoginTestStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		found:   true,
		row: authLoginRow{
			Username:       "operator",
			Role:           "Admin",
			PasswordSecret: "enc:" + passwordHash,
			AuthSource:     "local",
			MFADisabled:    true,
		},
	}
	auth := newAuthLoginTestService(store, &authLoginTestGate{state: map[string]any{"phase": bootstrapPhaseLoginRequired, "configured": true, "locked": false}}, &authLoginTestAegis{})
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"operator","password":"correct-password"}`))
	recorder := httptest.NewRecorder()

	authLoginHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if cleanText(payload["token"]) == "" {
		t.Fatalf("expected token payload, got %#v", payload)
	}
	if _, err := auth.verifier.verify(cleanText(payload["token"])); err != nil {
		t.Fatalf("issued token did not verify: %v", err)
	}
	if store.lastLoginUser != "operator" {
		t.Fatalf("expected last_login update for operator, got %q", store.lastLoginUser)
	}
}

func TestAuthLoginMFASetupVerifiesSignedPendingToken(t *testing.T) {
	passwordHash := sha512Hex("correct-password")
	store := &authLoginTestStore{
		profile: operatorProfile{Username: "operator", Role: "User"},
		found:   true,
		row: authLoginRow{
			Username:       "operator",
			Role:           "User",
			PasswordSecret: "enc:" + passwordHash,
			AuthSource:     "local",
		},
	}
	auth := newAuthLoginTestService(store, &authLoginTestGate{state: map[string]any{"phase": bootstrapPhaseLoginRequired, "configured": true, "locked": false}}, &authLoginTestAegis{})
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"operator","password_sha512":"`+passwordHash+`"}`))
	loginRecorder := httptest.NewRecorder()

	authLoginHandler(auth, nil).ServeHTTP(loginRecorder, loginRequest)

	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("expected login 200, got %d body=%s", loginRecorder.Code, loginRecorder.Body.String())
	}
	var loginPayload map[string]any
	if err := json.Unmarshal(loginRecorder.Body.Bytes(), &loginPayload); err != nil {
		t.Fatal(err)
	}
	secret := cleanText(loginPayload["secret"])
	pendingToken := cleanText(loginPayload["pending_token"])
	if cleanText(loginPayload["stage"]) != "setup" || secret == "" || pendingToken == "" {
		t.Fatalf("expected MFA setup payload, got %#v", loginPayload)
	}
	code := hotp(secret, uint64(time.Now().Unix()/30))
	verifyRequest := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/verify", strings.NewReader(`{"pending_token":"`+pendingToken+`","code":"`+code+`"}`))
	verifyRecorder := httptest.NewRecorder()

	authMFAVerifyHandler(auth, nil).ServeHTTP(verifyRecorder, verifyRequest)

	if verifyRecorder.Code != http.StatusOK {
		t.Fatalf("expected verify 200, got %d body=%s", verifyRecorder.Code, verifyRecorder.Body.String())
	}
	if store.updatedMFAUser != "operator" || store.updatedMFASecret != "enc:"+secret {
		t.Fatalf("expected encrypted MFA update, got user=%q secret=%q", store.updatedMFAUser, store.updatedMFASecret)
	}
}

func TestBootstrapAegisUnlockBridgesLegacyAndGoState(t *testing.T) {
	gate := &authLoginTestGate{state: map[string]any{"phase": bootstrapPhaseAegisUnlockRequired, "configured": true, "locked": true}}
	aegis := &authLoginTestAegis{}
	auth := newAuthLoginTestService(&authLoginTestStore{}, gate, aegis)
	request := httptest.NewRequest(http.MethodPost, "/api/bootstrap/aegis/unlock", strings.NewReader(`{"cipher":"test-cipher"}`))
	recorder := httptest.NewRecorder()

	bootstrapAegisUnlockHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !gate.unlockCalled {
		t.Fatal("expected legacy unlock bridge call")
	}
	if aegis.unlockedCipher != "test-cipher" {
		t.Fatalf("expected Go Aegis unlock, got %q", aegis.unlockedCipher)
	}
}
