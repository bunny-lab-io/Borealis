package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPILivenessDoesNotRequireDatabase(t *testing.T) {
	recorder := httptest.NewRecorder()
	apiLivenessHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/live", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("dependency-free liveness failed: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestAPIReadinessWithdrawsDuringDrain(t *testing.T) {
	apiDraining.Store(true)
	t.Cleanup(func() { apiDraining.Store(false) })
	recorder := httptest.NewRecorder()
	apiReadinessHandler(&authService{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("draining API remained ready: %d %s", recorder.Code, recorder.Body.String())
	}
}
