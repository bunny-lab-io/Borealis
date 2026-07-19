package agentruntime

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	"github.com/bunny-lab-io/borealis/go-agent/internal/localui"
	"github.com/bunny-lab-io/borealis/go-agent/internal/roles/current_user"
	"github.com/bunny-lab-io/borealis/go-agent/internal/roles/device_audit"
	"github.com/bunny-lab-io/borealis/go-agent/internal/roles/file_management"
	patchmanagement "github.com/bunny-lab-io/borealis/go-agent/internal/roles/patch_management"
	"github.com/bunny-lab-io/borealis/go-agent/internal/roles/process_management"
	"github.com/bunny-lab-io/borealis/go-agent/internal/roles/registry_management"
	"github.com/bunny-lab-io/borealis/go-agent/internal/roles/remote_shell"
	"github.com/bunny-lab-io/borealis/go-agent/internal/roles/service_management"
	"github.com/bunny-lab-io/borealis/go-agent/internal/roles/software_management"
	"github.com/bunny-lab-io/borealis/go-agent/internal/roles/system_context"
	vncrole "github.com/bunny-lab-io/borealis/go-agent/internal/roles/vnc"
	"github.com/bunny-lab-io/borealis/go-agent/internal/roles/wireguard_tunnel"
	"github.com/bunny-lab-io/borealis/go-agent/internal/transport"
)

type Options struct {
	ConfigPath         string
	ServerURL          string
	ServerIPFallback   string
	EnrollmentCode     string
	RepoRef            string
	ReleaseChannel     string
	ServiceMode        string
	TrustedEngineCAPEM string
	TrustedEngineCAB64 string
	BuildID            string
	Once               bool
	Verbose            bool
}

type Agent struct {
	options       Options
	configPath    string
	config        agentconfig.AgentConfig
	authClient    *auth.Client
	logger        *log.Logger
	hostname      string
	bootID        string
	dispatcher    *currentuser.Dispatcher
	auditor       *deviceaudit.Auditor
	files         *filemanagement.Manager
	patches       *patchmanagement.Manager
	processes     *processmanagement.Manager
	registry      *registrymanagement.Manager
	remoteShell   *remoteshell.Manager
	services      *servicemanagement.Manager
	software      *softwaremanagement.Manager
	vnc           *vncrole.Manager
	wireguard     *wireguardtunnel.Manager
	supervisor    *RoleSupervisor
	uiMu          sync.RWMutex
	uiSnapshot    localui.StatusSnapshot
	auditMu       sync.Mutex
	auditCache    deviceaudit.Snapshot
	auditAt       time.Time
	roleMu        sync.Mutex
	roleStates    map[string]string
	roleDetails   map[string]string
	socketMu      sync.RWMutex
	socketState   string
	socketStateAt int64
}

