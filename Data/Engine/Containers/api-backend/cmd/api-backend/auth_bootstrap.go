package main

import (
	"bytes"
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
const bootstrapPhaseAegisSetupRequired = "aegis_setup_required"
const bootstrapPhaseAegisUnlockRequired = "aegis_unlock_required"

type legacyBootstrapGate struct {
	baseURL *url.URL
	secret  []byte
	client  *http.Client
}

func (g *legacyBootstrapGate) operatorAuthAllowed(ctx context.Context) (bool, error) {
	payload, err := g.bootstrapState(ctx)
	if err != nil {
		return false, err
	}
	phase := strings.TrimSpace(fmt.Sprint(payload["phase"]))
	return phase == bootstrapPhaseLoginRequired, nil
}

func (g *legacyBootstrapGate) bootstrapState(ctx context.Context) (map[string]any, error) {
	if g == nil || g.baseURL == nil || len(g.secret) == 0 {
		return nil, errors.New("bootstrap gate unavailable")
	}
	client := g.client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	target := g.baseURL.ResolveReference(&url.URL{Path: "/api/internal/bootstrap/state"})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(internalTokenHeader, goInternalToken(g.secret))
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
		return nil, fmt.Errorf("legacy bootstrap state returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
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

func (g *legacyBootstrapGate) bootstrapAegisSetup(ctx context.Context, cipherText string) (map[string]any, int, error) {
	return g.postInternalAegis(ctx, "/api/internal/aegis/setup", cipherText)
}

func (g *legacyBootstrapGate) bootstrapAegisUnlock(ctx context.Context, cipherText string) (map[string]any, int, error) {
	return g.postInternalAegis(ctx, "/api/internal/aegis/unlock", cipherText)
}

func (g *legacyBootstrapGate) postInternalAegis(ctx context.Context, path string, cipherText string) (map[string]any, int, error) {
	if g == nil || g.baseURL == nil || len(g.secret) == 0 {
		return nil, http.StatusBadGateway, errors.New("bootstrap gate unavailable")
	}
	client := g.client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	body, err := json.Marshal(map[string]any{"cipher": cipherText})
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	target := g.baseURL.ResolveReference(&url.URL{Path: path})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(internalTokenHeader, goInternalToken(g.secret))
	resp, err := client.Do(req)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	var payload map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return payload, resp.StatusCode, nil
	}
	return payload, resp.StatusCode, nil
}
