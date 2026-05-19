package agentruntime

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type RoleSnapshot struct {
	RoleID        string
	RoleName      string
	RoleLabel     string
	Context       string
	Status        string
	StatusCode    string
	Detail        string
	DesiredState  string
	ObservedState string
	Details       map[string]any
	CheckedAt     int64
}

type supervisedRoleState struct {
	snapshot         RoleSnapshot
	lastStatus       string
	lastDetail       string
	lastSuccessAt    int64
	lastError        string
	recoveryAttempts int
}

type RoleSupervisor struct {
	mu           sync.Mutex
	revision     int64
	roles        map[string]supervisedRoleState
	logRecovery  func(component string, roleID string, action string, outcome string, reason string, err error)
	recoveryPlan map[string]func(RoleSnapshot)
}

func NewRoleSupervisor(logRecovery func(component string, roleID string, action string, outcome string, reason string, err error)) *RoleSupervisor {
	return &RoleSupervisor{
		roles:       map[string]supervisedRoleState{},
		logRecovery: logRecovery,
	}
}

func (s *RoleSupervisor) Update(snapshots []RoleSnapshot) map[string]any {
	if s == nil {
		return map[string]any{"roles": []map[string]any{}, "reported_at": time.Now().Unix()}
	}
	now := time.Now().Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.roles == nil {
		s.roles = map[string]supervisedRoleState{}
	}
	s.revision++
	out := make([]map[string]any, 0, len(snapshots))
	for _, snapshot := range snapshots {
		normalized := normalizeRoleSnapshot(snapshot, now)
		state := s.roles[normalized.RoleID]
		previousStatus := state.lastStatus
		previousDetail := state.lastDetail
		state.snapshot = normalized
		state.lastStatus = normalized.StatusCode
		state.lastDetail = normalized.Detail
		if normalized.StatusCode == "healthy" || normalized.StatusCode == "not_applicable" || normalized.StatusCode == "unsupported" {
			state.lastSuccessAt = now
			state.lastError = ""
			if normalized.StatusCode == "healthy" {
				state.recoveryAttempts = 0
			}
		} else {
			state.lastError = normalized.Detail
			if previousStatus != normalized.StatusCode || previousDetail != normalized.Detail {
				state.recoveryAttempts++
			}
		}
		s.roles[normalized.RoleID] = state
		s.logRoleTransition(previousStatus, previousDetail, normalized, state)
		out = append(out, roleSnapshotMap(normalized, state, s.revision))
	}
	return map[string]any{
		"roles":               out,
		"reported_at":         now,
		"supervisor_revision": s.revision,
	}
}

func (s *RoleSupervisor) logRoleTransition(previousStatus string, previousDetail string, snapshot RoleSnapshot, state supervisedRoleState) {
	if s.logRecovery == nil {
		return
	}
	if previousStatus != "" && previousStatus != snapshot.StatusCode {
		s.logRecovery("role_supervisor", snapshot.RoleID, "status_transition", snapshot.StatusCode, snapshot.Detail, nil)
	}
	switch snapshot.StatusCode {
	case "unhealthy", "recovering", "failed":
		if previousStatus != snapshot.StatusCode || previousDetail != snapshot.Detail {
			reason := snapshot.Detail
			if strings.TrimSpace(reason) == "" {
				reason = fmt.Sprintf("observed_state=%s", snapshot.ObservedState)
			}
			s.logRecovery("role_supervisor", snapshot.RoleID, "health_check", snapshot.StatusCode, reason, nil)
		}
	}
}

func normalizeRoleSnapshot(snapshot RoleSnapshot, now int64) RoleSnapshot {
	snapshot.RoleID = strings.TrimSpace(snapshot.RoleID)
	snapshot.RoleName = strings.TrimSpace(snapshot.RoleName)
	snapshot.RoleLabel = strings.TrimSpace(snapshot.RoleLabel)
	snapshot.Context = strings.TrimSpace(snapshot.Context)
	snapshot.Status = strings.TrimSpace(snapshot.Status)
	snapshot.StatusCode = strings.ToLower(strings.TrimSpace(firstNonEmpty(snapshot.StatusCode, snapshot.Status, "unknown")))
	snapshot.Detail = strings.TrimSpace(snapshot.Detail)
	snapshot.DesiredState = strings.ToLower(strings.TrimSpace(snapshot.DesiredState))
	snapshot.ObservedState = strings.ToLower(strings.TrimSpace(snapshot.ObservedState))
	if snapshot.RoleID == "" {
		snapshot.RoleID = "role:" + strings.ToLower(strings.ReplaceAll(snapshot.RoleName, " ", "_"))
	}
	if snapshot.RoleLabel == "" {
		snapshot.RoleLabel = snapshot.RoleName
	}
	if snapshot.Status == "" {
		snapshot.Status = snapshot.StatusCode
	}
	if snapshot.DesiredState == "" {
		snapshot.DesiredState = desiredStateForStatus(snapshot.StatusCode)
	}
	if snapshot.ObservedState == "" {
		snapshot.ObservedState = observedStateForStatus(snapshot.StatusCode)
	}
	if snapshot.CheckedAt <= 0 {
		snapshot.CheckedAt = now
	}
	if snapshot.Details == nil {
		snapshot.Details = map[string]any{}
	}
	return snapshot
}

func roleSnapshotMap(snapshot RoleSnapshot, state supervisedRoleState, revision int64) map[string]any {
	details := map[string]any{}
	for key, value := range snapshot.Details {
		details[key] = value
	}
	details["desired_state"] = snapshot.DesiredState
	details["observed_state"] = snapshot.ObservedState
	details["supervisor_revision"] = revision
	return map[string]any{
		"role_id":           snapshot.RoleID,
		"role_name":         snapshot.RoleName,
		"role_label":        snapshot.RoleLabel,
		"context":           snapshot.Context,
		"status":            snapshot.Status,
		"status_code":       snapshot.StatusCode,
		"detail":            snapshot.Detail,
		"desired_state":     snapshot.DesiredState,
		"observed_state":    snapshot.ObservedState,
		"last_checked_at":   snapshot.CheckedAt,
		"last_success_at":   state.lastSuccessAt,
		"last_error":        state.lastError,
		"recovery_attempts": state.recoveryAttempts,
		"details":           details,
	}
}

func desiredStateForStatus(status string) string {
	switch status {
	case "unsupported", "not_applicable":
		return "disabled"
	default:
		return "running"
	}
}

func observedStateForStatus(status string) string {
	switch status {
	case "healthy":
		return "ready"
	case "recovering":
		return "recovering"
	case "unhealthy", "failed":
		return "failed"
	case "unsupported", "not_applicable":
		return "not_applicable"
	default:
		return "unknown"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
