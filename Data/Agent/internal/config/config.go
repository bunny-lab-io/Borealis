package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	SchemaVersion = 1
	FileName      = "config.json"
)

type AgentConfig struct {
	SchemaVersion  int               `json:"schema_version"`
	ServerURL      string            `json:"server_url"`
	EnrollmentCode string            `json:"enrollment_code,omitempty"`
	Agent          AgentSection      `json:"agent"`
	Identity       IdentitySection   `json:"identity"`
	Tokens         TokenSection      `json:"tokens"`
	Trust          TrustSection      `json:"trust"`
	Runtime        RuntimeSection    `json:"runtime"`
	LastSavedAt    string            `json:"last_saved_at,omitempty"`
	Extra          map[string]string `json:"extra,omitempty"`
}

type AgentSection struct {
	GUID    string `json:"guid"`
	AgentID string `json:"agent_id"`
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

type RuntimeSection struct {
	FeatureFlags map[string]bool `json:"feature_flags"`
}

func Default() AgentConfig {
	return AgentConfig{
		SchemaVersion: SchemaVersion,
		Runtime: RuntimeSection{
			FeatureFlags: map[string]bool{
				"system_scripts":      true,
				"windows_currentuser": true,
				"linux_currentuser":   false,
			},
		},
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
	if err := json.Unmarshal(data, &cfg); err != nil {
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
	if cfg == nil {
		return errors.New("nil config")
	}
	cfg.ApplyDefaults()
	cfg.ServerURL = NormalizeServerURL(cfg.ServerURL)
	cfg.LastSavedAt = time.Now().UTC().Format(time.RFC3339)

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

func (c *AgentConfig) ApplyDefaults() {
	if c.SchemaVersion == 0 {
		c.SchemaVersion = SchemaVersion
	}
	if c.Runtime.FeatureFlags == nil {
		c.Runtime.FeatureFlags = map[string]bool{}
	}
	if _, ok := c.Runtime.FeatureFlags["system_scripts"]; !ok {
		c.Runtime.FeatureFlags["system_scripts"] = true
	}
	if _, ok := c.Runtime.FeatureFlags["windows_currentuser"]; !ok {
		c.Runtime.FeatureFlags["windows_currentuser"] = true
	}
	if _, ok := c.Runtime.FeatureFlags["linux_currentuser"]; !ok {
		c.Runtime.FeatureFlags["linux_currentuser"] = false
	}
}

func (c AgentConfig) Clone() AgentConfig {
	out := c
	out.Runtime.FeatureFlags = map[string]bool{}
	for k, v := range c.Runtime.FeatureFlags {
		out.Runtime.FeatureFlags[k] = v
	}
	if c.Extra != nil {
		out.Extra = map[string]string{}
		for k, v := range c.Extra {
			out.Extra[k] = v
		}
	}
	return out
}
