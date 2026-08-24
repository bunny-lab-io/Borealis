package main

import (
	"context"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

var apiDraining atomic.Bool

func apiStartupHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "phase": "started"})
	}
}

func apiLivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		// Liveness intentionally excludes PostgreSQL, Kubernetes, and network peers.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "phase": "live"})
	}
}

func apiReadinessHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		_, drainFileErr := os.Stat("/tmp/borealis-draining")
		if apiDraining.Load() || drainFileErr == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "reason": "draining"})
			return
		}
		store, ok := auth.store.(*postgresOperatorStore)
		if !ok || store.db == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "reason": "database_store_unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
		defer cancel()
		if err := store.db.PingContext(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "reason": "database_unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "phase": "ready"})
	}
}
