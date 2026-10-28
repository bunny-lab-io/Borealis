package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func automaticRecoveryFixture() (clusterAutomaticRecoveryRecord, clusterAutomaticRecoveryHealth) {
	r := clusterAutomaticRecoveryRecord{
		Version: 1, Origin: "automatic", ClusterID: newClusterUUID(), OperationID: newClusterUUID(), NodeID: newClusterUUID(),
		NodeName: "engine-2", NodeUID: newClusterUUID(), OriginController: "controller-original", Controller: "controller-original",
		Session: newClusterUUID(), Attempt: 1, Sequence: 1, DetectedAt: 1000, RecoveryDeadline: 1300, LastObservedAt: 1000,
		Phase: "recovering", Reason: "required_health_unavailable",
	}
	h := clusterAutomaticRecoveryHealth{NodeName: r.NodeName, NodeUID: r.NodeUID, Node: true, Application: true, Database: true, Storage: true, Roles: true, AgentPaths: true}
	return r, h
}

func TestClusterAutomaticRecoveryRequiresContinuousHealth(t *testing.T) {
	r, h := automaticRecoveryFixture()
	for now := int64(1010); now <= 1310; now += int64(clusterControllerLeaseRenewInterval / time.Second) {
		next, err := advanceClusterAutomaticRecovery(r, r.Controller, r.Session, now, h, nil)
		if err != nil || !next.valid() {
			t.Fatal("healthy observation failed", err)
		}
		want := "soaking"
		if now == 1310 {
			want = "health_observed"
		}
		if next.Phase != want || next.HealthySince != 1010 || next.RecoveredAt != 1010 || next.RecoveryDeadline != 1300 {
			t.Fatalf("continuous health lost deadline/soak: %+v", next)
		}
		r = next
	}
	// Observed health still grants no executable operation, and a later failure
	// cannot restart the original five-minute recovery budget.
	h.Application = false
	next, err := advanceClusterAutomaticRecovery(r, r.Controller, r.Session, 1311, h, nil)
	if err != nil || next.Phase != "blocked" || next.Reason != "recovery_deadline" || next.HealthySince != 0 {
		t.Fatalf("late recurrence restarted budget: %+v %v", next, err)
	}
}

func TestClusterAutomaticRecoveryResetsUnknownSparseAndRestartedHealth(t *testing.T) {
	for _, mode := range []string{"node", "application", "database", "storage", "roles", "agent", "unknown", "gap", "session", "controller", "clock", "identity"} {
		t.Run(mode, func(t *testing.T) {
			r, h := automaticRecoveryFixture()
			r, _ = advanceClusterAutomaticRecovery(r, r.Controller, r.Session, 1001, h, nil)
			now, controller, session := int64(1002), r.Controller, r.Session
			var unavailable error
			switch mode {
			case "node":
				h.Node = false
			case "application":
				h.Application = false
			case "database":
				h.Database = false
			case "storage":
				h.Storage = false
			case "roles":
				h.Roles = false
			case "agent":
				h.AgentPaths = false
			case "unknown":
				unavailable = errors.New("private observation failure")
			case "gap":
				now += int64(clusterControllerLeaseRenewalGrace / time.Second)
			case "session":
				session = newClusterUUID()
			case "controller":
				controller, session = "replacement-controller", newClusterUUID()
			case "clock":
				now = 999
			case "identity":
				h.NodeUID = newClusterUUID()
			}
			next, err := advanceClusterAutomaticRecovery(r, controller, session, now, h, unavailable)
			if err != nil || !next.valid() || next.HealthySince == r.HealthySince || next.Phase == "health_observed" || next.RecoveryDeadline != r.RecoveryDeadline {
				t.Fatalf("interruption retained healthy interval: %+v %v", next, err)
			}
			if mode == "controller" || mode == "session" {
				if next.Attempt != r.Attempt+1 || next.OriginController != r.OriginController || next.Session != session || next.Controller != controller {
					t.Fatalf("observer change lost generation/origin: %+v", next)
				}
			}
			if (mode == "clock" || mode == "identity") && next.Phase != "blocked" {
				t.Fatal("unsafe clock or identity allowed continuation")
			}
		})
	}
	r, h := automaticRecoveryFixture()
	r, _ = advanceClusterAutomaticRecovery(r, r.Controller, r.Session, 1000, h, nil)
	r, err := advanceClusterAutomaticRecovery(r, r.Controller, r.Session, 1300, h, nil)
	if err != nil || r.Phase != "soaking" || r.HealthySince != 1300 {
		t.Fatal("two sparse samples counted as uninterrupted health", r, err)
	}
}

