package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	aegisClusterKeyPath      = "/internal/cluster/aegis-key"
	aegisClusterMaxBodyBytes = 1024
	aegisClusterSyncInterval = 5 * time.Second
)

type aegisClusterKeyInstaller interface {
	installClusterKey(context.Context, []byte) error
}

func startAegisClusterKeyServer(auth *authService) (*http.Server, <-chan error, error) {
	if !aegisClusterFanoutEnabled() {
		return nil, nil, nil
	}
	service, ok := auth.aegis.(aegisClusterKeyInstaller)
	if !ok || service == nil {
		return nil, nil, errors.New("Aegis cluster key installer is unavailable")
	}
	tlsConfig, err := auth.clusterTLS().serverConfig()
	if err != nil {
		return nil, nil, err
	}
	address := net.JoinHostPort(envDefault("BOREALIS_AEGIS_CLUSTER_LISTEN_HOST", "0.0.0.0"), envDefault("BOREALIS_AEGIS_CLUSTER_PORT", "9444"))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, nil, fmt.Errorf("listen for Aegis cluster key fanout: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+aegisClusterKeyPath, aegisClusterKeyHandler(service))
	server := &http.Server{
		Addr:              listener.Addr().String(),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       15 * time.Second,
		TLSConfig:         tlsConfig,
	}
	// Every key transfer must authenticate against current trust, including
	// callers which try to keep a connection open across CA retirement.
	server.SetKeepAlivesEnabled(false)
	exited := make(chan error, 1)
	go func() {
		log.Printf("Aegis cluster key listener active on %s with required client certificates", address)
		err := server.Serve(tls.NewListener(listener, tlsConfig))
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		exited <- err
	}()
	return server, exited, nil
}

func aegisClusterKeyHandler(installer aegisClusterKeyInstaller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "verified_client_certificate_required"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, aegisClusterMaxBodyBytes+1))
		if err != nil || len(body) > aegisClusterMaxBodyBytes {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_key_payload"})
			return
		}
		var payload struct {
			Key string `json:"key"`
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_key_payload"})
			return
		}
		key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(payload.Key))
		if err != nil || len(key) != aegisKeyLength {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_key_payload"})
			return
		}
		defer zeroBytes(key)
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := installer.installClusterKey(ctx, key); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "key_verification_failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func fanoutAegisClusterKey(ctx context.Context, auth *authService) (int, error) {
	if !aegisClusterFanoutEnabled() {
		return 0, nil
	}
	service, ok := auth.aegis.(*goAegisService)
	if !ok || service == nil {
		return 0, errors.New("Aegis cluster key source is unavailable")
	}
	key, err := service.activeKey()
	if err != nil {
		return 0, err
	}
	defer zeroBytes(key)
	fanoutCtx, cancelFanout := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancelFanout()
	host := envDefault("BOREALIS_AEGIS_CLUSTER_PEER_HOST", "api-backend-aegis.borealis.svc")
	port := envDefault("BOREALIS_AEGIS_CLUSTER_PORT", "9444")
	lookupCtx, cancel := context.WithTimeout(fanoutCtx, 3*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return 0, fmt.Errorf("resolve Aegis API peers: %w", err)
	}
	if len(addresses) == 0 {
		return 0, errors.New("resolve Aegis API peers: DNS returned no addresses")
	}
	peers := make([]string, 0, len(addresses))
	seen := map[string]bool{}
	for _, address := range addresses {
		value := address.IP.String()
		if net.ParseIP(value) != nil && !seen[value] {
			seen[value] = true
			peers = append(peers, value)
		}
	}
	sort.Strings(peers)
	if len(peers) == 0 {
		return 0, errors.New("Aegis API peer DNS returned no usable addresses")
	}
	tlsConfig, err := auth.clusterTLS().config(false)
	if err != nil {
		return 0, err
	}
	tlsConfig.ServerName = host
	transport := &http.Transport{TLSClientConfig: tlsConfig, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Timeout: 5 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	raw, _ := json.Marshal(map[string]string{"key": base64.StdEncoding.EncodeToString(key)})
	defer zeroBytes(raw)
	type fanoutResult struct {
		peer string
		err  error
	}
	results := make(chan fanoutResult, len(peers))
	for _, peer := range peers {
		go func(peer string) {
			request, err := http.NewRequestWithContext(fanoutCtx, http.MethodPost, "https://"+net.JoinHostPort(peer, port)+aegisClusterKeyPath, bytes.NewReader(raw))
			if err == nil {
				request.Header.Set("Content-Type", "application/json")
				response, requestErr := client.Do(request)
				err = requestErr
				if response != nil {
					_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
					response.Body.Close()
					if err == nil && response.StatusCode != http.StatusOK {
						err = fmt.Errorf("returned HTTP %d", response.StatusCode)
					}
				}
			}
			results <- fanoutResult{peer: peer, err: err}
		}(peer)
	}
	unlocked := 0
	var firstError error
	for range peers {
		result := <-results
		if result.err != nil {
			if firstError == nil {
				firstError = fmt.Errorf("fan out Aegis key to %s: %w", result.peer, result.err)
			}
			continue
		}
		unlocked++
	}
	if firstError != nil {
		return unlocked, firstError
	}
	return unlocked, nil
}

func startAegisClusterKeyFanoutLoop(ctx context.Context, auth *authService) {
	if !aegisClusterFanoutEnabled() {
		return
	}
	runAegisClusterKeyFanoutLoop(ctx, aegisClusterSyncInterval, func(loopCtx context.Context) error {
		_, err := fanoutAegisClusterKey(loopCtx, auth)
		return err
	})
}

func runAegisClusterKeyFanoutLoop(ctx context.Context, interval time.Duration, reconcile func(context.Context) error) {
	if interval <= 0 || reconcile == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		lastFailure := ""
		for {
			err := reconcile(ctx)
			if err == nil || errors.Is(err, errAegisLocked) {
				lastFailure = ""
			} else if err.Error() != lastFailure {
				lastFailure = err.Error()
				log.Printf("Aegis cluster key reconciliation failed: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func aegisClusterFanoutEnabled() bool {
	value := strings.TrimSpace(os.Getenv("BOREALIS_AEGIS_CLUSTER_FANOUT_ENABLED"))
	return value == "1" || strings.EqualFold(value, "true")
}
