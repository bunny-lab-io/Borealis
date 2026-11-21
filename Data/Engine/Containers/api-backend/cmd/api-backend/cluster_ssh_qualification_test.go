package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
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
