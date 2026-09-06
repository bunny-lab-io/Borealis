package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"
	"unicode/utf8"

	"borealis/api-backend/internal/clusterremote"
)

const clusterSSHBodyMaxBytes = 128 << 10

// Preflight has no durable credentials or membership side effects. Admission
// workers will consume separately encrypted operation-scoped credentials.
type clusterSSHInspector interface {
	ProbeHostKey(context.Context, clusterremote.Target) (clusterremote.HostKey, error)
	Inspect(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterremote.HostFacts, error)
}

type clusterSSHNativeInspector struct{ clusterremote.Transport }

func (remote clusterSSHNativeInspector) Inspect(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterremote.HostFacts, error) {
	client, err := remote.Connect(ctx, target, key, credential)
	if err != nil {
		return clusterremote.HostFacts{}, err
	}
	defer client.Close()
	return client.Inspect(ctx)
}

type clusterSSHPreflight struct {
	auth   *authService
	remote clusterSSHInspector
	slots  chan struct{}
}

func registerClusterSSHPreflightRoutes(mux *http.ServeMux, auth *authService) {
	service := &clusterSSHPreflight{auth: auth, remote: clusterSSHNativeInspector{}, slots: make(chan struct{}, 4)}
	mux.HandleFunc("POST /api/server/cluster/onboarding/host-key", service.handler(false))
	mux.HandleFunc("POST /api/server/cluster/onboarding/inspect", service.handler(true))
}

