package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"time"
)

const (
	clusterAutomaticRecoveryField = "automatic_recovery"
	clusterAutomaticRecoveryStep  = "observe_automatic_recovery"
	clusterAutomaticRecoveryTime  = 5 * time.Minute
	clusterAutomaticHealthyTime   = 5 * time.Minute
)

// This record is observation evidence, never a host-action or return token.
// Runtime dispatch must separately prove an exact automatic maintenance entry.
type clusterAutomaticRecoveryRecord struct {
	Version          int    `json:"version"`
	Origin           string `json:"origin"`
	ClusterID        string `json:"cluster_id"`
	OperationID      string `json:"operation_id"`
	NodeID           string `json:"node_id"`
	NodeName         string `json:"node_name"`
	NodeUID          string `json:"node_uid"`
	OriginController string `json:"origin_controller"`
	Controller       string `json:"controller"`
	Session          string `json:"session"`
	Attempt          int64  `json:"attempt"`
	Sequence         int64  `json:"sequence"`
	DetectedAt       int64  `json:"detected_at"`
	RecoveryDeadline int64  `json:"recovery_deadline"`
	LastObservedAt   int64  `json:"last_observed_at"`
	RecoveredAt      int64  `json:"recovered_at"`
	HealthySince     int64  `json:"healthy_since"`
	Phase            string `json:"phase"`
	Reason           string `json:"reason"`
}

// Every required surface must be positively observed by the future native
// adapter. Zero values and observation errors cannot establish healthy time.
type clusterAutomaticRecoveryHealth struct {
	NodeName    string
	NodeUID     string
	Node        bool
	Application bool
	Database    bool
	Storage     bool
	Roles       bool
	AgentPaths  bool
}

func (h clusterAutomaticRecoveryHealth) healthy() bool {
	return h.Node && h.Application && h.Database && h.Storage && h.Roles && h.AgentPaths
}

// Observers are read-only and must honor cancellation; they receive no runtime
// action authority. Native health adapters remain a separate integration gate.
type clusterAutomaticRecoveryObserve func(context.Context, clusterControllerNode) (clusterAutomaticRecoveryHealth, error)

func (r clusterAutomaticRecoveryRecord) valid() bool {
	for _, id := range []string{r.ClusterID, r.OperationID, r.NodeID, r.NodeUID, r.Session} {
		if !clusterUUIDRE.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
			return false
		}
	}
	for _, holder := range []string{r.OriginController, r.Controller} {
		if strings.TrimSpace(holder) != holder || holder == "" || len(holder) > 1024 {
			return false
		}
	}
	if r.Version != 1 || r.Origin != "automatic" || !clusterControllerNodeRegex.MatchString(r.NodeName) ||
		r.Attempt < 1 || r.Sequence < 1 || r.DetectedAt <= 0 || r.DetectedAt > math.MaxInt64-int64(clusterAutomaticRecoveryTime/time.Second) ||
		r.RecoveryDeadline != r.DetectedAt+int64(clusterAutomaticRecoveryTime/time.Second) || r.LastObservedAt < r.DetectedAt {
		return false
	}
	switch r.Phase {
	case "recovering", "blocked":
		return r.HealthySince == 0 && r.RecoveredAt == 0 && textInSet(r.Reason, "required_health_unavailable", "observation_unknown", "observation_gap", "observer_changed", "recovery_deadline", "node_identity_changed", "clock_regressed")
	case "soaking", "health_observed":
		return r.Reason == "" && r.HealthySince >= r.DetectedAt && r.RecoveredAt == r.HealthySince &&
			r.RecoveredAt <= r.RecoveryDeadline && r.HealthySince <= r.LastObservedAt &&
			(r.Phase != "health_observed" || r.LastObservedAt-r.HealthySince >= int64(clusterAutomaticHealthyTime/time.Second))
	default:
		return false
	}
}

func parseClusterAutomaticRecovery(payload map[string]any) (clusterAutomaticRecoveryRecord, error) {
	var r clusterAutomaticRecoveryRecord
	raw, err := json.Marshal(payload[clusterAutomaticRecoveryField])
	if err != nil {
		return r, errClusterConflict
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&r) != nil || !r.valid() {
		return clusterAutomaticRecoveryRecord{}, errClusterConflict
	}
	return r, nil
}

