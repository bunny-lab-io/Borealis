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
	"github.com/bunny-lab-io/borealis/go-agent/internal/roles/deviceaudit"
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
	bootID     string
	dispatcher *currentuser.Dispatcher
	auditor    *deviceaudit.Auditor
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
	dispatcher := currentuser.NewDispatcher()
	auditor := deviceaudit.NewAuditor()
	return &Agent{
		options:    options,
		configPath: configPath,
		config:     cfg,
		authClient: authClient,
		logger:     logger,
		hostname:   hostname,
		bootID:     fmt.Sprintf("%d", time.Now().Unix()),
		dispatcher: dispatcher,
		auditor:    auditor,
	}, nil
}

func (a *Agent) Run(ctx context.Context) error {
	a.logger.Printf("agent starting service_mode=%s config=%s", auth.NormalizeServiceMode(a.options.ServiceMode), a.configPath)
	if err := a.authClient.EnsureAuthenticated(ctx); err != nil {
		_ = a.postStatus(context.Background(), "authenticating", "unhealthy", err.Error())
		return err
	}
	if err := a.postStatus(ctx, "status_channel_online", "healthy", "Status channel online."); err != nil {
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
	if err := a.postStatus(ctx, "socket_connecting", "healthy", "Engine socket connecting."); err != nil {
		a.logger.Printf("status post failed: %v", err)
	}
	headers := a.authClient.AuthHeaders()
	socket := transport.NewClient(a.authClient.BaseURL(), headers)
	role := systemcontext.New(socket, a.authClient, a.dispatcher)
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
		if a.dispatcher != nil && a.dispatcher.SupportsCurrentUserDispatch() {
			payload["helper_contexts"] = []string{"currentuser"}
			payload["capabilities"].(map[string]any)["helper_contexts"] = []string{"currentuser"}
		}
		if err := socket.Emit("connect_agent", payload); err != nil {
			return err
		}
		_ = a.postStatus(ctx, "steady_state_online", "healthy", "Agent steady state online.")
		return nil
	})
	return socket.Connect(ctx)
}

func (a *Agent) postStatus(ctx context.Context, phase string, status string, message string) error {
	now := time.Now().Unix()
	payload := map[string]any{
		"hostname":     a.hostname,
		"service_mode": auth.NormalizeServiceMode(a.options.ServiceMode),
		"phase":        phase,
		"status":       status,
		"message":      message,
		"boot_id":      a.bootID,
		"milestones":   startupMilestones(phase, status, message, now),
	}
	_, err := a.authClient.PostJSON(ctx, "/api/agent/status", payload, nil)
	return err
}

