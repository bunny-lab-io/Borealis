package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	SchemaVersion           = 1
	FileName                = "agent.json"
	MetadataQueueFileName   = "metadata-queue.json"
	DefaultBranch           = "main"
	ReleaseChannelStable    = "stable"
	ReleaseChannelUnstable  = "unstable"
	DefaultLogRetentionDays = 1
	MetadataFieldCount      = 500
	MetadataValueMaxLength  = 1024
)

var fileMu sync.Mutex

type AgentConfig struct {
	SchemaVersion    int              `json:"schema_version"`
	ServerURL        string           `json:"server_url"`
	ServerIPFallback string           `json:"server_ip_fallback,omitempty"`
	EnrollmentCode   string           `json:"enrollment_code,omitempty"`
	Agent            AgentSection     `json:"agent"`
	RemoteOps        RemoteOpsSection `json:"remote_ops"`
	Identity         IdentitySection  `json:"identity"`
	Tokens           TokenSection     `json:"tokens"`
	Trust            TrustSection     `json:"trust"`
}

type AgentSection struct {
	GUID             string                            `json:"guid"`
	AgentID          string                            `json:"agent_id"`
	ReleaseChannel   string                            `json:"release_channel"`
	Branch           string                            `json:"branch"`
	InstalledBuildID string                            `json:"installed_build_id"`
	LogRetentionDays int                               `json:"log_retention_days"`
	State            AgentStateSection                 `json:"state"`
	Update           AgentUpdateSection                `json:"update,omitempty"`
	Liveness         AgentLivenessSection              `json:"liveness"`
	DependencyState  map[string]DependencyStateSection `json:"dependency_state,omitempty"`
}

type AgentStateSection struct {
	Revision    int64  `json:"revision"`
	Writer      string `json:"writer"`
	LastWriteAt int64  `json:"last_write_at"`
}

type AgentUpdateSection struct {
	OperationID      string `json:"operation_id,omitempty"`
	Kind             string `json:"kind,omitempty"`
	Status           string `json:"status,omitempty"`
	StartedAt        int64  `json:"started_at,omitempty"`
	UpdatedAt        int64  `json:"updated_at,omitempty"`
	CompletedAt      int64  `json:"completed_at,omitempty"`
	DeadlineAt       int64  `json:"deadline_at,omitempty"`
	PreviousChannel  string `json:"previous_channel,omitempty"`
	PreviousBranch   string `json:"previous_branch,omitempty"`
	TargetChannel    string `json:"target_channel,omitempty"`
	TargetBranch     string `json:"target_branch,omitempty"`
	LastError        string `json:"last_error,omitempty"`
	RecoveryAttempts int    `json:"recovery_attempts,omitempty"`
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
	EngineCAPEM             string `json:"engine_ca_pem,omitempty"`
}

type RemoteOpsSection struct {
	Available       bool   `json:"available"`
	SiteID          int    `json:"site_id,omitempty"`
	WorkerGUID      string `json:"worker_guid,omitempty"`
	RouteGeneration int64  `json:"route_generation,omitempty"`
	RoutePathPrefix string `json:"route_path_prefix,omitempty"`
	BaseURL         string `json:"base_url,omitempty"`
	SocketURL       string `json:"socket_url,omitempty"`
	Reason          string `json:"reason,omitempty"`
	UpdatedAt       int64  `json:"updated_at,omitempty"`
}

type DependencyStateSection struct {
	Phase            string `json:"phase"`
	Status           string `json:"status"`
	DesiredVersion   string `json:"desired_version,omitempty"`
	InstalledVersion string `json:"installed_version,omitempty"`
	Detail           string `json:"detail,omitempty"`
	LastAttemptAt    int64  `json:"last_attempt_at,omitempty"`
	LastSuccessAt    int64  `json:"last_success_at,omitempty"`
	LastError        string `json:"last_error,omitempty"`
}

type MetadataFieldSection struct {
	Value      string `json:"value"`
	ModifiedAt int64  `json:"modified_at"`
	Source     string `json:"source,omitempty"`
	Actor      string `json:"actor,omitempty"`
}