type heartbeatResponse struct {
	MetadataFieldAcks []string `json:"metadata_field_acks"`
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
		if err := agentconfig.ValidateServerURLForEnrollment(options.ServerURL); err != nil {
			return nil, err
		}
		cfg.ServerURL = agentconfig.NormalizeServerURL(options.ServerURL)
	}
	if strings.TrimSpace(options.ServerIPFallback) != "" {
		if err := agentconfig.ValidateServerIPFallback(options.ServerIPFallback); err != nil {
			return nil, err
		}
		cfg.ServerIPFallback = agentconfig.NormalizeServerIPFallback(options.ServerIPFallback)
	}
	if strings.TrimSpace(options.TrustedEngineCAB64) != "" {
		pemText, err := agentconfig.DecodeEngineCAB64(options.TrustedEngineCAB64)
		if err != nil {
			return nil, err
		}
		cfg.Trust.EngineCAPEM = pemText
	}
	if strings.TrimSpace(options.TrustedEngineCAPEM) != "" {
		cfg.Trust.EngineCAPEM = agentconfig.NormalizeEngineCAPEM(options.TrustedEngineCAPEM)
	}
	if strings.TrimSpace(options.EnrollmentCode) != "" {
		cfg.EnrollmentCode = strings.TrimSpace(options.EnrollmentCode)
	}
	if strings.TrimSpace(options.RepoRef) != "" {
		cfg.Agent.Branch = agentconfig.NormalizeBranch(options.RepoRef)
		cfg.Agent.ReleaseChannel = agentconfig.ReleaseChannelForBranch(cfg.Agent.Branch)
	}
	if strings.TrimSpace(options.ReleaseChannel) != "" {
		cfg.Agent.ReleaseChannel = agentconfig.NormalizeReleaseChannel(options.ReleaseChannel)
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
	fileManager := filemanagement.New(authClient, hostname)
	patchManager := patchmanagement.New(authClient, hostname, options.ServiceMode)
	processManager := processmanagement.New(hostname)
	registryManager := registrymanagement.New(hostname, options.ServiceMode)
	remoteShellManager := remoteshell.New(hostname, options.ServiceMode, configPath)
	serviceManager := servicemanagement.New(authClient, hostname, options.ServiceMode)
	softwareManager := softwaremanagement.New(authClient, hostname, options.ServiceMode)
	vncManager := vncrole.New(authClient, hostname, options.ServiceMode, configPath)
	wireGuardManager := wireguardtunnel.New(authClient, hostname, options.ServiceMode, configPath)
	agent := &Agent{
		options:     options,
		configPath:  configPath,
		config:      cfg,
		authClient:  authClient,
		logger:      logger,
		hostname:    hostname,
		bootID:      fmt.Sprintf("%d", time.Now().Unix()),
		dispatcher:  dispatcher,
		auditor:     auditor,
		files:       fileManager,
		patches:     patchManager,
		processes:   processManager,
		registry:    registryManager,
		remoteShell: remoteShellManager,
		services:    serviceManager,
		software:    softwareManager,
		vnc:         vncManager,
		wireguard:   wireGuardManager,
		supervisor:  NewRoleSupervisor(nil),
		roleStates:  map[string]string{},
		roleDetails: map[string]string{},
	}
	agent.supervisor.logRecovery = agent.logRecovery
	agent.registerRoleRecoveryHandlers()
	if agent.wireguard != nil {
		agent.wireguard.SetStatusReporter(agent.postWireGuardStatus)
	}
	return agent, nil
}

func (a *Agent) registerRoleRecoveryHandlers() {
	if a == nil || a.supervisor == nil {
		return
	}
	if a.vnc != nil {
		a.supervisor.RegisterRecoveryHandler("system:vnc", func(snapshot RoleSnapshot) {
			a.logRecovery("role_supervisor", snapshot.RoleID, "recover", "start", "vnc_ensure", nil)
			a.vnc.RequestEnsure("role_supervisor_recovery")
		})
	}
	if a.wireguard != nil {
		a.supervisor.RegisterRecoveryHandler("system:wireguard_tunnel", func(snapshot RoleSnapshot) {
			a.logRecovery("role_supervisor", snapshot.RoleID, "recover", "start", "wireguard_ensure", nil)
			a.wireguard.RequestEnsure("role_supervisor_recovery")
		})
	}
	if a.remoteShell != nil {
		a.supervisor.RegisterRecoveryHandler("system:remote_shell", func(snapshot RoleSnapshot) {
			a.logRecovery("role_supervisor", snapshot.RoleID, "recover", "start", "remote_shell_restart", nil)
			a.remoteShell.Restart(context.Background(), "role_supervisor_recovery")
		})
	}
}

