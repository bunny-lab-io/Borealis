package main

import (
	"net/http"
	"time"
)

func registerServerWireGuardRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("/api/server/wireguard/recover", serverWireGuardRecoverHandler(auth))
}

func serverWireGuardRecoverHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		activeCount := activeWireGuardLeaseCount(ctx, auth.store)
		if activeCount <= 0 {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "no_active_sessions",
				"message": "Recover Listener is only available while Borealis has active VPN sessions.",
			})
			return
		}
		if !containerizedEngineEnabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":   "wireguard_unavailable",
				"message": "WireGuard listener recovery is available for containerized Engine deployments.",
			})
			return
		}
		store, ok := auth.store.(serverServiceActionStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "server_actions_unavailable"})
			return
		}
		action := map[string]any{"id": "reconcile", "label": "Reconcile", "action": "reconcile"}
		workItemID, err := store.queueServerServiceAction(ctx, "wireguard-tunnel", action)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "wireguard_recovery_failed", "message": err.Error()})
			return
		}
		attemptAt := time.Now().UTC()
		payload := collectOverviewWireGuardPayload(ctx, auth.store)
		payload["active_tunnel_count"] = activeCount
		payload["recover_supported"] = true
		payload["recovery_in_progress"] = true
		payload["last_recovery_attempt_at"] = attemptAt.Unix()
		payload["last_recovery_attempt_at_iso"] = attemptAt.Format(time.RFC3339Nano)
		payload["listener_reason"] = "manual_admin_recovery"
		payload["work_item_id"] = workItemID
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "wireguard": payload})
	}
}
