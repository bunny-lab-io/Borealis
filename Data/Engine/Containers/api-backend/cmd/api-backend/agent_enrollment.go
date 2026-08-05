package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	agentEnrollmentContextHeader = "X-Borealis-Agent-Context"
	agentEnrollmentProofTTL      = 5 * time.Minute
	scriptSigningKeyFilename     = "borealis-script-ed25519.key"
	scriptSigningPubFilename     = "borealis-script-ed25519.pub"
)

type agentEnrollmentStore interface {
	createAgentEnrollmentRequest(ctx context.Context, request agentEnrollmentRequestInput) (agentEnrollmentRequestResult, int, error)
	finalizeAgentEnrollment(ctx context.Context, request agentEnrollmentFinalizeInput) (agentEnrollmentFinalizeResult, int, error)
}

type agentEnrollmentRequestInput struct {
	Hostname          string
	EnrollmentCode    string
	AgentPublicKeyDER []byte
	Fingerprint       string
	ClientNonceB64    string
	OnboardingContext enrollmentOnboardingContext
	RemoteAddr        string
	Now               time.Time
}

type enrollmentOnboardingContext struct {
	JobID  *int64
	RunID  *int64
	Target string
}

type agentEnrollmentRequestResult struct {
	Status            string
	ApprovalReference string
	ServerNonceB64    string
	PollAfterMS       int
	AutoApproved      bool
}

type agentEnrollmentFinalizeInput struct {
	ApprovalReference string
	ClientNonceB64    string
	ClientNonce       []byte
	ProofSignature    []byte
	Now               time.Time
	ConsumeReplay     func(key string, now time.Time) bool
}

type agentEnrollmentFinalizeResult struct {
	Status           string
	Detail           string
	Reason           string
	PollAfterMS      int
	GUID             string
	Fingerprint      string
	TokenVersion     int
	RefreshToken     string
	SiteID           *int64
	Route            *agentWorkerRoute
	RemoteOpsReason  string
	ServerNonce      []byte
	ApprovalHostname string
}

type agentScriptSigner struct {
	privateKey ed25519.PrivateKey
	publicB64  string
}

type enrollmentReplayCache struct {
	mu      sync.Mutex
	seen    map[string]time.Time
	ttl     time.Duration
	nowFunc func() time.Time
}

type enrollmentRateLimiter struct {
	mu      sync.Mutex
	events  map[string][]time.Time
	nowFunc func() time.Time
}

func registerAgentEnrollmentRoutes(mux *http.ServeMux, auth *authService, jwtSigner *agentJWTSigner, scriptSigner *agentScriptSigner) {
	replay := &enrollmentReplayCache{seen: map[string]time.Time{}, ttl: agentEnrollmentProofTTL}
	ipLimiter := &enrollmentRateLimiter{events: map[string][]time.Time{}}
	fpLimiter := &enrollmentRateLimiter{events: map[string][]time.Time{}}
	mux.HandleFunc("POST /api/agent/enroll/request", agentEnrollmentRequestHandler(auth, scriptSigner, ipLimiter, fpLimiter))
	mux.HandleFunc("POST /api/agent/enroll/poll", agentEnrollmentPollHandler(auth, jwtSigner, scriptSigner, replay))
}

func agentEnrollmentRequestHandler(auth *authService, scriptSigner *agentScriptSigner, ipLimiter *enrollmentRateLimiter, fpLimiter *enrollmentRateLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		store, ok := auth.store.(agentEnrollmentStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "enrollment_unavailable"})
			return
		}

		now := time.Now().UTC()
		remote := requestRemoteAddr(r)
		if retryAfter, limited := ipLimiter.check("ip:"+remote, 40, time.Minute, now); limited {
			writeRateLimited(w, retryAfter)
			return
		}

		body, err := readJSONMap(r)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		hostname := cleanText(body["hostname"])
		enrollmentCode := cleanText(body["enrollment_code"])
		agentPubkeyB64, pubkeyOK := body["agent_pubkey"].(string)
		clientNonceB64, nonceOK := body["client_nonce"].(string)

		if hostname == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "hostname_required"})
			return
		}
		if enrollmentCode == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "enrollment_code_required"})
			return
		}
		if !pubkeyOK {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "agent_pubkey_required"})
			return
		}
		if !nonceOK {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "client_nonce_required"})
			return
		}

		agentPubkeyDER, err := decodeStandardBase64(agentPubkeyB64)
		if err != nil || len(agentPubkeyDER) < 10 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_agent_pubkey"})
			return
		}
		clientNonceBytes, err := decodeStandardBase64(clientNonceB64)
		if err != nil || len(clientNonceBytes) < 16 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_client_nonce"})
			return
		}

		fingerprint := fingerprintFromSPKIDER(agentPubkeyDER)
		if retryAfter, limited := fpLimiter.check("fp:"+fingerprint, 12, time.Minute, now); limited {
			writeRateLimited(w, retryAfter)
			return
		}

		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		result, status, err := store.createAgentEnrollmentRequest(ctx, agentEnrollmentRequestInput{
			Hostname:          hostname,
			EnrollmentCode:    enrollmentCode,
			AgentPublicKeyDER: agentPubkeyDER,
			Fingerprint:       fingerprint,
			ClientNonceB64:    clientNonceB64,
			OnboardingContext: parseEnrollmentOnboardingContext(body["onboarding_context"]),
			RemoteAddr:        remote,
			Now:               now,
		})
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}

		payload := map[string]any{
			"status":             result.Status,
			"approval_reference": result.ApprovalReference,
			"server_nonce":       result.ServerNonceB64,
			"poll_after_ms":      result.PollAfterMS,
			"signing_key":        scriptSigningKeyB64(scriptSigner),
		}
		if result.AutoApproved {
			payload["auto_approved"] = true
		}
		writeJSON(w, http.StatusOK, payload)
	}
}

