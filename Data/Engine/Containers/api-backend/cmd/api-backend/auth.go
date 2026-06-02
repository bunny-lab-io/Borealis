package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type operatorIdentity struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

type authFailure struct {
	status int
	body   map[string]any
}

func (f authFailure) write(w http.ResponseWriter) {
	writeJSON(w, f.status, f.body)
}

func requireLegacyUser(ctx context.Context, cfg gatewayConfig, r *http.Request) (operatorIdentity, *authFailure) {
	requestCtx, cancel := context.WithTimeout(ctx, cfg.AuthTimeout)
	defer cancel()

	authURL := cfg.LegacyURL.ResolveReference(&url.URL{Path: "/api/auth/me"})
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, authURL.String(), nil)
	if err != nil {
		return operatorIdentity{}, badGatewayAuthFailure(err)
	}

	for _, header := range []string{"Authorization", "Cookie", "X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host"} {
		if value := r.Header.Get(header); strings.TrimSpace(value) != "" {
			req.Header.Set(header, value)
		}
	}
	req.Header.Set("User-Agent", "borealis-go-api-backend-auth")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return operatorIdentity{}, badGatewayAuthFailure(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return operatorIdentity{}, &authFailure{
			status: http.StatusUnauthorized,
			body: map[string]any{
				"error":   "unauthorized",
				"message": "Authentication required. Please sign in and retry.",
			},
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return operatorIdentity{}, &authFailure{
			status: http.StatusBadGateway,
			body: map[string]any{
				"error":  "legacy_auth_unavailable",
				"detail": fmt.Sprintf("legacy auth returned HTTP %d", resp.StatusCode),
			},
		}
	}

	var identity operatorIdentity
	if err := json.Unmarshal(body, &identity); err != nil {
		return operatorIdentity{}, badGatewayAuthFailure(err)
	}
	if strings.TrimSpace(identity.Username) == "" {
		return operatorIdentity{}, badGatewayAuthFailure(fmt.Errorf("legacy auth returned no username"))
	}
	if strings.TrimSpace(identity.Role) == "" {
		identity.Role = "User"
	}
	return identity, nil
}

func requireLegacyAdmin(ctx context.Context, cfg gatewayConfig, r *http.Request) (operatorIdentity, *authFailure) {
	identity, failure := requireLegacyUser(ctx, cfg, r)
	if failure != nil {
		return operatorIdentity{}, failure
	}
	if strings.EqualFold(strings.TrimSpace(identity.Role), "admin") {
		return identity, nil
	}
	return operatorIdentity{}, &authFailure{
		status: http.StatusForbidden,
		body: map[string]any{
			"error":   "forbidden",
			"message": "Administrator permissions are required for this action.",
		},
	}
}

func badGatewayAuthFailure(err error) *authFailure {
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	return &authFailure{
		status: http.StatusBadGateway,
		body: map[string]any{
			"error":  "legacy_auth_unavailable",
			"detail": detail,
		},
	}
}
