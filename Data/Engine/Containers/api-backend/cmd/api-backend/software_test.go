package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeSoftwareIconStore struct {
	profile operatorProfile
	asset   softwareIconAsset
	found   bool
	seen    string
}

func (s *fakeSoftwareIconStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeSoftwareIconStore) loadSoftwareIconAsset(_ context.Context, iconHash string) (softwareIconAsset, bool, error) {
	s.seen = iconHash
	return s.asset, s.found, nil
}

func softwareIconTestAuth(store *fakeSoftwareIconStore) *authService {
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

func TestSoftwareIconHandlerReturnsAsset(t *testing.T) {
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	store := &fakeSoftwareIconStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		asset: softwareIconAsset{
			Hash:     hash,
			MIMEType: "image/png",
			Bytes:    []byte{0x89, 0x50, 0x4e, 0x47},
		},
		found: true,
	}
	mux := http.NewServeMux()
	registerSoftwareRoutes(mux, softwareIconTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/software/icon/"+hash, nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.seen != hash {
		t.Fatalf("expected normalized hash capture, got %q", store.seen)
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("unexpected content-type %q", got)
	}
	if got := recorder.Body.Bytes(); len(got) != 4 || got[1] != 0x50 {
		t.Fatalf("unexpected icon bytes %v", got)
	}
}

func TestSoftwareIconHandlerRejectsInvalidHash(t *testing.T) {
	store := &fakeSoftwareIconStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerSoftwareRoutes(mux, softwareIconTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/software/icon/not-a-hash", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.seen != "" {
		t.Fatalf("invalid hash should not hit store, got %q", store.seen)
	}
}

func TestSoftwareIconHandlerNotFound(t *testing.T) {
	hash := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	store := &fakeSoftwareIconStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerSoftwareRoutes(mux, softwareIconTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/software/icon/"+hash, nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}