func agentEnrollmentPollHandler(auth *authService, jwtSigner *agentJWTSigner, scriptSigner *agentScriptSigner, replay *enrollmentReplayCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		store, ok := auth.store.(agentEnrollmentStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "enrollment_unavailable"})
			return
		}

		body, err := readJSONMap(r)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		approvalReference, referenceOK := body["approval_reference"].(string)
		clientNonceB64, nonceOK := body["client_nonce"].(string)
		proofSigB64, sigOK := body["proof_sig"].(string)
		approvalReference = strings.TrimSpace(approvalReference)
		if !referenceOK || approvalReference == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "approval_reference_required"})
			return
		}
		if !nonceOK {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "client_nonce_required"})
			return
		}
		if !sigOK {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "proof_sig_required"})
			return
		}
		clientNonce, err := decodeStandardBase64(clientNonceB64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_client_nonce"})
			return
		}
		proofSig, err := decodeStandardBase64(proofSigB64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_proof_sig"})
			return
		}

		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		result, status, err := store.finalizeAgentEnrollment(ctx, agentEnrollmentFinalizeInput{
			ApprovalReference: approvalReference,
			ClientNonceB64:    clientNonceB64,
			ClientNonce:       clientNonce,
			ProofSignature:    proofSig,
			Now:               time.Now().UTC(),
			ConsumeReplay:     replay.consume,
		})
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		if result.Status != "approved" || result.GUID == "" {
			payload := map[string]any{"status": result.Status}
			if result.Detail != "" {
				payload["detail"] = result.Detail
			}
			if result.Reason != "" {
				payload["reason"] = result.Reason
			}
			if result.PollAfterMS > 0 {
				payload["poll_after_ms"] = result.PollAfterMS
			}
			writeJSON(w, status, payload)
			return
		}
		accessToken, err := jwtSigner.issueAccessToken(result.GUID, result.Fingerprint, result.TokenVersion, agentAccessTokenTTL)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "token_issue_failed"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status":           "approved",
			"guid":             result.GUID,
			"access_token":     accessToken,
			"expires_in":       int(agentAccessTokenTTL / time.Second),
			"refresh_token":    result.RefreshToken,
			"token_type":       "Bearer",
			"signing_key":      scriptSigningKeyB64(scriptSigner),
			"remote_ops_route": buildAgentRemoteOpsRoutePayload(r, result.SiteID, result.Route, result.RemoteOpsReason),
		})
	}
}

