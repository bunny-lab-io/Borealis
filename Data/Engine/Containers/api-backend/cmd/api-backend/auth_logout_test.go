package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthLogoutHandlerClearsCookies(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	authLogoutHandler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) < 2 {
		t.Fatalf("expected logout cookies, got %v", cookies)
	}
	seen := map[string]bool{}
	for _, cookie := range cookies {
		if cookie.MaxAge >= 0 {
			t.Fatalf("expected deleted cookie, got %+v", cookie)
		}
		seen[cookie.Name] = true
	}
	if !seen[authCookieName] || !seen["session"] {
		t.Fatalf("missing expected deleted cookies: %+v", seen)
	}
}