type MetadataQueue struct {
	Version int                             `json:"version"`
	Fields  map[string]MetadataFieldSection `json:"fields,omitempty"`
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

func ValidateServerURLForEnrollment(value string) error {
	text := NormalizeServerURL(value)
	if text == "" {
		return errors.New("server_url missing")
	}
	parsed, err := url.Parse(text)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("server_url must be absolute URL")
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), ".")
	if host == "" {
		return fmt.Errorf("server_url hostname missing")
	}
	if net.ParseIP(host) != nil {
		return fmt.Errorf("server_url must use Engine FQDN, not raw IP address")
	}
	if host == "localhost" || !strings.Contains(host, ".") {
		return fmt.Errorf("server_url must use Engine FQDN")
	}
	return nil
}

func NormalizeServerIPFallback(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}
	ip := net.ParseIP(text)
	if ip == nil {
		return ""
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
		return ""
	}
	return ip.String()
}

func ValidateServerIPFallback(value string) error {
	text := strings.TrimSpace(value)
	if text == "" {
		return nil
	}
	if strings.Contains(text, "://") || strings.Contains(text, "/") {
		return fmt.Errorf("server_ip_fallback must be a bare IP address, not URL or CIDR")
	}
	if NormalizeServerIPFallback(text) == "" {
		return fmt.Errorf("server_ip_fallback must be a non-loopback IP address")
	}
	return nil
}

func NormalizeEngineCAPEM(value string) string {
	text := strings.ReplaceAll(strings.TrimSpace(value), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if text == "" {
		return ""
	}
	return text + "\n"
}

func DecodeEngineCAB64(value string) (string, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return "", nil
	}
	decoded, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(text)
	}
	if err != nil {
		return "", fmt.Errorf("decode trusted engine CA: %w", err)
	}
	pemText := NormalizeEngineCAPEM(string(decoded))
	if pemText == "" {
		return "", fmt.Errorf("trusted engine CA is empty")
	}
	return pemText, nil
}

func NormalizeRemoteOpsURL(value string) string {
	return NormalizeServerURL(value)
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
		return ReleaseChannelUnstable
	default:
		return text
	}
}

func ReleaseChannelForBranch(branch string) string {
	normalizedBranch := NormalizeBranch(branch)
	if strings.EqualFold(normalizedBranch, DefaultBranch) {
		return ReleaseChannelStable
	}
	return ReleaseChannelUnstable
}

func UsesUnstableReleaseChannel(value string) bool {
	return NormalizeReleaseChannel(value) == ReleaseChannelUnstable
}

func NormalizeBuildID(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func NormalizeDependencyVersion(value string) string {
	return strings.TrimSpace(value)
}

func NormalizeDependencyName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
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
	return SaveWithWriter(path, defaultStateWriter(), cfg)
}

func SaveWithWriter(path string, writer string, cfg *AgentConfig) error {
	fileMu.Lock()
	defer fileMu.Unlock()
	return withProcessFileLock(path, func() error {
		if cfg != nil {
			if current, err := loadUnlocked(path); err == nil {
				mergeNewerLiveness(&cfg.Agent.Liveness, current.Agent.Liveness)
				mergeNewerUpdateState(&cfg.Agent.Update, current.Agent.Update)
				mergeNewerDependencyState(&cfg.Agent.DependencyState, current.Agent.DependencyState)
				if current.Agent.State.Revision > cfg.Agent.State.Revision {
					cfg.Agent.State.Revision = current.Agent.State.Revision
				}
			}
		}
		return saveUnlocked(path, cfg, writer)
	})
}

func saveUnlocked(path string, cfg *AgentConfig, writer string) error {
	if cfg == nil {
		return errors.New("nil config")
	}
	cfg.ApplyDefaults()
	cfg.ServerURL = NormalizeServerURL(cfg.ServerURL)
	cfg.ServerIPFallback = NormalizeServerIPFallback(cfg.ServerIPFallback)
	touchStateMetadata(cfg, writer)

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
	return UpdateWithWriter(path, defaultStateWriter(), update)
}

