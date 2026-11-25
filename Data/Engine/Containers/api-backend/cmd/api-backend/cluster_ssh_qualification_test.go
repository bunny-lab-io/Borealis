package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClusterSSHQualificationDistinguishesSourceFromProfileFailure(t *testing.T) {
	for _, sourceOK := range []bool{false, true} {
		f := newSSHNetworkTargetsFixture(t, false, "peer")
		reads := 0
		err := runClusterSSHNetworkTargets(context.Background(), f.a.Baseline, f.claims, f.deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
			sourceRead, profileErr := qualifyClusterSSHHostProfile(ctx, readers, func(context.Context) (clusterSSHPreparationSnapshot, error) {
				reads++
				if !sourceOK {
					return clusterSSHPreparationSnapshot{}, errors.New("source unavailable")
				}
				// Acquired successfully, but insufficient to establish profile sizing.
				return clusterSSHPreparationSnapshot{}, nil
			})
			if sourceRead != sourceOK || profileErr == nil {
				t.Fatalf("source success %t, acquired %t, profile error %v", sourceOK, sourceRead, profileErr)
			}
			return profileErr
		})
		if err == nil || reads != 1 {
			t.Fatalf("expected failed qualification after one source read, got %d reads, error %v", reads, err)
		}
	}
}

func TestClusterSSHQualificationReportBoundary(t *testing.T) {
	r := newClusterSSHQualificationReport(1)
	r.Checks[0].State = "blocked"
	raw, _ := json.Marshal(r)
	if _, err := parseClusterSSHQualificationReport(string(raw), 1); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"unknown", "duplicate", "alias", "ready", "attempt", "future", "missing check", "wrong code", "completed preparation", "storage shape"} {
		t.Run(mode, func(t *testing.T) {
			value := newClusterSSHQualificationReport(1)
			switch mode {
			case "ready":
				value.Ready = true
			case "attempt":
				value.Attempt = 2
			case "future":
				value.ObservedAt += 60
			case "missing check":
				value.Checks = value.Checks[:1]
			case "wrong code":
				value.Checks[1].Code = "command"
			case "completed preparation":
				value.Checks[len(value.Checks)-1].State = "passed"
			case "storage shape":
				value.Storage = nil
			}
			wire, _ := json.Marshal(value)
			switch mode {
			case "unknown":
				wire = bytes.Replace(wire, []byte(`"version":1`), []byte(`"version":1,"secret":"hidden"`), 1)
			case "duplicate":
				wire = bytes.Replace(wire, []byte(`"ready":false`), []byte(`"ready":true,"ready":false`), 1)
			case "alias":
				wire = bytes.Replace(wire, []byte(`"version"`), []byte(`"Version"`), 1)
			}
			if _, err := parseClusterSSHQualificationReport(string(wire), 1); err == nil {
				t.Fatal("malformed qualification accepted")
			}
		})
	}
	value := newClusterSSHQualificationReport(1)
	value.Storage = []clusterSSHTargetStorageCapacity{{TargetID: newClusterUUID(), StoragePath: "/var/lib/longhorn", Fits: true, Budgets: []clusterSSHStorageBudget{{Filesystem: strings.Repeat("1", 16), AvailableBytes: 10, OtherBytes: 10, Fits: true}}}}
	if value.valid(1) {
		t.Fatal("false fit accepted")
	}
	// Unsupported mutable baselines yield a static block before clients/SSH.
	got := runClusterSSHQualification(context.Background(), nil, nil, clusterSSHQualificationWork{Proof: clusterSSHParentProof{Cohort: clusterSSHInspectionCohort{Attempt: 1}}})
	if !got.valid(1) || got.Checks[0].State != "blocked" || got.Ready {
		t.Fatal("baseline bypass")
	}
}

func TestClusterSSHQualificationTotalDeadline(t *testing.T) {
	f := newSSHNetworkTargetsFixture(t, false, "peer")
	now := time.Now().Unix()
	work := clusterSSHQualificationWork{Proof: clusterSSHParentProof{Cohort: f.a.Cohort}}
	work.Proof.Cohort.Targets[0].InspectedAt = now - 18*60
	work.Proof.Cohort.Targets[1].InspectedAt = now - 19*60
	if want := now + 41*60; work.deadline().Unix() != want {
		t.Fatalf("expected original pair deadline %d, got %v", want, work.deadline())
	}
	f.a.Cohort.ObservedAt = now
	for i := range f.claims {
		f.claims[i].Lease.OperationStep = clusterSSHQualificationStep
		f.claims[i].Lease.Step = "qualify"
	}
	check := func(parent context.Context, want time.Time) {
		t.Helper()
		called := false
		err := runClusterSSHNetworkTargets(parent, f.a.Baseline, f.claims, f.deps, func(ctx context.Context, _ clusterSSHNetworkTargetReaders) error {
			called = true
			end, ok := ctx.Deadline()
			if !ok || end.Before(want.Add(-time.Second)) || end.After(want.Add(time.Second)) {
				t.Fatalf("unexpected target reader deadline %v, expected %v", end, want)
			}
			return nil
		})
		if err != nil || !called {
			t.Fatal("active qualification did not consume original claims", err)
		}
	}
	check(context.Background(), time.Now().Add(time.Hour))
	parent, stop := context.WithDeadline(context.Background(), work.deadline())
	defer stop()
	check(parent, work.deadline())
	f.a.Cohort.Targets[1].InspectedAt = now - 60*60
	if validateClusterSSHInspectionCohort(f.a.Cohort, f.a.Source) == nil {
		t.Fatal("expired original inspection accepted")
	}
}

func TestClusterSSHQualificationSnapshotAdmissionWindow(t *testing.T) {
	r, _, _ := sshBrokerFixture(t)
	r.Lease.OperationStep, r.Lease.Step = clusterSSHQualificationStep, "qualify"
	now := time.Now()
	r.AcceptBefore, r.ExpiresAt = now.Unix()+35, now.Unix()+65
	if !r.validSource(now) || r.snapshotTimeout() != time.Minute || r.valid(now) ||
		r.validSource(now.Add(35*time.Second)) {
		t.Fatal("incorrect qualification source route or arrival deadline")
	}
	r.ExpiresAt++
	if r.validSource(now) {
		t.Fatal("unsigned budget extension accepted")
	}
	r.ExpiresAt--
	r.Lease.OperationStep, r.Lease.Step = clusterSSHPreparationOperationStep, "stage_source"
	if r.validSource(now) {
		t.Fatal("unmarked preparation lease used the longer time allowance")
	}
	r.Joining = true
	if !r.validSource(now) || r.snapshotTimeout() != time.Minute || r.valid(now) || r.validSource(now.Add(35*time.Second)) {
		t.Fatal("joining storage snapshot must keep the original admission cutoff")
	}
	r.Joined = true
	if r.validSource(now) {
		t.Fatal("extended storage read admitted a joined-target request")
	}
}
