package agentruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	"github.com/bunny-lab-io/borealis/go-agent/internal/logutil"
)

const livenessTickInterval = 15 * time.Second

func (a *Agent) updateLiveness(update func(*agentconfig.AgentLivenessSection)) {
	if a == nil || strings.TrimSpace(a.configPath) == "" {
		return
	}
	err := agentconfig.Update(a.configPath, func(cfg *agentconfig.AgentConfig) {
		if update != nil {
			update(&cfg.Agent.Liveness)
		}
	})
	if err != nil && a.logger != nil {
		a.logger.Printf("liveness update failed: %v", err)
	}
}

func (a *Agent) startLivenessLoop(ctxDone <-chan struct{}) {
	a.updateLiveness(func(l *agentconfig.AgentLivenessSection) {
		now := time.Now().Unix()
		l.PID = os.Getpid()
		l.BootID = a.bootID
		l.StartedAt = now
		l.LastLocalTickAt = now
	})
	go func() {
		ticker := time.NewTicker(livenessTickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctxDone:
				return
			case <-ticker.C:
				a.updateLiveness(func(l *agentconfig.AgentLivenessSection) {
					l.PID = os.Getpid()
					l.BootID = a.bootID
					l.LastLocalTickAt = time.Now().Unix()
				})
			}
		}
	}()
}

func (a *Agent) recordHeartbeatAttempt() {
	a.updateLiveness(func(l *agentconfig.AgentLivenessSection) {
		l.LastHeartbeatAttemptAt = time.Now().Unix()
	})
}

func (a *Agent) recordHeartbeatResult(err error) {
	a.updateLiveness(func(l *agentconfig.AgentLivenessSection) {
		if err != nil {
			l.LastHeartbeatError = err.Error()
			return
		}
		l.LastHeartbeatSuccessAt = time.Now().Unix()
		l.LastHeartbeatError = ""
	})
}

func (a *Agent) recordSocketState(state string) {
	clean := strings.TrimSpace(state)
	if clean == "" {
		return
	}
	a.updateLiveness(func(l *agentconfig.AgentLivenessSection) {
		l.LastSocketState = clean
		l.LastSocketStateAt = time.Now().Unix()
	})
}

func (a *Agent) logRecovery(component string, roleID string, action string, outcome string, reason string, err error) {
	if a == nil {
		return
	}
	logPath := filepath.Join(filepath.Dir(a.configPath), "Logs", "Agent", "role_recovery.log")
	errorText := ""
	if err != nil {
		errorText = err.Error()
	}
	logutil.Append(
		logPath,
		logutil.RetentionDaysFromConfig(a.configPath),
		"[%s] [role-recovery] component=%s role_id=%s action=%s outcome=%s reason=%s error=%s",
		time.Now().Format("2006-01-02T15:04:05"),
		logField(component),
		logField(roleID),
		logField(action),
		logField(outcome),
		logField(reason),
		logField(errorText),
	)
}

func logField(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return "-"
	}
	return strings.ReplaceAll(text, "\n", " ")
}

func (a *Agent) recordRecoveryAction(action string, reason string) {
	message := strings.TrimSpace(action)
	if strings.TrimSpace(reason) != "" {
		message = fmt.Sprintf("%s:%s", message, strings.TrimSpace(reason))
	}
	a.updateLiveness(func(l *agentconfig.AgentLivenessSection) {
		l.LastRecoveryAction = message
		l.LastRecoveryAt = time.Now().Unix()
	})
}
