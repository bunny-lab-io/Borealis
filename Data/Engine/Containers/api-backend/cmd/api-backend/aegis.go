package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

type aegisStatusProvider interface {
	aegisStatus(ctx context.Context) (map[string]any, error)
}

type goAegisStatusProvider struct {
	auth *authService
}

func registerAegisRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("GET /api/aegis/status", aegisStatusHandler(auth, &goAegisStatusProvider{auth: auth}))
	mux.HandleFunc("POST /api/aegis/setup", aegisSetupHandler(auth))
	mux.HandleFunc("POST /api/aegis/unlock", aegisUnlockHandler(auth))
	mux.HandleFunc("POST /api/aegis/rotate", aegisRotateHandler(auth))
	mux.HandleFunc("POST /api/aegis/force_reset", aegisForceResetHandler(auth))
}

func aegisStatusHandler(auth *authService, provider aegisStatusProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		user, failure := requireUser(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		if provider == nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "aegis_status_unavailable"})
			return
		}
		payload, err := provider.aegisStatus(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":   "aegis_status_unavailable",
				"message": err.Error(),
			})
			return
		}
		if !aegisPayloadBool(payload, "configured") || aegisPayloadBool(payload, "locked") {
			unauthorizedAuthFailure().write(w)
			return
		}
		response := copyMap(payload)
		response["user_role"] = firstText(user.Role, "User")
		writeJSON(w, http.StatusOK, response)
	}
}

func (p *goAegisStatusProvider) aegisStatus(ctx context.Context) (map[string]any, error) {
	if p == nil || p.auth == nil || p.auth.aegis == nil {
		return nil, errors.New("aegis status provider unavailable")
	}
	return p.auth.aegis.status(ctx)
}

func aegisSetupHandler(auth *authService) http.HandlerFunc {
	return aegisLifecyclePostHandler(auth, func(ctx context.Context, body map[string]any) (map[string]any, error) {
		if auth == nil || auth.aegis == nil {
			return nil, errors.New("aegis store unavailable")
		}
		return auth.aegis.setupWithCipher(ctx, cleanText(body["cipher"]))
	})
}

func aegisUnlockHandler(auth *authService) http.HandlerFunc {
	return aegisLifecyclePostHandler(auth, func(ctx context.Context, body map[string]any) (map[string]any, error) {
		if auth == nil || auth.aegis == nil {
			return nil, errors.New("aegis store unavailable")
		}
		payload, err := auth.aegis.unlockWithCipher(ctx, cleanText(body["cipher"]))
		if err != nil {
			return nil, err
		}
		return payload, nil
	})
}

func aegisRotateHandler(auth *authService) http.HandlerFunc {
	return aegisLifecyclePostHandler(auth, func(ctx context.Context, body map[string]any) (map[string]any, error) {
		if auth == nil || auth.aegis == nil {
			return nil, errors.New("aegis store unavailable")
		}
		newCipher := cleanText(body["new_cipher"])
		payload, err := auth.aegis.rotateWithCipher(ctx, cleanText(body["current_cipher"]), newCipher)
		if err != nil {
			return nil, err
		}
		return payload, nil
	})
}

func aegisForceResetHandler(auth *authService) http.HandlerFunc {
	return aegisLifecyclePostHandler(auth, func(ctx context.Context, _ map[string]any) (map[string]any, error) {
		if auth == nil || auth.aegis == nil {
			return nil, errors.New("aegis store unavailable")
		}
		return auth.aegis.forceReset(ctx)
	})
}

func aegisLifecyclePostHandler(auth *authService, action func(context.Context, map[string]any) (map[string]any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		user, failure := requireAdmin(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		body, ok := readAuthJSON(w, r)
		if !ok {
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		payload, err := action(ctx, body)
		if err != nil {
			errorBody, status := aegisErrorBody(err)
			writeJSON(w, status, errorBody)
			return
		}
		response := copyMap(payload)
		response["status"] = "ok"
		response["user_role"] = firstText(user.Role, "User")
		writeJSON(w, http.StatusOK, response)
	}
}

func aegisPayloadBool(payload map[string]any, key string) bool {
	switch value := payload[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true") || strings.TrimSpace(value) == "1"
	case float64:
		return value != 0
	case int:
		return value != 0
	case int64:
		return value != 0
	default:
		return false
	}
}
