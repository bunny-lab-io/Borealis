package main

import "net/http"

func registerAgentScriptRoutes(mux *http.ServeMux, auth *authService, signer *agentJWTSigner, dpop *dpopVerifier, scriptSigner *agentScriptSigner) {
	mux.HandleFunc("POST /api/agent/script/request", agentScriptRequestHandler(auth, signer, dpop, scriptSigner))
}

func agentScriptRequestHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier, scriptSigner *agentScriptSigner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceCtx, failure := authenticateDeviceBearer(r.Context(), r, auth, signer, dpop)
		if failure != nil {
			failure.write(w)
			return
		}
		signingKey := scriptSigningKeyB64(scriptSigner)
		if deviceCtx.Status != "active" {
			writeJSON(w, http.StatusOK, map[string]any{
				"status":        "quarantined",
				"poll_after_ms": 60000,
				"sig_alg":       "ed25519",
				"signing_key":   signingKey,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":        "idle",
			"poll_after_ms": 30000,
			"sig_alg":       "ed25519",
			"signing_key":   signingKey,
		})
	}
}
