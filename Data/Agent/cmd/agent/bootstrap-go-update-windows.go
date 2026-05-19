//go:build windows

package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
)

func stageAgentUpdateBinary(cfg BootstrapConfig, sourceRoot string, buildID string, logger *BootstrapLogger) (bool, error) {
	candidates := []string{
		filepath.Join(sourceRoot, "Agent.exe"),
		filepath.Join(sourceRoot, "Data", "Agent", "Agent.exe"),
		filepath.Join(sourceRoot, "Data", "Agent", "dist", "windows-amd64", "Agent.exe"),
		filepath.Join(sourceRoot, "dist", "windows-amd64", "Agent.exe"),
	}
	for _, candidate := range candidates {
		if !fileExists(candidate) {
			continue
		}
		destination := filepath.Join(cfg.InstallDir, "Agent.exe")
		pending := filepath.Join(cfg.InstallDir, "Agent.exe.update")
		if logger != nil {
			logger.Tracef("Staging Go Agent update binary: source=%s destination=%s", candidate, destination)
		}
		expectedSHA256, err := sha256File(candidate)
		if err != nil {
			return false, err
		}
		if err := copyFile(candidate, pending); err != nil {
			return false, err
		}
		if err := verifyFileSHA256(pending, expectedSHA256); err != nil {
			_ = os.Remove(pending)
			return false, err
		}
		exe, _ := os.Executable()
		if samePath(exe, destination) {
			if err := scheduleAgentSelfReplacement(cfg, pending, destination, buildID, expectedSHA256, logger); err != nil {
				_ = os.Remove(pending)
				return false, err
			}
			return true, nil
		}
		if err := copyFile(pending, destination); err != nil {
			if logger != nil {
				logger.Warnf("Agent.exe direct replacement failed; scheduling deferred replacement: %v", err)
			}
			if scheduleErr := scheduleAgentSelfReplacement(cfg, pending, destination, buildID, expectedSHA256, logger); scheduleErr != nil {
				_ = os.Remove(pending)
				return false, fmt.Errorf("replace Agent.exe: %w; schedule deferred replacement: %v", err, scheduleErr)
			}
			return true, nil
		}
		if err := verifyFileSHA256(destination, expectedSHA256); err != nil {
			return false, err
		}
		_ = os.Remove(pending)
		return false, nil
	}
	if sourceRootHasGoAgent(sourceRoot) {
		return false, fmt.Errorf("Go Agent source archive does not include a built Windows Agent.exe; update artifact must include Data\\Agent\\dist\\windows-amd64\\Agent.exe")
	}
	return false, fmt.Errorf("update artifact missing Agent.exe")
}

