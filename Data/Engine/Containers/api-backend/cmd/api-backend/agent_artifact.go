package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const agentArtifactSourceEngine = "engine"

func collectAgentArtifactSettings() map[string]any {
	loaded := loadJSONSettingsFile(agentArtifactSettingsPath())
	artifact, _ := loaded["artifact"].(map[string]any)
	if artifact == nil {
		artifact = map[string]any{}
	}
	return map[string]any{
		"version":    coerceInt64(loaded["version"]),
		"source":     agentArtifactSourceEngine,
		"artifact":   deepCopyAgentArtifactMap(artifact),
		"created_at": coerceInt64(loaded["created_at"]),
		"updated_at": coerceInt64(loaded["updated_at"]),
	}
}

func agentArtifactSettingsPath() string {
	if override := strings.TrimSpace(os.Getenv("BOREALIS_AGENT_ARTIFACT_PATH")); override != "" {
		return expandHomePath(override)
	}
	root := strings.TrimSpace(os.Getenv("BOREALIS_PROJECT_ROOT"))
	if root == "" {
		root = "/opt/Borealis"
	}
	return filepath.Join(expandHomePath(root), "Engine", "Services", "api-backend", "config", "agent_artifact.json")
}

func agentArtifactTarget(settings map[string]any) map[string]any {
	target, _ := settings["artifact"].(map[string]any)
	if target == nil {
		return map[string]any{"source": agentArtifactSourceEngine}
	}
	copied := deepCopyAgentArtifactMap(target)
	copied["source"] = agentArtifactSourceEngine
	return copied
}

func agentArtifactPlatformArtifacts(value any) map[string]any {
	if artifacts, ok := value.(map[string]any); ok && len(artifacts) > 0 {
		return deepCopyAgentArtifactMap(artifacts)
	}
	return deepCopyAgentArtifactMap(requiredGoAgentArtifacts)
}

func deepCopyAgentArtifactMap(source map[string]any) map[string]any {
	if source == nil {
		return map[string]any{}
	}
	body, err := json.Marshal(source)
	if err != nil {
		return map[string]any{}
	}
	var copied map[string]any
	if err := json.Unmarshal(body, &copied); err != nil || copied == nil {
		return map[string]any{}
	}
	return copied
}

func validateAgentArtifact(path string) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open Agent artifact: %w", err)
	}
	defer archive.Close()
	manifestBody, ok := zipMemberBytes(archive.File, "manifest.json")
	if !ok || len(manifestBody) == 0 {
		return fmt.Errorf("Agent artifact missing manifest.json")
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestBody, &manifest); err != nil {
		return fmt.Errorf("parse Agent artifact manifest: %w", err)
	}
	if cleanText(manifest["artifact_format"]) != agentUpdateArtifactFormat {
		return fmt.Errorf("unsupported Agent artifact format")
	}
	for _, member := range requiredGoAgentArtifacts {
		name := cleanText(member)
		body, found := zipMemberBytes(archive.File, name)
		if !found || len(body) == 0 {
			return fmt.Errorf("Agent artifact missing %s", name)
		}
	}
	return nil
}

func agentArtifactCompiledAt(path string, info os.FileInfo) int64 {
	archive, err := zip.OpenReader(path)
	if err == nil {
		defer archive.Close()
		if body, ok := zipMemberBytes(archive.File, "manifest.json"); ok {
			var manifest map[string]any
			if json.Unmarshal(body, &manifest) == nil {
				if compiledAt := coerceInt64(manifest["compiled_at"]); compiledAt > 0 {
					return compiledAt
				}
			}
		}
	}
	if info != nil {
		return info.ModTime().Unix()
	}
	return 0
}

func zipMemberBytes(files []*zip.File, suffix string) ([]byte, bool) {
	wanted := strings.Trim(strings.ReplaceAll(suffix, "\\", "/"), "/")
	for _, file := range files {
		name := strings.Trim(strings.ReplaceAll(file.Name, "\\", "/"), "/")
		if name != wanted && !strings.HasSuffix(name, "/"+wanted) {
			continue
		}
		handle, err := file.Open()
		if err != nil {
			return nil, false
		}
		body, readErr := io.ReadAll(handle)
		closeErr := handle.Close()
		if readErr != nil || closeErr != nil {
			return nil, false
		}
		return body, true
	}
	return nil, false
}

func sha256File(path string) string {
	handle, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer handle.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, handle); err != nil {
		return ""
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}
