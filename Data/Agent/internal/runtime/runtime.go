package agentruntime

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	"github.com/bunny-lab-io/borealis/go-agent/internal/roles/currentuser"
	"github.com/bunny-lab-io/borealis/go-agent/internal/roles/systemcontext"
	"github.com/bunny-lab-io/borealis/go-agent/internal/transport"
)

type Options struct {
	ConfigPath     string
	ServerURL      string
	EnrollmentCode string
	ServiceMode    string
	Once           bool
	Verbose        bool
}

type Agent struct {
	options    Options
	configPath string
	config     agentconfig.AgentConfig
	authClient *auth.Client
	logger     *log.Logger
	hostname   string
}

func New(options Options, logger *log.Logger) (*Agent, error) {
	configPath := strings.TrimSpace(options.ConfigPath)
	if configPath == "" {
		resolved, err := agentconfig.PathFromBinary()
		if err != nil {
			return nil, err
		}
		configPath = resolved
	}
	cfg, err := agentconfig.LoadOrCreate(configPath)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(options.ServerURL) != "" {
		cfg.ServerURL = agentconfig.NormalizeServerURL(options.ServerURL)
	}
	if strings.TrimSpace(options.EnrollmentCode) != "" {
		cfg.EnrollmentCode = strings.TrimSpace(options.EnrollmentCode)
	}
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = log.New(os.Stdout, "", log.LstdFlags)
	}
	hostname, _ := os.Hostname()
	authClient, err := auth.NewClient(configPath, &cfg, options.ServiceMode, auth.WithHostname(hostname))
	if err != nil {
		return nil, err
	}
	return &Agent{
		options:    options,
		configPath: configPath,
		config:     cfg,
		authClient: authClient,
		logger:     logger,
		hostname:   hostname,
	}, nil
}

func (a *Agent) Run(ctx context.Context) error {
	a.logger.Printf("agent starting service_mode=%s config=%s", auth.NormalizeServiceMode(a.options.ServiceMode), a.configPath)
	if err := a.authClient.EnsureAuthenticated(ctx); err != nil {
		_ = a.postStatus(context.Background(), "auth_failed", "unhealthy", err.Error())
		return err
	}
	if err := a.postStatus(ctx, "auth_complete", "healthy", "Agent authenticated."); err != nil {
		a.logger.Printf("status post failed: %v", err)
	}
	if err := a.postHeartbeat(ctx); err != nil {
		a.logger.Printf("heartbeat failed: %v", err)
	}
	if a.options.Once {
		return nil
	}
	go a.heartbeatLoop(ctx)
	return a.socketLoop(ctx)
}

func (a *Agent) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.postHeartbeat(ctx); err != nil {
				a.logger.Printf("heartbeat failed: %v", err)
			}
		}
	}
}

func (a *Agent) socketLoop(ctx context.Context) error {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := a.connectSocket(ctx); err != nil {
			a.logger.Printf("socket disconnected: %v", err)
			if err := a.postStatus(context.Background(), "socket_disconnected", "degraded", err.Error()); err != nil {
				a.logger.Printf("status post failed: %v", err)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (a *Agent) connectSocket(ctx context.Context) error {
	headers := a.authClient.AuthHeaders()
	socket := transport.NewClient(a.authClient.BaseURL(), headers)
	dispatcher := currentuser.NewDispatcher()
	role := systemcontext.New(socket, a.authClient, dispatcher)
	role.Hostname = a.hostname
	socket.On("quick_job_run", role.HandleQuickJob)
	socket.OnConnected(func(ctx context.Context) error {
		payload := map[string]any{
			"agent_id":     a.authClient.AgentID(),
			"service_mode": auth.NormalizeServiceMode(a.options.ServiceMode),
			"hostname":     a.hostname,
			"capabilities": map[string]any{
				"runtime":           "go",
				"system_scripts":    true,
				"python_fallback":   false,
				"linux_currentuser": false,
			},
		}
		if dispatcher.SupportsCurrentUserDispatch() {
			payload["helper_contexts"] = []string{"currentuser"}
			payload["capabilities"].(map[string]any)["helper_contexts"] = []string{"currentuser"}
		}
		if err := socket.Emit("connect_agent", payload); err != nil {
			return err
		}
		_ = a.postStatus(ctx, "socket_connected", "healthy", "Agent socket connected.")
		return nil
	})
	return socket.Connect(ctx)
}

func (a *Agent) postStatus(ctx context.Context, phase string, status string, message string) error {
	payload := map[string]any{
		"hostname":     a.hostname,
		"service_mode": auth.NormalizeServiceMode(a.options.ServiceMode),
		"phase":        phase,
		"status":       status,
		"message":      message,
		"boot_id":      bootID(),
		"milestones": []map[string]any{
			{"name": phase, "status": status, "message": message, "completed_at": time.Now().Unix()},
		},
	}
	_, err := a.authClient.PostJSON(ctx, "/api/agent/status", payload, nil)
	return err
}

func (a *Agent) postHeartbeat(ctx context.Context) error {
	payload := map[string]any{
		"hostname":     a.hostname,
		"service_mode": auth.NormalizeServiceMode(a.options.ServiceMode),
		"metrics": map[string]any{
			"service_mode":     auth.NormalizeServiceMode(a.options.ServiceMode),
			"operating_system": operatingSystemName(),
		},
		"agent_role_health": map[string]any{
			"roles": []map[string]any{
				{
					"role_id":         "system:context_system",
					"role_name":       "context_system",
					"role_label":      "SYSTEM Context",
					"context":         "system",
					"status":          "healthy",
					"status_code":     "healthy",
					"detail":          "Go SYSTEM script listener is ready.",
					"last_checked_at": time.Now().Unix(),
					"details": map[string]any{
						"running_status":    "Ready",
						"execution_context": "SYSTEM",
						"runtime":           "go",
					},
				},
				{
					"role_id":         "system:context_currentuser",
					"role_name":       "context_currentuser",
					"role_label":      "Current User Context",
					"context":         "currentuser",
					"status":          "degraded",
					"status_code":     "degraded",
					"detail":          "Go CURRENTUSER helper broker is pending migration.",
					"last_checked_at": time.Now().Unix(),
					"details": map[string]any{
						"running_status": "Pending Migration",
						"runtime":        "go",
					},
				},
			},
		},
	}
	cfg := a.authClient.Config()
	if cfg.Agent.AgentID != "" {
		payload["agent_id"] = cfg.Agent.AgentID
	}
	_, err := a.authClient.PostJSON(ctx, "/api/agent/heartbeat", payload, nil)
	return err
}

func bootID() string {
	return fmt.Sprintf("%d", time.Now().Unix())
}
