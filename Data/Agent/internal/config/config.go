package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
	DefaultBranch           = "main"
	ReleaseChannelStable    = "stable"
	ReleaseChannelUnstable  = "unstable"
	DefaultLogRetentionDays = 1
	MetadataFieldCount      = 500
	MetadataValueMaxLength  = 1024
)

var fileMu sync.Mutex

type AgentConfig struct {
	SchemaVersion  int             `json:"schema_version"`
	ServerURL      string          `json:"server_url"`
	EnrollmentCode string          `json:"enrollment_code,omitempty"`
	Agent          AgentSection    `json:"agent"`
	Identity       IdentitySection `json:"identity"`
	Tokens         TokenSection    `json:"tokens"`
	Trust          TrustSection    `json:"trust"`
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
	MetadataFields   map[string]MetadataFieldSection   `json:"metadata_fields,omitempty"`
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
				mergeNewerMetadataFields(&cfg.Agent.MetadataFields, current.Agent.MetadataFields)
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

func mergeNewerMetadataFields(target *map[string]MetadataFieldSection, current map[string]MetadataFieldSection) {
	if target == nil || len(current) == 0 {
		return
	}
	if *target == nil {
		*target = map[string]MetadataFieldSection{}
	}
	for rawKey, currentField := range current {
		fieldNumber, ok := ParseMetadataFieldNumber(rawKey)
		if !ok {
			continue
		}
		key := MetadataFieldKey(fieldNumber)
		normalizeMetadataField(&currentField)
		targetField, exists := (*target)[key]
		normalizeMetadataField(&targetField)
		if !exists || currentField.ModifiedAt > targetField.ModifiedAt {
			(*target)[key] = currentField
		}
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
	if len(c.Agent.MetadataFields) > 0 {
		c.Agent.MetadataFields = NormalizeMetadataFields(c.Agent.MetadataFields)
	}
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
	clean := strings.ReplaceAll(value, "\x00", "")
	runes := []rune(clean)
	if len(runes) > MetadataValueMaxLength {
		return string(runes[:MetadataValueMaxLength])
	}
	return clean
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
	field.Value = NormalizeMetadataFieldValue(field.Value)
	field.Source = strings.TrimSpace(field.Source)
	field.Actor = strings.TrimSpace(field.Actor)
	if field.ModifiedAt < 0 {
		field.ModifiedAt = 0
	}
}

func UpdateMetadataField(path string, fieldNumber int, value string, source string) error {
	if fieldNumber < 1 || fieldNumber > MetadataFieldCount {
		return fmt.Errorf("field must be between 1 and %d", MetadataFieldCount)
	}
	writer := "metadata:" + strings.TrimSpace(source)
	if strings.TrimSpace(source) == "" {
		writer = "metadata:cli"
	}
	key := MetadataFieldKey(fieldNumber)
	now := time.Now().Unix()
	return UpdateWithWriter(path, writer, func(cfg *AgentConfig) {
		if cfg.Agent.MetadataFields == nil {
			cfg.Agent.MetadataFields = map[string]MetadataFieldSection{}
		}
		cfg.Agent.MetadataFields[key] = MetadataFieldSection{
			Value:      NormalizeMetadataFieldValue(value),
			ModifiedAt: now,
			Source:     strings.TrimSpace(source),
		}
	})
}

func ApplyMetadataSyncResponse(path string, updates map[string]MetadataFieldSection, acks []string) error {
	if len(updates) == 0 && len(acks) == 0 {
		return nil
	}
	return UpdateWithWriter(path, "metadata:engine_sync", func(cfg *AgentConfig) {
		if cfg.Agent.MetadataFields == nil {
			cfg.Agent.MetadataFields = map[string]MetadataFieldSection{}
		}
		for rawKey, update := range updates {
			number, ok := ParseMetadataFieldNumber(rawKey)
			if !ok {
				continue
			}
			key := MetadataFieldKey(number)
			normalizeMetadataField(&update)
			if strings.TrimSpace(update.Source) == "" {
				update.Source = "engine"
			}
			current, exists := cfg.Agent.MetadataFields[key]
			normalizeMetadataField(&current)
			if !exists || update.ModifiedAt >= current.ModifiedAt {
				cfg.Agent.MetadataFields[key] = update
			}
		}
		for _, rawKey := range acks {
			number, ok := ParseMetadataFieldNumber(rawKey)
			if !ok {
				continue
			}
			key := MetadataFieldKey(number)
			current, exists := cfg.Agent.MetadataFields[key]
			if exists && current.Value == "" {
				delete(cfg.Agent.MetadataFields, key)
			}
		}
		if len(cfg.Agent.MetadataFields) == 0 {
			cfg.Agent.MetadataFields = nil
		}
	})
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

func (c AgentConfig) Clone() AgentConfig {
	return c
}