func UpdateWithWriter(path string, writer string, update func(*AgentConfig)) error {
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
		return saveUnlocked(path, &cfg, writer)
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

func defaultStateWriter() string {
	name := filepath.Base(os.Args[0])
	if strings.TrimSpace(name) == "" {
		name = "agent"
	}
	return fmt.Sprintf("%s:%d", name, os.Getpid())
}

func touchStateMetadata(cfg *AgentConfig, writer string) {
	if cfg == nil {
		return
	}
	cleanWriter := strings.TrimSpace(writer)
	if cleanWriter == "" {
		cleanWriter = defaultStateWriter()
	}
	cfg.Agent.State.Revision++
	cfg.Agent.State.Writer = cleanWriter
	cfg.Agent.State.LastWriteAt = time.Now().Unix()
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

func mergeNewerDependencyState(target *map[string]DependencyStateSection, current map[string]DependencyStateSection) {
	if target == nil || len(current) == 0 {
		return
	}
	if *target == nil {
		*target = map[string]DependencyStateSection{}
	}
	for rawName, currentState := range current {
		name := NormalizeDependencyName(rawName)
		if name == "" {
			continue
		}
		targetState, ok := (*target)[name]
		if !ok || dependencyStateTimestamp(currentState) > dependencyStateTimestamp(targetState) {
			(*target)[name] = currentState
		}
	}
}

func mergeNewerUpdateState(target *AgentUpdateSection, current AgentUpdateSection) {
	if target == nil {
		return
	}
	if current.UpdatedAt > target.UpdatedAt {
		*target = current
	}
}

func dependencyStateTimestamp(state DependencyStateSection) int64 {
	if state.LastSuccessAt > state.LastAttemptAt {
		return state.LastSuccessAt
	}
	return state.LastAttemptAt
}

func (c *AgentConfig) ApplyDefaults() {
	if c.SchemaVersion == 0 {
		c.SchemaVersion = SchemaVersion
	}
	c.Agent.ReleaseChannel = NormalizeReleaseChannel(c.Agent.ReleaseChannel)
	c.Agent.Branch = NormalizeBranch(c.Agent.Branch)
	if c.Agent.ReleaseChannel == ReleaseChannelStable {
		c.Agent.Branch = DefaultBranch
	}
	c.Agent.InstalledBuildID = NormalizeBuildID(c.Agent.InstalledBuildID)
	c.ServerIPFallback = NormalizeServerIPFallback(c.ServerIPFallback)
	c.Trust.EngineCAPEM = NormalizeEngineCAPEM(c.Trust.EngineCAPEM)
	c.RemoteOps = normalizeRemoteOpsSection(c.RemoteOps)
	c.Agent.Update = normalizeUpdateSection(c.Agent.Update)
	if c.Agent.LogRetentionDays <= 0 {
		c.Agent.LogRetentionDays = DefaultLogRetentionDays
	}
	if len(c.Agent.DependencyState) > 0 {
		normalized := map[string]DependencyStateSection{}
		for name, state := range c.Agent.DependencyState {
			key := NormalizeDependencyName(name)
			if key == "" {
				continue
			}
			normalizeDependencyState(&state)
			normalized[key] = state
		}
		c.Agent.DependencyState = normalized
	}
}

func (c *AgentConfig) ResetAuthForEnrollment() {
	c.Tokens = TokenSection{}
	c.RemoteOps = RemoteOpsSection{}
}

func MetadataFieldKey(fieldNumber int) string {
	return fmt.Sprintf("field_%03d", fieldNumber)
}

func ParseMetadataFieldNumber(value string) (int, bool) {
	text := strings.ToLower(strings.TrimSpace(value))
	if text == "" {
		return 0, false
	}
	text = strings.TrimPrefix(text, "metadata")
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "field")
	text = strings.Trim(text, " _-")
	if text == "" {
		return 0, false
	}
	number, err := strconv.Atoi(text)
	if err != nil {
		return 0, false
	}
	if number < 1 || number > MetadataFieldCount {
		return 0, false
	}
	return number, true
}

func NormalizeMetadataFieldValue(value string) string {
	runes := []rune(value)
	if len(runes) > MetadataValueMaxLength {
		return string(runes[:MetadataValueMaxLength])
	}
	return value
}

func EncodeMetadataFieldValue(value string) string {
	clean := NormalizeMetadataFieldValue(value)
	if clean == "" {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(clean))
}

func DecodeMetadataFieldValue(value string) string {
	clean := strings.TrimSpace(value)
	if clean == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return NormalizeMetadataFieldValue(value)
	}
	return NormalizeMetadataFieldValue(string(decoded))
}

