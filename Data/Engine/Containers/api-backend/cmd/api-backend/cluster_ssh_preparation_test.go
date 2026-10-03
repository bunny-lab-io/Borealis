package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"strings"
	"testing"
)

func sshPreparationFixture(t *testing.T) (clusterSSHInspectionCohort, clusterSSHSourceCohort, clusterSSHTargetLease, clusterbootstrap.Expected) {
	t.Helper()
	cohort, source := sshInspectionCohortFixture(t)
	lease := clusterSSHTargetLease{TargetID: cohort.Targets[0].Binding.TargetID, OperationID: cohort.OperationID, Holder: newClusterUUID(), Generation: 2,
		Step: "stage_source", ControllerHolder: cohort.ControllerHolder, OperationAttempt: cohort.Attempt, OperationStep: clusterSSHPreparationOperationStep, OperationKind: "ssh_onboarding"}
	release := clusterbootstrap.Expected{Repository: "bunny-lab-io/Borealis", Release: "2026.09.999-rc.1", SourceSHA: strings.Repeat("a", 40), AllowQualification: true}
	return cohort, source, lease, release
}

func sshPreparationRuntimeFixture() map[string]string {
	return map[string]string{"BOREALIS_PUBLIC_HOSTNAME": "engine.example.test", "BOREALIS_ENGINE_NETWORK_MODE": "public", "BOREALIS_AGENT_ENGINE_CA_PEM_B64": "",
		"BOREALIS_CLUSTER_SIZING_RANK": "0", "BOREALIS_CLUSTER_SIZING_MEMORY_MIB": "8192", "POSTGRES_DB": "borealis", "POSTGRES_USER": "borealis", "POSTGRES_PASSWORD": "private-test",
		"BOREALIS_DATABASE_URL": "postgresql://borealis:private-test@borealis-postgres-rw.borealis.svc:5432/borealis", "BOREALIS_OPERATOR_SECRET": "private-operator-test"}
}

func TestClusterSSHPreparationExpectedBindsEntireCohortAndFreshClaim(t *testing.T) {
	cohort, source, lease, release := sshPreparationFixture(t)
	build := func() (clusterbootstrap.PreparationExpected, error) {
		return buildClusterSSHPreparationExpected(cohort, source, lease, release, "v1.36.3+k3s1", "10.42.0.0/16", "10.43.0.0/16")
	}
	expected, err := build()
	if err != nil {
		t.Fatal(err)
	}
	if expected.Target.TargetID != lease.TargetID || expected.Target.Generation != lease.Generation || expected.Target.HolderID != lease.Holder || expected.TargetBootID != cohort.Targets[0].Report.BootID || len(expected.PeerAddresses) != 3 {
		t.Fatal("incomplete target/cohort binding")
	}
	cohort.ObservedAt++
	fresh, err := build()
	if err != nil || fresh.CohortSHA256 != expected.CohortSHA256 {
		t.Fatal("observation clock relabeled stable proof")
	}
	// A changed second target/source identity must invalidate the first target's package.
	cohort.Targets[1].Report.BootID = newClusterUUID()
	changed, err := build()
	if err != nil || changed.CohortSHA256 == expected.CohortSHA256 {
		t.Fatal("other target identity omitted from binding")
	}
	source.Members[0].NodeUID = newClusterUUID()
	changedAgain, err := build()
	if err != nil || changedAgain.CohortSHA256 == changed.CohortSHA256 {
		t.Fatal("source identity omitted from binding")
	}
	// Recorded two-active/three-desired replacement retains all three peers.
	member := cohort.Targets[1]
	source.Members = append(source.Members, clusterSSHSourceMember{NodeID: member.Binding.TargetID, NodeUID: newClusterUUID(), Name: member.Report.Hostname, Address: member.Binding.Address, MachineID: member.Report.MachineID, BootID: member.Report.BootID, SSHFingerprint: member.Key.Fingerprint})
	source.ActiveSize, source.DesiredSize, source.Status = 2, 3, "Degraded Quorum"
	cohort.Targets = cohort.Targets[:1]
	replacement, err := build()
	if err != nil || len(replacement.PeerAddresses) != 3 {
		t.Fatal("recorded replacement rejected")
	}
}