func (s *postgresOperatorStore) createAgentEnrollmentRequest(ctx context.Context, request agentEnrollmentRequestInput) (agentEnrollmentRequestResult, int, error) {
	hostname := strings.TrimSpace(request.Hostname)
	enrollmentCode := strings.TrimSpace(request.EnrollmentCode)
	fingerprint := strings.ToLower(strings.TrimSpace(request.Fingerprint))
	if hostname == "" || enrollmentCode == "" || fingerprint == "" || len(request.AgentPublicKeyDER) < 10 || strings.TrimSpace(request.ClientNonceB64) == "" {
		return agentEnrollmentRequestResult{}, http.StatusBadRequest, errors.New("invalid_request")
	}
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return agentEnrollmentRequestResult{}, http.StatusInternalServerError, err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return agentEnrollmentRequestResult{}, http.StatusInternalServerError, err
	}
	defer tx.Rollback()

	site, found, err := loadEnrollmentSite(ctx, tx, enrollmentCode)
	if err != nil {
		return agentEnrollmentRequestResult{}, http.StatusInternalServerError, err
	}
	if !found {
		if err := recordWrongEnrollmentCodeAttempt(ctx, tx, hostname, fingerprint, enrollmentCode, request.RemoteAddr, now); err != nil {
			return agentEnrollmentRequestResult{}, http.StatusInternalServerError, err
		}
		if err := tx.Commit(); err != nil {
			return agentEnrollmentRequestResult{}, http.StatusInternalServerError, err
		}
		return agentEnrollmentRequestResult{}, http.StatusBadRequest, errors.New("invalid_enrollment_code")
	}
	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM engine.enrollment_code_failures WHERE ssl_key_fingerprint_claimed=$1`,
		fingerprint,
	); err != nil {
		return agentEnrollmentRequestResult{}, http.StatusInternalServerError, err
	}

	autoApproval, err := decideEnrollmentAutoApproval(ctx, tx, site, hostname, fingerprint, now)
	if err != nil {
		return agentEnrollmentRequestResult{}, http.StatusInternalServerError, err
	}
	approvalStatus := autoApproval.Status
	if approvalStatus == "" {
		approvalStatus = "pending"
	}

	recordID := ""
	approvalReference := ""
	err = tx.QueryRowContext(
		ctx,
		`
		SELECT id, approval_reference
		  FROM engine.device_approvals
		 WHERE ssl_key_fingerprint_claimed=$1
		   AND status='pending'
		 LIMIT 1
		`,
		fingerprint,
	).Scan(&recordID, &approvalReference)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return agentEnrollmentRequestResult{}, http.StatusInternalServerError, err
	}
	serverNonce, err := randomBytes(32)
	if err != nil {
		return agentEnrollmentRequestResult{}, http.StatusInternalServerError, err
	}
	serverNonceB64 := base64.StdEncoding.EncodeToString(serverNonce)
	nowISO := isoUTC(now)
	var reuseGUID any
	if autoApproval.GUID != "" {
		reuseGUID = autoApproval.GUID
	}
	var approvedBy any
	if approvalStatus == "approved" {
		approvedBy = "site_auto_approval"
	}
	var jobID any
	if request.OnboardingContext.JobID != nil {
		jobID = *request.OnboardingContext.JobID
	}
	var runID any
	if request.OnboardingContext.RunID != nil {
		runID = *request.OnboardingContext.RunID
	}
	var onboardingTarget any
	if request.OnboardingContext.Target != "" {
		onboardingTarget = request.OnboardingContext.Target
	}

	if recordID != "" {
		_, err = tx.ExecContext(
			ctx,
			`
			UPDATE engine.device_approvals
			   SET hostname_claimed=$1,
			       guid=$2,
			       enrollment_code=$3,
			       site_id=$4,
			       client_nonce=$5,
			       server_nonce=$6,
			       agent_pubkey_der=$7,
			       onboarding_job_id=$8,
			       onboarding_run_id=$9,
			       onboarding_target=$10,
			       status=$11,
			       approved_by_user_id=$12,
			       updated_at=$13
			 WHERE id=$14
			`,
			hostname,
			reuseGUID,
			site.EnrollmentCode,
			site.ID,
			request.ClientNonceB64,
			serverNonceB64,
			request.AgentPublicKeyDER,
			jobID,
			runID,
			onboardingTarget,
			approvalStatus,
			approvedBy,
			nowISO,
			recordID,
		)
	} else {
		recordID, err = newUUID()
		if err != nil {
			return agentEnrollmentRequestResult{}, http.StatusInternalServerError, err
		}
		approvalReference, err = newUUID()
		if err != nil {
			return agentEnrollmentRequestResult{}, http.StatusInternalServerError, err
		}
		_, err = tx.ExecContext(
			ctx,
			`
			INSERT INTO engine.device_approvals (
			    id, approval_reference, guid, hostname_claimed,
			    ssl_key_fingerprint_claimed, enrollment_code, site_id,
			    status, client_nonce, server_nonce, agent_pubkey_der,
			    created_at, updated_at, approved_by_user_id, onboarding_job_id, onboarding_run_id,
			    onboarding_target
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
			`,
			recordID,
			approvalReference,
			reuseGUID,
			hostname,
			fingerprint,
			site.EnrollmentCode,
			site.ID,
			approvalStatus,
			request.ClientNonceB64,
			serverNonceB64,
			request.AgentPublicKeyDER,
			nowISO,
			nowISO,
			approvedBy,
			jobID,
			runID,
			onboardingTarget,
		)
	}
	if err != nil {
		return agentEnrollmentRequestResult{}, http.StatusInternalServerError, err
	}
	if err := tx.Commit(); err != nil {
		return agentEnrollmentRequestResult{}, http.StatusInternalServerError, err
	}

	pollAfter := 3000
	if approvalStatus == "approved" {
		pollAfter = 250
	}
	return agentEnrollmentRequestResult{
		Status:            approvalStatus,
		ApprovalReference: approvalReference,
		ServerNonceB64:    serverNonceB64,
		PollAfterMS:       pollAfter,
		AutoApproved:      approvalStatus == "approved",
	}, http.StatusOK, nil
}

func (s *postgresOperatorStore) finalizeAgentEnrollment(ctx context.Context, request agentEnrollmentFinalizeInput) (agentEnrollmentFinalizeResult, int, error) {
	approvalReference := strings.TrimSpace(request.ApprovalReference)
	clientNonceB64 := strings.TrimSpace(request.ClientNonceB64)
	if approvalReference == "" || clientNonceB64 == "" || len(request.ClientNonce) == 0 || len(request.ProofSignature) == 0 {
		return agentEnrollmentFinalizeResult{}, http.StatusBadRequest, errors.New("invalid_request")
	}
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return agentEnrollmentFinalizeResult{}, http.StatusInternalServerError, err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return agentEnrollmentFinalizeResult{}, http.StatusInternalServerError, err
	}
	defer tx.Rollback()

	approval, found, err := loadEnrollmentApproval(ctx, tx, approvalReference)
	if err != nil {
		return agentEnrollmentFinalizeResult{}, http.StatusInternalServerError, err
	}
	if !found {
		return agentEnrollmentFinalizeResult{Status: "unknown"}, http.StatusNotFound, nil
	}
	if strings.TrimSpace(approval.ClientNonceB64) != clientNonceB64 {
		return agentEnrollmentFinalizeResult{}, http.StatusBadRequest, errors.New("nonce_mismatch")
	}
	serverNonce, err := decodeStandardBase64(approval.ServerNonceB64)
	if err != nil {
		return agentEnrollmentFinalizeResult{}, http.StatusBadRequest, errors.New("server_nonce_invalid")
	}
	publicKeyAny, err := x509.ParsePKIXPublicKey(approval.AgentPublicKeyDER)
	if err != nil {
		return agentEnrollmentFinalizeResult{}, http.StatusBadRequest, errors.New("agent_pubkey_invalid")
	}
	publicKey, ok := publicKeyAny.(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return agentEnrollmentFinalizeResult{}, http.StatusBadRequest, errors.New("agent_pubkey_invalid")
	}
	message := make([]byte, 0, len(serverNonce)+len(approvalReference)+len(request.ClientNonce))
	message = append(message, serverNonce...)
	message = append(message, []byte(approvalReference)...)
	message = append(message, request.ClientNonce...)
	if !ed25519.Verify(publicKey, message, request.ProofSignature) {
		return agentEnrollmentFinalizeResult{}, http.StatusBadRequest, errors.New("invalid_proof")
	}

	switch strings.ToLower(strings.TrimSpace(approval.Status)) {
	case "pending":
		return agentEnrollmentFinalizeResult{Status: "pending", PollAfterMS: 5000}, http.StatusOK, nil
	case "denied":
		return agentEnrollmentFinalizeResult{Status: "denied", Reason: "operator_denied"}, http.StatusOK, nil
	case "expired":
		return agentEnrollmentFinalizeResult{Status: "expired"}, http.StatusOK, nil
	case "completed":
		return agentEnrollmentFinalizeResult{Status: "approved", Detail: "finalized"}, http.StatusOK, nil
	case "approved":
	default:
		status := strings.TrimSpace(approval.Status)
		if status == "" {
			status = "unknown"
		}
		return agentEnrollmentFinalizeResult{Status: status}, http.StatusBadRequest, nil
	}
	if request.ConsumeReplay != nil {
		nonceKey := approvalReference + ":" + base64.StdEncoding.EncodeToString(request.ProofSignature)
		if !request.ConsumeReplay(nonceKey, now) {
			return agentEnrollmentFinalizeResult{}, http.StatusConflict, errors.New("proof_replayed")
		}
	}

	effectiveGUID := normalizeCanonicalGUID(approval.GUID)
	if effectiveGUID == "" {
		effectiveGUID, err = newUUID()
		if err != nil {
			return agentEnrollmentFinalizeResult{}, http.StatusInternalServerError, err
		}
	}
	device, err := ensureEnrollmentDeviceRecord(ctx, tx, effectiveGUID, approval.Hostname, approval.Fingerprint, now)
	if err != nil {
		return agentEnrollmentFinalizeResult{}, http.StatusInternalServerError, err
	}
	if err := storeEnrollmentDeviceKey(ctx, tx, effectiveGUID, approval.Fingerprint, now); err != nil {
		return agentEnrollmentFinalizeResult{}, http.StatusInternalServerError, err
	}

	var siteID *int64
	remoteOpsReason := "site_worker_unavailable"
	var route *agentWorkerRoute
	if approval.SiteID.Valid {
		siteValue := approval.SiteID.Int64
		siteID = &siteValue
		_, err = tx.ExecContext(
			ctx,
			`
			INSERT INTO engine.device_sites(device_hostname, site_id, assigned_at)
			VALUES ($1, $2, $3)
			ON CONFLICT(device_hostname)
			DO UPDATE SET site_id=excluded.site_id, assigned_at=excluded.assigned_at
			`,
			device.Hostname,
			siteValue,
			now.Unix(),
		)
		if err != nil {
			return agentEnrollmentFinalizeResult{}, http.StatusInternalServerError, err
		}
		route, err = fetchAgentWorkerRouteTx(ctx, tx, siteValue)
		if err != nil {
			return agentEnrollmentFinalizeResult{}, http.StatusInternalServerError, err
		}
	} else {
		remoteOpsReason = "device_site_unassigned"
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE engine.device_approvals SET guid=$1, status='completed', updated_at=$2 WHERE id=$3`,
		effectiveGUID,
		isoUTC(now),
		approval.ID,
	); err != nil {
		return agentEnrollmentFinalizeResult{}, http.StatusInternalServerError, err
	}
	refreshToken, err := issueEnrollmentRefreshToken(ctx, tx, effectiveGUID, now)
	if err != nil {
		return agentEnrollmentFinalizeResult{}, http.StatusInternalServerError, err
	}
	if err := clearEnrollmentPurgeBarrier(ctx, tx, effectiveGUID); err != nil {
		return agentEnrollmentFinalizeResult{}, http.StatusInternalServerError, err
	}
	if err := tx.Commit(); err != nil {
		return agentEnrollmentFinalizeResult{}, http.StatusInternalServerError, err
	}

	return agentEnrollmentFinalizeResult{
		Status:           "approved",
		GUID:             effectiveGUID,
		Fingerprint:      strings.ToLower(strings.TrimSpace(approval.Fingerprint)),
		TokenVersion:     device.TokenVersion,
		RefreshToken:     refreshToken,
		SiteID:           siteID,
		Route:            route,
		RemoteOpsReason:  remoteOpsReason,
		ServerNonce:      serverNonce,
		ApprovalHostname: approval.Hostname,
	}, http.StatusOK, nil
}

