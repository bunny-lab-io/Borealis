package agentruntime

import (
	"fmt"
	"strings"
	"time"
)

type watchdogDecisionInput struct {
	ServiceExists          bool
	ServiceRunning         bool
	ServicePID             uint32
	LivenessPID            int
	LastLocalTickAt        int64
	LastHeartbeatAttemptAt int64
	LastHeartbeatSuccessAt int64
	LastHeartbeatError     string
	LastSocketState        string
	LastSocketStateAt      int64
	Now                    time.Time
	UpdateActive           bool
	UpdateExpired          bool
}

const (
	watchdogStaleAfter          = 180 * time.Second
	watchdogSocketStaleAfter    = 180 * time.Second
	watchdogHeartbeatStaleAfter = 240 * time.Second
)

type watchdogDecision struct {
	Action  string
	Outcome string
	Reason  string
}

func decideWatchdogRecovery(input watchdogDecisionInput) watchdogDecision {
	if input.UpdateActive && !input.UpdateExpired {
		return watchdogDecision{Action: "check_liveness", Outcome: "skipped", Reason: "update_active"}
	}
	if !input.ServiceExists {
		return watchdogDecision{Action: "repair_service", Outcome: "missing", Reason: "service_missing"}
	}
	if !input.ServiceRunning {
		return watchdogDecision{Action: "start_service", Outcome: "needed", Reason: "service_stopped"}
	}
	if input.ServicePID > 0 && input.LivenessPID > 0 && int(input.ServicePID) != input.LivenessPID {
		return restartWatchdogDecision(input, fmt.Sprintf("pid_mismatch_service=%d_liveness=%d", input.ServicePID, input.LivenessPID))
	}
	if input.LastLocalTickAt <= 0 {
		return watchdogDecision{Action: "check_liveness", Outcome: "unknown", Reason: "no_local_tick"}
	}
	age := input.Now.Sub(time.Unix(input.LastLocalTickAt, 0))
	if age > watchdogStaleAfter {
		return restartWatchdogDecision(input, fmt.Sprintf("stale_liveness_age=%s", age.Round(time.Second)))
	}
	socketState := strings.ToLower(strings.TrimSpace(input.LastSocketState))
	if (socketState == "disconnected" || socketState == "connecting") && input.LastSocketStateAt > 0 {
		socketAge := input.Now.Sub(time.Unix(input.LastSocketStateAt, 0))
		if socketAge > watchdogSocketStaleAfter {
			return restartWatchdogDecision(input, fmt.Sprintf("socket_%s_age=%s", socketState, socketAge.Round(time.Second)))
		}
	}
	if input.LastHeartbeatSuccessAt > 0 {
		heartbeatAge := input.Now.Sub(time.Unix(input.LastHeartbeatSuccessAt, 0))
		if heartbeatAge > watchdogHeartbeatStaleAfter {
			if socketConnectedRecently(input, watchdogSocketStaleAfter) {
				return watchdogDecision{Action: "check_liveness", Outcome: "healthy", Reason: fmt.Sprintf("age=%s heartbeat_stale_age=%s socket_alive", age.Round(time.Second), heartbeatAge.Round(time.Second))}
			}
			return restartWatchdogDecision(input, fmt.Sprintf("stale_heartbeat_success_age=%s", heartbeatAge.Round(time.Second)))
		}
	} else if input.LastHeartbeatAttemptAt > 0 {
		attemptAge := input.Now.Sub(time.Unix(input.LastHeartbeatAttemptAt, 0))
		if attemptAge > watchdogHeartbeatStaleAfter {
			if socketConnectedRecently(input, watchdogSocketStaleAfter) {
				return watchdogDecision{Action: "check_liveness", Outcome: "healthy", Reason: fmt.Sprintf("age=%s heartbeat_attempt_stale_age=%s socket_alive", age.Round(time.Second), attemptAge.Round(time.Second))}
			}
			return restartWatchdogDecision(input, fmt.Sprintf("heartbeat_never_succeeded_attempt_age=%s", attemptAge.Round(time.Second)))
		}
	}
	return watchdogDecision{Action: "check_liveness", Outcome: "healthy", Reason: fmt.Sprintf("age=%s", age.Round(time.Second))}
}

func socketConnectedRecently(input watchdogDecisionInput, maxAge time.Duration) bool {
	if strings.ToLower(strings.TrimSpace(input.LastSocketState)) != "connected" || input.LastSocketStateAt <= 0 {
		return false
	}
	return input.Now.Sub(time.Unix(input.LastSocketStateAt, 0)) <= maxAge
}

func restartWatchdogDecision(input watchdogDecisionInput, reason string) watchdogDecision {
	if input.UpdateActive && !input.UpdateExpired {
		return watchdogDecision{Action: "restart_service", Outcome: "skipped", Reason: "update_active"}
	}
	return watchdogDecision{Action: "restart_service", Outcome: "needed", Reason: reason}
}
