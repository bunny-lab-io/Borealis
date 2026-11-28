package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"borealis/api-backend/internal/clusterbootstrap"
)

func TestPairTargetCapacityReportsMeasuredBounds(t *testing.T) {
	const availableKiB = (clusterSSHHostMemoryReserveBytes + 1023) / 1024
	cpu, err := pairTargetCapacity(0, 800, 0, 200, 1000, availableKiB)
	if err != nil || cpu != 1000 {
		t.Fatalf("exact fit rejected: cpu=%d err=%v", cpu, err)
	}
	for _, tc := range []struct {
		availableCPU, memoryKiB uint64
		want                    string
	}{
		{999, availableKiB, "required_cpu_milli=1000 available_cpu_milli=999"},
		{1000, availableKiB - 1, "required_memory_kib=7324219 available_memory_kib=7324218"},
	} {
		_, err := pairTargetCapacity(1, 800, 0, 200, tc.availableCPU, tc.memoryKiB)
		if !errors.Is(err, clusterbootstrap.ErrPreparationConfig) || !strings.Contains(err.Error(), "pair target 2 capacity") || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("missing capacity evidence: %v", err)
		}
	}
	if _, err := pairTargetCapacity(0, clusterSSHCapacityLimit, 0, 1, 1000, availableKiB); !errors.Is(err, clusterbootstrap.ErrPreparationConfig) {
		t.Fatal("overflow accepted")
	}
}

func TestExplicitPairBootHandoffPreservesInputs(t *testing.T) {
	cohort, original, lease, base := sshPreparationFixture(t)
	old := original.Members[0]
	pin := clusterbootstrap.PairExecutorPin{Plan: strings.Repeat("f", 64), Boot: clusterbootstrap.PairBootHandoff{Before: old.BootID, After: newClusterUUID(), SourceUID: old.NodeUID, Database: "98765", Journal: strings.Repeat("a", 64)}}
	record := clusterSSHPairResume{Plan: pin.Plan, Boot: &pin.Boot}
	raw, _ := json.Marshal(map[string]any{"ssh_pair_resume": record})
	view, err := projectPairSourceBoot(pin, string(raw), original, true)
	if err != nil || view.Members[0].BootID != pin.Boot.After || original.Members[0] != old {
		t.Fatal("observation changed frozen source")
	}
	frozen, err := projectPairSourceBoot(pin, string(raw), view, false)
	if err != nil || !reflect.DeepEqual(frozen, original) {
		t.Fatal("frozen source mismatch")
	}
	input := func(s clusterSSHSourceCohort) string {
		e, err := buildClusterSSHPreparationExpected(cohort, s, lease, base, "v1.36.1+k3s1", "10.42.0.0/16", "10.43.0.0/16")
		if err != nil {
			t.Fatal(err)
		}
		return e.CohortSHA256
	}
	if input(original) != input(frozen) || input(view) == input(original) {
		t.Fatal("journal input must use original source incarnation")
	}
	view.Members[0].BootID = newClusterUUID()
	if _, err := projectPairSourceBoot(pin, string(raw), view, false); err == nil {
		t.Fatal("unapproved third boot accepted")
	}
	for _, field := range []string{pin.Boot.Journal, pin.Boot.Database, pin.Boot.SourceUID, pin.Plan, pin.Boot.After} {
		if _, err := projectPairSourceBoot(pin, strings.Replace(string(raw), field, "changed", 1), original, true); err == nil {
			t.Fatal("changed handoff accepted")
		}
	}
}

func TestResumedPairUsesOneFreshGrant(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	c := clusterSSHPreparationConfirmation{PlanSHA256: strings.Repeat("e", 64), ConfirmedAt: now.Add(-24 * time.Hour).Unix()}
	build := func(plan string, at int64) string {
		r, _ := json.Marshal(map[string]any{"ssh_pair_reconciliation": 2, "ssh_preparation_readiness": map[string]int{"version": 5}, "ssh_pair_resume": clusterSSHPairResume{Plan: plan, AuthorizedAt: at}})
		return string(r)
	}
	payload := build(c.PlanSHA256, now.Unix())
	d, err := pairExecutionDeadline(payload, c, time.Hour, now.Add(time.Minute))
	if err != nil || !d.Equal(now.Add(time.Hour)) {
		t.Fatal("restarted worker extended or lost deadline", d, err)
	}
	for _, p := range []string{"{}", build(c.PlanSHA256, now.Add(-time.Hour).Unix()), build(c.PlanSHA256, now.Add(time.Minute).Unix()), build("other", now.Unix())} {
		if _, err := pairExecutionDeadline(p, c, time.Hour, now); err == nil {
			t.Fatal("invalid or expired grant accepted")
		}
	}
}
