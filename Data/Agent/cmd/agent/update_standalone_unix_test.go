//go:build !windows

package main

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestStageLinuxAgentUpdateRestoresRuntimeAfterStagingFailure(t *testing.T) {
	stageErr := errors.New("candidate failed validation")
	configPath := filepath.Join(t.TempDir(), "agent.json")
	var installedPath string

	originalStage := stageLinuxAgentUpdateForRequest
	originalInstall := installLinuxAgentServiceForUpdate
	stageLinuxAgentUpdateForRequest = func(string, string) (string, error) {
		return "", stageErr
	}
	installLinuxAgentServiceForUpdate = func(path string) error {
		installedPath = path
		return nil
	}
	t.Cleanup(func() {
		stageLinuxAgentUpdateForRequest = originalStage
		installLinuxAgentServiceForUpdate = originalInstall
	})

	_, err := stageLinuxAgentUpdateWithRecovery(configPath, "agent-update.zip", nil)
	if !errors.Is(err, stageErr) {
		t.Fatalf("stage error = %v, want %v", err, stageErr)
	}
	if want := filepath.Join(filepath.Dir(configPath), "Agent"); installedPath != want {
		t.Fatalf("restored runtime path = %q, want %q", installedPath, want)
	}
}

func TestRestoreLinuxRuntimeReportsUpdateAndRecoveryFailures(t *testing.T) {
	updateErr := errors.New("staging failed")
	restoreErr := errors.New("systemd restart failed")
	configPath := filepath.Join(t.TempDir(), "agent.json")

	originalInstall := installLinuxAgentServiceForUpdate
	installLinuxAgentServiceForUpdate = func(string) error {
		return restoreErr
	}
	t.Cleanup(func() {
		installLinuxAgentServiceForUpdate = originalInstall
	})

	err := restoreLinuxRuntimeAfterUpdateFailure(configPath, nil, updateErr)
	if !errors.Is(err, updateErr) {
		t.Fatalf("recovery error %v does not preserve update failure", err)
	}
	if !errors.Is(err, restoreErr) {
		t.Fatalf("recovery error %v does not preserve restart failure", err)
	}
}