func scheduleAgentSelfReplacement(cfg BootstrapConfig, pending string, destination string, buildID string, expectedSHA256 string, logger *BootstrapLogger) error {
	if logger != nil {
		logger.Tracef("Scheduling Agent.exe self-replacement: pending=%s destination=%s expected_sha256=%s", pending, destination, expectedSHA256)
	}
	script := deferredReplacementScript(cfg, pending, destination, buildID, expectedSHA256)
	cmd := exec.Command(
		"powershell.exe",
		"-NoProfile",
		"-ExecutionPolicy",
		"Bypass",
		"-EncodedCommand",
		encodePowerShellCommand(script),
	)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func deferredReplacementScript(cfg BootstrapConfig, pending string, destination string, buildID string, expectedSHA256 string) string {
	logPath := filepath.Join(cfg.InstallDir, "Logs", "Agent", "updater.log")
	configPath := agentConfigPath(cfg.InstallDir)
	return fmt.Sprintf(`
$ErrorActionPreference = 'Continue'
$logPath = %s
$pending = %s
$destination = %s
$configPath = %s
$buildId = %s
$expectedSha256 = %s
$agentTaskName = %s
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $logPath) | Out-Null
function Write-UpdaterLog([string]$message) {
  Add-Content -LiteralPath $logPath -Value ("[{0}] {1}" -f (Get-Date).ToString("s"), $message)
}
function Start-AgentTask() {
  schtasks.exe /Run /TN $agentTaskName *> $null
  Write-UpdaterLog "Agent task restart requested."
}
Write-UpdaterLog "Deferred Agent.exe replacement starting. pending=$pending destination=$destination build=$buildId expected_sha256=$expectedSha256"
Start-Sleep -Seconds 3
for ($attempt = 1; $attempt -le 20; $attempt++) {
  try {
    if (!(Test-Path -LiteralPath $pending)) {
      Write-UpdaterLog "Attempt $attempt failed: pending binary missing."
      Start-AgentTask
      exit 1
    }
    schtasks.exe /End /TN $agentTaskName *> $null
    Get-Process -Name Agent -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 500
    Move-Item -LiteralPath $pending -Destination $destination -Force -ErrorAction Stop
    $actualSha256 = (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actualSha256 -ne $expectedSha256) {
      Write-UpdaterLog "Attempt $attempt failed: hash mismatch actual=$actualSha256."
      Start-AgentTask
      exit 1
    }
    $finalizeOutput = & $destination --finalize-update --config-path $configPath --build-id $buildId --expected-sha256 $expectedSha256 2>&1
    foreach ($line in $finalizeOutput) { Write-UpdaterLog ("finalize: " + $line) }
    if ($LASTEXITCODE -ne 0) {
      Write-UpdaterLog "Attempt $attempt failed: finalize exited $LASTEXITCODE."
      Start-AgentTask
      exit 1
    }
    Start-AgentTask
    Write-UpdaterLog "Deferred Agent.exe replacement complete."
    exit 0
  } catch {
    Write-UpdaterLog ("Attempt $attempt failed: " + $_.Exception.Message)
    Start-Sleep -Seconds 2
  }
}
Remove-Item -LiteralPath $pending -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath ($destination + '.tmp') -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath ($pending + '.tmp') -Force -ErrorAction SilentlyContinue
Write-UpdaterLog "Deferred Agent.exe replacement failed after all attempts; staged update artifacts removed."
Start-AgentTask
exit 1
`,
		powershellSingleQuoted(logPath),
		powershellSingleQuoted(pending),
		powershellSingleQuoted(destination),
		powershellSingleQuoted(configPath),
		powershellSingleQuoted(agentconfigBuildID(buildID)),
		powershellSingleQuoted(strings.ToLower(strings.TrimSpace(expectedSHA256))),
		powershellSingleQuoted(agentTaskName),
	)
}

func agentconfigBuildID(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func encodePowerShellCommand(script string) string {
	encoded := utf16.Encode([]rune(script))
	raw := make([]byte, len(encoded)*2)
	for i, item := range encoded {
		raw[i*2] = byte(item)
		raw[i*2+1] = byte(item >> 8)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func verifyFileSHA256(path string, expected string) error {
	expected = strings.TrimSpace(strings.ToLower(expected))
	if expected == "" {
		return nil
	}
	actual, err := sha256File(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("Agent binary hash mismatch expected=%s actual=%s", expected, actual)
	}
	return nil
}

func cleanupStaleAgentUpdateBinary(cfg BootstrapConfig, logger *BootstrapLogger) {
	destination := filepath.Join(cfg.InstallDir, "Agent.exe")
	pending := destination + ".update"
	now := time.Now()
	for _, tempPath := range []string{destination + ".tmp", pending + ".tmp"} {
		info, err := os.Stat(tempPath)
		if err != nil || info.IsDir() {
			continue
		}
		if now.Sub(info.ModTime()) < 30*time.Second {
			if logger != nil {
				logger.Tracef("Leaving fresh Agent staging temp artifact in place: path=%s mtime=%s", tempPath, info.ModTime().Format(time.RFC3339))
			}
			continue
		}
		if err := os.Remove(tempPath); err != nil {
			if logger != nil {
				logger.Tracef("Agent staging temp artifact cleanup skipped: path=%s err=%v", tempPath, err)
			}
			continue
		}
		if logger != nil {
			logger.Tracef("Removed stale Agent staging temp artifact: path=%s", tempPath)
		}
	}
	pendingInfo, err := os.Stat(pending)
	if err != nil || pendingInfo.IsDir() {
		return
	}
	destinationInfo, err := os.Stat(destination)
	if err != nil || destinationInfo.IsDir() {
		return
	}
	pendingHash, pendingHashErr := sha256File(pending)
	destinationHash, destinationHashErr := sha256File(destination)
	if pendingHashErr == nil && destinationHashErr == nil && strings.EqualFold(pendingHash, destinationHash) {
		_ = os.Remove(pending)
		if logger != nil {
			logger.Tracef("Removed stale Agent.exe.update: pending matches installed Agent.exe sha256=%s", pendingHash)
		}
		return
	}
	if !pendingInfo.ModTime().After(destinationInfo.ModTime().Add(1 * time.Second)) {
		_ = os.Remove(pending)
		if logger != nil {
			logger.Tracef("Removed stale Agent.exe.update: pending_mtime=%s installed_mtime=%s", pendingInfo.ModTime().Format(time.RFC3339), destinationInfo.ModTime().Format(time.RFC3339))
		}
		return
	}
	if time.Since(pendingInfo.ModTime()) > 2*time.Minute {
		_ = os.Remove(pending)
		if logger != nil {
			logger.Tracef("Removed abandoned Agent.exe.update: pending_mtime=%s age=%s", pendingInfo.ModTime().Format(time.RFC3339), time.Since(pendingInfo.ModTime()).Round(time.Second))
		}
	}
}

func copyReaderToFile(reader io.Reader, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, reader)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