// Decode a flat, exact field contract without generic text sanitization: secret
// punctuation and whitespace must survive. Reject duplicate/case-aliased keys
// instead of allowing JSON's last-value-wins semantics for the approved pin.
func readClusterSSHBody(r *http.Request, inspect bool) (map[string]json.RawMessage, error) {
	if r.URL.RawQuery != "" {
		return nil, errors.New("query parameters are not accepted")
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return nil, errors.New("Content-Type must be application/json")
	}
	raw, err := readLimitedRequestBody(r, clusterSSHBodyMaxBytes)
	if err != nil || !utf8.Valid(raw) {
		return nil, errors.New("body must be valid UTF-8 JSON within 128 KiB")
	}
	defer clear(raw)
	allowed := map[string]bool{"address": true, "port": true}
	if inspect {
		for _, name := range []string{"host_key_algorithm", "host_key_fingerprint", "host_key_base64", "host_key_approved", "username", "auth_method", "password", "private_key", "passphrase"} {
			allowed[name] = true
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return nil, errors.New("body must be a JSON object")
	}
	body := map[string]json.RawMessage{}
	valid := false
	defer func() {
		if !valid {
			for _, value := range body {
				clear(value)
			}
		}
	}()
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || !allowed[name] || body[name] != nil {
			return nil, errors.New("body contains an unknown or duplicate field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("body must be valid JSON")
		}
		body[name] = value
	}
	if last, err := decoder.Token(); err != nil || last != json.Delim('}') {
		return nil, errors.New("body must be valid JSON")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("body must contain exactly one JSON object")
	}
	valid = true
	return body, nil
}

func clusterSSHString(body map[string]json.RawMessage, name string, maximum int, optional bool) (string, error) {
	raw, exists := body[name]
	if !exists && optional {
		return "", nil
	}
	var value string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil || len(value) > maximum || (!optional && value == "") || !utf8.ValidString(value) {
		return "", errors.New("invalid " + name)
	}
	return value, nil
}

func clusterSSHTarget(body map[string]json.RawMessage) (clusterremote.Target, error) {
	address, err := clusterSSHString(body, "address", 15, false)
	var port int
	if err != nil || json.Unmarshal(body["port"], &port) != nil {
		return clusterremote.Target{}, clusterremote.ErrInvalidTarget
	}
	target := clusterremote.Target{Address: address, Port: port}
	return target, target.Validate()
}

func clusterSSHApproval(body map[string]json.RawMessage) (clusterremote.HostKey, error) {
	var approved bool
	if json.Unmarshal(body["host_key_approved"], &approved) != nil || !approved {
		return clusterremote.HostKey{}, errors.New("explicit host key approval required")
	}
	algorithm, err := clusterSSHString(body, "host_key_algorithm", 64, false)
	if err != nil {
		return clusterremote.HostKey{}, err
	}
	fingerprint, err := clusterSSHString(body, "host_key_fingerprint", 64, false)
	if err != nil {
		return clusterremote.HostKey{}, err
	}
	encoded, err := clusterSSHString(body, "host_key_base64", 5500, false)
	if err != nil {
		return clusterremote.HostKey{}, err
	}
	publicKey, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.StdEncoding.EncodeToString(publicKey) != encoded {
		return clusterremote.HostKey{}, errors.New("invalid host_key_base64")
	}
	key := clusterremote.HostKey{Algorithm: algorithm, Fingerprint: fingerprint, PublicKey: publicKey}
	return key, key.Validate()
}

func clusterSSHCredential(body map[string]json.RawMessage) (*clusterremote.Credential, error) {
	username, err := clusterSSHString(body, "username", 64, false)
	if err != nil {
		return nil, err
	}
	method, err := clusterSSHString(body, "auth_method", 11, false)
	if err != nil {
		return nil, err
	}
	switch method {
	case "password":
		if body["private_key"] != nil || body["passphrase"] != nil {
			return nil, errors.New("password authentication cannot include private key fields")
		}
		password, err := clusterSSHString(body, "password", clusterremote.MaxPasswordBytes, false)
		if err != nil {
			return nil, err
		}
		return clusterremote.PasswordCredential(username, []byte(password))
	case "private_key":
		if body["password"] != nil {
			return nil, errors.New("private key authentication cannot include password")
		}
		key, err := clusterSSHString(body, "private_key", clusterremote.MaxPrivateKeyBytes, false)
		if err != nil {
			return nil, err
		}
		passphrase, err := clusterSSHString(body, "passphrase", clusterremote.MaxPasswordBytes, true)
		if err != nil {
			return nil, err
		}
		return clusterremote.KeyCredential(username, []byte(key), []byte(passphrase))
	default:
		return nil, errors.New("auth_method must be password or private_key")
	}
}

func (service *clusterSSHPreflight) handler(inspect bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if _, failure := requireAdmin(r.Context(), service.auth, r); failure != nil {
			failure.write(w)
			return
		}
		body, err := readClusterSSHBody(r, inspect)
		if err != nil {
			writePublicValidationErrors(w, []publicValidationError{{Field: "body", Message: err.Error()}})
			return
		}
		defer func() {
			for _, value := range body {
				clear(value)
			}
		}()
		target, err := clusterSSHTarget(body)
		if err != nil {
			writePublicValidationErrors(w, []publicValidationError{{Field: "address", Message: err.Error()}})
			return
		}
		var approved clusterremote.HostKey
		var credential *clusterremote.Credential
		if inspect {
			approved, err = clusterSSHApproval(body)
			if err == nil {
				credential, err = clusterSSHCredential(body)
			}
			if err != nil {
				writePublicValidationErrors(w, []publicValidationError{{Field: "body", Message: err.Error()}})
				return
			}
			defer credential.Destroy()
		}
		select {
		case service.slots <- struct{}{}:
			defer func() { <-service.slots }()
		default:
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "ssh_preflight_busy"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		// Admin lookup has returned its database connection before any SSH work.
		if !inspect {
			key, err := service.remote.ProbeHostKey(ctx, target)
			if err != nil {
				writeClusterSSHError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"address": target.Address, "port": target.Port, "host_key_algorithm": key.Algorithm, "host_key_fingerprint": key.Fingerprint, "host_key_base64": base64.StdEncoding.EncodeToString(key.PublicKey)})
			return
		}
		facts, err := service.remote.Inspect(ctx, target, approved, credential)
		if err != nil {
			writeClusterSSHError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"address": target.Address, "port": target.Port, "hostname": facts.Hostname, "kernel": facts.Kernel, "architecture": facts.Architecture, "uid": facts.UID, "host_key_fingerprint": approved.Fingerprint})
	}
}

func writeClusterSSHError(w http.ResponseWriter, err error) {
	status, code := http.StatusBadGateway, "ssh_preflight_failed"
	if errors.Is(err, clusterremote.ErrHostKeyChanged) {
		status, code = http.StatusConflict, "ssh_host_key_changed"
	} else if errors.Is(err, clusterremote.ErrCancelled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		status, code = http.StatusGatewayTimeout, "ssh_preflight_timeout"
	}
	// Never forward SSH banners, diagnostics, target output or auth errors.
	writeJSON(w, status, map[string]any{"error": code})
}