func TestClusterSSHPreparationExpectedRejectsInspectionOrChangedCohort(t *testing.T) {
	for _, mode := range []string{"inspection claim", "old generation", "wrong controller", "wrong target", "wrong attempt", "wrong operation", "wrong kind", "wrong parent phase", "incomplete cohort", "stale report", "cloned identity", "existing installation", "development baseline", "missing K3s network", "three active"} {
		t.Run(mode, func(t *testing.T) {
			cohort, source, lease, release := sshPreparationFixture(t)
			pods := "10.42.0.0/16"
			switch mode {
			case "inspection claim":
				lease.Step = "inspect"
			case "old generation":
				lease.Generation = 1
			case "wrong controller":
				lease.ControllerHolder = "other-" + lease.ControllerHolder
			case "wrong target":
				lease.TargetID = newClusterUUID()
			case "wrong attempt":
				lease.OperationAttempt++
			case "wrong operation":
				lease.OperationID = newClusterUUID()
			case "wrong kind":
				lease.OperationKind = "admission"
			case "wrong parent phase":
				lease.OperationStep = clusterSSHQualificationStep
			case "incomplete cohort":
				cohort.Targets = cohort.Targets[:1]
			case "stale report":
				cohort.ObservedAt += clusterSSHInspectionLifetimeSeconds
			case "cloned identity":
				cohort.Targets[1].Report.MachineID = cohort.Targets[0].Report.MachineID
			case "existing installation":
				cohort.Targets[0].Report.NoExistingInstallation = false
			case "development baseline":
				release.Release = "dev-" + release.SourceSHA[:12]
			case "missing K3s network":
				pods = ""
			case "three active":
				source.ActiveSize = 3
			}
			if _, err := buildClusterSSHPreparationExpected(cohort, source, lease, release, "v1.36.3+k3s1", pods, "10.43.0.0/16"); err == nil {
				t.Fatal("unqualified claim/cohort accepted")
			}
		})
	}
}

func TestClusterSSHPreparationInputsRejectChangedAuthorityBeforeAndAfterDownload(t *testing.T) {
	for _, mode := range []string{"initial ownership lost", "ownership lost during download", "source changed", "config changed", "other target changed", "generation changed", "cancelled read", "cancelled download", "invalid input", "download failed", "unverified bundle"} {
		t.Run(mode, func(t *testing.T) {
			cohort, source, lease, release := sshPreparationFixture(t)
			e, err := buildClusterSSHPreparationExpected(cohort, source, lease, release, "v1.36.3+k3s1", "10.42.0.0/16", "10.43.0.0/16")
			if err != nil {
				t.Fatal(err)
			}
			runtime := sshPreparationRuntimeFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads, downloads := 0, 0
			read := func(context.Context) (clusterbootstrap.PreparationExpected, map[string]string, error) {
				reads++
				current := e
				settings := sshPreparationRuntimeFixture()
				if mode == "initial ownership lost" || (reads > 1 && mode == "ownership lost during download") {
					return current, nil, errors.New("private-test")
				}
				if mode == "cancelled read" {
					cancel()
				}
				if reads > 1 {
					switch mode {
					case "source changed":
						current.Source.SourceSHA = strings.Repeat("b", 40)
					case "config changed":
						settings["BOREALIS_OPERATOR_SECRET"] = "changed-private-test"
					case "other target changed":
						current.CohortSHA256 = strings.Repeat("b", 64)
					case "generation changed":
						current.Target.Generation++
					}
				}
				return current, settings, nil
			}
			download := func(context.Context, clusterbootstrap.Expected, string) (*clusterbootstrap.Bundle, error) {
				downloads++
				if mode == "cancelled download" {
					cancel()
				}
				if mode == "download failed" {
					return nil, errors.New("private-test signed-url")
				}
				return nil, nil
			}
			if mode == "invalid input" {
				runtime["SECRET_KEY"] = "private-test"
			}
			p, err := prepareClusterSSHTargetInputsWith(ctx, e, runtime, t.TempDir(), read, download)
			if err == nil || p != nil || strings.Contains(err.Error(), "private-test") {
				t.Fatal("invalid preparation reached accepted output/private diagnostics")
			}
			if mode == "initial ownership lost" || mode == "cancelled read" || mode == "invalid input" {
				if downloads != 0 {
					t.Fatal("download preceded input/authority validation")
				}
			} else if downloads != 1 {
				t.Fatal("unexpected source download count")
			}
			if mode == "invalid input" && reads != 0 {
				t.Fatal("invalid input reached source reader")
			}
		})
	}
}

func TestClusterSSHPreparationInputsUseFreshPublicationVerifier(t *testing.T) {
	f := newBootstrapReleaseFixture(t)
	cohort, source, lease, _ := sshPreparationFixture(t)
	e, err := buildClusterSSHPreparationExpected(cohort, source, lease, f.expected, "v1.36.3+k3s1", "10.42.0.0/16", "10.43.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	settings := sshPreparationRuntimeFixture()
	read := func(context.Context) (clusterbootstrap.PreparationExpected, map[string]string, error) {
		return e, settings, nil
	}
	immutable := false
	f.release.Immutable = &immutable
	if _, err := prepareClusterSSHTargetInputs(bootstrapContext(), e, settings, t.TempDir(), read); err == nil || f.archiveReads != 0 {
		t.Fatal("mutable publication reached archive")
	}
	immutable = true
	if _, err := prepareClusterSSHTargetInputs(bootstrapContext(), e, settings, t.TempDir(), read); err == nil || f.archiveReads != 1 {
		t.Fatal("invalid archive accepted or fixed verifier bypassed")
	}
}
