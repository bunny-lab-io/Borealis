package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func registerInternalSchedulerRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("/api/internal/job-scheduler/public-base-url", internalSchedulerPublicBaseURLHandler(auth))
	mux.HandleFunc("/api/internal/job-scheduler/host-service-event", internalSchedulerHostServiceEventHandler(auth))
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

func internalSchedulerHostServiceEventHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		if !validInternalSchedulerRequest(auth, r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		store, ok := auth.store.(deviceProcessStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "device_route_lookup_unavailable"})
			return
		}
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		hostname := cleanText(body["hostname"])
		serviceMode := firstText(cleanText(firstNonEmpty(body["service_mode"], body["mode"])), "system")
		eventName := cleanText(body["event_name"])
		if hostname == "" || eventName == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "hostname_and_event_name_required"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		snapshot, status, err := store.loadDeviceProcessContext(ctx, operatorProfile{Username: "job-scheduler", Role: "Admin"}, hostname)
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		if snapshot.Route == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable", "message": "No active site-worker route is available for this device site."})
			return
		}
		pendingTTL := coerceInt64(body["pending_ttl_seconds"])
		if pendingTTL <= 0 {
			pendingTTL = 180
		}
		result, workerStatus, workerErr := emitWorkerHostServiceEvent(r.Context(), auth, snapshot.Route, map[string]any{
			"hostname":            snapshot.Hostname,
			"service_mode":        serviceMode,
			"event_name":          eventName,
			"payload":             firstNonEmpty(body["payload"], map[string]any{}),
			"allow_pending":       boolFromAny(body["allow_pending"]),
			"pending_ttl_seconds": pendingTTL,
		}, 6*time.Second)
		if workerErr != nil {
			writeJSON(w, workerStatus, workerErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"emitted": boolFromAny(result["emitted"]),
			"queued":  boolFromAny(result["queued"]),
		})
	}
}

func emitWorkerHostServiceEvent(ctx context.Context, auth *authService, route *agentWorkerRoute, body map[string]any, timeout time.Duration) (map[string]any, int, map[string]any) {
	if auth == nil || route == nil {
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable"}
	}
	target := workerInternalURL(route, "/remote-ops/host-service/event")
	if target == "" {
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable"}
	}
	if timeout <= 0 {
		timeout = 6 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, http.StatusBadRequest, map[string]any{"error": "invalid_request", "message": "The host-service event request could not be encoded."}
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, http.StatusBadGateway, map[string]any{"error": "worker_request_failed", "message": err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable", "message": "The site-worker route did not answer."}
	}
	defer resp.Body.Close()
	var payloadMap map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payloadMap); err != nil {
		return nil, http.StatusBadGateway, map[string]any{"error": "invalid_worker_response", "message": "The site-worker returned an invalid response."}
	}
	if resp.StatusCode >= 400 {
		status := resp.StatusCode
		if cleanText(payloadMap["error"]) == "" {
			payloadMap["error"] = "site_worker_error"
		}
		return nil, status, payloadMap
	}
	return payloadMap, http.StatusOK, nil
}