type enrollmentSiteRecord struct {
	ID               int64
	Name             string
	EnrollmentCode   string
	AutoApproveUntil sql.NullInt64
}

type enrollmentAutoApproval struct {
	Status string
	GUID   string
	Reason string
}

type enrollmentApprovalRecord struct {
	ID                string
	GUID              string
	Hostname          string
	Fingerprint       string
	EnrollmentCode    string
	SiteID            sql.NullInt64
	Status            string
	ClientNonceB64    string
	ServerNonceB64    string
	AgentPublicKeyDER []byte
}

type enrollmentDeviceRecord struct {
	GUID         string
	Hostname     string
	TokenVersion int
}

func loadEnrollmentSite(ctx context.Context, tx *sql.Tx, enrollmentCode string) (enrollmentSiteRecord, bool, error) {
	var site enrollmentSiteRecord
	err := tx.QueryRowContext(
		ctx,
		`
		SELECT id, COALESCE(name, ''), COALESCE(enrollment_code, ''), auto_approve_until
		  FROM engine.sites
		 WHERE UPPER(enrollment_code)=UPPER($1)
		 LIMIT 1
		`,
		enrollmentCode,
	).Scan(&site.ID, &site.Name, &site.EnrollmentCode, &site.AutoApproveUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return enrollmentSiteRecord{}, false, nil
	}
	if err != nil {
		return enrollmentSiteRecord{}, false, err
	}
	return site, true, nil
}

