package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const containerServiceActionDelay = 2 * time.Second

type serverServiceActionStore interface {
	queueServerServiceAction(ctx context.Context, serviceKey string, action map[string]any) (int64, error)
}

func registerServerActionRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("/api/server/services/", serverServiceActionHandler(auth, fallback))
}

func serverServiceActionHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/server/services/"), "/"), "/")
		if len(parts) != 2 || (parts[1] != "action" && parts[1] != "restart") || r.Method != http.MethodPost {
			if fallback != nil {
				fallback.ServeHTTP(w, r)
				return
			}
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		if !containerizedEngineEnabled() {
			if parts[1] == "restart" && fallback != nil {
				fallback.ServeHTTP(w, r)
				return
			}
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "service_action_unsupported",
				"message": "Generic service actions are available for containerized Engine deployments only.",
			})
			return
		}
		store, ok := auth.store.(serverServiceActionStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "server_actions_unavailable"})
			return
		}
		serviceKey := strings.ToLower(cleanText(parts[0]))
		if !knownComposeService(serviceKey) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "invalid_service_key", "message": "Unsupported service key."})
			return
		}
		var action map[string]any
		if parts[1] == "restart" {
			action = resolveOverviewServiceAction(serviceKey, map[string]any{"action": "restart"})
			if action == nil {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "invalid_service_key", "message": "Unsupported service key."})
				return
			}
		} else {
			body, err := readJSONMap(r)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json", "message": "Request body must be valid JSON."})
				return
			}
			action = resolveOverviewServiceAction(serviceKey, body)
			if action == nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_service_action", "message": "Unsupported action for this service."})
				return
			}
		}

		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		workItemID, err := store.queueServerServiceAction(ctx, serviceKey, action)
		if err != nil {
			code := "service_action_failed"
			if parts[1] == "restart" {
				code = "restart_failed"
			}
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": code, "message": err.Error()})
			return
		}
		actionName := strings.ToLower(cleanText(action["action"]))
		mode := strings.ToLower(cleanText(action["mode"]))
		payload := map[string]any{
			"queued":        true,
			"service_key":   serviceKey,
			"action":        actionName,
			"work_item_id":  workItemID,
			"scheduled_for": time.Now().UTC().Add(containerServiceActionDelay).Format(time.RFC3339Nano),
		}
		if parts[1] == "action" {
			payload["mode"] = nil
			if mode != "" {
				payload["mode"] = mode
			}
		}
		writeJSON(w, http.StatusAccepted, payload)
	}
}

func (s *postgresOperatorStore) queueServerServiceAction(ctx context.Context, serviceKey string, action map[string]any) (int64, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	workItemID, err := enqueueGoServiceAction(ctx, tx, serviceKey, action)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	committed = true
	return workItemID, nil
}

func knownComposeService(serviceKey string) bool {
	for _, spec := range composeServiceSpecs {
		if spec.key == serviceKey {
			return true
		}
	}
	return false
}

func resolveOverviewServiceAction(serviceKey string, body map[string]any) map[string]any {
	requestedAction := strings.ToLower(cleanText(body["action"]))
	requestedMode := strings.ToLower(cleanText(body["mode"]))
	requestedID := strings.ToLower(cleanText(firstNonEmpty(body["id"], body["action_id"])))
	for _, candidate := range overviewServiceActions(serviceKey) {
		actionName := strings.ToLower(cleanText(candidate["action"]))
		actionMode := strings.ToLower(cleanText(candidate["mode"]))
		actionID := strings.ToLower(cleanText(candidate["id"]))
		if requestedID != "" && requestedID == actionID {
			return stringMapToAny(candidate)
		}
		if requestedAction != actionName {
			continue
		}
		if actionMode != "" && requestedMode != "" && requestedMode != actionMode {
			continue
		}
		if actionMode != "" && requestedMode == "" && requestedAction == "rebuild" {
			continue
		}
		return stringMapToAny(candidate)
	}
	return nil
}

func stringMapToAny(input map[string]string) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func containerizedEngineEnabled() bool {
	if parseTruthy(os.Getenv("BOREALIS_ENGINE_CONTAINERIZED")) {
		return true
	}
	root := firstText(strings.TrimSpace(os.Getenv("BOREALIS_PROJECT_ROOT")), "/opt/Borealis")
	_, err := os.Stat(filepath.Join(root, "Engine", "Deploy", "deploy-manifest.json"))
	return err == nil
}

var _ serverServiceActionStore = (*postgresOperatorStore)(nil)
