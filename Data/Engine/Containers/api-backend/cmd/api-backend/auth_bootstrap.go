package main

import (
	"bytes"
	"context"
	"database/sql"
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
const bootstrapPhaseAdminSetupRequired = "admin_setup_required"
const bootstrapPhaseAdminRecoveryRequired = "admin_recovery_required"

type bootstrapCounts struct {
	UserCount              int64
	AdminCount             int64
	ReadyAdminCount        int64
	AuthResetRequiredCount int64
}

type bootstrapStateStore interface {
	bootstrapCounts(ctx context.Context) (bootstrapCounts, error)
}

type goBootstrapGate struct {
	store       operatorStore
	aegis       authSecretService
	legacyAegis bootstrapAegisLifecycle
}

func (g *goBootstrapGate) operatorAuthAllowed(ctx context.Context) (bool, error) {
	payload, err := g.bootstrapState(ctx)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(fmt.Sprint(payload["phase"])) == bootstrapPhaseLoginRequired, nil
}

func (g *goBootstrapGate) bootstrapState(ctx context.Context) (map[string]any, error) {
	if g == nil || g.aegis == nil {
		return nil, errors.New("bootstrap gate unavailable")
	}
	status, err := g.aegis.status(ctx)
	if err != nil {
		return nil, err
	}
	counts := bootstrapCounts{}
	if store, ok := g.store.(bootstrapStateStore); ok {
		counts, err = store.bootstrapCounts(ctx)
		if err != nil {
			return nil, err
		}
	}
	configured := boolFromAny(status["configured"])
	locked := configured && boolFromAny(status["locked"])
	phase := bootstrapPhaseLoginRequired
	switch {
	case !configured:
		phase = bootstrapPhaseAegisSetupRequired
	case locked:
		phase = bootstrapPhaseAegisUnlockRequired
	case counts.UserCount <= 0 || counts.AdminCount <= 0:
		phase = bootstrapPhaseAdminSetupRequired
	case counts.ReadyAdminCount <= 0:
		phase = bootstrapPhaseAdminRecoveryRequired
	}
	return map[string]any{
		"phase":                     phase,
		"configured":                configured,
		"locked":                    locked,
		"user_count":                counts.UserCount,
		"admin_count":               counts.AdminCount,
		"ready_admin_count":         counts.ReadyAdminCount,
		"auth_reset_required_count": counts.AuthResetRequiredCount,
	}, nil
}

func (g *goBootstrapGate) bootstrapAegisSetup(ctx context.Context, cipherText string) (map[string]any, int, error) {
	payload, err := g.aegis.setupWithCipher(ctx, cipherText)
	if err != nil {
		return aegisErrorPayload(err)
	}
	if g.legacyAegis != nil {
		if legacyPayload, status, err := g.legacyAegis.bootstrapAegisUnlock(ctx, cipherText); err != nil {
			return legacyPayload, status, err
		} else if status < 200 || status > 299 {
			return legacyPayload, status, nil
		}
	}
	payload = copyMap(payload)
	payload["status"] = "ok"
	return payload, http.StatusOK, nil
}

func (g *goBootstrapGate) bootstrapAegisUnlock(ctx context.Context, cipherText string) (map[string]any, int, error) {
	payload, err := g.aegis.unlockWithCipher(ctx, cipherText)
	if err != nil {
		return aegisErrorPayload(err)
	}
	if g.legacyAegis != nil {
		if legacyPayload, status, err := g.legacyAegis.bootstrapAegisUnlock(ctx, cipherText); err != nil {
			return legacyPayload, status, err
		} else if status < 200 || status > 299 {
			return legacyPayload, status, nil
		}
	}
	payload = copyMap(payload)
	payload["status"] = "ok"
	return payload, http.StatusOK, nil
}

func (s *postgresOperatorStore) bootstrapCounts(ctx context.Context) (bootstrapCounts, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return bootstrapCounts{}, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	var counts bootstrapCounts
	err = conn.QueryRowContext(ctx, `
		SELECT
			COUNT(*) AS user_count,
			COUNT(*) FILTER (WHERE LOWER(COALESCE(role, ''))='admin') AS admin_count,
			COUNT(*) FILTER (
				WHERE LOWER(COALESCE(role, ''))='admin'
				  AND COALESCE(auth_reset_required, 0)=0
				  AND COALESCE(password_sha512, '')<>''
			) AS ready_admin_count,
			COUNT(*) FILTER (WHERE COALESCE(auth_reset_required, 0)<>0) AS auth_reset_required_count
		  FROM engine.users
	`).Scan(
		&counts.UserCount,
		&counts.AdminCount,
		&counts.ReadyAdminCount,
		&counts.AuthResetRequiredCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return counts, nil
	}
	return counts, err
}

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
