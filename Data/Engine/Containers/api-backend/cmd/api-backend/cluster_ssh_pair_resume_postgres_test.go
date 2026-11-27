package main

import (
	"strings"
	"testing"

	"borealis/api-backend/internal/clusterbootstrap"
)

func TestStoppedPairFreshAccessPostgres(t *testing.T) {
	f, a := newSSHPreparationClaimFixture(t, false)
	store := f.c.store
	source, err := readClusterSSHQueueSource(f.ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := store.db.QueryRowContext(f.ctx, "SELECT payload_json FROM engine.cluster_operations WHERE id=$1", f.op.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	proof, _, err := parseClusterSSHPreparationInspection([]byte(raw), f.lease, f.baseline)
	if err != nil {
		t.Fatal(err)
	}
	cpu := sshPreparationTargetCPUFixture(clusterSSHPreparationApprovalSnapshot{work: clusterSSHPreparationWork{Proof: proof}})
	install := &clusterSSHPairInstallation{Inspection: a.InspectionSHA256, Configuration: strings.Repeat("c", 64), PostgresImage: clusterbootstrap.PostgresImageRepository + "@sha256:" + strings.Repeat("a", 64), Targets: cpu, Packages: sshPreparationTargetPackagesFixture(cpu), Manifests: map[string]string{}}
	for name := range pairManifestLimits() {
		install.Manifests[name] = "sha256:" + strings.Repeat("a", 64)
	}
	report := newClusterSSHQualificationReport(f.op.Attempt)
	report.Version = 2
	report.Ready = true
	report.Checks = nil
	report.Installation = install
	for _, code := range clusterSSHPairCheckCodes {
		report.Checks = append(report.Checks, clusterSSHQualificationCheck{code, "passed"})
	}
	for _, target := range cpu {
		install.SystemDiskBytes = append(install.SystemDiskBytes, clusterSSHSystemDiskMinimum)
		install.Demand = append(install.Demand, clusterSSHPreparationCapacityTarget{target.TargetID, clusterSSHPreparationTargetCapacity{CPURequestMilli: 500, MemoryLimitBytes: 1 << 30, HostMemoryReserveBytes: clusterSSHHostMemoryReserveBytes}})
		report.Storage = append(report.Storage, clusterSSHTargetStorageCapacity{target.TargetID, "/var/lib/longhorn", []clusterSSHStorageBudget{{Filesystem: "aaaaaaaaaaaaaaaa", AvailableBytes: 1 << 30, Fits: true}}, true})
	}
	if !report.valid(f.op.Attempt) {
		t.Fatal("invalid pair fixture")
	}
	a.QualificationSHA256, err = clusterSSHPreparationDigest(report)
	if err != nil {
		t.Fatal(err)
	}
	c := sshPreparationConfirmationFixture(t, &a, proof.Source)
	ready, err := pairReadiness(a, install)
	if err != nil {
		t.Fatal(err)
	}
	f.op.Payload["ssh_preparation_authorization"] = a
	f.op.Payload["ssh_preparation_confirmation"] = c
	f.op.Payload["ssh_preparation_readiness"] = ready
	f.op.Payload["ssh_qualification"] = report
	f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, marshalClusterJSON(f.op.Payload))
	prior, err := store.claimFencedSSHPreparation(f.ctx, f.op, f.c.holder, newClusterUUID())
	if err != nil {
		t.Fatal("initial claim", err)
	}
	if err := store.openSSHPreparationAllocation(f.ctx, prior.Claims); err != nil {
		t.Fatal(err)
	}
	if _, err := store.transitionClusterOperation(f.ctx, "fixture", f.op.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	f.exec(t, "UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id IN (SELECT id FROM engine.cluster_onboarding_targets WHERE operation_id=$1)", f.op.ID)
	if err := store.cleanupClusterSSHCredentials(f.ctx); err != nil {
		t.Fatal(err)
	}
	p, err := store.stoppedPair(f.ctx, f.op.ID, c.PlanSHA256)
	if err != nil || p.source != source {
		t.Fatal("stopped pair", err)
	}
	fresh := append([]clusterSSHPlannedTarget(nil), f.targets...)
	for i, tgt := range fresh {
		env, err := f.aegis.openClusterSSHCredentials(f.ctx, tgt.Sealed, tgt.Binding)
		if err != nil {
			t.Fatal(err)
		}
		fresh[i].Sealed, err = f.aegis.sealClusterSSHCredentials(f.ctx, env)
		if err != nil {
			t.Fatal(err)
		}
	}
	bad := append([]clusterSSHPlannedTarget(nil), fresh...)
	bad[1].Sealed.generation = "wrong"
	if store.resumeStoppedPair(f.ctx, "fixture", c.PlanSHA256, p, bad) == nil {
		t.Fatal("partially authorized pair accepted")
	}
	if err := store.resumeStoppedPair(f.ctx, "fixture", c.PlanSHA256, p, fresh); err != nil {
		t.Fatal("resume", err)
	}
	if store.resumeStoppedPair(f.ctx, "fixture", c.PlanSHA256, p, fresh) == nil {
		t.Fatal("replay accepted")
	}
	if _, err := store.updateSSHPreparationOwnership(f.ctx, prior, true); err == nil {
		t.Fatal("old credentials or worker renewed")
	}
	work, err := store.claimFencedSSHPreparation(f.ctx, f.op, f.c.holder, newClusterUUID())
	if err != nil {
		t.Fatal("resumed claim", err)
	}
	for i, l := range work.Claims {
		if l.Lease.Generation != prior.Claims[i].Lease.Generation+2 || l.Sealed != fresh[i].Sealed || work.Authorization.Targets[i] != a.Targets[i] {
			t.Fatal("new fence or retained authorization mismatch")
		}
		if _, err := store.loadClusterSSHPreparationAuthority(f.ctx, l.Lease, f.baseline, l.Sealed); err != nil {
			t.Fatal("fresh authority", err)
		}
	}
}
