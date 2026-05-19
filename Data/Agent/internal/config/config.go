package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	SchemaVersion           = 1
	FileName                = "agent.json"
	DefaultBranch           = "main"
	ReleaseChannelStable    = "stable"
	ReleaseChannelSource    = "source"
	DefaultLogRetentionDays = 1
)

var fileMu sync.Mutex

type AgentConfig struct {
	SchemaVersion      int                        `json:"schema_version"`
	ServerURL          string                     `json:"server_url"`
	EnrollmentCode     string                     `json:"enrollment_code,omitempty"`
	Agent              AgentSection               `json:"agent"`
	Identity           IdentitySection            `json:"identity"`
	Tokens             TokenSection               `json:"tokens"`
	Trust              TrustSection               `json:"trust"`
	DependencyVersions *DependencyVersionsSection `json:"dependency_versions,omitempty"`
}

type AgentSection struct {
	GUID             string               `json:"guid"`
	AgentID          string               `json:"agent_id"`
	ReleaseChannel   string               `json:"release_channel"`
	Branch           string               `json:"branch"`
	InstalledBuildID string               `json:"installed_build_id"`
	LogRetentionDays int                  `json:"log_retention_days"`
	Liveness         AgentLivenessSection `json:"liveness"`
}

type AgentLivenessSection struct {
	PID                    int    `json:"pid,omitempty"`
	BootID                 string `json:"boot_id,omitempty"`
	StartedAt              int64  `json:"started_at,omitempty"`
	LastLocalTickAt        int64  `json:"last_local_tick_at,omitempty"`
	LastHeartbeatAttemptAt int64  `json:"last_heartbeat_attempt_at,omitempty"`
	LastHeartbeatSuccessAt int64  `json:"last_heartbeat_success_at,omitempty"`
	LastHeartbeatError     string `json:"last_heartbeat_error,omitempty"`
	LastSocketState        string `json:"last_socket_state,omitempty"`
	LastSocketStateAt      int64  `json:"last_socket_state_at,omitempty"`
	LastWatchdogCheckAt    int64  `json:"last_watchdog_check_at,omitempty"`
	LastRecoveryAction     string `json:"last_recovery_action,omitempty"`
	LastRecoveryAt         int64  `json:"last_recovery_at,omitempty"`
}

type IdentitySection struct {
	PrivateKeyPKCS8B64 string `json:"private_key_pkcs8_b64"`
	PublicKeySPKIB64   string `json:"public_key_spki_b64"`
}

type TokenSection struct {
	AccessToken     string `json:"access_token"`
	AccessExpiresAt int64  `json:"access_expires_at"`
	RefreshToken    string `json:"refresh_token"`
}

type TrustSection struct {
	ServerSigningKeySPKIB64 string `json:"server_signing_key_spki_b64"`
}

type DependencyVersionsSection struct {
	WireGuard string `json:"wireguard,omitempty"`
	UltraVNC  string `json:"ultravnc,omitempty"`
}

func Default() AgentConfig {
	return AgentConfig{
		SchemaVersion: SchemaVersion,
	}
}

func PathFromBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), FileName), nil
}

func NormalizeServerURL(value string) string {
	text := strings.TrimSpace(value)
	text = strings.TrimRight(text, "/")
	return text
}

func NormalizeBranch(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return DefaultBranch
	}
	return text
}

func NormalizeReleaseChannel(value string) string {
	text := strings.ToLower(strings.TrimSpace(value))
	switch text {
	case "", "stable", "release", "releases":
		return ReleaseChannelStable
	case "source", "sources", "branch", "repo", "repository", "unstable":
		return ReleaseChannelSource
	default:
		return text
	}
}

func ReleaseChannelForBranch(branch string) string {
	normalizedBranch := NormalizeBranch(branch)
	if strings.EqualFold(normalizedBranch, DefaultBranch) {
		return ReleaseChannelStable
	}
	return ReleaseChannelSource
}

func UsesSourceReleaseChannel(value string) bool {
	return NormalizeReleaseChannel(value) == ReleaseChannelSource
}

func NormalizeBuildID(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func NormalizeDependencyVersion(value string) string {
	return strings.TrimSpace(value)
}

func Load(path string) (AgentConfig, error) {
	var cfg AgentConfig
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg = Default()
			return cfg, nil
		}
		return cfg, err
	}
	if len(data) == 0 {
		cfg = Default()
		return cfg, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}
	cfg.ApplyDefaults()
	return cfg, nil
}

