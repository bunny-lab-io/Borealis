package main

import (
	"crypto/hmac"
	"net/http"
	"os"
	"strings"
)

func registerInternalSchedulerRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("/api/internal/job-scheduler/public-base-url", internalSchedulerPublicBaseURLHandler(auth))
}

func internalSchedulerPublicBaseURLHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		if !validInternalSchedulerRequest(auth, r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"public_base_url": configuredPublicBaseURL(r)})
	}
}

func validInternalSchedulerRequest(auth *authService, r *http.Request) bool {
	if auth == nil || auth.verifier == nil || len(auth.verifier.secret) == 0 {
		return false
	}
	expected := goInternalToken(auth.verifier.secret)
	presented := strings.TrimSpace(r.Header.Get(internalTokenHeader))
	if expected == "" || presented == "" {
		return false
	}
	return hmac.Equal([]byte(expected), []byte(presented))
}

func configuredPublicBaseURL(r *http.Request) string {
	for _, name := range []string{"BOREALIS_PUBLIC_BASE_URL", "PUBLIC_BASE_URL"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return strings.TrimRight(value, "/")
		}
	}
	return publicBaseURLForRequest(r)
}