func (a *Agent) postHeartbeat(ctx context.Context) error {
	currentUserHealth := currentuser.RoleHealth{
		Status:     "unsupported",
		StatusCode: "unsupported",
		Detail:     "CURRENTUSER helper broker is unavailable.",
		Details: map[string]any{
			"running_status": "Unavailable",
			"runtime":        "go",
		},
	}
	if a.dispatcher != nil {
		currentUserHealth = a.dispatcher.RoleHealth()
	}
	auditSnapshot := deviceaudit.Snapshot{
		Inventory: map[string]any{},
		Metrics:   map[string]any{},
		Health: deviceaudit.RoleHealth{
			Status:     "recovering",
			StatusCode: "recovering",
			Detail:     "Device audit inventory has not run.",
			Details: map[string]any{
				"running_status": "Starting",
				"runtime":        "go",
			},
		},
	}
	if a.auditor != nil {
		auditSnapshot = a.auditor.Collect(ctx)
	}
	metrics := map[string]any{
		"service_mode":     auth.NormalizeServiceMode(a.options.ServiceMode),
		"operating_system": operatingSystemName(),
	}
	for key, value := range auditSnapshot.Metrics {
		if value != nil && fmt.Sprint(value) != "" {
			metrics[key] = value
		}
	}
	payload := map[string]any{
		"hostname":     a.hostname,
		"service_mode": auth.NormalizeServiceMode(a.options.ServiceMode),
		"metrics":      metrics,
		"inventory":    auditSnapshot.Inventory,
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
					"status":          currentUserHealth.Status,
					"status_code":     currentUserHealth.StatusCode,
					"detail":          currentUserHealth.Detail,
					"last_checked_at": time.Now().Unix(),
					"details":         currentUserHealth.Details,
				},
				{
					"role_id":         "system:device_auditor",
					"role_name":       "device_auditor",
					"role_label":      "Device Auditor",
					"context":         "system",
					"status":          auditSnapshot.Health.Status,
					"status_code":     auditSnapshot.Health.StatusCode,
					"detail":          auditSnapshot.Health.Detail,
					"last_checked_at": time.Now().Unix(),
					"details":         auditSnapshot.Health.Details,
				},
			},
		},
	}
	if auditSnapshot.InternalIP != "" {
		payload["internal_ip"] = auditSnapshot.InternalIP
	}
	if auditSnapshot.DeviceType != "" {
		payload["device_type"] = auditSnapshot.DeviceType
	}
	cfg := a.authClient.Config()
	if cfg.Agent.AgentID != "" {
		payload["agent_id"] = cfg.Agent.AgentID
	}
	_, err := a.authClient.PostJSON(ctx, "/api/agent/heartbeat", payload, nil)
	return err
}

type startupMilestoneDefinition struct {
	Key   string
	Label string
}

var startupMilestoneDefinitions = []startupMilestoneDefinition{
	{Key: "process_start", Label: "Agent process started"},
	{Key: "server_config_loaded", Label: "Server configuration loaded"},
	{Key: "identity_loaded", Label: "Device identity loaded"},
	{Key: "authenticating", Label: "Engine authentication started"},
	{Key: "authenticated", Label: "Engine authentication complete"},
	{Key: "roles_loading", Label: "Agent role loading"},
	{Key: "roles_ready", Label: "Agent roles ready"},
	{Key: "status_channel_online", Label: "Status channel online"},
	{Key: "socket_connecting", Label: "Engine socket connecting"},
	{Key: "socket_connected", Label: "Engine socket connected"},
	{Key: "steady_state_online", Label: "Agent steady state online"},
}

func startupMilestones(phase string, status string, message string, timestamp int64) []map[string]any {
	normalizedPhase := strings.ToLower(strings.TrimSpace(phase))
	if normalizedPhase == "socket_connected" {
		normalizedPhase = "steady_state_online"
	}
	phaseRank := len(startupMilestoneDefinitions) - 1
	for index, definition := range startupMilestoneDefinitions {
		if definition.Key == normalizedPhase {
			phaseRank = index
			break
		}
	}
	failed := strings.EqualFold(strings.TrimSpace(status), "unhealthy") || strings.EqualFold(strings.TrimSpace(status), "failed")
	out := make([]map[string]any, 0, len(startupMilestoneDefinitions))
	for index, definition := range startupMilestoneDefinitions {
		state := "pending"
		if index < phaseRank {
			state = "complete"
		} else if index == phaseRank {
			if failed {
				state = "failed"
			} else if definition.Key == "steady_state_online" {
				state = "complete"
			} else {
				state = "active"
			}
		}
		if normalizedPhase == "steady_state_online" && index <= phaseRank {
			state = "complete"
		}
		item := map[string]any{
			"key":   definition.Key,
			"label": definition.Label,
			"state": state,
		}
		if state == "complete" || state == "active" || state == "failed" {
			item["updated_at"] = timestamp
		}
		if state == "complete" {
			item["completed_at"] = timestamp
		}
		if state == "active" {
			item["started_at"] = timestamp
		}
		if state == "failed" {
			item["detail"] = message
		}
		out = append(out, item)
	}
	return out
}
