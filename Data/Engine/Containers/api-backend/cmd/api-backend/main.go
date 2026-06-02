package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var version = "dev"

type gatewayConfig struct {
	ListenHost         string
	ListenPort         string
	LegacyHost         string
	LegacyPort         string
	LegacyURL          *url.URL
	DatabaseURL        string
	DBSSLMode          string
	DBConnectTimeout   time.Duration
	DBMaxOpenConns     int
	DBMaxIdleConns     int
	EngineSecretPath   string
	LegacyReadyTimeout time.Duration
	HealthTimeout      time.Duration
	AuthTimeout        time.Duration
	AuthTokenTTL       time.Duration
	ShutdownTimeout    time.Duration
}

type legacyState struct {
	mu              sync.RWMutex
	PID             int       `json:"pid,omitempty"`
	StartedAt       time.Time `json:"started_at,omitempty"`
	Healthy         bool      `json:"healthy"`
	LastHealthAt    time.Time `json:"last_health_at,omitempty"`
	LastHealthError string    `json:"last_health_error,omitempty"`
	Exited          bool      `json:"exited"`
	ExitError       string    `json:"exit_error,omitempty"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	rootCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	state := &legacyState{}
	legacyCmd, err := startLegacyBackend(cfg, state)
	if err != nil {
		log.Fatalf("failed to start Python compatibility backend: %v", err)
	}

	legacyExited := make(chan error, 1)
	go func() {
		legacyExited <- legacyCmd.Wait()
	}()

	if err := waitForLegacyReady(rootCtx, cfg, state); err != nil {
		state.markExited(terminateLegacy(legacyCmd, legacyExited, cfg.ShutdownTimeout))
		log.Fatalf("Python compatibility backend did not become healthy: %v", err)
	}

	auth, closeAuth, err := newAuthService(cfg)
	if err != nil {
		state.markExited(terminateLegacy(legacyCmd, legacyExited, cfg.ShutdownTimeout))
		log.Fatalf("failed to initialise Go auth service: %v", err)
	}
	defer closeAuth()

	proxy := newLegacyProxy(cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler(cfg, state))
	mux.HandleFunc("/api/system/go-backend/status", statusHandler(cfg, state))
	registerAuthRoutes(mux, auth)
	registerServerTimeRoutes(mux, auth)
	registerDeviceSearchRoutes(mux, auth)
	mux.Handle("/", proxy)

	server := &http.Server{
		Addr:              net.JoinHostPort(cfg.ListenHost, cfg.ListenPort),
		Handler:           withRequestHeaders(mux),
		ReadHeaderTimeout: 15 * time.Second,
	}

	serverExited := make(chan error, 1)
	go func() {
		log.Printf("Go api-backend gateway listening on http://%s with Python compatibility backend %s", server.Addr, cfg.LegacyURL.String())
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverExited <- err
	}()

	exitCode := 0
	legacyAlreadyExited := false
	serverAlreadyExited := false
	select {
	case <-rootCtx.Done():
		log.Printf("shutdown requested")
	case err := <-legacyExited:
		legacyAlreadyExited = true
		state.markExited(err)
		if err != nil {
			log.Printf("Python compatibility backend exited: %v", err)
			exitCode = 1
		} else {
			log.Printf("Python compatibility backend exited")
		}
	case err := <-serverExited:
		serverAlreadyExited = true
		if err != nil {
			log.Printf("Go api-backend gateway exited: %v", err)
			exitCode = 1
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("gateway shutdown error: %v", err)
		_ = server.Close()
	}
	if !legacyAlreadyExited {
		state.markExited(terminateLegacy(legacyCmd, legacyExited, cfg.ShutdownTimeout))
	}
	if !serverAlreadyExited {
		select {
		case err := <-serverExited:
			if err != nil {
				log.Printf("Go api-backend gateway exited during shutdown: %v", err)
				exitCode = 1
			}
		default:
		}
	}

	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func loadConfig() (gatewayConfig, error) {
	legacyHost := envDefault("BOREALIS_GO_API_LEGACY_HOST", "127.0.0.1")
	legacyPort := envDefault("BOREALIS_GO_API_LEGACY_PORT", "5001")
	legacyURL, err := url.Parse("http://" + net.JoinHostPort(legacyHost, legacyPort))
	if err != nil {
		return gatewayConfig{}, err
	}

	return gatewayConfig{
		ListenHost:         envDefault("BOREALIS_GO_API_HOST", "127.0.0.1"),
		ListenPort:         envDefault("BOREALIS_GO_API_PORT", "5000"),
		LegacyHost:         legacyHost,
		LegacyPort:         legacyPort,
		LegacyURL:          legacyURL,
		DatabaseURL:        envDefault("BOREALIS_DATABASE_URL", ""),
		DBSSLMode:          envDefault("BOREALIS_DB_SSLMODE", "prefer"),
		DBConnectTimeout:   envDurationSeconds("BOREALIS_DB_CONNECT_TIMEOUT", 15*time.Second),
		DBMaxOpenConns:     envInt("BOREALIS_GO_API_DB_MAX_OPEN_CONNS", 8, 1, 100),
		DBMaxIdleConns:     envInt("BOREALIS_GO_API_DB_MAX_IDLE_CONNS", 4, 0, 100),
		EngineSecretPath:   envDefault("BOREALIS_ENGINE_SECRET_PATH", "/opt/Borealis/Engine/Services/api-backend/secrets/engine_secret.txt"),
		LegacyReadyTimeout: envDurationSeconds("BOREALIS_GO_API_LEGACY_READY_TIMEOUT_SECONDS", 120*time.Second),
		HealthTimeout:      envDurationSeconds("BOREALIS_GO_API_HEALTH_TIMEOUT_SECONDS", 2*time.Second),
		AuthTimeout:        envDurationSeconds("BOREALIS_GO_API_AUTH_TIMEOUT_SECONDS", 3*time.Second),
		AuthTokenTTL:       envDurationSeconds("BOREALIS_TOKEN_TTL_SECONDS", 30*24*time.Hour),
		ShutdownTimeout:    envDurationSeconds("BOREALIS_GO_API_SHUTDOWN_TIMEOUT_SECONDS", 20*time.Second),
	}, nil
}

func envDefault(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func envDurationSeconds(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds * float64(time.Second))
}

func envInt(name string, fallback, minimum, maximum int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	if parsed < minimum {
		return minimum
	}
	if maximum > 0 && parsed > maximum {
		return maximum
	}
	return parsed
}

func startLegacyBackend(cfg gatewayConfig, state *legacyState) (*exec.Cmd, error) {
	cmd := exec.Command("python", "-m", "Data.Engine.bootstrapper")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = legacyEnv(os.Environ(), cfg)

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	state.markStarted(cmd.Process.Pid)
	log.Printf("started Python compatibility backend pid=%d url=%s", cmd.Process.Pid, cfg.LegacyURL.String())
	return cmd, nil
}

func legacyEnv(base []string, cfg gatewayConfig) []string {
	env := setEnv(base, "BOREALIS_ENGINE_HOST", cfg.LegacyHost)
	env = setEnv(env, "BOREALIS_ENGINE_PORT", cfg.LegacyPort)
	return env
}

func setEnv(base []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(base)+1)
	replaced := false
	for _, item := range base {
		if strings.HasPrefix(item, prefix) {
			result = append(result, prefix+value)
			replaced = true
			continue
		}
		result = append(result, item)
	}
	if !replaced {
		result = append(result, prefix+value)
	}
	return result
}

func waitForLegacyReady(ctx context.Context, cfg gatewayConfig, state *legacyState) error {
	deadlineCtx, cancel := context.WithTimeout(ctx, cfg.LegacyReadyTimeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		if err := probeLegacyHealth(deadlineCtx, cfg, cfg.HealthTimeout); err == nil {
			state.markHealth(true, "")
			return nil
		} else {
			state.markHealth(false, err.Error())
		}

		select {
		case <-deadlineCtx.Done():
			return deadlineCtx.Err()
		case <-ticker.C:
		}
	}
}

func probeLegacyHealth(ctx context.Context, cfg gatewayConfig, timeout time.Duration) error {
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	healthURL := cfg.LegacyURL.ResolveReference(&url.URL{Path: "/health"})
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, healthURL.String(), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("legacy health returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func newLegacyProxy(cfg gatewayConfig) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(cfg.LegacyURL)
	proxy.FlushInterval = 100 * time.Millisecond
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("proxy error method=%s path=%s err=%v", r.Method, r.URL.Path, err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":  "legacy api-backend unavailable",
			"detail": err.Error(),
		})
	}
	return proxy
}

func healthHandler(cfg gatewayConfig, state *legacyState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := probeLegacyHealth(r.Context(), cfg, cfg.HealthTimeout)
		if err != nil {
			state.markHealth(false, err.Error())
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status":  "unhealthy",
				"backend": "go",
				"version": version,
				"legacy":  state.snapshot(),
			})
			return
		}

		state.markHealth(true, "")
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "healthy",
			"backend": "go",
			"version": version,
			"legacy":  state.snapshot(),
		})
	}
}

func statusHandler(cfg gatewayConfig, state *legacyState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":          "running",
			"backend":         "go",
			"version":         version,
			"listen":          net.JoinHostPort(cfg.ListenHost, cfg.ListenPort),
			"legacy_upstream": cfg.LegacyURL.String(),
			"legacy":          state.snapshot(),
		})
	}
}

func withRequestHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Borealis-Api-Backend", "go")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeMethodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
		"error": "method_not_allowed",
	})
}

func terminateLegacy(cmd *exec.Cmd, exited <-chan error, timeout time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)

	select {
	case err := <-exited:
		return err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return <-exited
	}
}

func (s *legacyState) markStarted(pid int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PID = pid
	s.StartedAt = time.Now().UTC()
	s.Exited = false
	s.ExitError = ""
}

func (s *legacyState) markHealth(healthy bool, errText string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Healthy = healthy
	s.LastHealthAt = time.Now().UTC()
	s.LastHealthError = errText
}

func (s *legacyState) markExited(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Exited = true
	s.Healthy = false
	if err != nil {
		s.ExitError = err.Error()
	}
}

func (s *legacyState) snapshot() legacyState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return legacyState{
		PID:             s.PID,
		StartedAt:       s.StartedAt,
		Healthy:         s.Healthy,
		LastHealthAt:    s.LastHealthAt,
		LastHealthError: s.LastHealthError,
		Exited:          s.Exited,
		ExitError:       s.ExitError,
	}
}