func (a *Agent) Run(ctx context.Context) error {
	a.logger.Printf("agent starting service_mode=%s config=%s", auth.NormalizeServiceMode(a.options.ServiceMode), a.configPath)
	a.updateUIStatus("process_start", "starting", "Agent process starting.")
	a.startLivenessLoop(ctx.Done())
	if err := writeInstalledBuildID(a.configPath, a.options.BuildID); err != nil {
		a.logger.Printf("record installed build failed: %v", err)
	}
	if err := cleanupStartupTemp(a.configPath, a.logger); err != nil {
		a.logger.Printf("startup temp cleanup failed: %v", err)
	}
	a.startTrayBridgeIfSupported(ctx)
	if a.dispatcher != nil {
		a.dispatcher.Start(ctx, a.configPath)
	}
	if err := a.waitAuthenticated(ctx); err != nil {
		return err
	}
	if a.processes != nil {
		a.processes.Start(ctx)
	}
	if a.services != nil {
		a.services.Start(ctx)
	}
	if a.patches != nil {
		a.patches.Start(ctx)
	}
	if a.software != nil {
		a.software.Start(ctx)
	}
	if a.wireguard != nil {
		a.wireguard.Start(ctx)
	}
	if a.vnc != nil {
		a.vnc.Start(ctx)
		defer a.vnc.Stop(context.Background())
	}
	if a.remoteShell != nil {
		a.remoteShell.Start(ctx)
		defer a.remoteShell.Stop(context.Background())
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

func (a *Agent) waitAuthenticated(ctx context.Context) error {
	backoff := 5 * time.Second
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		a.updateUIStatus("authenticating", "recovering", "Engine authentication starting.")
		if err := a.authClient.EnsureAuthenticated(ctx); err != nil {
			a.logger.Printf("authentication failed; retrying: %v", err)
			a.logRecovery("runtime", "startup:auth", "authenticate", "retry", "auth_failed", err)
			a.updateUIStatus("authenticating", "recovering", err.Error())
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			if backoff < time.Minute {
				backoff *= 2
				if backoff > time.Minute {
					backoff = time.Minute
				}
			}
			continue
		}
		a.updateUIStatus("authenticated", "healthy", "Engine authentication complete.")
		a.logRecovery("runtime", "startup:auth", "authenticate", "success", "authenticated", nil)
		return nil
	}
}

func (a *Agent) heartbeatLoop(ctx context.Context) {
	for {
		waitFor := heartbeatIntervalWithJitter(time.Now())
		timer := time.NewTimer(waitFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if err := a.postHeartbeat(ctx); err != nil {
				a.logger.Printf("heartbeat failed: %v", err)
			}
		}
	}
}

func heartbeatIntervalWithJitter(now time.Time) time.Duration {
	base := 20 * time.Second
	jitter := time.Duration(now.UnixNano() % int64(5*time.Second))
	return base + jitter
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
			a.recordSocketState("disconnected")
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
	a.recordSocketState("connecting")
	if err := a.postStatus(ctx, "socket_connecting", "healthy", "Site-worker socket connecting."); err != nil {
		a.logger.Printf("status post failed: %v", err)
	}
	if err := a.authClient.RefreshRemoteOpsRoute(ctx); err != nil {
		a.logger.Printf("remote ops route refresh failed: %v", err)
	}
	socketBaseURL := a.authClient.RemoteOpsBaseURL()
	if socketBaseURL == "" {
		return fmt.Errorf("site-worker remote ops route unavailable")
	}
	a.logger.Printf("socket connecting route=%s", socketBaseURL)
	headers := a.authClient.AuthHeaders()
	socket := transport.NewClient(socketBaseURL, headers, transport.WithTLSConfig(a.authClient.TLSConfig()), transport.WithDialContext(a.authClient.DialContext()))
	socket.OnActivity(a.recordConnectedSocketActivity)
	role := systemcontext.New(socket, a.authClient, a.dispatcher)
	role.Hostname = a.hostname
	if a.software != nil {
		role.SoftwareRefresh = a.software
	}
	socket.On("quick_job_run", role.HandleQuickJob)
	if a.files != nil {
		socket.On("file_management_request", a.files.HandleRequest)
	}
	if a.processes != nil {
		socket.On("process_management_request", a.processes.HandleRequest)
	}
	if a.registry != nil {
		socket.On("registry_management_request", a.registry.HandleRequest)
	}
	if a.services != nil {
		socket.On("service_control_action", a.services.HandleControlAction)
	}
	if a.patches != nil {
		socket.On("patch_inventory_refresh_request", a.patches.HandleRefreshRequest)
		socket.On("patch_install_request", a.patches.HandleInstallRequest)
		socket.On("patch_policy_enforcement_request", a.patches.HandlePolicyEnforcementRequest)
		socket.On("patch_reboot_request", a.patches.HandleRebootRequest)
	}
	if a.software != nil {
		socket.On("software_inventory_refresh_request", a.software.HandleRefreshRequest)
	}
	if a.wireguard != nil {
		socket.On("vpn_tunnel_start", a.wireguard.HandleStart)
		socket.On("vpn_tunnel_stop", a.wireguard.HandleStop)
		socket.On("vpn_tunnel_activity", a.wireguard.HandleActivity)
	}
	if a.remoteShell != nil {
		socket.On("remote_shell_restart", a.handleRemoteShellRestart)
	}
	if a.vnc != nil {
		socket.On("vnc_start", a.vnc.HandleStart)
		socket.On("vnc_stop", a.vnc.HandleStop)
		socket.On("vnc_refresh", a.vnc.HandleRefresh)
		socket.On("vnc_credential_request", a.vnc.HandleCredentialRequest)
	}
	socket.On("agent_update_request", a.handleUpdateRequest)
	socket.On("agent_release_channel_changed", a.handleReleaseChannelChanged)
	socket.On("agent_maintenance_request", a.handleAgentMaintenanceRequest)
	socket.OnConnected(func(ctx context.Context) error {
		payload := map[string]any{
			"agent_id":     a.authClient.AgentID(),
			"service_mode": auth.NormalizeServiceMode(a.options.ServiceMode),
			"hostname":     a.hostname,
			"capabilities": map[string]any{
				"runtime":             "go",
				"system_scripts":      true,
				"file_management":     true,
				"patch_management":    true,
				"process_management":  true,
				"registry_management": true,
				"remote_shell":        true,
				"service_management":  true,
				"software_management": true,
				"vnc":                 true,
				"wireguard_tunnel":    true,
				"python_fallback":     false,
				"linux_currentuser":   false,
			},
		}
		if a.dispatcher != nil && a.dispatcher.SupportsCurrentUserDispatch() {
			payload["helper_contexts"] = []string{"currentuser"}
			payload["capabilities"].(map[string]any)["helper_contexts"] = []string{"currentuser"}
		}
		if err := socket.Emit("connect_agent", payload); err != nil {
			return err
		}
		a.recordSocketState("connected")
		if a.wireguard != nil {
			a.wireguard.RequestEnsure("socket_connect")
		}
		_ = a.postStatus(ctx, "steady_state_online", "healthy", "Agent steady state online.")
		return nil
	})
	if err := socket.Connect(ctx); err != nil {
		if ctx.Err() == nil {
			refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if refreshErr := a.authClient.RefreshRemoteOpsRoute(refreshCtx); refreshErr != nil {
				a.logger.Printf("remote ops route refresh after socket disconnect failed: %v", refreshErr)
			}
		}
		return err
	}
	return nil
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
	a.updateUIStatus(phase, status, message)
	_, err := a.authClient.PostJSON(ctx, "/api/agent/status", payload, nil)
	return err
}

func (a *Agent) postWireGuardStatus(ctx context.Context, phase string, status string, message string) error {
	err := a.postStatus(ctx, phase, status, message)
	switch strings.TrimSpace(phase) {
	case "wireguard_starting", "wireguard_online":
		go func() {
			heartbeatCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			if heartbeatErr := a.postHeartbeat(heartbeatCtx); heartbeatErr != nil {
				a.logger.Printf("wireguard heartbeat refresh failed: %v", heartbeatErr)
			}
		}()
	}
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
	auditSnapshot := a.cachedAuditSnapshot(ctx)
	fileHealth := filemanagement.RoleHealth{
		Status:     "unsupported",
		StatusCode: "unsupported",
		Detail:     "File Management role is unavailable.",
		Details: map[string]any{
			"running_status": "Unavailable",
			"runtime":        "go",
		},
	}
	if a.files != nil {
		fileHealth = a.files.Health()
	}
	processHealth := processmanagement.RoleHealth{
		Status:     "unsupported",
		StatusCode: "unsupported",
		Detail:     "Process Management role is unavailable.",
		Details: map[string]any{
			"running_status": "Unavailable",
			"runtime":        "go",
		},
	}
	if a.processes != nil {
		processHealth = a.processes.Health()
	}
	registryHealth := registrymanagement.RoleHealth{
		Status:     "unsupported",
		StatusCode: "unsupported",
		Detail:     "Registry Management role is unavailable.",
		Details: map[string]any{
			"running_status": "Unavailable",
			"runtime":        "go",
		},
	}
	if a.registry != nil {
		registryHealth = a.registry.Health()
	}
	remoteShellHealth := remoteshell.RoleHealth{
		Status:     "unsupported",
		StatusCode: "unsupported",
		Detail:     "Remote Shell role is unavailable.",
		Details: map[string]any{
			"running_status": "Unavailable",
			"runtime":        "go",
		},
	}
	if a.remoteShell != nil {
		remoteShellHealth = a.remoteShell.Health()
	}
	serviceHealth := servicemanagement.RoleHealth{
		Status:     "unsupported",
		StatusCode: "unsupported",
		Detail:     "Service Management role is unavailable.",
		Details: map[string]any{
			"running_status": "Unavailable",
			"runtime":        "go",
		},
	}
	if a.services != nil {
		serviceHealth = a.services.Health()
	}
	patchHealth := patchmanagement.RoleHealth{
		Status:     "unsupported",
		StatusCode: "unsupported",
		Detail:     "Patch Management role is unavailable.",
		Details: map[string]any{
			"running_status": "Unavailable",
			"runtime":        "go",
		},
	}
	if a.patches != nil {
		patchHealth = a.patches.Health()
	}
	softwareHealth := softwaremanagement.RoleHealth{
		Status:     "unsupported",
		StatusCode: "unsupported",
		Detail:     "Software Management role is unavailable.",
		Details: map[string]any{
			"running_status": "Unavailable",
			"runtime":        "go",
		},
	}
	if a.software != nil {
		softwareHealth = a.software.Health()
	}
	vncHealth := vncrole.RoleHealth{
		Status:     "unsupported",
		StatusCode: "unsupported",
		Detail:     "VNC role is unavailable.",
		Details: map[string]any{
			"running_status": "Unavailable",
			"runtime":        "go",
		},
	}
	if a.vnc != nil {
		vncHealth = a.vnc.Health()
	}
	wireGuardHealth := wireguardtunnel.RoleHealth{
		Status:     "unsupported",
		StatusCode: "unsupported",
		Detail:     "WireGuard tunnel role is unavailable.",
		Details: map[string]any{
			"running_status": "Unavailable",
			"runtime":        "go",
		},
	}
	if a.wireguard != nil {
		wireGuardHealth = a.wireguard.Health()
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
	now := time.Now().Unix()
	if a.supervisor == nil {
		a.supervisor = NewRoleSupervisor(a.logRecovery)
	}
	roleHealthPayload := a.supervisor.Update([]RoleSnapshot{
		{
			RoleID:     "system:context_system",
			RoleName:   "context_system",
			RoleLabel:  "SYSTEM Context",
			Context:    "system",
			Status:     "healthy",
			StatusCode: "healthy",
			Detail:     "Go SYSTEM script listener is ready.",
			Details: map[string]any{
				"running_status":    "Ready",
				"execution_context": "SYSTEM",
				"runtime":           "go",
			},
			CheckedAt: now,
		},
		a.engineSocketRoleSnapshot(now),
		roleSnapshotFromHealth("system:context_currentuser", "context_currentuser", "Current User Context", "currentuser", currentUserHealth.Status, currentUserHealth.StatusCode, currentUserHealth.Detail, currentUserHealth.Details, now),
		roleSnapshotFromHealth("system:device_auditor", "device_auditor", "Device Auditor", "system", auditSnapshot.Health.Status, auditSnapshot.Health.StatusCode, auditSnapshot.Health.Detail, auditSnapshot.Health.Details, now),
		roleSnapshotFromHealth("system:file_management", "file_management", "File Management", "system", fileHealth.Status, fileHealth.StatusCode, fileHealth.Detail, fileHealth.Details, now),
		roleSnapshotFromHealth("system:process_management", "process_management", "Process Management", "system", processHealth.Status, processHealth.StatusCode, processHealth.Detail, processHealth.Details, now),
		roleSnapshotFromHealth("system:registry_management", "registry_management", "Registry Management", "system", registryHealth.Status, registryHealth.StatusCode, registryHealth.Detail, registryHealth.Details, now),
		roleSnapshotFromHealth("system:remote_shell", "remote_shell", "Remote Shell", "system", remoteShellHealth.Status, remoteShellHealth.StatusCode, remoteShellHealth.Detail, remoteShellHealth.Details, now),
		roleSnapshotFromHealth("system:service_management", "service_management", "Service Management", "system", serviceHealth.Status, serviceHealth.StatusCode, serviceHealth.Detail, serviceHealth.Details, now),
		roleSnapshotFromHealth("system:patch_management", "patch_management", "Patch Management", "system", patchHealth.Status, patchHealth.StatusCode, patchHealth.Detail, patchHealth.Details, now),
		roleSnapshotFromHealth("system:software_management", "software_management", "Software Management", "system", softwareHealth.Status, softwareHealth.StatusCode, softwareHealth.Detail, softwareHealth.Details, now),
		roleSnapshotFromHealth("system:vnc", "vnc", "UltraVNC Service", "system", vncHealth.Status, vncHealth.StatusCode, vncHealth.Detail, vncHealth.Details, now),
		roleSnapshotFromHealth("system:wireguard_tunnel", "wireguard_tunnel", "WireGuard VPN", "system", wireGuardHealth.Status, wireGuardHealth.StatusCode, wireGuardHealth.Detail, wireGuardHealth.Details, now),
	})
	payload := map[string]any{
		"hostname":          a.hostname,
		"service_mode":      auth.NormalizeServiceMode(a.options.ServiceMode),
		"metrics":           metrics,
		"inventory":         auditSnapshot.Inventory,
		"agent_build_id":    strings.TrimSpace(a.options.BuildID),
		"agent_role_health": roleHealthPayload,
	}
	if auditSnapshot.InternalIP != "" {
		payload["internal_ip"] = auditSnapshot.InternalIP
	}
	if auditSnapshot.DeviceType != "" {
		payload["device_type"] = auditSnapshot.DeviceType
	}
	cfg := a.authClient.Config()
	if diskCfg, err := agentconfig.Load(a.configPath); err == nil {
		cfg = diskCfg
	}
	if cfg.Agent.AgentID != "" {
		payload["agent_id"] = cfg.Agent.AgentID
	}
	if queuedMetadataFields, queueErr := agentconfig.LoadQueuedMetadataFields(a.configPath); queueErr != nil {
		a.logger.Printf("metadata queue load failed: %v", queueErr)
	} else if len(queuedMetadataFields) > 0 {
		payload["metadata_fields"] = queuedMetadataFields
	}
	installedBuildID := agentconfig.NormalizeBuildID(cfg.Agent.InstalledBuildID)
	if installedBuildID == "" {
		installedBuildID = agentconfig.NormalizeBuildID(a.options.BuildID)
	}
	payload["installed_build_id"] = installedBuildID
	payload["agent_release_channel"] = agentconfig.NormalizeReleaseChannel(cfg.Agent.ReleaseChannel)
	payload["agent_branch"] = agentconfig.NormalizeBranch(cfg.Agent.Branch)
	if strings.TrimSpace(cfg.Agent.Update.OperationID) != "" {
		payload["agent_update_status"] = map[string]any{
			"operation_id":     cfg.Agent.Update.OperationID,
			"kind":             cfg.Agent.Update.Kind,
			"state":            cfg.Agent.Update.Status,
			"target_channel":   cfg.Agent.Update.TargetChannel,
			"target_branch":    cfg.Agent.Update.TargetBranch,
			"previous_channel": cfg.Agent.Update.PreviousChannel,
			"previous_branch":  cfg.Agent.Update.PreviousBranch,
			"started_at":       cfg.Agent.Update.StartedAt,
			"updated_at":       cfg.Agent.Update.UpdatedAt,
			"completed_at":     cfg.Agent.Update.CompletedAt,
			"deadline_at":      cfg.Agent.Update.DeadlineAt,
			"last_error":       cfg.Agent.Update.LastError,
		}
	}
	a.updateUIHeartbeat(payload)
	a.recordHeartbeatAttempt()
	var response heartbeatResponse
	_, err := a.authClient.PostJSON(ctx, "/api/agent/heartbeat", payload, &response)
	if err == nil {
		if syncErr := agentconfig.AckQueuedMetadataFields(a.configPath, response.MetadataFieldAcks); syncErr != nil {
			a.logger.Printf("metadata queue ack failed: %v", syncErr)
		}
	}
	a.recordHeartbeatResult(err)
	return err
}

func roleSnapshotFromHealth(roleID string, roleName string, roleLabel string, contextLabel string, status string, statusCode string, detail string, details map[string]any, checkedAt int64) RoleSnapshot {
	return RoleSnapshot{
		RoleID:     roleID,
		RoleName:   roleName,
		RoleLabel:  roleLabel,
		Context:    contextLabel,
		Status:     status,
		StatusCode: statusCode,
		Detail:     detail,
		Details:    details,
		CheckedAt:  checkedAt,
	}
}

func (a *Agent) cachedAuditSnapshot(ctx context.Context) deviceaudit.Snapshot {
	fallback := deviceaudit.Snapshot{
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
	if a.auditor == nil {
		return fallback
	}
	a.auditMu.Lock()
	if !a.auditAt.IsZero() && time.Since(a.auditAt) < 5*time.Minute {
		snapshot := a.auditCache
		a.auditMu.Unlock()
		return snapshot
	}
	a.auditMu.Unlock()
	snapshot := a.auditor.Collect(ctx)
	a.auditMu.Lock()
	a.auditCache = snapshot
	a.auditAt = time.Now()
	a.auditMu.Unlock()
	return snapshot
}

func (a *Agent) recordRoleHealthTransitions(payload map[string]any) {
	rawHealth, _ := payload["agent_role_health"].(map[string]any)
	rawRoles, _ := rawHealth["roles"].([]map[string]any)
	if len(rawRoles) == 0 {
		if anyRoles, ok := rawHealth["roles"].([]any); ok {
			for _, item := range anyRoles {
				if mapped, ok := item.(map[string]any); ok {
					a.recordOneRoleHealth(mapped)
				}
			}
		}
		return
	}
	for _, role := range rawRoles {
		a.recordOneRoleHealth(role)
	}
}

func (a *Agent) recordOneRoleHealth(role map[string]any) {
	roleID := strings.TrimSpace(fmt.Sprint(role["role_id"]))
	status := strings.TrimSpace(fmt.Sprint(firstNonNil(role["status_code"], role["status"])))
	detail := strings.TrimSpace(fmt.Sprint(role["detail"]))
	if roleID == "" || status == "" {
		return
	}
	a.roleMu.Lock()
	if a.roleStates == nil {
		a.roleStates = map[string]string{}
	}
	if a.roleDetails == nil {
		a.roleDetails = map[string]string{}
	}
	previous := a.roleStates[roleID]
	previousDetail := a.roleDetails[roleID]
	if previous != status {
		a.roleStates[roleID] = status
	}
	if previousDetail != detail {
		a.roleDetails[roleID] = detail
	}
	a.roleMu.Unlock()
	if previous != "" && previous != status {
		a.logRecovery("role_supervisor", roleID, "status_transition", status, detail, nil)
	}
	if (status == "unhealthy" || status == "recovering" || status == "failed") && (previous != status || previousDetail != detail) {
		a.logRecovery("role_supervisor", roleID, "health_check", status, detail, nil)
	}
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return ""
}

func (a *Agent) engineSocketRoleSnapshot(now int64) RoleSnapshot {
	socketState, socketStateAt := a.currentSocketState()
	normalizedState := strings.ToLower(strings.TrimSpace(socketState))
	status := "recovering"
	statusCode := "recovering"
	runningStatus := "Starting"
	detail := "Engine Socket.IO control channel has not connected yet."
	if normalizedState == "" {
		normalizedState = "not_started"
	}
	switch normalizedState {
	case "connected":
		status = "healthy"
		statusCode = "healthy"
		runningStatus = "Ready"
		detail = "Engine Socket.IO control channel is connected."
		if socketStateAt > 0 && time.Unix(now, 0).Sub(time.Unix(socketStateAt, 0)) > watchdogSocketStaleAfter {
			status = "unhealthy"
			statusCode = "unhealthy"
			runningStatus = "Stale"
			detail = "Engine Socket.IO control channel has stale connected state."
		}
	case "connecting":
		runningStatus = "Connecting"
		detail = "Engine Socket.IO control channel is connecting."
		if socketStateAt > 0 && time.Unix(now, 0).Sub(time.Unix(socketStateAt, 0)) > watchdogSocketStaleAfter {
			status = "unhealthy"
			statusCode = "unhealthy"
			runningStatus = "Stale"
			detail = "Engine Socket.IO control channel is stuck connecting."
		}
	case "disconnected":
		status = "unhealthy"
		statusCode = "unhealthy"
		runningStatus = "Disconnected"
		detail = "Engine Socket.IO control channel is disconnected."
	}
	details := map[string]any{
		"running_status": runningStatus,
		"runtime":        "go",
		"socket_state":   normalizedState,
	}
	if socketStateAt > 0 {
		details["socket_state_at"] = socketStateAt
		details["socket_state_age_seconds"] = now - socketStateAt
	}
	return RoleSnapshot{
		RoleID:     "system:engine_socket",
		RoleName:   "engine_socket",
		RoleLabel:  "Engine Socket",
		Context:    "system",
		Status:     status,
		StatusCode: statusCode,
		Detail:     detail,
		Details:    details,
		CheckedAt:  now,
	}
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
	{Key: "wireguard_starting", Label: "WireGuard tunnel starting"},
	{Key: "wireguard_online", Label: "WireGuard tunnel online"},
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
