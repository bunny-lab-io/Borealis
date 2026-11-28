package main

import (
	"borealis/api-backend/internal/clusterremote"
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

type clusterSSHWorkerTestClient struct {
	beforeInspect    func(context.Context) error
	beforePrivileged func(context.Context, []byte) error
	closed           atomic.Bool
}

func sshWorkerFacts() (clusterremote.HostFacts, clusterremote.PrivilegedFacts) {
	host := clusterremote.HostFacts{Kernel: "Linux", Architecture: "x86_64", UID: 1000, Hostname: "joining-engine", OSID: "ubuntu", OSVersion: "24.04", CPUCount: 16, MemoryKiB: 33554432, DiskTotalKiB: 524288000, DiskFreeKiB: 314572800, DiskScope: "opt", BorealisPath: "absent", K3sUnit: "not-found"}
	privileged := clusterremote.PrivilegedFacts{MachineID: strings.Repeat("a", 32), BootID: "11111111-1111-4111-8111-111111111111", BorealisPath: "absent", BorealisConfig: "absent", K3sConfig: "absent", K3sData: "absent", K3sBinary: "absent", K3sLoad: "not-found", K3sActive: "inactive", K3sAgentLoad: "not-found", K3sAgentActive: "inactive", KubeSystemUID: "not-present", NodesState: "not-present"}
	return host, privileged
}

func (c *clusterSSHWorkerTestClient) Inspect(ctx context.Context) (clusterremote.HostFacts, error) {
	host, _ := sshWorkerFacts()
	if c.beforeInspect != nil {
		if err := c.beforeInspect(ctx); err != nil {
			return clusterremote.HostFacts{}, err
		}
	}
	return host, nil
}
func (c *clusterSSHWorkerTestClient) InspectPrivileged(ctx context.Context, secret []byte) (clusterremote.PrivilegedFacts, error) {
	_, privileged := sshWorkerFacts()
	if c.beforePrivileged != nil {
		if err := c.beforePrivileged(ctx, secret); err != nil {
			return clusterremote.PrivilegedFacts{}, err
		}
	}
	return privileged, nil
}
func (c *clusterSSHWorkerTestClient) Close() error { c.closed.Store(true); return nil }

func TestClusterSSHInspectionReportPreservesUncertainty(t *testing.T) {
	for _, mode := range []string{"clean observed paths", "earlier installation", "unsupported platform", "unknown machine", "unknown path", "existing member"} {
		t.Run(mode, func(t *testing.T) {
			host, privileged := sshWorkerFacts()
			switch mode {
			case "earlier installation":
				host.BorealisPath = "present"
			case "unsupported platform":
				host.OSID = "debian"
			case "unknown machine":
				privileged.MachineID = "unknown"
			case "unknown path":
				privileged.K3sData = "unknown"
			case "existing member":
				privileged.KubeSystemUID = "22222222-2222-4222-8222-222222222222"
				privileged.NodesState = "observed"
				privileged.Nodes = []clusterremote.InspectedKubernetesNode{{Name: "joining-engine", UID: "33333333-3333-4333-8333-333333333333"}}
			}
			report, err := clusterSSHReport(host, privileged, "192.168.90.21")
			if err != nil {
				t.Fatal(err)
			}
			if report.NoExistingInstallation != (mode == "clean observed paths" || mode == "unsupported platform") {
				t.Fatal("partial inventory became absent-installation proof")
			}
			if report.SupportedPlatform != (mode != "unsupported platform") {
				t.Fatal("platform result changed")
			}
			if report.ConnectedPrefix != "" {
				t.Fatal("missing route evidence became network proof")
			}
		})
	}
	host, privileged := sshWorkerFacts()
	report, err := clusterSSHReport(host, privileged, "192.168.90.21")
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*clusterSSHInspectionReport){
		"hostname markup": func(r *clusterSSHInspectionReport) { r.Hostname = "<script>" },
		"oversized OS":    func(r *clusterSSHInspectionReport) { r.OSVersion = strings.Repeat("x", 129) },
		"arbitrary path":  func(r *clusterSSHInspectionReport) { r.BorealisPath = "private remote diagnostic" },
		"invalid machine": func(r *clusterSSHInspectionReport) { r.MachineID = "not metadata" },
		"invalid prefix":  func(r *clusterSSHInspectionReport) { r.ConnectedPrefix = "192.168.90.21/24" },
		"false platform":  func(r *clusterSSHInspectionReport) { r.SupportedPlatform = false },
		"unsafe capacity": func(r *clusterSSHInspectionReport) { r.MemoryKiB = 1 << 53 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := report
			change(&changed)
			if changed.valid() == nil {
				t.Fatal("unsafe durable metadata accepted")
			}
		})
	}
}
