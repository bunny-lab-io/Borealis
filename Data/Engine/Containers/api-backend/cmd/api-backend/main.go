package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

type gatewayConfig struct {
	ListenHost       string
	ListenPort       string
	DatabaseURL      string
	DBSSLMode        string
	DBConnectTimeout time.Duration
	DBMaxOpenConns   int
	DBMaxIdleConns   int
	EngineSecretPath string
	AuthTimeout      time.Duration
	AuthTokenTTL     time.Duration
	ShutdownTimeout  time.Duration
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	rootCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	if schedulerHealthcheckMode() {
		if err := runGoJobSchedulerHealthcheck(rootCtx, cfg); err != nil {
			log.Printf("Go job-scheduler healthcheck failed: %v", err)
			os.Exit(1)
		}
		return
	}

	if schedulerManagerMode() {
		if err := runGoJobSchedulerManager(rootCtx, cfg); err != nil {
			log.Fatalf("Go job-scheduler manager exited: %v", err)
		}
		return
	}

	auth, closeAuth, err := newAuthService(cfg)
	if err != nil {
		log.Fatalf("failed to initialise Go auth service: %v", err)
	}
	defer closeAuth()

	fallback := http.NotFoundHandler()
	operatorRealtime := newOperatorRealtimeHub()
	vpnRuntime := newVPNTunnelService(auth)
	vncRuntime := newVNCRuntime(auth, vpnRuntime)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler(cfg))
	mux.HandleFunc("/api/system/go-backend/status", statusHandler(cfg))
	registerRealtimeRoutes(mux, auth, operatorRealtime)
	registerAuthRoutes(mux, auth, fallback)
	registerAegisRoutes(mux, auth)
	registerBackupRoutes(mux, auth)
	if err := registerAgentTokenRoutes(mux, auth, operatorRealtime); err != nil {
		log.Fatalf("failed to initialise Agent token routes: %v", err)
	}
	if err := registerAgentVPNRuntimeRoutes(mux, auth, vpnRuntime, vncRuntime); err != nil {
		log.Fatalf("failed to initialise Agent VPN/VNC runtime routes: %v", err)
	}
	if err := registerRemoteShellRoutes(mux, auth, nil, vpnRuntime); err != nil {
		log.Fatalf("failed to initialise remote shell routes: %v", err)
	}
	registerTunnelRoutes(mux, auth, vpnRuntime)
	registerServerTimeRoutes(mux, auth)
	registerAgentReleaseChannelRoutes(mux, auth, fallback)
	registerServerOverviewRoutes(mux, auth, operatorRealtime, fallback)
	registerServerSettingsRoutes(mux, auth, fallback)
	registerServerWorkerRoutes(mux, auth, fallback)
	registerServerActionRoutes(mux, auth, fallback)
	registerServerWireGuardRoutes(mux, auth)
	registerServerLogRoutes(mux, auth, fallback)
	registerMetadataRoutes(mux, auth)
	registerDeviceRoutes(mux, auth, devicePurgeRuntime{vpn: vpnRuntime, vnc: vncRuntime}, operatorRealtime)
	registerSoftwareRoutes(mux, auth, fallback)
	registerAgentMaintenanceRoutes(mux, auth)
	registerProcessRoutes(mux, auth, fallback)
	registerRemoteFileRoutes(mux, auth, fallback)
	registerVNCRoutes(mux, auth, vpnRuntime, vncRuntime)
	registerDeviceViewRoutes(mux, auth)
	registerDeviceFilterRoutes(mux, auth)
	registerDeviceSearchRoutes(mux, auth)
	registerSiteRoutes(mux, auth)
	registerUserRoutes(mux, auth, fallback)
	registerUserSiteAssignmentRoutes(mux, auth, fallback)
	registerDirectoryRoutes(mux, auth, fallback)
	registerCredentialRoutes(mux, auth, fallback)
	registerAssemblyRoutes(mux, auth, fallback)
	registerQuickRunRoutes(mux, auth, operatorRealtime)
	registerAdminDeviceRoutes(mux, auth)
	registerWorkflowRoutes(mux, auth, fallback)
	registerWatchdogRoutes(mux, auth, fallback, operatorRealtime)
	registerScheduledJobRoutes(mux, auth, fallback)
	registerNotificationRoutes(mux, auth, operatorRealtime)
	registerInternalSchedulerRoutes(mux, auth, vpnRuntime, fallback)
	registerActivityRoutes(mux, auth)
	mux.Handle("/", fallback)
	startGoWatchdogRuntime(rootCtx, auth, operatorRealtime)

	server := &http.Server{
		Addr:              net.JoinHostPort(cfg.ListenHost, cfg.ListenPort),
		Handler:           withRequestHeaders(mux),
		ReadHeaderTimeout: 15 * time.Second,
	}

	serverExited := make(chan error, 1)
	go func() {
		log.Printf("Go api-backend listening on http://%s", server.Addr)
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverExited <- err
	}()

	exitCode := 0
	serverAlreadyExited := false
	select {
	case <-rootCtx.Done():
		log.Printf("shutdown requested")
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

func schedulerManagerMode() bool {
	role := strings.ToLower(strings.TrimSpace(os.Getenv("BOREALIS_PROCESS_ROLE")))
	if role == "job-scheduler" || role == "scheduler-manager" {
		return true
	}
	if len(os.Args) > 1 {
		arg := strings.ToLower(strings.TrimSpace(os.Args[1]))
		return arg == "job-scheduler" || arg == "scheduler-manager"
	}
	return false
}

func schedulerHealthcheckMode() bool {
	role := strings.ToLower(strings.TrimSpace(os.Getenv("BOREALIS_PROCESS_ROLE")))
	if role == "job-scheduler-healthcheck" || role == "scheduler-healthcheck" {
		return true
	}
	for _, arg := range os.Args[1:] {
		normalized := strings.ToLower(strings.TrimSpace(arg))
		if normalized == "job-scheduler-healthcheck" || normalized == "scheduler-healthcheck" {
			return true
		}
	}
	return false
}

func loadConfig() (gatewayConfig, error) {
	return gatewayConfig{
		ListenHost:       envDefault("BOREALIS_GO_API_HOST", "127.0.0.1"),
		ListenPort:       envDefault("BOREALIS_GO_API_PORT", "5000"),
		DatabaseURL:      envDefault("BOREALIS_DATABASE_URL", ""),
		DBSSLMode:        envDefault("BOREALIS_DB_SSLMODE", "prefer"),
		DBConnectTimeout: envDurationSeconds("BOREALIS_DB_CONNECT_TIMEOUT", 15*time.Second),
		DBMaxOpenConns:   envInt("BOREALIS_GO_API_DB_MAX_OPEN_CONNS", 8, 1, 100),
		DBMaxIdleConns:   envInt("BOREALIS_GO_API_DB_MAX_IDLE_CONNS", 4, 0, 100),
		EngineSecretPath: envDefault("BOREALIS_ENGINE_SECRET_PATH", "/opt/Borealis/Engine/Services/api-backend/secrets/engine_secret.txt"),
		AuthTimeout:      envDurationSeconds("BOREALIS_GO_API_AUTH_TIMEOUT_SECONDS", 3*time.Second),
		AuthTokenTTL:     envDurationSeconds("BOREALIS_TOKEN_TTL_SECONDS", 30*24*time.Hour),
		ShutdownTimeout:  envDurationSeconds("BOREALIS_GO_API_SHUTDOWN_TIMEOUT_SECONDS", 20*time.Second),
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

func healthHandler(cfg gatewayConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "healthy",
			"backend": "go",
			"version": version,
			"listen":  net.JoinHostPort(cfg.ListenHost, cfg.ListenPort),
		})
	}
}

func statusHandler(cfg gatewayConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "running",
			"backend": "go",
			"version": version,
			"listen":  net.JoinHostPort(cfg.ListenHost, cfg.ListenPort),
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

func proxyFallbackOrMethodNotAllowed(w http.ResponseWriter, r *http.Request, fallback http.Handler, allowed string) {
	if fallback != nil {
		fallback.ServeHTTP(w, r)
		return
	}
	writeMethodNotAllowed(w, allowed)
}