func decideEnrollmentAutoApproval(ctx context.Context, tx *sql.Tx, site enrollmentSiteRecord, hostname string, fingerprint string, now time.Time) (enrollmentAutoApproval, error) {
	if !site.AutoApproveUntil.Valid || site.AutoApproveUntil.Int64 <= now.Unix() {
		return enrollmentAutoApproval{Status: "pending", Reason: "inactive"}, nil
	}
	var existingGUID, existingFingerprint sql.NullString
	err := tx.QueryRowContext(
		ctx,
		`
		SELECT guid, ssl_key_fingerprint
		  FROM engine.devices
		 WHERE LOWER(hostname)=LOWER($1)
		 LIMIT 1
		`,
		hostname,
	).Scan(&existingGUID, &existingFingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return enrollmentAutoApproval{Status: "approved", Reason: "site_auto_approval"}, nil
	}
	if err != nil {
		return enrollmentAutoApproval{}, err
	}
	guid := normalizeCanonicalGUID(existingGUID.String)
	if guid != "" && strings.ToLower(strings.TrimSpace(existingFingerprint.String)) == strings.ToLower(strings.TrimSpace(fingerprint)) {
		return enrollmentAutoApproval{Status: "approved", GUID: guid, Reason: "site_auto_approval_fingerprint_match"}, nil
	}
	return enrollmentAutoApproval{Status: "pending", Reason: "hostname_conflict"}, nil
}