func TestClusterAutomaticRecoveryDeadlineAndUncertainOutcome(t *testing.T) {
	for _, now := range []int64{1300, 1301} {
		r, h := automaticRecoveryFixture()
		next, err := advanceClusterAutomaticRecovery(r, r.Controller, r.Session, now, h, nil)
		if err != nil || !next.valid() {
			t.Fatal(err)
		}
		want := "soaking"
		if now > r.RecoveryDeadline {
			want = "blocked"
		}
		if next.Phase != want {
			t.Fatalf("recovery at %d got %s", now, next.Phase)
		}
	}
	r, h := automaticRecoveryFixture()
	next, err := advanceClusterAutomaticRecovery(r, r.Controller, r.Session, 1301, h, errors.New("transport lost"))
	if err != nil || next.Phase != "blocked" || next.HealthySince != 0 {
		t.Fatal("uncertain result counted as recovery", next, err)
	}
	if _, err := advanceClusterAutomaticRecovery(next, r.Controller, r.Session, 1302, h, nil); err == nil {
		t.Fatal("blocked recovery automatically retried")
	}
}

func TestClusterAutomaticRecoveryPayloadAndGenericRetryFence(t *testing.T) {
	r, _ := automaticRecoveryFixture()
	payload := map[string]any{clusterAutomaticRecoveryField: r}
	decoded, err := parseClusterAutomaticRecovery(parseClusterJSON(marshalClusterJSON(payload)))
	if err != nil || decoded != r {
		t.Fatal("durable identity roundtrip failed", err)
	}
	for _, field := range []string{"origin", "version", "node_uid", "controller", "session", "attempt", "sequence", "detected_at", "recovery_deadline", "phase", "unexpected"} {
		changed := parseClusterJSON(marshalClusterJSON(r))
		changed[field] = "invalid"
		if field == "controller" {
			changed[field] = ""
		}
		if _, err := parseClusterAutomaticRecovery(map[string]any{clusterAutomaticRecoveryField: changed}); err == nil {
			t.Fatal("invalid durable field accepted", field)
		}
	}
	for _, value := range []any{r, nil, false, "malformed"} {
		if err := rejectClusterAutomaticRecoveryRetry("node_maintenance", marshalClusterJSON(map[string]any{clusterAutomaticRecoveryField: value})); err == nil {
			t.Fatal("automatic record became generic retry authority")
		}
	}
	if err := rejectClusterAutomaticRecoveryRetry("node_maintenance", `{"action":"exit","reason":"operator"}`); err != nil {
		t.Fatal("operator maintenance changed", err)
	}
}

func TestClusterAutomaticRecoveryCoordinatorFailureInvalidatesSession(t *testing.T) {
	c := newClusterAutomaticRecoveryCoordinator(nil)
	previous := c.session
	if _, err := c.Observe(context.Background(), newClusterUUID(), nil); err == nil || c.session == previous {
		t.Fatal("failed observation retained continuity")
	}
	previous = c.session
	if _, err := c.Reserve(context.Background(), newClusterUUID(), nil); err == nil || reflect.DeepEqual(c.session, previous) {
		t.Fatal("failed reservation retained observer session")
	}
}