func NormalizeEncodedMetadataFieldValue(value string) string {
	return EncodeMetadataFieldValue(DecodeMetadataFieldValue(value))
}

func NormalizeMetadataFields(fields map[string]MetadataFieldSection) map[string]MetadataFieldSection {
	if len(fields) == 0 {
		return nil
	}
	normalized := map[string]MetadataFieldSection{}
	for rawKey, field := range fields {
		number, ok := ParseMetadataFieldNumber(rawKey)
		if !ok {
			continue
		}
		normalizeMetadataField(&field)
		normalized[MetadataFieldKey(number)] = field
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeMetadataField(field *MetadataFieldSection) {
	if field == nil {
		return
	}
	field.Value = NormalizeEncodedMetadataFieldValue(field.Value)
	field.Source = strings.TrimSpace(field.Source)
	field.Actor = strings.TrimSpace(field.Actor)
	if field.ModifiedAt < 0 {
		field.ModifiedAt = 0
	}
}

func MetadataQueuePath(configPath string) (string, error) {
	cleanPath := strings.TrimSpace(configPath)
	if cleanPath == "" {
		return "", errors.New("config path missing")
	}
	return filepath.Join(filepath.Dir(cleanPath), MetadataQueueFileName), nil
}

func LoadQueuedMetadataFields(configPath string) (map[string]MetadataFieldSection, error) {
	queuePath, err := MetadataQueuePath(configPath)
	if err != nil {
		return nil, err
	}
	fields := map[string]MetadataFieldSection{}
	fileMu.Lock()
	defer fileMu.Unlock()
	err = withProcessFileLock(queuePath, func() error {
		queue, loadErr := loadMetadataQueueUnlocked(queuePath)
		if loadErr != nil {
			return loadErr
		}
		fields = NormalizeMetadataFields(queue.Fields)
		if fields == nil {
			fields = map[string]MetadataFieldSection{}
		}
		return nil
	})
	return fields, err
}

func QueuedMetadataFieldValue(configPath string, fieldNumber int) (string, bool, error) {
	if fieldNumber < 1 || fieldNumber > MetadataFieldCount {
		return "", false, fmt.Errorf("field must be between 1 and %d", MetadataFieldCount)
	}
	fields, err := LoadQueuedMetadataFields(configPath)
	if err != nil {
		return "", false, err
	}
	field, ok := fields[MetadataFieldKey(fieldNumber)]
	if !ok {
		return "", false, nil
	}
	normalizeMetadataField(&field)
	return DecodeMetadataFieldValue(field.Value), true, nil
}

func QueueMetadataField(configPath string, fieldNumber int, value string, source string) error {
	if fieldNumber < 1 || fieldNumber > MetadataFieldCount {
		return fmt.Errorf("field must be between 1 and %d", MetadataFieldCount)
	}
	queuePath, err := MetadataQueuePath(configPath)
	if err != nil {
		return err
	}
	key := MetadataFieldKey(fieldNumber)
	now := time.Now().Unix()
	fileMu.Lock()
	defer fileMu.Unlock()
	return withProcessFileLock(queuePath, func() error {
		queue, loadErr := loadMetadataQueueUnlocked(queuePath)
		if loadErr != nil {
			return loadErr
		}
		if queue.Fields == nil {
			queue.Fields = map[string]MetadataFieldSection{}
		}
		queue.Fields[key] = MetadataFieldSection{
			Value:      EncodeMetadataFieldValue(value),
			ModifiedAt: now,
			Source:     strings.TrimSpace(source),
		}
		return saveMetadataQueueUnlocked(queuePath, queue)
	})
}

func AckQueuedMetadataFields(configPath string, acks []string) error {
	if len(acks) == 0 {
		return nil
	}
	queuePath, err := MetadataQueuePath(configPath)
	if err != nil {
		return err
	}
	fileMu.Lock()
	defer fileMu.Unlock()
	return withProcessFileLock(queuePath, func() error {
		queue, loadErr := loadMetadataQueueUnlocked(queuePath)
		if loadErr != nil {
			return loadErr
		}
		if len(queue.Fields) == 0 {
			return nil
		}
		for _, rawKey := range acks {
			number, ok := ParseMetadataFieldNumber(rawKey)
			if !ok {
				continue
			}
			key := MetadataFieldKey(number)
			delete(queue.Fields, key)
		}
		return saveMetadataQueueUnlocked(queuePath, queue)
	})
}

func loadMetadataQueueUnlocked(path string) (MetadataQueue, error) {
	queue := MetadataQueue{Version: 1}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return queue, nil
		}
		return queue, err
	}
	if len(data) == 0 {
		return queue, nil
	}
	if err := json.Unmarshal(data, &queue); err != nil {
		return queue, fmt.Errorf("parse metadata queue: %w", err)
	}
	queue.Version = 1
	queue.Fields = NormalizeMetadataFields(queue.Fields)
	return queue, nil
}

