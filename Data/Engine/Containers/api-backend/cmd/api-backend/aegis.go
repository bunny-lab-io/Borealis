package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type aegisStatusProvider interface {
	aegisStatus(ctx context.Context) (map[string]any, error)
}

type legacyAegisStatusProvider struct {
	baseURL *url.URL
	auth    *authService
	client  *http.Client
}

func registerAegisRoutes(mux *http.ServeMux, auth *authService, legacyURL *url.URL) {
	mux.HandleFunc("GET /api/aegis/status", aegisStatusHandler(auth, &legacyAegisStatusProvider{
		baseURL: legacyURL,
		auth:    auth,
		client:  &http.Client{Timeout: 3 * time.Second},
	}))
}

func aegisStatusHandler(auth *authService, provider aegisStatusProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		user, failure := requireUser(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		if provider == nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "aegis_status_unavailable"})
			return
		}
		payload, err := provider.aegisStatus(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":   "aegis_status_unavailable",
				"message": err.Error(),
			})
			return
		}
		if !aegisPayloadBool(payload, "configured") || aegisPayloadBool(payload, "locked") {
			unauthorizedAuthFailure().write(w)
			return
		}
		response := copyMap(payload)
		response["user_role"] = firstText(user.Role, "User")
		writeJSON(w, http.StatusOK, response)
	}
}

func (p *legacyAegisStatusProvider) aegisStatus(ctx context.Context) (map[string]any, error) {
	if p == nil || p.baseURL == nil || p.auth == nil || p.auth.verifier == nil || len(p.auth.verifier.secret) == 0 {
		return nil, errors.New("aegis status provider unavailable")
	}
	client := p.client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	target := p.baseURL.ResolveReference(&url.URL{Path: "/api/internal/aegis/status"})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(internalTokenHeader, goInternalToken(p.auth.verifier.secret))
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("legacy aegis status returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if payload == nil {
		payload = map[string]any{}
	}
	return payload, nil
}

func aegisPayloadBool(payload map[string]any, key string) bool {
	switch value := payload[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true") || strings.TrimSpace(value) == "1"
	case float64:
		return value != 0
	case int:
		return value != 0
	case int64:
		return value != 0
	default:
		return false
	}
}
