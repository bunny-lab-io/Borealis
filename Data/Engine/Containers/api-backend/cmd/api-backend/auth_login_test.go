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
	state map[string]any
}

func (g *authLoginTestGate) operatorAuthAllowed(_ context.Context) (bool, error) {
	return cleanText(g.state["phase"]) == bootstrapPhaseLoginRequired, nil
}

func (g *authLoginTestGate) bootstrapState(_ context.Context) (map[string]any, error) {
	return copyMap(g.state), nil
}

type authLoginTestAegis struct {
	unlockedCipher string
}

func (a *authLoginTestAegis) status(_ context.Context) (map[string]any, error) {
	return map[string]any{"configured": true, "locked": a.unlockedCipher == ""}, nil
}

func (a *authLoginTestAegis) setupWithCipher(_ context.Context, cipherText string) (map[string]any, error) {
	a.unlockedCipher = cipherText
	return map[string]any{"configured": true, "locked": false}, nil
}

func (a *authLoginTestAegis) unlockWithCipher(_ context.Context, cipherText string) (map[string]any, error) {
	a.unlockedCipher = cipherText
	return map[string]any{"configured": true, "locked": false}, nil
}

func (a *authLoginTestAegis) rotateWithCipher(_ context.Context, _ string, newCipher string) (map[string]any, error) {
	a.unlockedCipher = newCipher
	return map[string]any{"configured": true, "locked": false}, nil
}

func (a *authLoginTestAegis) forceReset(_ context.Context) (map[string]any, error) {
	a.unlockedCipher = ""
	return map[string]any{"configured": false, "locked": false}, nil
}

func (a *authLoginTestAegis) decryptSecretText(_ context.Context, value any) (string, error) {
	if raw, ok := value.([]byte); ok {
		return strings.TrimPrefix(string(raw), "enc:"), nil
	}
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
	updatedPassword  string
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

func (s *authLoginTestStore) updateUserPasswordSecret(_ context.Context, username string, encryptedSecret string, _ int64) error {
	s.updatedPassword = encryptedSecret
	s.row.PasswordSecret = encryptedSecret
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

const correctPasswordSHA512 = "f30c82a8ee16931f5d2ab132d4d8b4ec940cb6a0a26fb052b7bfe928fafbecc33bf65534835e0bbc823a1d383c987f55c7a151d1f4966608426ec7bc670db267"

func mustPasswordVerifier(t *testing.T, credential passwordCredential) string {
	t.Helper()
	verifier, err := newPasswordVerifierFromCredential(credential)
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

func TestAuthLoginLocalMFADisabledIssuesToken(t *testing.T) {
	store := &authLoginTestStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		found:   true,
		row: authLoginRow{
			Username:       "operator",
			Role:           "Admin",
			PasswordSecret: "enc:" + mustPasswordVerifier(t, passwordCredential{Plain: "correct-password"}),
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
	if cleanText(payload["token"]) != "" {
		t.Fatalf("did not expect browser-readable token payload, got %#v", payload)
	}
	cookie := recorder.Result().Cookies()[0]
	if cookie.Name != authCookieName || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("expected secure HttpOnly auth cookie, got %#v", cookie)
	}
	if _, err := auth.verifier.verify(cookie.Value); err != nil {
		t.Fatalf("issued token did not verify: %v", err)
	}
	if store.lastLoginUser != "operator" {
		t.Fatalf("expected last_login update for operator, got %q", store.lastLoginUser)
	}
}

func TestAuthLoginMFASetupVerifiesSignedPendingToken(t *testing.T) {
	store := &authLoginTestStore{
		profile: operatorProfile{Username: "operator", Role: "User"},
		found:   true,
		row: authLoginRow{
			Username:       "operator",
			Role:           "User",
			PasswordSecret: "enc:" + correctPasswordSHA512,
			AuthSource:     "local",
		},
	}
	auth := newAuthLoginTestService(store, &authLoginTestGate{state: map[string]any{"phase": bootstrapPhaseLoginRequired, "configured": true, "locked": false}}, &authLoginTestAegis{})
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"operator","password":"correct-password","password_sha512":"`+correctPasswordSHA512+`"}`))
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
	if !strings.HasPrefix(store.updatedPassword, "enc:"+passwordVerifierVersion+"$") {
		t.Fatalf("expected legacy password verifier upgrade, got %q", store.updatedPassword)
	}
}

func TestBootstrapAegisUnlockUpdatesGoState(t *testing.T) {
	gate := &authLoginTestGate{state: map[string]any{"phase": bootstrapPhaseAegisUnlockRequired, "configured": true, "locked": true}}
	aegis := &authLoginTestAegis{}
	auth := newAuthLoginTestService(&authLoginTestStore{}, gate, aegis)
	request := httptest.NewRequest(http.MethodPost, "/api/bootstrap/aegis/unlock", strings.NewReader(`{"cipher":"test-cipher"}`))
	recorder := httptest.NewRecorder()

	bootstrapAegisUnlockHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if aegis.unlockedCipher != "test-cipher" {
		t.Fatalf("expected Go Aegis unlock, got %q", aegis.unlockedCipher)
	}
}