func saveMetadataQueueUnlocked(path string, queue MetadataQueue) error {
	queue.Version = 1
	queue.Fields = NormalizeMetadataFields(queue.Fields)
	if len(queue.Fields) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if err := RestrictParent(parent); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(queue, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	tmp, err := os.CreateTemp(parent, ".metadata-queue-*.tmp")
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

func normalizeUpdateSection(update AgentUpdateSection) AgentUpdateSection {
	update.OperationID = strings.TrimSpace(update.OperationID)
	update.Kind = strings.TrimSpace(update.Kind)
	update.Status = strings.ToLower(strings.TrimSpace(update.Status))
	update.PreviousChannel = normalizeOptionalReleaseChannel(update.PreviousChannel)
	update.PreviousBranch = normalizeOptionalBranch(update.PreviousBranch)
	update.TargetChannel = normalizeOptionalReleaseChannel(update.TargetChannel)
	update.TargetBranch = normalizeOptionalBranch(update.TargetBranch)
	if update.PreviousChannel == ReleaseChannelStable {
		update.PreviousBranch = DefaultBranch
	}
	if update.TargetChannel == ReleaseChannelStable {
		update.TargetBranch = DefaultBranch
	}
	update.LastError = strings.TrimSpace(update.LastError)
	return update
}

func normalizeOptionalReleaseChannel(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}
	return NormalizeReleaseChannel(text)
}

func normalizeOptionalBranch(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}
	return NormalizeBranch(text)
}

func (c *AgentConfig) UpdateDependencyState(name string, update func(*DependencyStateSection)) {
	key := NormalizeDependencyName(name)
	if key == "" {
		return
	}
	if c.Agent.DependencyState == nil {
		c.Agent.DependencyState = map[string]DependencyStateSection{}
	}
	state := c.Agent.DependencyState[key]
	if update != nil {
		update(&state)
	}
	normalizeDependencyState(&state)
	c.Agent.DependencyState[key] = state
}

func normalizeDependencyState(state *DependencyStateSection) {
	if state == nil {
		return
	}
	state.Phase = strings.ToLower(strings.TrimSpace(state.Phase))
	state.Status = strings.ToLower(strings.TrimSpace(state.Status))
	state.DesiredVersion = NormalizeDependencyVersion(state.DesiredVersion)
	state.InstalledVersion = NormalizeDependencyVersion(state.InstalledVersion)
	state.Detail = strings.TrimSpace(state.Detail)
	state.LastError = strings.TrimSpace(state.LastError)
}

func normalizeRemoteOpsSection(section RemoteOpsSection) RemoteOpsSection {
	section.WorkerGUID = strings.TrimSpace(section.WorkerGUID)
	section.RoutePathPrefix = strings.TrimSpace(section.RoutePathPrefix)
	section.BaseURL = NormalizeRemoteOpsURL(section.BaseURL)
	section.SocketURL = NormalizeRemoteOpsURL(section.SocketURL)
	section.Reason = strings.TrimSpace(section.Reason)
	if section.SiteID < 0 {
		section.SiteID = 0
	}
	if section.RouteGeneration < 0 {
		section.RouteGeneration = 0
	}
	if section.UpdatedAt < 0 {
		section.UpdatedAt = 0
	}
	if !section.Available {
		section.WorkerGUID = ""
		section.RouteGeneration = 0
		section.RoutePathPrefix = ""
		section.BaseURL = ""
		section.SocketURL = ""
	}
	return section
}

func (c AgentConfig) Clone() AgentConfig {
	return c
}
