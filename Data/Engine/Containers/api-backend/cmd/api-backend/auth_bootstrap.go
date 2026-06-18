package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
	store operatorStore
	aegis authSecretService
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
