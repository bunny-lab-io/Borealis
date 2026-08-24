package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	clusterJSONMaxBytes      int64 = 64 << 10
	clusterInviteMaxBytes          = 16 << 10
	clusterReasonMaxLength         = 256
	clusterReleaseMaxLength        = 32
	clusterNodeNameMaxLength       = 63
	clusterHostnameMaxLength       = 253
	clusterStepUpMaxAge            = 5 * time.Minute
	clusterInviteTTL               = 15 * time.Minute
	clusterReleaseCacheTTL         = 15 * time.Minute
)

var (
	clusterUUIDRE    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	clusterReleaseRE = regexp.MustCompile(`^[0-9]{4}\.[0-9]{1,2}\.[0-9]+(?:\.[0-9]+)?$`)
	clusterRepoRE    = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	clusterK3sRE     = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+\+k3s[0-9]+$`)
	clusterUbuntuRE  = regexp.MustCompile(`^Ubuntu[[:space:]]+([0-9]+)\.([0-9]+)(?:\.[0-9]+)?(?:[[:space:]].*)?$`)

	errClusterConflict    = errors.New("cluster operation conflict")
	errClusterNotFound    = errors.New("cluster resource not found")
	errClusterUnavailable = errors.New("cluster control unavailable")
)

type clusterMutation struct {
	Kind          string
	TargetNodeID  string
	TargetRelease string
	TargetSHA     string
	Payload       map[string]any
}

type clusterStore interface {
	clusterSnapshot(ctx context.Context) (map[string]any, error)
	clusterEvents(ctx context.Context, afterID int64) ([]map[string]any, error)
	createClusterOperation(ctx context.Context, actor string, mutation clusterMutation) (map[string]any, error)
	createClusterInvitation(ctx context.Context, actor string, invitation map[string]any) error
	consumeClusterInvitation(ctx context.Context, admission map[string]any) (map[string]any, error)
	approveClusterAdmission(ctx context.Context, actor string, admissionID string) (map[string]any, error)
	retryClusterOperation(ctx context.Context, actor string, operationID string) (map[string]any, error)
	cancelClusterOperation(ctx context.Context, actor string, operationID string) (map[string]any, error)
}

type clusterReleaseManifest struct {
	SchemaVersion              int    `json:"schema_version"`
	ClusterCompatible          bool   `json:"cluster_compatible"`
	MinimumRollingVersion      string `json:"minimum_rolling_version"`
	MaximumVersionSkewReleases int    `json:"maximum_version_skew_releases"`
	DatabaseMigration          string `json:"database_migration"`
	RequiredK3sBaseline        string `json:"required_k3s_baseline"`
	RequiredK3sConformance     string `json:"required_k3s_probe_conformance"`
}

type clusterGitHubRelease struct {
	TagName         string `json:"tag_name"`
	Name            string `json:"name"`
	Draft           bool   `json:"draft"`
	Prerelease      bool   `json:"prerelease"`
	PublishedAt     string `json:"published_at"`
	TargetCommitish string `json:"target_commitish"`
}

type clusterReleaseCache struct {
	mu        sync.Mutex
	key       string
	expiresAt time.Time
	items     []map[string]any
}

var serverClusterReleaseCache clusterReleaseCache

type clusterGitHubTokenContextKey struct{}

func registerServerClusterRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("GET /api/server/cluster", clusterSnapshotHandler(auth))
	mux.HandleFunc("GET /api/server/cluster/banner", clusterBannerHandler(auth))
	mux.HandleFunc("GET /api/server/cluster/events", clusterEventsHandler(auth))
	mux.HandleFunc("GET /api/server/cluster/releases", clusterReleasesHandler(auth))
	mux.HandleFunc("POST /api/server/cluster/enable", clusterEnableHandler(auth))
	mux.HandleFunc("POST /api/server/cluster/invitations", clusterInvitationHandler(auth))
	mux.HandleFunc("POST /api/server/cluster/admissions/{id}/approve", clusterAdmissionApproveHandler(auth))
	mux.HandleFunc("POST /api/server/cluster/membership/scale", clusterScaleHandler(auth))
	mux.HandleFunc("POST /api/server/cluster/nodes/{id}/maintenance", clusterNodeMaintenanceHandler(auth))
	mux.HandleFunc("POST /api/server/cluster/nodes/{id}/remove", clusterNodeRemoveHandler(auth))
	mux.HandleFunc("POST /api/server/cluster/postgres/switchover", clusterPostgresOperationHandler(auth, "postgres_switchover"))
	mux.HandleFunc("POST /api/server/cluster/postgres/emergency-failover", clusterPostgresOperationHandler(auth, "postgres_emergency_failover"))
	mux.HandleFunc("POST /api/server/cluster/hmr/start", clusterHMRStartHandler(auth))
	mux.HandleFunc("POST /api/server/cluster/hmr/exit", clusterHMRExitHandler(auth))
	mux.HandleFunc("POST /api/server/cluster/updates", clusterUpdateHandler(auth))
	mux.HandleFunc("POST /api/server/cluster/operations/{id}/retry", clusterOperationRetryHandler(auth))
	mux.HandleFunc("POST /api/server/cluster/operations/{id}/cancel", clusterOperationCancelHandler(auth))
	mux.HandleFunc("POST /api/bootstrap/cluster/join", clusterJoinHandler(auth))
	mux.HandleFunc("GET /api/bootstrap/cluster/join/{id}/events", clusterJoinEventsHandler(auth))
}

func clusterBannerHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := auth.currentProfile(r.Context(), r); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		store, ok := auth.store.(clusterStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster_control_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		snapshot, err := store.clusterSnapshot(ctx)
		if err != nil {
			writeClusterError(w, err)
			return
		}
		hmr := mapStringAny(snapshot["hmr"])
		var activeOperation any
		for _, raw := range anySlice(snapshot["operations"]) {
			operation, _ := raw.(map[string]any)
			if textInSet(cleanText(operation["state"]), "queued", "running", "waiting", "failed") {
				activeOperation = map[string]any{"kind": operation["kind"], "state": operation["state"], "current_step": operation["current_step"]}
				break
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"enabled": snapshot["enabled"], "status": snapshot["status"], "hmr_state": hmr["state"], "active_operation": activeOperation})
	}
}

func clusterSnapshotHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		store, ok := auth.store.(clusterStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster_control_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		payload, err := store.clusterSnapshot(ctx)
		if err != nil {
			writeClusterError(w, err)
			return
		}
		payload["probe_conformance"] = clusterProbeConformancePayload()
		writeJSON(w, http.StatusOK, payload)
	}
}

func clusterEventsHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		afterID := int64(0)
		if raw := strings.TrimSpace(r.URL.Query().Get("after_id")); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || parsed < 0 {
				writePublicValidationErrors(w, []publicValidationError{{Field: "query.after_id", Message: "must be a non-negative integer"}})
				return
			}
			afterID = parsed
		}
		store, ok := auth.store.(clusterStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster_control_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		events, err := store.clusterEvents(ctx, afterID)
		if err != nil {
			writeClusterError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events})
	}
}

func clusterReleasesHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		ctx = clusterContextWithGitHubToken(ctx, auth)
		current := clusterCurrentRelease(auth, ctx)
		items, err := fetchClusterReleaseCatalog(ctx, current)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "release_catalog_unavailable", "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"current_release": current, "releases": items})
	}
}

func clusterEnableHandler(auth *authService) http.HandlerFunc {
	return clusterMutationHandler(auth, "cluster_enable", func(body map[string]any) (clusterMutation, []publicValidationError) {
		allowed := map[string]bool{"control_plane_vip": true, "edge_vip": true, "node_name": true, "management_ip": true, "architecture": true, "confirmation": true}
		errs := rejectUnknownClusterFields(body, allowed)
		controlVIP := cleanText(body["control_plane_vip"])
		edgeVIP := cleanText(body["edge_vip"])
		nodeName := cleanText(body["node_name"])
		managementIP := cleanText(body["management_ip"])
		architecture := strings.ToLower(cleanText(body["architecture"]))
		confirmation := cleanText(body["confirmation"])
		errs = append(errs, validateClusterIP("control_plane_vip", controlVIP)...)
		errs = append(errs, validateClusterIP("edge_vip", edgeVIP)...)
		errs = append(errs, validateClusterNodeName("node_name", nodeName)...)
		errs = append(errs, validateClusterIP("management_ip", managementIP)...)
		if architecture != "amd64" && architecture != "arm64" {
			errs = append(errs, publicValidationError{Field: "architecture", Message: "must be amd64 or arm64"})
		}
		if controlVIP != "" && controlVIP == edgeVIP {
			errs = append(errs, publicValidationError{Field: "edge_vip", Message: "must differ from control_plane_vip"})
		}
		if confirmation != "ENABLE CLUSTER" {
			errs = append(errs, publicValidationError{Field: "confirmation", Message: "must equal ENABLE CLUSTER"})
		}
		if !clusterProbeConformancePassed() {
			errs = append(errs, publicValidationError{Field: "probe_conformance", Message: "stable K3s probe conformance has not passed"})
		}
		baselineRelease := strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_RELEASE_VERSION"))
		baselineSHA := strings.ToLower(strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_SOURCE_SHA")))
		if !clusterReleaseRE.MatchString(baselineRelease) || !clusterControllerSHARegex.MatchString(baselineSHA) {
			errs = append(errs, publicValidationError{Field: "release", Message: "cluster enable requires a published dotted-numeric Engine release pinned to a commit SHA"})
		}
		return clusterMutation{Kind: "cluster_enable", Payload: map[string]any{"control_plane_vip": controlVIP, "edge_vip": edgeVIP, "node_name": nodeName, "management_ip": managementIP, "architecture": architecture, "baseline_release": baselineRelease, "baseline_sha": baselineSHA}}, errs
	})
}

func clusterScaleHandler(auth *authService) http.HandlerFunc {
	return clusterMutationHandler(auth, "membership_scale", func(body map[string]any) (clusterMutation, []publicValidationError) {
		errs := rejectUnknownClusterFields(body, map[string]bool{"desired_size": true, "reason": true})
		desired := int(coerceInt64(body["desired_size"]))
		if desired != 1 && desired != 3 && desired != 5 {
			errs = append(errs, publicValidationError{Field: "desired_size", Message: "must be 1, 3, or 5"})
		}
		reason := cleanText(body["reason"])
		errs = append(errs, validateClusterReason(reason)...)
		return clusterMutation{Kind: "membership_scale", Payload: map[string]any{"desired_size": desired, "reason": reason}}, errs
	})
}

func clusterNodeMaintenanceHandler(auth *authService) http.HandlerFunc {
	return clusterNodeMutationHandler(auth, "node_maintenance", func(body map[string]any) (map[string]any, []publicValidationError) {
		errs := rejectUnknownClusterFields(body, map[string]bool{"action": true, "reason": true})
		action := strings.ToLower(cleanText(body["action"]))
		if action != "enter" && action != "exit" {
			errs = append(errs, publicValidationError{Field: "action", Message: "must be enter or exit"})
		}
		reason := cleanText(body["reason"])
		errs = append(errs, validateClusterReason(reason)...)
		return map[string]any{"action": action, "reason": reason}, errs
	})
}

func clusterNodeRemoveHandler(auth *authService) http.HandlerFunc {
	return clusterNodeMutationHandler(auth, "node_remove", func(body map[string]any) (map[string]any, []publicValidationError) {
		errs := rejectUnknownClusterFields(body, map[string]bool{"emergency": true, "confirmation": true, "reason": true})
		emergency, _ := body["emergency"].(bool)
		confirmation := cleanText(body["confirmation"])
		required := "REMOVE NODE"
		if emergency {
			required = "EMERGENCY REMOVE NODE"
		}
		if confirmation != required {
			errs = append(errs, publicValidationError{Field: "confirmation", Message: "must equal " + required})
		}
		reason := cleanText(body["reason"])
		errs = append(errs, validateClusterReason(reason)...)
		return map[string]any{"emergency": emergency, "reason": reason}, errs
	})
}

func clusterPostgresOperationHandler(auth *authService, kind string) http.HandlerFunc {
	return clusterMutationHandler(auth, kind, func(body map[string]any) (clusterMutation, []publicValidationError) {
		errs := rejectUnknownClusterFields(body, map[string]bool{"target_node_id": true, "confirmation": true, "reason": true})
		target := strings.ToLower(cleanText(body["target_node_id"]))
		errs = append(errs, validateClusterUUID("target_node_id", target)...)
		if kind == "postgres_emergency_failover" && cleanText(body["confirmation"]) != "EMERGENCY FAILOVER" {
			errs = append(errs, publicValidationError{Field: "confirmation", Message: "must equal EMERGENCY FAILOVER"})
		}
		reason := cleanText(body["reason"])
		errs = append(errs, validateClusterReason(reason)...)
		return clusterMutation{Kind: kind, TargetNodeID: target, Payload: map[string]any{"reason": reason}}, errs
	})
}

func clusterHMRStartHandler(auth *authService) http.HandlerFunc {
	return clusterMutationHandler(auth, "hmr_start", func(body map[string]any) (clusterMutation, []publicValidationError) {
		errs := rejectUnknownClusterFields(body, map[string]bool{"node_id": true, "confirmation": true})
		nodeID := strings.ToLower(cleanText(body["node_id"]))
		errs = append(errs, validateClusterUUID("node_id", nodeID)...)
		if cleanText(body["confirmation"]) != "ENABLE HMR" {
			errs = append(errs, publicValidationError{Field: "confirmation", Message: "must equal ENABLE HMR"})
		}
		return clusterMutation{Kind: "hmr_start", TargetNodeID: nodeID, Payload: map[string]any{}}, errs
	})
}

func clusterHMRExitHandler(auth *authService) http.HandlerFunc {
	return clusterMutationHandler(auth, "hmr_exit", func(body map[string]any) (clusterMutation, []publicValidationError) {
		errs := rejectUnknownClusterFields(body, map[string]bool{"confirmation": true})
		if cleanText(body["confirmation"]) != "EXIT HMR" {
			errs = append(errs, publicValidationError{Field: "confirmation", Message: "must equal EXIT HMR"})
		}
		return clusterMutation{Kind: "hmr_exit", Payload: map[string]any{}}, errs
	})
}

func clusterUpdateHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, failure := requireRecentAdmin(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		body, err := readJSONMapWithLimit(r, clusterJSONMaxBytes)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		errs := rejectUnknownClusterFields(body, map[string]bool{"scope": true, "node_ids": true, "release_tag": true, "confirmation": true, "maintenance_outage_acknowledgement": true})
		scope := strings.ToLower(cleanText(body["scope"]))
		if scope != "node" && scope != "all" {
			errs = append(errs, publicValidationError{Field: "scope", Message: "must be node or all"})
		}
		releaseTag := cleanText(body["release_tag"])
		errs = append(errs, validateClusterRelease(releaseTag)...)
		nodeIDs, nodeErrs := clusterStringList(body["node_ids"], "node_ids", 5, validateClusterUUID)
		errs = append(errs, nodeErrs...)
		if scope == "node" && len(nodeIDs) != 1 {
			errs = append(errs, publicValidationError{Field: "node_ids", Message: "node scope requires exactly one node"})
		}
		if scope == "all" && len(nodeIDs) != 0 {
			errs = append(errs, publicValidationError{Field: "node_ids", Message: "all scope must not provide node_ids"})
		}
		if cleanText(body["confirmation"]) != "UPDATE CLUSTER" {
			errs = append(errs, publicValidationError{Field: "confirmation", Message: "must equal UPDATE CLUSTER"})
		}
		if len(errs) > 0 {
			writePublicValidationErrors(w, errs)
			return
		}
		store, ok := auth.store.(clusterStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster_control_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		snapshot, snapshotErr := store.clusterSnapshot(ctx)
		if snapshotErr != nil {
			writeClusterError(w, snapshotErr)
			return
		}
		outageAcknowledgement := cleanText(body["maintenance_outage_acknowledgement"])
		if coerceInt64(snapshot["active_size"]) == 1 && outageAcknowledgement != "ACCEPT OUTAGE" {
			writePublicValidationErrors(w, []publicValidationError{{Field: "maintenance_outage_acknowledgement", Message: "must equal ACCEPT OUTAGE for one-node clusters"}})
			return
		}
		if outageAcknowledgement != "" && outageAcknowledgement != "ACCEPT OUTAGE" {
			writePublicValidationErrors(w, []publicValidationError{{Field: "maintenance_outage_acknowledgement", Message: "must equal ACCEPT OUTAGE when supplied"}})
			return
		}
		ctx = clusterContextWithGitHubToken(ctx, auth)
		current := clusterCurrentRelease(auth, ctx)
		release, err := resolveClusterRelease(ctx, releaseTag, current)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "release_not_selectable", "message": err.Error()})
			return
		}
		payload := map[string]any{"scope": scope, "node_ids": nodeIDs, "release_title": release["title"], "source_release": current, "compatibility": release["compatibility"], "maintenance_outage_acknowledgement": outageAcknowledgement}
		targetNodeID := ""
		if scope == "node" {
			targetNodeID = nodeIDs[0]
		}
		result, err := store.createClusterOperation(ctx, identity.Username, clusterMutation{Kind: "engine_update", TargetNodeID: targetNodeID, TargetRelease: releaseTag, TargetSHA: cleanText(release["commit_sha"]), Payload: payload})
		if err != nil {
			writeClusterError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, result)
	}
}

func clusterMutationHandler(auth *authService, kind string, parse func(map[string]any) (clusterMutation, []publicValidationError)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, failure := requireRecentAdmin(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		body, err := readJSONMapWithLimit(r, clusterJSONMaxBytes)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		mutation, errs := parse(body)
		mutation.Kind = kind
		if len(errs) > 0 {
			writePublicValidationErrors(w, errs)
			return
		}
		store, ok := auth.store.(clusterStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster_control_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		result, err := store.createClusterOperation(ctx, identity.Username, mutation)
		if err != nil {
			writeClusterError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, result)
	}
}

func clusterNodeMutationHandler(auth *authService, kind string, parse func(map[string]any) (map[string]any, []publicValidationError)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, failure := requireRecentAdmin(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		nodeID := strings.ToLower(cleanText(r.PathValue("id")))
		if errs := validateClusterUUID("id", nodeID); len(errs) > 0 {
			writePublicValidationErrors(w, errs)
			return
		}
		body, err := readJSONMapWithLimit(r, clusterJSONMaxBytes)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		payload, errs := parse(body)
		if len(errs) > 0 {
			writePublicValidationErrors(w, errs)
			return
		}
		store, ok := auth.store.(clusterStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster_control_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		result, err := store.createClusterOperation(ctx, identity.Username, clusterMutation{Kind: kind, TargetNodeID: nodeID, Payload: payload})
		if err != nil {
			writeClusterError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, result)
	}
}

func clusterInvitationHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, failure := requireRecentAdmin(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		body, err := readJSONMapWithLimit(r, clusterJSONMaxBytes)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		errs := rejectUnknownClusterFields(body, map[string]bool{"node_name": true})
		nodeName := cleanText(body["node_name"])
		errs = append(errs, validateClusterNodeName("node_name", nodeName)...)
		if len(errs) > 0 {
			writePublicValidationErrors(w, errs)
			return
		}
		store, ok := auth.store.(clusterStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster_control_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		snapshot, err := store.clusterSnapshot(ctx)
		if err != nil {
			writeClusterError(w, err)
			return
		}
		if !coerceClusterBool(snapshot["enabled"]) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "cluster_not_enabled"})
			return
		}
		invitationID := newClusterUUID()
		token := newClusterSecret(32)
		expiresAt := time.Now().UTC().Add(clusterInviteTTL)
		claims := map[string]any{"type": "cluster-invite", "invitation_id": invitationID, "cluster_id": cleanText(snapshot["cluster_id"]), "node_name": nodeName, "endpoint": strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_BASE_URL")), "token": token, "expires_at": expiresAt.Unix()}
		bundle, err := auth.verifier.signPayload(claims)
		if err != nil || len(bundle) > clusterInviteMaxBytes {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "invitation_sign_failed"})
			return
		}
		invitation := map[string]any{"id": invitationID, "cluster_id": claims["cluster_id"], "node_name": nodeName, "token_hash": clusterTokenHash(token), "expires_at": expiresAt.Unix()}
		if err := store.createClusterInvitation(ctx, identity.Username, invitation); err != nil {
			writeClusterError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": invitationID, "node_name": nodeName, "expires_at": expiresAt.Format(time.RFC3339), "invite_bundle": bundle})
	}
}

func clusterJoinHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := readJSONMapWithLimit(r, clusterJSONMaxBytes)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		errs := rejectUnknownClusterFields(body, map[string]bool{"invite_bundle": true, "node_name": true, "hostname": true, "management_ip": true, "architecture": true, "os_version": true})
		bundle := cleanText(body["invite_bundle"])
		if bundle == "" || len(bundle) > clusterInviteMaxBytes {
			errs = append(errs, publicValidationError{Field: "invite_bundle", Message: "must be a non-empty authenticated bundle no larger than 16 KiB"})
		}
		nodeName := cleanText(body["node_name"])
		hostname := cleanText(body["hostname"])
		managementIP := cleanText(body["management_ip"])
		architecture := strings.ToLower(cleanText(body["architecture"]))
		osVersion := cleanText(body["os_version"])
		errs = append(errs, validateClusterNodeName("node_name", nodeName)...)
		if err := validateHostInput("hostname", hostname); err != nil || len(hostname) > clusterHostnameMaxLength {
			errs = append(errs, publicValidationError{Field: "hostname", Message: "must be a valid hostname no longer than 253 characters"})
		}
		errs = append(errs, validateClusterIP("management_ip", managementIP)...)
		if architecture != "amd64" && architecture != "arm64" {
			errs = append(errs, publicValidationError{Field: "architecture", Message: "must be amd64 or arm64"})
		}
		if !clusterSupportedUbuntu(osVersion) {
			errs = append(errs, publicValidationError{Field: "os_version", Message: "must identify Ubuntu 24.04 or newer"})
		}
		if len(errs) > 0 {
			writePublicValidationErrors(w, errs)
			return
		}
		claims, err := auth.verifier.signedPayload(bundle, clusterInviteTTL)
		if err != nil || cleanText(claims["type"]) != "cluster-invite" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_invite_bundle"})
			return
		}
		if expected := cleanText(claims["node_name"]); expected != "" && !strings.EqualFold(expected, nodeName) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "invite_node_mismatch"})
			return
		}
		store, ok := auth.store.(clusterStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster_control_unavailable"})
			return
		}
		admission := map[string]any{"id": newClusterUUID(), "invitation_id": cleanText(claims["invitation_id"]), "cluster_id": cleanText(claims["cluster_id"]), "token_hash": clusterTokenHash(cleanText(claims["token"])), "node_name": nodeName, "hostname": hostname, "management_ip": managementIP, "architecture": architecture, "os_version": osVersion}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		result, err := store.consumeClusterInvitation(ctx, admission)
		if err != nil {
			writeClusterError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, result)
	}
}

func clusterAdmissionApproveHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, failure := requireRecentAdmin(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		admissionID := strings.ToLower(cleanText(r.PathValue("id")))
		if errs := validateClusterUUID("id", admissionID); len(errs) > 0 {
			writePublicValidationErrors(w, errs)
			return
		}
		body, err := readJSONMapWithLimit(r, clusterJSONMaxBytes)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		errs := rejectUnknownClusterFields(body, map[string]bool{"confirmation": true})
		if cleanText(body["confirmation"]) != "APPROVE NODE" {
			errs = append(errs, publicValidationError{Field: "confirmation", Message: "must equal APPROVE NODE"})
		}
		if len(errs) > 0 {
			writePublicValidationErrors(w, errs)
			return
		}
		store, ok := auth.store.(clusterStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster_control_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		result, err := store.approveClusterAdmission(ctx, identity.Username, admissionID)
		if err != nil {
			writeClusterError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, result)
	}
}

func clusterJoinEventsHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		admissionID := strings.ToLower(cleanText(r.PathValue("id")))
		if errs := validateClusterUUID("id", admissionID); len(errs) > 0 {
			writePublicValidationErrors(w, errs)
			return
		}
		bundle := strings.TrimSpace(r.Header.Get("X-Borealis-Cluster-Invite"))
		claims, err := auth.verifier.signedPayload(bundle, clusterInviteTTL)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_invite_bundle"})
			return
		}
		store, ok := auth.store.(clusterStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster_control_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		events, err := store.clusterEvents(ctx, 0)
		if err != nil {
			writeClusterError(w, err)
			return
		}
		filtered := make([]map[string]any, 0)
		for _, event := range events {
			if cleanText(event["admission_id"]) == admissionID && cleanText(event["cluster_id"]) == cleanText(claims["cluster_id"]) {
				filtered = append(filtered, event)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"admission_id": admissionID, "events": filtered})
	}
}

func clusterOperationRetryHandler(auth *authService) http.HandlerFunc {
	return clusterOperationControlHandler(auth, "retry")
}

func clusterOperationCancelHandler(auth *authService) http.HandlerFunc {
	return clusterOperationControlHandler(auth, "cancel")
}

func clusterOperationControlHandler(auth *authService, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, failure := requireRecentAdmin(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		operationID := strings.ToLower(cleanText(r.PathValue("id")))
		if errs := validateClusterUUID("id", operationID); len(errs) > 0 {
			writePublicValidationErrors(w, errs)
			return
		}
		body, err := readJSONMapWithLimit(r, clusterJSONMaxBytes)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		errs := rejectUnknownClusterFields(body, map[string]bool{"confirmation": true})
		required := strings.ToUpper(action) + " OPERATION"
		if cleanText(body["confirmation"]) != required {
			errs = append(errs, publicValidationError{Field: "confirmation", Message: "must equal " + required})
		}
		if len(errs) > 0 {
			writePublicValidationErrors(w, errs)
			return
		}
		store, ok := auth.store.(clusterStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster_control_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		var result map[string]any
		if action == "retry" {
			result, err = store.retryClusterOperation(ctx, identity.Username, operationID)
		} else {
			result, err = store.cancelClusterOperation(ctx, identity.Username, operationID)
		}
		if err != nil {
			writeClusterError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, result)
	}
}

func requireRecentAdmin(ctx context.Context, auth *authService, r *http.Request) (operatorIdentity, *authFailure) {
	identity, failure := requireAdmin(ctx, auth, r)
	if failure != nil {
		return operatorIdentity{}, failure
	}
	token, err := extractAuthToken(r)
	if err != nil || auth == nil || auth.verifier == nil {
		return operatorIdentity{}, unauthorizedAuthFailure()
	}
	if _, err := auth.verifier.signedPayload(token, clusterStepUpMaxAge); err != nil {
		return operatorIdentity{}, &authFailure{status: http.StatusPreconditionRequired, body: map[string]any{"error": "step_up_required", "message": "Sign in again before changing cluster state."}}
	}
	return identity, nil
}

func clusterProbeConformancePassed() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("BOREALIS_K3S_PROBE_CONFORMANCE")), "passed")
}

func clusterProbeConformancePayload() map[string]any {
	status := firstText(strings.TrimSpace(os.Getenv("BOREALIS_K3S_PROBE_CONFORMANCE")), "not-run")
	return map[string]any{"id": "pod-restart-policy-startup-probe-v1", "status": status, "cluster_activation_allowed": status == "passed"}
}

func writeClusterError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errClusterConflict):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "cluster_operation_conflict", "message": err.Error()})
	case errors.Is(err, errClusterNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster_resource_not_found", "message": err.Error()})
	case errors.Is(err, errClusterUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster_control_unavailable", "message": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "cluster_control_failed", "message": err.Error()})
	}
}

func validateClusterUUID(field, value string) []publicValidationError {
	if !clusterUUIDRE.MatchString(strings.ToLower(strings.TrimSpace(value))) {
		return []publicValidationError{{Field: field, Message: "must be a canonical UUID"}}
	}
	return nil
}

func validateClusterRelease(value string) []publicValidationError {
	if len(value) > clusterReleaseMaxLength || !clusterReleaseRE.MatchString(value) {
		return []publicValidationError{{Field: "release_tag", Message: "must be a dotted numeric release no longer than 32 characters"}}
	}
	return nil
}

func validateClusterNodeName(field, value string) []publicValidationError {
	if value == "" || len(value) > clusterNodeNameMaxLength || validateSlugInput(field, strings.ToLower(value)) != nil {
		return []publicValidationError{{Field: field, Message: "must be a DNS-compatible node name no longer than 63 characters"}}
	}
	return nil
}

func validateClusterIP(field, value string) []publicValidationError {
	ip := net.ParseIP(value)
	if ip == nil || ip.To4() == nil || strings.Contains(value, ":") || ip.IsUnspecified() || ip.IsMulticast() {
		return []publicValidationError{{Field: field, Message: "must be a unicast IPv4 address"}}
	}
	return nil
}

func clusterSupportedUbuntu(value string) bool {
	matches := clusterUbuntuRE.FindStringSubmatch(strings.TrimSpace(value))
	if len(matches) != 3 {
		return false
	}
	major, majorErr := strconv.Atoi(matches[1])
	minor, minorErr := strconv.Atoi(matches[2])
	return majorErr == nil && minorErr == nil && (major > 24 || major == 24 && minor >= 4)
}

func validateClusterReason(value string) []publicValidationError {
	if err := validateInputUTF8AndControls("reason", value, clusterReasonMaxLength, false); err != nil {
		return []publicValidationError{{Field: "reason", Message: err.Error()}}
	}
	return nil
}

func rejectUnknownClusterFields(body map[string]any, allowed map[string]bool) []publicValidationError {
	errs := make([]publicValidationError, 0)
	for key := range body {
		if !allowed[key] {
			errs = append(errs, publicValidationError{Field: key, Message: "field is not allowed"})
		}
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].Field < errs[j].Field })
	return errs
}

func clusterStringList(value any, field string, maximum int, validate func(string, string) []publicValidationError) ([]string, []publicValidationError) {
	if value == nil {
		return []string{}, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, []publicValidationError{{Field: field, Message: "must be an array"}}
	}
	if len(items) > maximum {
		return nil, []publicValidationError{{Field: field, Message: fmt.Sprintf("must contain no more than %d items", maximum)}}
	}
	out := make([]string, 0, len(items))
	errs := make([]publicValidationError, 0)
	seen := map[string]bool{}
	for index, item := range items {
		text := strings.ToLower(cleanText(item))
		itemField := fmt.Sprintf("%s[%d]", field, index)
		errs = append(errs, validate(itemField, text)...)
		if !seen[text] {
			seen[text] = true
			out = append(out, text)
		}
	}
	return out, errs
}

func newClusterUUID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func newClusterSecret(size int) string {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func clusterTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func clusterCurrentRelease(auth *authService, ctx context.Context) string {
	if auth != nil {
		if store, ok := auth.store.(clusterStore); ok {
			if payload, err := store.clusterSnapshot(ctx); err == nil {
				if current := cleanText(payload["baseline_release"]); clusterReleaseRE.MatchString(current) {
					return current
				}
			}
		}
	}
	return firstText(strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_RELEASE_VERSION")), strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_SOURCE_RELEASE")))
}

func clusterGitHubRepo() string {
	if configured := strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_GITHUB_REPOSITORY")); clusterRepoRE.MatchString(configured) {
		return configured
	}
	return "bunny-lab-io/Borealis"
}

func clusterGitHubAPIBase() string {
	return strings.TrimRight(firstText(strings.TrimSpace(os.Getenv("BOREALIS_GITHUB_API_BASE_URL")), "https://api.github.com"), "/")
}

func fetchClusterReleaseCatalog(ctx context.Context, current string) ([]map[string]any, error) {
	repo := clusterGitHubRepo()
	cacheKey := repo + "|" + current + "|" + clusterGitHubAPIBase()
	serverClusterReleaseCache.mu.Lock()
	if serverClusterReleaseCache.key == cacheKey && time.Now().Before(serverClusterReleaseCache.expiresAt) {
		items := copyClusterReleaseItems(serverClusterReleaseCache.items)
		serverClusterReleaseCache.mu.Unlock()
		return items, nil
	}
	serverClusterReleaseCache.mu.Unlock()

	items := make([]map[string]any, 0)
	foundCurrent := false
	for page := 1; page <= 20 && !foundCurrent; page++ {
		endpoint := fmt.Sprintf("%s/repos/%s/releases?per_page=100&page=%d", clusterGitHubAPIBase(), repo, page)
		var releases []clusterGitHubRelease
		if err := clusterGitHubJSON(ctx, endpoint, &releases); err != nil {
			return nil, err
		}
		if len(releases) == 0 {
			break
		}
		for _, release := range releases {
			tag := strings.TrimSpace(release.TagName)
			if release.Draft || release.Prerelease || !clusterReleaseRE.MatchString(tag) {
				continue
			}
			if current != "" && compareClusterReleases(tag, current) < 0 {
				foundCurrent = true
				break
			}
			entry, err := hydrateClusterRelease(ctx, release, current)
			if err != nil {
				entry = map[string]any{"tag": tag, "title": firstText(strings.TrimSpace(release.Name), tag), "published_at": release.PublishedAt, "selectable": false, "reason": err.Error()}
			}
			items = append(items, entry)
			if tag == current {
				foundCurrent = true
				break
			}
		}
		// Standalone or pre-migration installs may not have a recorded baseline yet.
		// One page is enough to populate the picker without walking the full catalog.
		if current == "" {
			foundCurrent = true
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return compareClusterReleases(cleanText(items[i]["tag"]), cleanText(items[j]["tag"])) > 0
	})
	serverClusterReleaseCache.mu.Lock()
	serverClusterReleaseCache.key = cacheKey
	serverClusterReleaseCache.expiresAt = time.Now().Add(clusterReleaseCacheTTL)
	serverClusterReleaseCache.items = copyClusterReleaseItems(items)
	serverClusterReleaseCache.mu.Unlock()
	return items, nil
}

func hydrateClusterRelease(ctx context.Context, release clusterGitHubRelease, current string) (map[string]any, error) {
	tag := strings.TrimSpace(release.TagName)
	sha, err := resolveClusterGitHubTagSHA(ctx, tag)
	if err != nil {
		return nil, err
	}
	manifestURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/Data/Engine/release-manifest.json", clusterGitHubRepo(), url.PathEscape(tag))
	if override := strings.TrimSpace(os.Getenv("BOREALIS_GITHUB_RAW_BASE_URL")); override != "" {
		manifestURL = strings.TrimRight(override, "/") + "/" + url.PathEscape(tag) + "/Data/Engine/release-manifest.json"
	}
	var manifest clusterReleaseManifest
	manifestErr := clusterGitHubJSON(ctx, manifestURL, &manifest)
	runningK3s := strings.TrimSpace(os.Getenv("BOREALIS_K3S_VERSION"))
	selectable := manifestErr == nil && manifest.SchemaVersion == 1 && manifest.ClusterCompatible && clusterReleaseRE.MatchString(manifest.MinimumRollingVersion) && textInSet(manifest.DatabaseMigration, "none", "expand-contract") && clusterK3sRE.MatchString(manifest.RequiredK3sBaseline) && manifest.RequiredK3sConformance == "pod-restart-policy-startup-probe-v1"
	reason := ""
	if manifestErr != nil {
		reason = "release lacks cluster compatibility manifest"
	} else if !selectable {
		reason = "release manifest is not rolling-cluster compatible"
	} else if current != "" && compareClusterReleases(tag, current) < 0 {
		selectable = false
		reason = "downgrades are not supported"
	} else if current != "" && compareClusterReleases(current, manifest.MinimumRollingVersion) < 0 {
		selectable = false
		reason = "current release is older than minimum rolling source"
	} else if runningK3s != "" && runningK3s != manifest.RequiredK3sBaseline {
		selectable = false
		reason = "running K3s baseline does not match release manifest"
	}
	return map[string]any{"tag": tag, "title": firstText(strings.TrimSpace(release.Name), tag), "published_at": release.PublishedAt, "commit_sha": sha, "current": tag == current, "selectable": selectable, "reason": reason, "compatibility": manifest}, nil
}

func resolveClusterRelease(ctx context.Context, tag string, current string) (map[string]any, error) {
	items, err := fetchClusterReleaseCatalog(ctx, current)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if cleanText(item["tag"]) == tag {
			if selectable, _ := item["selectable"].(bool); !selectable {
				return nil, errors.New(firstText(cleanText(item["reason"]), "release is not selectable"))
			}
			return item, nil
		}
	}
	return nil, errors.New("release is not present in stable catalog")
}

func resolveClusterGitHubTagSHA(ctx context.Context, tag string) (string, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/git/ref/tags/%s", clusterGitHubAPIBase(), clusterGitHubRepo(), url.PathEscape(tag))
	var ref struct {
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"object"`
	}
	if err := clusterGitHubJSON(ctx, endpoint, &ref); err != nil {
		return "", err
	}
	sha := strings.ToLower(strings.TrimSpace(ref.Object.SHA))
	objectType := strings.ToLower(strings.TrimSpace(ref.Object.Type))
	for depth := 0; depth < 3 && objectType == "tag"; depth++ {
		var annotated struct {
			Object struct {
				SHA  string `json:"sha"`
				Type string `json:"type"`
				URL  string `json:"url"`
			} `json:"object"`
		}
		if err := clusterGitHubJSON(ctx, ref.Object.URL, &annotated); err != nil {
			return "", err
		}
		sha = strings.ToLower(strings.TrimSpace(annotated.Object.SHA))
		objectType = strings.ToLower(strings.TrimSpace(annotated.Object.Type))
		ref.Object.URL = annotated.Object.URL
	}
	if objectType != "commit" || len(sha) != 40 {
		return "", errors.New("release tag does not resolve to commit")
	}
	if _, err := hex.DecodeString(sha); err != nil {
		return "", errors.New("release tag returned invalid commit SHA")
	}
	return sha, nil
}

