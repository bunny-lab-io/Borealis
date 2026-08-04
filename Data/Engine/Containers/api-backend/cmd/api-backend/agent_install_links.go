package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

type agentInstallLinkRecord struct {
	ID               int64
	SiteID           int64
	Platform         string
	ArtifactID       string
	LinkNonce        string
	IssuedAt         int64
	ExpiresAt        int64
	RevokedAt        int64
	DownloadCount    int64
	LastDownloadedAt int64
}

var errAgentInstallLinkSchemaPending = errors.New("engine sites table unavailable")

func (s *postgresOperatorStore) ensureAgentInstallLinkSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errOperatorStoreDown
	}
	s.agentInstallLinkSchemaMu.Lock()
	defer s.agentInstallLinkSchemaMu.Unlock()
	if s.agentInstallLinkSchemaOK {
		return nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS engine`); err != nil {
		return err
	}
	var sitesReady bool
	if err := conn.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM information_schema.tables
			 WHERE table_schema='engine'
			   AND table_name='sites'
		)
	`).Scan(&sitesReady); err != nil {
		return err
	}
	if !sitesReady {
		return errAgentInstallLinkSchemaPending
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS engine.site_agent_install_links (
			id BIGSERIAL PRIMARY KEY,
			site_id BIGINT NOT NULL REFERENCES engine.sites(id) ON DELETE CASCADE,
			platform TEXT NOT NULL,
			artifact_id TEXT NOT NULL,
			link_nonce TEXT NOT NULL,
			issued_at BIGINT NOT NULL DEFAULT 0,
			expires_at BIGINT NOT NULL DEFAULT 0,
			revoked_at BIGINT NOT NULL DEFAULT 0,
			download_count BIGINT NOT NULL DEFAULT 0,
			last_downloaded_at BIGINT NOT NULL DEFAULT 0
		)`,
		`ALTER TABLE engine.site_agent_install_links ADD COLUMN IF NOT EXISTS site_id BIGINT REFERENCES engine.sites(id) ON DELETE CASCADE`,
		`ALTER TABLE engine.site_agent_install_links ADD COLUMN IF NOT EXISTS platform TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE engine.site_agent_install_links ADD COLUMN IF NOT EXISTS artifact_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE engine.site_agent_install_links ADD COLUMN IF NOT EXISTS link_nonce TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE engine.site_agent_install_links ADD COLUMN IF NOT EXISTS issued_at BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE engine.site_agent_install_links ADD COLUMN IF NOT EXISTS expires_at BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE engine.site_agent_install_links ADD COLUMN IF NOT EXISTS revoked_at BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE engine.site_agent_install_links ADD COLUMN IF NOT EXISTS download_count BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE engine.site_agent_install_links ADD COLUMN IF NOT EXISTS last_downloaded_at BIGINT NOT NULL DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS idx_site_agent_install_links_site ON engine.site_agent_install_links(site_id, platform, artifact_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_site_agent_install_links_active ON engine.site_agent_install_links(site_id, platform) WHERE revoked_at = 0`,
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	s.agentInstallLinkSchemaOK = true
	return nil
}

func (s *postgresOperatorStore) ensureAgentInstallLink(ctx context.Context, siteID int64, platform string, artifactID string, expiresAt int64) (agentInstallLinkRecord, error) {
	if err := s.ensureAgentInstallLinkSchema(ctx); err != nil {
		return agentInstallLinkRecord{}, err
	}
	platform = normalizeAgentInstallPlatform(platform)
	artifactID = cleanText(artifactID)
	if siteID <= 0 || platform == "" || artifactID == "" || expiresAt <= 0 {
		return agentInstallLinkRecord{}, errInvalidToken
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return agentInstallLinkRecord{}, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return agentInstallLinkRecord{}, err
	}
	defer rollbackQuietly(tx)

	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.site_agent_install_links
		   SET revoked_at=$1
		 WHERE site_id=$2
		   AND platform=$3
		   AND revoked_at=0
		   AND (expires_at <= $1 OR artifact_id <> $4)
	`, now, siteID, platform, artifactID); err != nil {
		return agentInstallLinkRecord{}, err
	}
	record, found, err := fetchActiveAgentInstallLinkTx(ctx, tx, siteID, platform, artifactID, 0)
	if err != nil {
		return agentInstallLinkRecord{}, err
	}
	if found {
		if err := tx.Commit(); err != nil {
			return agentInstallLinkRecord{}, err
		}
		return record, nil
	}
	record, err = insertAgentInstallLinkTx(ctx, tx, siteID, platform, artifactID, expiresAt, now)
	if err != nil {
		if isPostgresUniqueViolation(err) {
			_ = tx.Rollback()
			return s.ensureAgentInstallLink(ctx, siteID, platform, artifactID, expiresAt)
		}
		return agentInstallLinkRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return agentInstallLinkRecord{}, err
	}
	return record, nil
}