func recordWrongEnrollmentCodeAttempt(ctx context.Context, tx *sql.Tx, hostname string, fingerprint string, enrollmentCode string, remoteAddr string, now time.Time) error {
	cutoff := isoUTC(now.Add(-24 * time.Hour))
	if _, err := tx.ExecContext(ctx, `DELETE FROM engine.enrollment_code_failures WHERE last_seen_at < $1`, cutoff); err != nil {
		return err
	}
	nowISO := isoUTC(now)
	var id string
	var attemptCount int
	err := tx.QueryRowContext(
		ctx,
		`SELECT id, COALESCE(attempt_count, 0) FROM engine.enrollment_code_failures WHERE ssl_key_fingerprint_claimed=$1`,
		fingerprint,
	).Scan(&id, &attemptCount)
	if err == nil {
		_, err = tx.ExecContext(
			ctx,
			`
			UPDATE engine.enrollment_code_failures
			   SET hostname_claimed=$1,
			       enrollment_code_mask=$2,
			       remote_addr=$3,
			       last_seen_at=$4,
			       attempt_count=$5,
			       last_error=$6
			 WHERE id=$7
			`,
			hostname,
			maskEnrollmentCode(enrollmentCode),
			remoteAddr,
			nowISO,
			attemptCount+1,
			"invalid_enrollment_code",
			id,
		)
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	newID, err := newUUID()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(
		ctx,
		`
		INSERT INTO engine.enrollment_code_failures (
		    id, hostname_claimed, ssl_key_fingerprint_claimed, enrollment_code_mask,
		    remote_addr, first_seen_at, last_seen_at, attempt_count, last_error
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`,
		newID,
		hostname,
		fingerprint,
		maskEnrollmentCode(enrollmentCode),
		remoteAddr,
		nowISO,
		nowISO,
		1,
		"invalid_enrollment_code",
	)
	return err
}

func loadEnrollmentApproval(ctx context.Context, tx *sql.Tx, approvalReference string) (enrollmentApprovalRecord, bool, error) {
	var record enrollmentApprovalRecord
	var guid sql.NullString
	err := tx.QueryRowContext(
		ctx,
		`
		SELECT id, guid, hostname_claimed, ssl_key_fingerprint_claimed,
		       enrollment_code, site_id, status, client_nonce, server_nonce,
		       agent_pubkey_der
		  FROM engine.device_approvals
		 WHERE approval_reference=$1
		 LIMIT 1
		`,
		approvalReference,
	).Scan(
		&record.ID,
		&guid,
		&record.Hostname,
		&record.Fingerprint,
		&record.EnrollmentCode,
		&record.SiteID,
		&record.Status,
		&record.ClientNonceB64,
		&record.ServerNonceB64,
		&record.AgentPublicKeyDER,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return enrollmentApprovalRecord{}, false, nil
	}
	if err != nil {
		return enrollmentApprovalRecord{}, false, err
	}
	record.GUID = normalizeCanonicalGUID(guid.String)
	record.Fingerprint = strings.ToLower(strings.TrimSpace(record.Fingerprint))
	return record, true, nil
}

func ensureEnrollmentDeviceRecord(ctx context.Context, tx *sql.Tx, guid string, hostname string, fingerprint string, now time.Time) (enrollmentDeviceRecord, error) {
	guid = normalizeCanonicalGUID(guid)
	hostname = strings.TrimSpace(hostname)
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	if guid == "" {
		return enrollmentDeviceRecord{}, errors.New("valid_guid_required")
	}
	if hostname == "" {
		hostname = guid
	}
	if len(hostname) > 253 {
		hostname = hostname[:253]
	}
	requiredVersion, err := requiredEnrollmentTokenVersion(ctx, tx, guid)
	if err != nil {
		return enrollmentDeviceRecord{}, err
	}
	if requiredVersion < 1 {
		requiredVersion = 1
	}

	var rowGUID, rowHostname, rowFingerprint, rowStatus, rowKeyAddedAt sql.NullString
	var tokenVersion sql.NullInt64
	var lastEnrollment sql.NullInt64
	err = tx.QueryRowContext(
		ctx,
		`
		SELECT guid, hostname, token_version, status, ssl_key_fingerprint, key_added_at, last_enrollment_at
		  FROM engine.devices
		 WHERE UPPER(guid)=UPPER($1)
		 LIMIT 1
		`,
		guid,
	).Scan(&rowGUID, &rowHostname, &tokenVersion, &rowStatus, &rowFingerprint, &rowKeyAddedAt, &lastEnrollment)
	nowISO := isoUTC(now)
	nowUnix := now.Unix()
	if err == nil {
		storedFingerprint := strings.ToLower(strings.TrimSpace(rowFingerprint.String))
		currentVersion := int(tokenVersion.Int64)
		if currentVersion < 1 {
			currentVersion = 1
		}
		effectiveCurrent := maxEnrollmentInt(currentVersion, requiredVersion)
		switch {
		case storedFingerprint == "" && fingerprint != "":
			_, err = tx.ExecContext(
				ctx,
				`
				UPDATE engine.devices
				   SET ssl_key_fingerprint=$1,
				       key_added_at=$2,
				       last_enrollment_at=$3,
				       token_version=$4,
				       status='active'
				 WHERE guid=$5
				`,
				fingerprint,
				nowISO,
				nowUnix,
				effectiveCurrent,
				rowGUID.String,
			)
			currentVersion = effectiveCurrent
		case fingerprint != "" && storedFingerprint != fingerprint:
			newVersion := maxEnrollmentInt(effectiveCurrent+1, requiredVersion, 1)
			_, err = tx.ExecContext(
				ctx,
				`
				UPDATE engine.devices
				   SET ssl_key_fingerprint=$1,
				       key_added_at=$2,
				       last_enrollment_at=$3,
				       token_version=$4,
				       status='active'
				 WHERE guid=$5
				`,
				fingerprint,
				nowISO,
				nowUnix,
				newVersion,
				rowGUID.String,
			)
			if err != nil {
				return enrollmentDeviceRecord{}, err
			}
			_, err = tx.ExecContext(
				ctx,
				`UPDATE engine.refresh_tokens SET revoked_at=$1 WHERE guid=$2 AND revoked_at IS NULL`,
				nowISO,
				rowGUID.String,
			)
			currentVersion = newVersion
		default:
			if currentVersion != effectiveCurrent {
				_, err = tx.ExecContext(
					ctx,
					`UPDATE engine.devices SET last_enrollment_at=$1, token_version=$2, status='active' WHERE guid=$3`,
					nowUnix,
					effectiveCurrent,
					rowGUID.String,
				)
				currentVersion = effectiveCurrent
			} else {
				_, err = tx.ExecContext(
					ctx,
					`UPDATE engine.devices SET last_enrollment_at=$1, status='active' WHERE guid=$2`,
					nowUnix,
					rowGUID.String,
				)
			}
		}
		if err != nil {
			return enrollmentDeviceRecord{}, err
		}
		return enrollmentDeviceRecord{GUID: normalizeCanonicalGUID(rowGUID.String), Hostname: rowHostname.String, TokenVersion: currentVersion}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return enrollmentDeviceRecord{}, err
	}

	resolvedHostname, err := normalizeEnrollmentHostname(ctx, tx, hostname, guid)
	if err != nil {
		return enrollmentDeviceRecord{}, err
	}
	_, err = tx.ExecContext(
		ctx,
		`
		INSERT INTO engine.devices (
		    guid, hostname, created_at, last_enrollment_at, last_seen, ssl_key_fingerprint,
		    token_version, status, key_added_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'active', $8)
		`,
		guid,
		resolvedHostname,
		nowUnix,
		nowUnix,
		nowUnix,
		fingerprint,
		requiredVersion,
		nowISO,
	)
	if err != nil {
		return enrollmentDeviceRecord{}, err
	}
	return enrollmentDeviceRecord{GUID: guid, Hostname: resolvedHostname, TokenVersion: requiredVersion}, nil
}

func normalizeEnrollmentHostname(ctx context.Context, tx *sql.Tx, hostname string, guid string) (string, error) {
	guid = normalizeCanonicalGUID(guid)
	base := strings.TrimSpace(hostname)
	if base == "" {
		base = guid
	}
	if len(base) > 253 {
		base = base[:253]
	}
	candidate := base
	for suffix := 1; suffix <= 50; suffix++ {
		var existingGUID string
		err := tx.QueryRowContext(ctx, `SELECT guid FROM engine.devices WHERE hostname=$1 LIMIT 1`, candidate).Scan(&existingGUID)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		if normalizeCanonicalGUID(existingGUID) == guid {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, suffix)
	}
	return guid, nil
}

func storeEnrollmentDeviceKey(ctx context.Context, tx *sql.Tx, guid string, fingerprint string, now time.Time) error {
	id, err := newUUID()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(
		ctx,
		`
		INSERT INTO engine.device_keys (id, guid, ssl_key_fingerprint, added_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT(guid, ssl_key_fingerprint) DO NOTHING
		`,
		id,
		normalizeCanonicalGUID(guid),
		strings.ToLower(strings.TrimSpace(fingerprint)),
		isoUTC(now),
	)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(
		ctx,
		`
		UPDATE engine.device_keys
		   SET retired_at=$1
		 WHERE guid=$2
		   AND ssl_key_fingerprint<>$3
		   AND retired_at IS NULL
		`,
		isoUTC(now),
		normalizeCanonicalGUID(guid),
		strings.ToLower(strings.TrimSpace(fingerprint)),
	)
	return err
}

func requiredEnrollmentTokenVersion(ctx context.Context, tx *sql.Tx, guid string) (int, error) {
	exists, err := relationExistsTx(ctx, tx, "engine.device_purge_barriers")
	if err != nil || !exists {
		return 1, err
	}
	var required sql.NullInt64
	err = tx.QueryRowContext(
		ctx,
		`SELECT required_token_version FROM engine.device_purge_barriers WHERE UPPER(guid)=UPPER($1) LIMIT 1`,
		guid,
	).Scan(&required)
	if errors.Is(err, sql.ErrNoRows) {
		return 1, nil
	}
	if err != nil {
		return 1, err
	}
	if required.Valid && required.Int64 > 1 {
		return int(required.Int64), nil
	}
	return 1, nil
}

func clearEnrollmentPurgeBarrier(ctx context.Context, tx *sql.Tx, guid string) error {
	exists, err := relationExistsTx(ctx, tx, "engine.device_purge_barriers")
	if err != nil || !exists {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM engine.device_purge_barriers WHERE UPPER(guid)=UPPER($1)`, guid)
	return err
}

func issueEnrollmentRefreshToken(ctx context.Context, tx *sql.Tx, guid string, now time.Time) (string, error) {
	id, err := newUUID()
	if err != nil {
		return "", err
	}
	token, err := randomTokenURLSafe(48)
	if err != nil {
		return "", err
	}
	expiresAt := now.UTC().Add(refreshTokenSlidingTTL)
	_, err = tx.ExecContext(
		ctx,
		`
		INSERT INTO engine.refresh_tokens (id, guid, token_hash, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		`,
		id,
		normalizeCanonicalGUID(guid),
		hashRefreshToken(token),
		isoUTC(now),
		isoUTC(expiresAt),
	)
	if err != nil {
		return "", err
	}
	return token, nil
}

func fetchAgentWorkerRouteTx(ctx context.Context, tx *sql.Tx, siteID int64) (*agentWorkerRoute, error) {
	var route agentWorkerRoute
	err := tx.QueryRowContext(
		ctx,
		`
		SELECT worker_guid, site_id, route_path_prefix, upstream_scheme, upstream_host, upstream_port, generation
		  FROM engine.job_scheduler_worker_routes
		 WHERE site_id=$1
		   AND status='active'
		 ORDER BY updated_at DESC, generation DESC
		 LIMIT 1
		`,
		siteID,
	).Scan(&route.WorkerGUID, &route.SiteID, &route.RoutePathPrefix, &route.UpstreamScheme, &route.UpstreamHost, &route.UpstreamPort, &route.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &route, nil
}

func relationExistsTx(ctx context.Context, tx *sql.Tx, name string) (bool, error) {
	var relation sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass($1)`, strings.TrimSpace(name)).Scan(&relation); err != nil {
		return false, err
	}
	return relation.Valid && strings.TrimSpace(relation.String) != "", nil
}

func parseEnrollmentOnboardingContext(value any) enrollmentOnboardingContext {
	raw, ok := value.(map[string]any)
	if !ok {
		return enrollmentOnboardingContext{}
	}
	var context enrollmentOnboardingContext
	if jobID := positiveInt64(raw["job_id"]); jobID > 0 {
		context.JobID = &jobID
	}
	if runID := positiveInt64(raw["run_id"]); runID > 0 {
		context.RunID = &runID
	}
	target := cleanText(raw["target"])
	if target != "" {
		if len(target) > 253 {
			target = target[:253]
		}
		context.Target = target
	}
	return context
}

func positiveInt64(value any) int64 {
	switch typed := value.(type) {
	case float64:
		if typed > 0 {
			return int64(typed)
		}
	case int64:
		if typed > 0 {
			return typed
		}
	case int:
		if typed > 0 {
			return int64(typed)
		}
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if parsed > 0 {
			return parsed
		}
	}
	return 0
}

func decodeStandardBase64(value string) ([]byte, error) {
	cleaned := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		if r == '-' {
			return '+'
		}
		if r == '_' {
			return '/'
		}
		return r
	}, strings.TrimSpace(value))
	return base64.StdEncoding.DecodeString(cleaned)
}

