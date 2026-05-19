package agentruntime

import (
	"fmt"
	"time"
)

type watchdogDecisionInput struct {
	ServiceExists   bool
	ServiceRunning  bool
	LastLocalTickAt int64
	Now             time.Time
	UpdateActive    bool
}

const watchdogStaleAfter = 180 * time.Second

type watchdogDecision struct {
	Action  string
	Outcome string
	Reason  string
}

func decideWatchdogRecovery(input watchdogDecisionInput) watchdogDecision {
	if !input.ServiceExists {
		return watchdogDecision{Action: "repair_service", Outcome: "missing", Reason: "service_missing"}
	}
	if !input.ServiceRunning {
		return watchdogDecision{Action: "start_service", Outcome: "needed", Reason: "service_stopped"}
	}
	if input.LastLocalTickAt <= 0 {
		return watchdogDecision{Action: "check_liveness", Outcome: "unknown", Reason: "no_local_tick"}
	}
	age := input.Now.Sub(time.Unix(input.LastLocalTickAt, 0))
	if age <= watchdogStaleAfter {
		return watchdogDecision{Action: "check_liveness", Outcome: "healthy", Reason: fmt.Sprintf("age=%s", age.Round(time.Second))}
	}
	if input.UpdateActive {
		return watchdogDecision{Action: "restart_service", Outcome: "skipped", Reason: "update_active"}
	}
	return watchdogDecision{Action: "restart_service", Outcome: "needed", Reason: fmt.Sprintf("stale_liveness_age=%s", age.Round(time.Second))}
}