func (s *postgresOperatorStore) agentInstallLinkForDownload(ctx context.Context, siteID int64, platform string, artifactID string, expiresAt int64) (agentInstallLinkRecord, error) {
	if err := s.ensureAgentInstallLinkSchema(ctx); err != nil {
		return agentInstallLinkRecord{}, err
	}
	platform = normalizeAgentInstallPlatform(platform)
	artifactID = cleanText(artifactID)
	if siteID <= 0 || platform == "" || artifactID == "" || expiresAt <= 0 {
		return agentInstallLinkRecord{}, errInvalidToken
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return agentInstallLinkRecord{}, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	record, found, err := fetchActiveAgentInstallLinkConn(ctx, conn, siteID, platform, artifactID, expiresAt)
	if err != nil {
		return agentInstallLinkRecord{}, err
	}
	if !found {
		return agentInstallLinkRecord{}, errInvalidToken
	}
	return record, nil
}

func (s *postgresOperatorStore) recordAgentInstallLinkDownload(ctx context.Context, linkID int64) error {
	if linkID <= 0 {
		return nil
	}
	if err := s.ensureAgentInstallLinkSchema(ctx); err != nil {
		return err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `
		UPDATE engine.site_agent_install_links
		   SET download_count=download_count+1,
			   last_downloaded_at=$1
		 WHERE id=$2
		   AND revoked_at=0
	`, time.Now().Unix(), linkID)
	return err
}

func (s *postgresOperatorStore) revokeAgentInstallLink(ctx context.Context, siteID int64, platform string, artifactID string, expiresAt int64) (agentInstallLinkRecord, error) {
	if err := s.ensureAgentInstallLinkSchema(ctx); err != nil {
		return agentInstallLinkRecord{}, err
	}
	platform = normalizeAgentInstallPlatform(platform)
	artifactID = cleanText(artifactID)
	if siteID <= 0 || platform == "" || artifactID == "" || expiresAt <= 0 {
		return agentInstallLinkRecord{}, errInvalidToken
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return agentInstallLinkRecord{}, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return agentInstallLinkRecord{}, err
	}
	defer rollbackQuietly(tx)
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.site_agent_install_links
		   SET revoked_at=$1
		 WHERE site_id=$2
		   AND platform=$3
		   AND revoked_at=0
	`, now, siteID, platform); err != nil {
		return agentInstallLinkRecord{}, err
	}
	record, err := insertAgentInstallLinkTx(ctx, tx, siteID, platform, artifactID, expiresAt, now)
	if err != nil {
		return agentInstallLinkRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return agentInstallLinkRecord{}, err
	}
	return record, nil
}

func fetchActiveAgentInstallLinkTx(ctx context.Context, tx *sql.Tx, siteID int64, platform string, artifactID string, expiresAt int64) (agentInstallLinkRecord, bool, error) {
	query := `
		SELECT id, site_id, platform, artifact_id, link_nonce, issued_at, expires_at, revoked_at, download_count, last_downloaded_at
		  FROM engine.site_agent_install_links
		 WHERE site_id=$1
		   AND platform=$2
		   AND artifact_id=$3
		   AND revoked_at=0
		   AND expires_at>$4
	`
	params := []any{siteID, platform, artifactID, time.Now().Unix()}
	if expiresAt > 0 {
		query += " AND expires_at=$5"
		params = append(params, expiresAt)
	}
	query += " ORDER BY id DESC LIMIT 1"
	return scanAgentInstallLink(tx.QueryRowContext(ctx, query, params...))
}

func fetchActiveAgentInstallLinkConn(ctx context.Context, conn *sql.Conn, siteID int64, platform string, artifactID string, expiresAt int64) (agentInstallLinkRecord, bool, error) {
	query := `
		SELECT id, site_id, platform, artifact_id, link_nonce, issued_at, expires_at, revoked_at, download_count, last_downloaded_at
		  FROM engine.site_agent_install_links
		 WHERE site_id=$1
		   AND platform=$2
		   AND artifact_id=$3
		   AND revoked_at=0
		   AND expires_at>$4
	`
	params := []any{siteID, platform, artifactID, time.Now().Unix()}
	if expiresAt > 0 {
		query += " AND expires_at=$5"
		params = append(params, expiresAt)
	}
	query += " ORDER BY id DESC LIMIT 1"
	return scanAgentInstallLink(conn.QueryRowContext(ctx, query, params...))
}

func insertAgentInstallLinkTx(ctx context.Context, tx *sql.Tx, siteID int64, platform string, artifactID string, expiresAt int64, issuedAt int64) (agentInstallLinkRecord, error) {
	nonce, err := randomAgentInstallLinkNonce()
	if err != nil {
		return agentInstallLinkRecord{}, err
	}
	return scanRequiredAgentInstallLink(tx.QueryRowContext(ctx, `
		INSERT INTO engine.site_agent_install_links(site_id, platform, artifact_id, link_nonce, issued_at, expires_at, revoked_at, download_count, last_downloaded_at)
		VALUES ($1, $2, $3, $4, $5, $6, 0, 0, 0)
		RETURNING id, site_id, platform, artifact_id, link_nonce, issued_at, expires_at, revoked_at, download_count, last_downloaded_at
	`, siteID, platform, artifactID, nonce, issuedAt, expiresAt))
}

type agentInstallLinkScanner interface {
	Scan(dest ...any) error
}

func scanAgentInstallLink(row agentInstallLinkScanner) (agentInstallLinkRecord, bool, error) {
	record, err := scanRequiredAgentInstallLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return agentInstallLinkRecord{}, false, nil
	}
	return record, err == nil, err
}

func scanRequiredAgentInstallLink(row agentInstallLinkScanner) (agentInstallLinkRecord, error) {
	var record agentInstallLinkRecord
	var platform, artifactID, nonce sql.NullString
	err := row.Scan(
		&record.ID,
		&record.SiteID,
		&platform,
		&artifactID,
		&nonce,
		&record.IssuedAt,
		&record.ExpiresAt,
		&record.RevokedAt,
		&record.DownloadCount,
		&record.LastDownloadedAt,
	)
	if err != nil {
		return agentInstallLinkRecord{}, err
	}
	record.Platform = normalizeAgentInstallPlatform(nullString(platform))
	record.ArtifactID = cleanText(nullString(artifactID))
	record.LinkNonce = cleanText(nullString(nonce))
	return record, nil
}

func randomAgentInstallLinkNonce() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func normalizeAgentInstallPlatform(value string) string {
	text := strings.ToLower(strings.TrimSpace(value))
	switch text {
	case "windows", "win", "win64", "windows-amd64":
		return "windows-amd64"
	case "linux", "linux64", "linux-amd64":
		return "linux-amd64"
	default:
		return ""
	}
}

func agentInstallOSID(platform string) string {
	switch normalizeAgentInstallPlatform(platform) {
	case "windows-amd64":
		return "windows"
	case "linux-amd64":
		return "linux"
	default:
		return ""
	}
}

func agentInstallLinkError(message string) error {
	return fmt.Errorf("agent install link unavailable: %s", message)
}