func fingerprintFromSPKIDER(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

func requestRemoteAddr(r *http.Request) string {
	forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	remote := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	if remote == "" {
		return "unknown"
	}
	return remote
}

func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(retryAfter.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", fmt.Sprintf("%d", seconds))
	writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"error":       "rate_limited",
		"retry_after": retryAfter.Seconds(),
	})
}

func (l *enrollmentRateLimiter) check(key string, limit int, window time.Duration, now time.Time) (time.Duration, bool) {
	if l == nil || limit <= 0 || window <= 0 {
		return 0, false
	}
	if l.nowFunc != nil {
		now = l.nowFunc().UTC()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.events == nil {
		l.events = map[string][]time.Time{}
	}
	cutoff := now.Add(-window)
	events := l.events[key]
	filtered := events[:0]
	for _, event := range events {
		if event.After(cutoff) {
			filtered = append(filtered, event)
		}
	}
	if len(filtered) >= limit {
		retry := filtered[0].Add(window).Sub(now)
		if retry < time.Second {
			retry = time.Second
		}
		l.events[key] = filtered
		return retry, true
	}
	filtered = append(filtered, now)
	l.events[key] = filtered
	return 0, false
}

func (c *enrollmentReplayCache) consume(key string, now time.Time) bool {
	if c == nil {
		return false
	}
	if c.ttl <= 0 {
		c.ttl = agentEnrollmentProofTTL
	}
	if c.nowFunc != nil {
		now = c.nowFunc().UTC()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = map[string]time.Time{}
	}
	if expiry, ok := c.seen[key]; ok && expiry.After(now) {
		return false
	}
	c.seen[key] = now.Add(c.ttl)
	for nonce, expiry := range c.seen {
		if !expiry.After(now) {
			delete(c.seen, nonce)
		}
	}
	return true
}

func maskEnrollmentCode(code string) string {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return "<missing>"
	}
	if len(trimmed) <= 6 {
		return "***"
	}
	return trimmed[:3] + "***" + trimmed[len(trimmed)-3:]
}

