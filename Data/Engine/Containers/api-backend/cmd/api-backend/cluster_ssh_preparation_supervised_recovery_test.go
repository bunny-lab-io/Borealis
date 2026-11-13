package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestClusterSSHPreparationSupervisedRecoveryFreshNativeProof(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "copied proposal", "wrong plan", "claimed work", "missing artifact scope", "missing target reader", "artifact failure", "target failure", "invalid artifact", "invalid target", "changed artifact", "changed targets", "cancelled", "cancel during target"} {
		t.Run(mode, func(t *testing.T) {
			snapshot, _ := sshPreparationCleanTargetsFixture(t, mode == "replacement")
			plan, _ := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
			if mode == "claimed work" {
				snapshot.work.Claims = []clusterSSHNetworkTargetClaim{{}}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			artifactReads, targetReads := 0, 0
			var artifacts clusterSSHPreparationArtifactGate = func(ctx context.Context, requested string) (string, error) {
				artifactReads++
				if ctx.Done() == nil || requested != plan {
					t.Fatal("artifact call lost deadline or proposal")
				}
				if mode == "artifact failure" {
					return "", errors.New("private artifact detail")
				}
				if mode == "invalid artifact" {
					return strings.Repeat("0", 64), nil
				}
				if mode == "changed artifact" && artifactReads > 1 {
					return strings.Repeat("f", 64), nil
				}
				return strings.Repeat("a", 64), nil
			}
			clean := func(ctx context.Context, requested string) (string, error) {
				targetReads++
				if ctx.Done() == nil || requested != plan {
					t.Fatal("target call lost deadline or proposal")
				}
				if mode == "target failure" {
					return "", errors.New("private transport detail")
				}
				if mode == "invalid target" {
					return strings.Repeat("0", 64), nil
				}
				if mode == "changed targets" && targetReads > 1 {
					return strings.Repeat("f", 64), nil
				}
				if mode == "cancel during target" {
					cancel()
				}
				return strings.Repeat("b", 64), nil
			}
			if mode == "missing artifact scope" {
				artifacts = nil
			}
			if mode == "missing target reader" {
				clean = nil
			}
			read := newClusterSSHPreparationSupervisedRecoveryRead(snapshot, artifacts, clean)
			if mode == "copied proposal" {
				snapshot.work.Authorization.Targets[0].TargetID = newClusterUUID()
				snapshot.work.Proof.Source.Members[0].NodeUID = newClusterUUID()
			}
			if mode == "wrong plan" {
				plan = strings.Repeat("f", 64)
			}
			if mode == "cancelled" {
				cancel()
			}
			digest, err := read(ctx, plan)
			if mode == "changed artifact" || mode == "changed targets" {
				if err != nil || !validClusterSSHPreparationDigest(digest) {
					t.Fatal("initial native evidence rejected", err)
				}
				digest, err = read(ctx, plan)
			}
			success := mode == "pair" || mode == "replacement" || mode == "copied proposal"
			if success {
				again, nextErr := read(ctx, plan)
				if err != nil || nextErr != nil || !validClusterSSHPreparationDigest(digest) || again != digest || artifactReads != 2 || targetReads != 2 {
					t.Fatal("fresh complete proof did not remain stable", err, nextErr)
				}
				return
			}
			var blocked clusterSSHPreparationGateBlocked
			if digest != "" || !errors.As(err, &blocked) || blocked.Gate != "supervised_recovery" || strings.Contains(err.Error(), "private") {
				t.Fatal("failed native evidence returned receipt or private detail", err)
			}
			before := artifactReads + targetReads
			if _, err := read(context.Background(), plan); err == nil || before != artifactReads+targetReads {
				t.Fatal("failed supervision reader revived")
			}
		})
	}
}

func TestClusterSSHPreparationSupervisedRecoveryOwnsArtifactLifetime(t *testing.T) {
	snapshot, _ := sshPreparationCleanTargetsFixture(t, false)
	plan, _ := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	bytes := &sshApprovalArtifactBytes{digest: strings.Repeat("e", 64)}
	var escaped func(context.Context, string) (string, error)
	reads := 0
	err := runClusterSSHPreparationArtifacts(context.Background(), plan, sshApprovalArtifactDependencies(bytes),
		func(ctx context.Context, artifact clusterSSHPreparationArtifactGate, _ clusterSSHPreparationArtifactDemandRead) (clusterSSHPreparationReadinessReceipt, error) {
			escaped = newClusterSSHPreparationSupervisedRecoveryRead(snapshot, artifact, func(context.Context, string) (string, error) {
				reads++
				return strings.Repeat("a", 64), nil
			})
			if digest, err := escaped(ctx, plan); err != nil || !validClusterSSHPreparationDigest(digest) {
				t.Fatal("live owned artifact scope rejected", err)
			}
			return clusterSSHPreparationReadinessReceipt{}, nil
		})
	if err != nil || bytes.closes.Load() != 1 || reads != 1 {
		t.Fatal("artifact scope did not join cleanup", err)
	}
	if digest, err := escaped(context.Background(), plan); digest != "" || err == nil || reads != 1 {
		t.Fatal("supervision gate escaped immutable artifact ownership", err)
	}
}