func clusterGitHubJSON(ctx context.Context, endpoint string, output any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Borealis-Cluster-Controller")
	if token, _ := ctx.Value(clusterGitHubTokenContextKey{}).(string); token != "" && clusterGitHubAPIEndpoint(endpoint) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("GitHub returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if err := decoder.Decode(output); err != nil {
		return err
	}
	return nil
}

func clusterContextWithGitHubToken(ctx context.Context, auth *authService) context.Context {
	if auth == nil || auth.aegis == nil {
		return ctx
	}
	store, ok := auth.store.(githubTokenManagementStore)
	if !ok {
		return ctx
	}
	record, err := store.loadGithubToken(ctx, auth.aegis)
	if err != nil || strings.TrimSpace(record.Token) == "" {
		return ctx
	}
	return context.WithValue(ctx, clusterGitHubTokenContextKey{}, strings.TrimSpace(record.Token))
}

func clusterGitHubAPIEndpoint(endpoint string) bool {
	target, targetErr := url.Parse(endpoint)
	base, baseErr := url.Parse(clusterGitHubAPIBase())
	return targetErr == nil && baseErr == nil && strings.EqualFold(target.Scheme, base.Scheme) && strings.EqualFold(target.Host, base.Host)
}

func compareClusterReleases(left, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	width := len(leftParts)
	if len(rightParts) > width {
		width = len(rightParts)
	}
	for index := 0; index < width; index++ {
		leftValue, rightValue := 0, 0
		if index < len(leftParts) {
			leftValue, _ = strconv.Atoi(leftParts[index])
		}
		if index < len(rightParts) {
			rightValue, _ = strconv.Atoi(rightParts[index])
		}
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
	}
	return 0
}

func copyClusterReleaseItems(input []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(input))
	for _, item := range input {
		out = append(out, copyMap(item))
	}
	return out
}
