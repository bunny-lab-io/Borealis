package clusterremote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparationProposalCPUAndProcSelfSymlink(t *testing.T) {
	for _, mode := range []string{"fresh proc self symlink", "PID1 mount symlink", "limited quota", "limited cpuset", "limited manager affinity", "offline CPU", "missing CPU interface", "old v2 request"} {
		t.Run(mode, func(t *testing.T) {
			root, write, prelude := preparationNativeFixture(t)
			request := preparationProposalFixture()
			// Real procfs exposes self as a symlink. Never replace this with a fake
			// directory: O_NOFOLLOW must remain intact and PID1 mountinfo must be used.
			if err := os.Symlink("123", filepath.Join(root, "proc/self")); err != nil {
				t.Fatal(err)
			}
			write("proc/123/mountinfo", "foreign observer mount namespace\n")
			switch mode {
			case "PID1 mount symlink":
				if err := os.Remove(filepath.Join(root, "proc/1/mountinfo")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../123/mountinfo", filepath.Join(root, "proc/1/mountinfo")); err != nil {
					t.Fatal(err)
				}
			case "limited quota":
				write("sys/fs/cgroup/system.slice/cpu.max", "100000 100000\n")
			case "limited cpuset":
				write("sys/fs/cgroup/system.slice/cpuset.cpus.effective", "0-7\n")
			case "limited manager affinity":
				write("proc/1/status", "Name:\tsystemd\nUid:\t0\t0\t0\t0\nCpus_allowed_list:\t0-7\n")
			case "offline CPU":
				write("sys/devices/system/cpu/online", "0-7\n")
			case "missing CPU interface":
				if err := os.Remove(filepath.Join(root, "sys/fs/cgroup/system.slice/cpu.max")); err != nil {
					t.Fatal(err)
				}
			case "old v2 request":
				request.Version = 2
			}
			raw, err := runPreparationNativeFixture(t, root, prelude, request)
			if mode == "fresh proc self symlink" {
				proof, parseErr := parsePreparationProposalProof(raw, request)
				if err != nil || parseErr != nil || proof.CPU.AllocatableMilli != 16000 || proof.CPU.OnlineCPUs != "0-15" {
					t.Fatal("fixed PID1 CPU observation rejected", err, parseErr)
				}
			} else if err == nil || len(raw) != 0 {
				t.Fatal("unsafe native CPU returned proof", string(raw))
			}
		})
	}
	p := preparationProposalFixture()
	proof := preparationProofFixture(p)
	proof.Version = 2
	if proof.Validate(p) == nil {
		t.Fatal("v2 clean proof relabeled as CPU evidence")
	}
	proof = preparationProofFixture(p)
	proof.CPU.PlanSHA256 = strings.Repeat("f", 64)
	if proof.Validate(p) == nil {
		t.Fatal("foreign kubelet plan accepted")
	}
}
