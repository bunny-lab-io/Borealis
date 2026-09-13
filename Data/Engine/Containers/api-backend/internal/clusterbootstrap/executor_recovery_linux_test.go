package clusterbootstrap

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"
)

func stoppedExecutorFixture(prior ExecutorIdentity) map[string]string {
	return map[string]string{"Id": prior.Unit, "InvocationID": prior.InvocationID, "MainPID": "0", "ControlGroup": prior.ControlGroup,
		"LoadState": "loaded", "ActiveState": "failed", "SubState": "failed", "Job": "", "Restart": "no", "Transient": "yes"}
}

func collectedExecutorFixture(prior ExecutorIdentity) map[string]string {
	return map[string]string{"Id": prior.Unit, "InvocationID": "", "MainPID": "0", "ControlGroup": "",
		"LoadState": "not-found", "ActiveState": "inactive", "SubState": "dead", "Job": "", "Restart": "no", "Transient": "no"}
}

func TestExecutorRecoveryRejectsLiveReusedOrAmbiguousUnits(t *testing.T) {
	prior := testMutationExecutor(mutationDigest("prior executor"))
	for _, kind := range []string{"failed", "dead", "collected", "previous boot"} {
		t.Run(kind, func(t *testing.T) {
			values, boot := stoppedExecutorFixture(prior), prior.BootID
			if kind == "dead" {
				values["ActiveState"], values["SubState"] = "inactive", "dead"
			}
			if kind == "collected" || kind == "previous boot" {
				values = collectedExecutorFixture(prior)
			}
			if kind == "previous boot" {
				boot = "22222222-2222-4222-8222-222222222222"
			}
			if !executorStopped(prior, boot, values) {
				t.Fatal("valid stopped observation rejected")
			}
			for _, key := range executorRecoveryProperties {
				bad := maps.Clone(values)
				bad[key] = "changed"
				if executorStopped(prior, boot, bad) {
					t.Fatalf("changed %s accepted", key)
				}
				delete(bad, key)
				if executorStopped(prior, boot, bad) {
					t.Fatalf("missing %s accepted", key)
				}
			}
		})
	}
	for _, change := range []func(*ExecutorIdentity){
		func(e *ExecutorIdentity) { e.Unit = "../k3s.service" },
		func(e *ExecutorIdentity) { e.ControlGroup = "/system.slice/k3s.service" },
		func(e *ExecutorIdentity) { e.InvocationID = strings.Repeat("0", 32) },
		func(e *ExecutorIdentity) { e.BootID = "unknown" },
	} {
		bad := prior
		change(&bad)
		if executorStopped(bad, prior.BootID, collectedExecutorFixture(bad)) {
			t.Fatal("malformed persisted identity accepted")
		}
	}
}

func TestExecutorRecoveryRequiresStableManagerBootAndKernelEmptiness(t *testing.T) {
	prior := testMutationExecutor(mutationDigest("prior executor"))
	for _, mode := range []string{"valid", "unit missing", "manager failed", "kernel failed", "boot failed", "boot changed", "unit changed", "active", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			boots, units, groups := 0, 0, 0
			r := executorRecoveryReader{
				boot: func() (string, error) {
					boots++
					if mode == "boot failed" {
						return "", errors.New("private diagnostic")
					}
					if mode == "boot changed" && boots == 2 {
						return "22222222-2222-4222-8222-222222222222", nil
					}
					return prior.BootID, nil
				},
				unit: func(context.Context, ExecutorIdentity) (map[string]string, error) {
					units++
					if mode == "manager failed" {
						return nil, errors.New("private manager error")
					}
					v := stoppedExecutorFixture(prior)
					if mode == "unit missing" || mode == "kernel failed" {
						v = collectedExecutorFixture(prior)
					}
					if mode == "unit changed" && units == 2 {
						v = collectedExecutorFixture(prior)
					}
					if mode == "active" {
						v["ActiveState"], v["SubState"] = "active", "running"
					}
					return v, nil
				},
				empty: func(ExecutorIdentity) error {
					groups++
					if mode == "kernel failed" {
						return errors.New("private permission error")
					}
					if mode == "cancelled" {
						cancel()
					}
					return nil
				},
			}
			err := r.check(ctx, prior)
			if mode == "valid" || mode == "unit missing" {
				if err != nil || boots != 2 || units != 2 || groups != 2 {
					t.Fatal("missing independent stable observations")
				}
			} else if err != ErrExecutorContainment {
				t.Fatal("ambiguous observation accepted or diagnostic exposed")
			}
		})
	}
}

func TestExecutorRecoveryUsesRecursivePopulatedFlag(t *testing.T) {
	for _, raw := range []string{"populated 0\nfrozen 0\n", "frozen 1\npopulated 0\n"} {
		if !executorCgroupEmpty([]byte(raw)) {
			t.Fatal("empty cgroup refused")
		}
	}
	for _, raw := range []string{"", "frozen 0\n", "populated 1\nfrozen 0\n", "populated 0", "populated 0\npopulated 1\n", "populated 0\nfrozen x\n", "populated 0\n\x00\n", strings.Repeat("x", 4097)} {
		if executorCgroupEmpty([]byte(raw)) {
			t.Fatal("missing/malformed/populated observation accepted")
		}
	}
}