func LoadOrCreate(path string) (AgentConfig, error) {
	cfg, err := Load(path)
	if err != nil {
		return cfg, err
	}
	if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
		if err := Save(path, &cfg); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}

func Save(path string, cfg *AgentConfig) error {
	fileMu.Lock()
	defer fileMu.Unlock()
	return withProcessFileLock(path, func() error {
		if cfg != nil {
			if current, err := loadUnlocked(path); err == nil {
				mergeNewerLiveness(&cfg.Agent.Liveness, current.Agent.Liveness)
			}
		}
		return saveUnlocked(path, cfg)
	})
}

func saveUnlocked(path string, cfg *AgentConfig) error {
	if cfg == nil {
		return errors.New("nil config")
	}
	cfg.ApplyDefaults()
	cfg.ServerURL = NormalizeServerURL(cfg.ServerURL)

	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if err := RestrictParent(parent); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')

	tmp, err := os.CreateTemp(parent, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return RestrictFile(path)
}

func Update(path string, update func(*AgentConfig)) error {
	fileMu.Lock()
	defer fileMu.Unlock()
	return withProcessFileLock(path, func() error {
		cfg, err := loadUnlocked(path)
		if err != nil {
			return err
		}
		if update != nil {
			update(&cfg)
		}
		return saveUnlocked(path, &cfg)
	})
}

func loadUnlocked(path string) (AgentConfig, error) {
	var cfg AgentConfig
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg = Default()
			return cfg, nil
		}
		return cfg, err
	}
	if len(data) == 0 {
		cfg = Default()
		return cfg, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}
	cfg.ApplyDefaults()
	return cfg, nil
}

func withProcessFileLock(path string, fn func() error) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("config path missing")
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	lock, err := acquireProcessFileLock(path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	if fn == nil {
		return nil
	}
	return fn()
}

func mergeNewerLiveness(target *AgentLivenessSection, current AgentLivenessSection) {
	if target == nil {
		return
	}
	if current.LastLocalTickAt > target.LastLocalTickAt {
		target.PID = current.PID
		target.BootID = current.BootID
		target.StartedAt = current.StartedAt
		target.LastLocalTickAt = current.LastLocalTickAt
	}
	if current.LastHeartbeatAttemptAt > target.LastHeartbeatAttemptAt {
		target.LastHeartbeatAttemptAt = current.LastHeartbeatAttemptAt
	}
	if current.LastHeartbeatSuccessAt > target.LastHeartbeatSuccessAt {
		target.LastHeartbeatSuccessAt = current.LastHeartbeatSuccessAt
	}
	if current.LastHeartbeatError != "" && current.LastHeartbeatAttemptAt >= target.LastHeartbeatAttemptAt {
		target.LastHeartbeatError = current.LastHeartbeatError
	}
	if current.LastSocketStateAt > target.LastSocketStateAt {
		target.LastSocketState = current.LastSocketState
		target.LastSocketStateAt = current.LastSocketStateAt
	}
	if current.LastWatchdogCheckAt > target.LastWatchdogCheckAt {
		target.LastWatchdogCheckAt = current.LastWatchdogCheckAt
	}
	if current.LastRecoveryAt > target.LastRecoveryAt {
		target.LastRecoveryAction = current.LastRecoveryAction
		target.LastRecoveryAt = current.LastRecoveryAt
	}
}

func (c *AgentConfig) ApplyDefaults() {
	if c.SchemaVersion == 0 {
		c.SchemaVersion = SchemaVersion
	}
	c.Agent.ReleaseChannel = NormalizeReleaseChannel(c.Agent.ReleaseChannel)
	c.Agent.Branch = NormalizeBranch(c.Agent.Branch)
	c.Agent.InstalledBuildID = NormalizeBuildID(c.Agent.InstalledBuildID)
	if c.Agent.LogRetentionDays <= 0 {
		c.Agent.LogRetentionDays = DefaultLogRetentionDays
	}
	if c.DependencyVersions != nil {
		c.DependencyVersions.WireGuard = NormalizeDependencyVersion(c.DependencyVersions.WireGuard)
		c.DependencyVersions.UltraVNC = NormalizeDependencyVersion(c.DependencyVersions.UltraVNC)
	}
}

func (c *AgentConfig) EnsureDependencyVersions() *DependencyVersionsSection {
	if c.DependencyVersions == nil {
		c.DependencyVersions = &DependencyVersionsSection{}
	}
	return c.DependencyVersions
}

func (c AgentConfig) Clone() AgentConfig {
	return c
}