func scriptSigningKeyB64(signer *agentScriptSigner) string {
	if signer == nil {
		return ""
	}
	return strings.TrimSpace(signer.publicB64)
}

func loadOrCreateScriptSigner() (*agentScriptSigner, error) {
	keyPath, err := scriptSigningKeyPath()
	if err != nil {
		return nil, err
	}
	if existing, err := os.ReadFile(keyPath); err == nil && len(existing) > 0 {
		block, _ := pem.Decode(existing)
		if block == nil {
			return nil, fmt.Errorf("invalid PEM in %s", keyPath)
		}
		keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		key, ok := keyAny.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("script signing key is %T, expected Ed25519 private key", keyAny)
		}
		return newAgentScriptSignerFromKey(key)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(keyPath, pemBytes, 0o600); err != nil {
		return nil, err
	}
	signer, err := newAgentScriptSignerFromKey(privateKey)
	if err != nil {
		return nil, err
	}
	_ = os.WriteFile(filepath.Join(filepath.Dir(keyPath), scriptSigningPubFilename), signerPublicDER(signer), 0o600)
	return signer, nil
}

func scriptSigningKeyPath() (string, error) {
	certRoot := strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_CERT_ROOT"))
	if certRoot == "" {
		engineRoot := strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_ROOT"))
		if engineRoot == "" {
			engineRoot = strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_RUNTIME"))
		}
		if engineRoot == "" {
			engineRoot = "/opt/Borealis/Engine"
		}
		certRoot = filepath.Join(engineRoot, "Services", "api-backend", "secrets", "Certificates")
	}
	absRoot, err := filepath.Abs(certRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(absRoot, "Code-Signing", scriptSigningKeyFilename), nil
}

func newAgentScriptSignerFromKey(privateKey ed25519.PrivateKey) (*agentScriptSigner, error) {
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("invalid Ed25519 public key")
	}
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	return &agentScriptSigner{
		privateKey: privateKey,
		publicB64:  base64.StdEncoding.EncodeToString(der),
	}, nil
}

func signerPublicDER(signer *agentScriptSigner) []byte {
	if signer == nil || len(signer.privateKey) == 0 {
		return nil
	}
	publicKey, ok := signer.privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return nil
	}
	der, _ := x509.MarshalPKIXPublicKey(publicKey)
	return der
}

func randomBytes(size int) ([]byte, error) {
	out := make([]byte, size)
	_, err := rand.Read(out)
	return out, err
}

func randomTokenURLSafe(size int) (string, error) {
	raw, err := randomBytes(size)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func newUUID() (string, error) {
	raw, err := randomBytes(16)
	if err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return strings.ToUpper(fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])), nil
}

func isoUTC(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func maxEnrollmentInt(values ...int) int {
	max := values[0]
	for _, value := range values[1:] {
		if value > max {
			max = value
		}
	}
	return max
}
