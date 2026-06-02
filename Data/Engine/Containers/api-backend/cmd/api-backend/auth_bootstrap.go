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

const bootstrapPhaseLoginRequired = "login_required"

type legacyBootstrapGate struct {
	baseURL *url.URL
	secret  []byte
	client  *http.Client
}

func (g *legacyBootstrapGate) operatorAuthAllowed(ctx context.Context) (bool, error) {
	if g == nil || g.baseURL == nil || len(g.secret) == 0 {
		return false, errors.New("bootstrap gate unavailable")
	}
	client := g.client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	target := g.baseURL.ResolveReference(&url.URL{Path: "/api/internal/bootstrap/state"})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(internalTokenHeader, goInternalToken(g.secret))
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
	if err != nil {
		return false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return false, fmt.Errorf("legacy bootstrap state returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return false, err
	}
	phase := strings.TrimSpace(fmt.Sprint(payload["phase"]))
	return phase == bootstrapPhaseLoginRequired, nil
}