func advanceClusterAutomaticRecovery(r clusterAutomaticRecoveryRecord, controller, session string, now int64, health clusterAutomaticRecoveryHealth, observationErr error) (clusterAutomaticRecoveryRecord, error) {
	if !r.valid() || r.Phase == "blocked" || r.Sequence == math.MaxInt64 || r.Attempt == math.MaxInt64 {
		return r, errClusterConflict
	}
	next := r
	next.Sequence++
	block := func(reason string) (clusterAutomaticRecoveryRecord, error) {
		next.Phase, next.Reason, next.HealthySince, next.RecoveredAt = "blocked", reason, 0, 0
		return next, nil
	}
	if now < r.LastObservedAt {
		return block("clock_regressed")
	}
	next.LastObservedAt = now
	changed := controller != r.Controller || session != r.Session
	if changed {
		next.Controller, next.Session, next.Attempt = controller, session, r.Attempt+1
	}
	if !next.valid() {
		return r, errClusterConflict
	}
	if observationErr == nil && (health.NodeName != r.NodeName || health.NodeUID != r.NodeUID) {
		return block("node_identity_changed")
	}
	gap := now-r.LastObservedAt > int64(clusterControllerLeaseRenewalGrace/time.Second)
	if changed || gap || observationErr != nil || !health.healthy() {
		next.HealthySince, next.RecoveredAt = 0, 0
		next.Phase, next.Reason = "recovering", "required_health_unavailable"
		switch {
		case changed:
			next.Reason = "observer_changed"
		case gap:
			next.Reason = "observation_gap"
		case observationErr != nil:
			next.Reason = "observation_unknown"
		}
	}
	if next.HealthySince == 0 && now > next.RecoveryDeadline {
		return block("recovery_deadline")
	}
	if observationErr == nil && health.healthy() {
		if next.HealthySince == 0 {
			next.HealthySince, next.RecoveredAt = now, now
		}
		next.Phase, next.Reason = "soaking", ""
		if now-next.HealthySince >= int64(clusterAutomaticHealthyTime/time.Second) {
			next.Phase = "health_observed"
		}
	}
	return next, nil
}

// One observer session belongs to one coordinator lifetime. Any failed call
// invalidates continuity even if database failure prevented recording a reset.
// A new controller/session increments the retained operation attempt, so late
// observations from the former attempt cannot commit.
type clusterAutomaticRecoveryCoordinator struct {
	mu         sync.Mutex
	controller *clusterController
	session    string
}

func newClusterAutomaticRecoveryCoordinator(c *clusterController) *clusterAutomaticRecoveryCoordinator {
	return &clusterAutomaticRecoveryCoordinator{controller: c, session: newClusterUUID()}
}

func (c *clusterAutomaticRecoveryCoordinator) Reserve(parent context.Context, nodeID string, observe clusterAutomaticRecoveryObserve) (record clusterAutomaticRecoveryRecord, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	completed := false
	defer func() {
		if !completed || err != nil {
			c.session = newClusterUUID()
		}
	}()
	if c.controller == nil || c.controller.store == nil || c.controller.store.db == nil || observe == nil || !clusterUUIDRE.MatchString(nodeID) {
		return record, errClusterConflict
	}
	ctx, cancel := context.WithTimeout(parent, clusterControllerLeaseAcquireTimeout)
	defer cancel()
	record, err = c.reserve(ctx, nodeID, observe)
	completed = true
	return record, err
}

func (c *clusterAutomaticRecoveryCoordinator) Observe(parent context.Context, operationID string, observe clusterAutomaticRecoveryObserve) (record clusterAutomaticRecoveryRecord, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	completed := false
	defer func() {
		if !completed || err != nil {
			c.session = newClusterUUID()
		}
	}()
	if c.controller == nil || c.controller.store == nil || c.controller.store.db == nil || observe == nil || !clusterUUIDRE.MatchString(operationID) {
		return record, errClusterConflict
	}
	ctx, cancel := context.WithTimeout(parent, clusterControllerLeaseAcquireTimeout)
	defer cancel()
	record, err = c.observe(ctx, operationID, observe)
	completed = true
	return record, err
}

var errClusterAutomaticRecoveryHealthy = errors.New("automatic recovery requires observed unavailable health")
