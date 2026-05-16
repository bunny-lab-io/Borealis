package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func finalizeDeferredUpdate(configPath string, buildID string, expectedSHA256 string) error {
	normalizedBuildID := agentconfig.NormalizeBuildID(buildID)
	if normalizedBuildID == "" || strings.EqualFold(normalizedBuildID, "dev") {
		return fmt.Errorf("build id missing")
	}
	expected := strings.TrimSpace(strings.ToLower(expectedSHA256))
	if expected != "" {
		exePath, err := os.Executable()
		if err != nil {
			return err
		}
		actual, err := sha256File(exePath)
		if err != nil {
			return err
		}
		if !strings.EqualFold(actual, expected) {
			return fmt.Errorf("Agent binary hash mismatch expected=%s actual=%s", expected, actual)
		}
		_ = os.Remove(exePath + ".update")
	}
	if strings.TrimSpace(configPath) == "" {
		resolved, err := agentconfig.PathFromBinary()
		if err != nil {
			return err
		}
		configPath = resolved
	}
	current, err := agentconfig.LoadOrCreate(configPath)
	if err != nil {
		return err
	}
	current.Agent.InstalledBuildID = normalizedBuildID
	if strings.TrimSpace(current.Agent.Branch) == "" {
		current.Agent.Branch = agentconfig.NormalizeBranch("main")
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	return agentconfig.Save(configPath, &current)
}
