package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func preparationTargetCPUNativeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, raw := range map[string]string{
		"sys/devices/system/node/has_memory":               "0\n",
		"sys/fs/cgroup/cpuset.mems.effective":              "0\n",
		"sys/fs/cgroup/system.slice/cpuset.mems.effective": "0\n",
		"sys/fs/cgroup/init.scope/cpuset.mems.effective":   "0\n",
		"sys/devices/system/cpu/online":                    "0-3\n",
		"sys/devices/system/cpu/present":                   "0-3\n",
		"sys/devices/system/node/node0/cpulist":            "0-3\n",
		"proc/1/cgroup":                                    "0::/init.scope\n",
		"proc/1/status":                                    "Name:\tsystemd\nVmRSS:\t1000 kB\nCpus_allowed_list:\t0-3\n",
		"proc/meminfo":                                     "MemTotal:       33554432 kB\nMemFree:        12345 kB\n",
		"proc/1/mountinfo":                                 "29 23 0:26 / /sys/fs/cgroup rw,nosuid,nodev,noexec,relatime - cgroup2 cgroup rw\n",
		"sys/fs/cgroup/cpuset.cpus.effective":              "0-3\n",
		"sys/fs/cgroup/cgroup.controllers":                 "cpuset cpu io memory pids\n",
		"sys/fs/cgroup/cgroup.subtree_control":             "cpuset cpu memory\n",
		"sys/fs/cgroup/system.slice/cpuset.cpus.effective": "0-3\n",
		"sys/fs/cgroup/init.scope/cpuset.cpus.effective":   "0-3\n",
		"sys/fs/cgroup/system.slice/cpu.max":               "max 100000\n",
		"sys/fs/cgroup/init.scope/cpu.max":                 "max 100000\n",
		"sys/fs/cgroup/system.slice/memory.max":            "max\n",
		"sys/fs/cgroup/system.slice/memory.high":           "max\n",
		"sys/fs/cgroup/init.scope/memory.max":              "max\n",
		"sys/fs/cgroup/init.scope/memory.high":             "max\n",
	} {
		preparationTargetCPUFixtureWrite(t, root, path, raw)
	}
	for id := 0; id < 4; id++ {
		if err := os.Mkdir(filepath.Join(root, "sys/devices/system/cpu/cpu"+strconv.Itoa(id)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestPreparationTargetCPUNativeDisabledChildControllers(t *testing.T) {
	for _, enabled := range []string{"", "cpu", "cpuset", "memory", "cpu cpuset", "cpu memory", "cpuset memory"} {
		t.Run("enabled="+enabled, func(t *testing.T) {
			root := preparationTargetCPUNativeFixture(t)
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/cgroup.subtree_control", enabled+"\n")
			controllers := map[string]bool{}
			for _, controller := range strings.Fields(enabled) {
				controllers[controller] = true
			}
			for controller, files := range map[string][]string{"cpu": {"cpu.max"}, "cpuset": {"cpuset.cpus.effective", "cpuset.mems.effective"}, "memory": {"memory.max", "memory.high"}} {
				if controllers[controller] {
					continue
				}
				for _, child := range []string{"system.slice", "init.scope"} {
					for _, file := range files {
						if err := os.Remove(filepath.Join(root, "sys/fs/cgroup", child, file)); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			observed, err := observePreparationTargetCPU(context.Background(), K3sPins().Version, func(context.Context) error { return nil }, preparationTargetCPUFixtureScript(root, ""))
			if err != nil || observed.AllocatableMilli != 4000 || observed.MemoryKiB != 33554432 {
				t.Fatal("unrestricted inherited CPU resources rejected", observed, err)
			}
		})
	}
}

func TestPreparationTargetCPUNativeMemTotal(t *testing.T) {
	for _, memory := range []uint64{1, (1<<53 - 1) / 1024} {
		root := preparationTargetCPUNativeFixture(t)
		preparationTargetCPUFixtureWrite(t, root, "proc/meminfo", "MemTotal: "+strconv.FormatUint(memory, 10)+" kB\n")
		observed, err := observePreparationTargetCPU(context.Background(), K3sPins().Version, func(context.Context) error { return nil }, preparationTargetCPUFixtureScript(root, ""))
		if err != nil || observed.MemoryKiB != memory {
			t.Fatal("bounded native MemTotal rejected", memory, observed, err)
		}
	}
	for name, raw := range map[string]string{
		"missing":          "MemFree: 33554432 kB\n",
		"duplicate":        "MemTotal: 33554432 kB\nMemTotal: 33554432 kB\n",
		"zero":             "MemTotal: 0 kB\n",
		"leading zero":     "MemTotal: 033554432 kB\n",
		"signed":           "MemTotal: +33554432 kB\n",
		"negative":         "MemTotal: -33554432 kB\n",
		"fraction":         "MemTotal: 33554432.0 kB\n",
		"wrong units":      "MemTotal: 33554432 KB\n",
		"missing units":    "MemTotal: 33554432\n",
		"missing newline":  "MemTotal: 33554432 kB",
		"carriage return":  "MemTotal: 33554432 kB\r\n",
		"capacity ceiling": "MemTotal: 8796093022208 kB\n",
		"overflow":         "MemTotal: 18446744073709551616 kB\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := preparationTargetCPUNativeFixture(t)
			preparationTargetCPUFixtureWrite(t, root, "proc/meminfo", raw)
			observed, err := observePreparationTargetCPU(context.Background(), K3sPins().Version, func(context.Context) error { return nil }, preparationTargetCPUFixtureScript(root, ""))
			if !errors.Is(err, ErrPreparationConfig) || observed != (PreparationTargetCPU{}) {
				t.Fatal("invalid MemTotal produced native evidence", observed, err)
			}
		})
	}
}

func preparationTargetCPUFixtureWrite(t *testing.T, root, path, raw string) {
	t.Helper()
	target := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func preparationTargetCPUFixtureScript(root, hook string) string {
	// Overrides exist only in this privately constructed test script. Production
	// Observe/Verify always use the fixed script, root and ownership constants.
	return preparationTargetCPUNativeLibrary + "\nROOT = " + strconv.Quote(root) +
		"\nEXPECTED_UID = " + strconv.Itoa(os.Geteuid()) + "\nEXPECTED_GID = " + strconv.Itoa(os.Getegid()) + "\n" + hook +
		preparationTargetCPUNativeScript[len(preparationTargetCPUNativeLibrary):]
}

func TestPreparationTargetCPUNativeFreshAndVerification(t *testing.T) {
	root := preparationTargetCPUNativeFixture(t)
	script := preparationTargetCPUFixtureScript(root, "")
	calls := 0
	check := func(context.Context) error { calls++; return nil }
	observed, err := observePreparationTargetCPU(context.Background(), K3sPins().Version, check, script)
	if err != nil || calls != 2 || observed.OnlineCPUs != "0-3" || observed.AllocatableMilli != 4000 || observed.MemoryKiB != 33554432 {
		t.Fatal("fresh native CPU proof rejected", observed, calls, err)
	}
	sum := sha256.Sum256([]byte("borealis-preparation-target-cpu-v1\n" + `{"allocatable_milli":4000,"memory_kib":33554432,"memory_nodes":"0","online_cpus":"0-3","pid1_affinity":"0-3","topology":[["node0","0-3"]],"unconstrained":true}`))
	if observed.ObservationSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("native CPU digest differs from shared semantic contract", observed.ObservationSHA256)
	}
	if err := verifyPreparationTargetCPU(context.Background(), observed, K3sPins().Version, check, script); err != nil {
		t.Fatal("unchanged native CPU proof rejected", err)
	}
	observed.ObservationSHA256 = strings.Repeat("b", 64)
	if err := verifyPreparationTargetCPU(context.Background(), observed, K3sPins().Version, check, script); !errors.Is(err, ErrPreparationConfig) {
		t.Fatal("changed native CPU receipt accepted", err)
	}
	if err := os.RemoveAll(filepath.Join(root, "sys/devices/system/node")); err != nil {
		t.Fatal(err)
	}
	if observed, err := observePreparationTargetCPU(context.Background(), K3sPins().Version, check, script); err != nil || observed.AllocatableMilli != 4000 {
		t.Fatal("all-present-online non-NUMA fallback rejected", observed, err)
	}
}

func TestPreparationTargetCPUNativeRejectsRestrictedOrAmbiguousHost(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, string){
		"unavailable memory controller": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/cgroup.controllers", "cpuset cpu io pids\n")
		},
		"disabled unexpected memory files": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/cgroup.subtree_control", "cpuset cpu\n")
		},
		"enabled missing memory max": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "sys/fs/cgroup/system.slice/memory.max")); err != nil {
				t.Fatal(err)
			}
		},
		"enabled missing memory high": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "sys/fs/cgroup/init.scope/memory.high")); err != nil {
				t.Fatal(err)
			}
		},
		"system memory max": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/system.slice/memory.max", "34359738368\n")
		},
		"system memory high": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/system.slice/memory.high", "34359738368\n")
		},
		"init memory max": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/init.scope/memory.max", "34359738368\n")
		},
		"init memory high": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/init.scope/memory.high", "34359738368\n")
		},
		"unavailable controller": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/cgroup.controllers", "cpu io memory pids\n")
		},
		"duplicate controller": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/cgroup.subtree_control", "cpu cpu cpuset\n")
		},
		"disabled unexpected quota file": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/cgroup.subtree_control", "cpuset\n")
		},
		"disabled unexpected cpuset file": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/cgroup.subtree_control", "cpu\n")
		},
		"enabled missing quota file": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "sys/fs/cgroup/system.slice/cpu.max")); err != nil {
				t.Fatal(err)
			}
		},
		"enabled missing cpuset file": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "sys/fs/cgroup/system.slice/cpuset.cpus.effective")); err != nil {
				t.Fatal(err)
			}
		},
		"system quota": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/system.slice/cpu.max", "100000 100000\n")
		},
		"init quota": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/init.scope/cpu.max", "200000 100000\n")
		},
		"restricted memory nodes": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/system.slice/cpuset.mems.effective", "1\n")
		},
		"root cpuset": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/cpuset.cpus.effective", "0-2\n")
		},
		"system cpuset": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/system.slice/cpuset.cpus.effective", "0-2\n")
		},
		"init cpuset": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/fs/cgroup/init.scope/cpuset.cpus.effective", "0-2\n")
		},
		"PID1 affinity": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "proc/1/status", "Cpus_allowed_list:\t0-2\n")
		},
		"duplicate affinity": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "proc/1/status", "Cpus_allowed_list:\t0-3\nCpus_allowed_list:\t0-3\n")
		},
		"PID1 cgroup": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "proc/1/cgroup", "0::/system.slice\n")
		},
		"offline CPU": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/devices/system/cpu/online", "0-2\n")
		},
		"missing CPU directory": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "sys/devices/system/cpu/cpu3")); err != nil {
				t.Fatal(err)
			}
		},
		"incomplete topology": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/devices/system/node/node0/cpulist", "0-2\n")
		},
		"overlapping topology": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/devices/system/node/node1/cpulist", "2-3\n")
		},
		"cgroup v1 mount": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "proc/1/mountinfo", "29 23 0:26 / /sys/fs/cgroup rw - cgroup cgroup rw\n")
		},
		"nested cgroup mount": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "proc/1/mountinfo", "29 23 0:26 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n30 29 0:27 / /sys/fs/cgroup/system.slice rw - cgroup2 cgroup rw\n")
		},
		"oversized read": func(t *testing.T, root string) {
			preparationTargetCPUFixtureWrite(t, root, "sys/devices/system/cpu/online", strings.Repeat("0", 32769))
		},
		"writable file": func(t *testing.T, root string) {
			if err := os.Chmod(filepath.Join(root, "sys/devices/system/cpu/online"), 0o666); err != nil {
				t.Fatal(err)
			}
		},
		"writable ancestor": func(t *testing.T, root string) {
			if err := os.Chmod(filepath.Join(root, "sys/devices"), 0o777); err != nil {
				t.Fatal(err)
			}
		},
		"symlink leaf": func(t *testing.T, root string) {
			path := filepath.Join(root, "sys/devices/system/cpu/online")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("present", path); err != nil {
				t.Fatal(err)
			}
		},
		"symlink ancestor": func(t *testing.T, root string) {
			path := filepath.Join(root, "proc/1")
			if err := os.Rename(path, path+"-real"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("1-real", path); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := preparationTargetCPUNativeFixture(t)
			mutate(t, root)
			observed, err := observePreparationTargetCPU(context.Background(), K3sPins().Version, func(context.Context) error { return nil }, preparationTargetCPUFixtureScript(root, ""))
			if !errors.Is(err, ErrPreparationConfig) || observed != (PreparationTargetCPU{}) {
				t.Fatal("unsupported host produced CPU proof", observed, err)
			}
		})
	}
}

func TestPreparationTargetCPUNativeTwoRoundDrift(t *testing.T) {
	for name, change := range map[string]string{
		"quota":       `("sys/fs/cgroup/system.slice/cpu.max", b"max 200000\n")`,
		"mount":       `("proc/1/mountinfo", b"30 23 0:26 / /sys/fs/cgroup rw,nosuid,nodev,noexec,relatime - cgroup2 cgroup rw\n")`,
		"topology":    `("sys/devices/system/node/node0/cpulist", b"0-2\n")`,
		"affinity":    `("proc/1/status", b"Cpus_allowed_list:\t0-2\n")`,
		"memory":      `("proc/meminfo", b"MemTotal:       67108864 kB\nMemFree:        12345 kB\n")`,
		"memory free": `("proc/meminfo", b"MemTotal:       33554432 kB\nMemFree:        99999 kB\n")`,
		"unrelated":   `("proc/1/status", b"Name:\tsystemd\nVmRSS:\t9999 kB\nCpus_allowed_list:\t0-3\n")`,
	} {
		t.Run(name, func(t *testing.T) {
			root := preparationTargetCPUNativeFixture(t)
			hook := `original_observe = observe
rounds = 0
def observe():
    global rounds
    result = original_observe()
    rounds += 1
    if rounds == 1:
        path, raw = ` + change + `
        with open(ROOT + "/" + path, "wb") as target:
            target.write(raw)
    return result
`
			observed, err := observePreparationTargetCPU(context.Background(), K3sPins().Version, func(context.Context) error { return nil }, preparationTargetCPUFixtureScript(root, hook))
			if name == "unrelated" || name == "memory free" {
				if err != nil {
					t.Fatal("unrelated PID1 status change invalidated CPU proof", err)
				}
			} else if !errors.Is(err, ErrPreparationConfig) || observed != (PreparationTargetCPU{}) {
				t.Fatal("between-round CPU evidence drift accepted", observed, err)
			}
		})
	}
}

func TestPreparationTargetCPUNativeAuthorityAndProcessBounds(t *testing.T) {
	root := preparationTargetCPUNativeFixture(t)
	script := preparationTargetCPUFixtureScript(root, "")
	for failAt := 1; failAt <= 2; failAt++ {
		calls := 0
		check := func(context.Context) error {
			calls++
			if calls == failAt {
				return errors.New("lost claim")
			}
			return nil
		}
		observed, err := observePreparationTargetCPU(context.Background(), K3sPins().Version, check, script)
		if !errors.Is(err, ErrSessionAuthority) || observed != (PreparationTargetCPU{}) || calls != failAt {
			t.Fatal("lost authority produced native CPU evidence", observed, calls, err)
		}
	}
	if _, err := ObservePreparationTargetCPU(context.Background(), K3sPins().Version, nil); !errors.Is(err, ErrPreparationConfig) {
		t.Fatal("nil authority accepted", err)
	}
	if err := VerifyPreparationTargetCPU(context.Background(), PreparationTargetCPU{}, K3sPins().Version, nil); !errors.Is(err, ErrPreparationConfig) {
		t.Fatal("invalid expected proof accepted", err)
	}
	validRaw, err := json.Marshal(preparationTargetCPUFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{
		"oversized output": "import sys; sys.stdout.write('x' * (33 << 10))",
		"malformed output": "print('{}')",
		"unknown field":    "print(" + strconv.Quote(string(validRaw[:len(validRaw)-1])+`,"foreign":true}`) + ")",
		"trailing frame":   "print(" + strconv.Quote(string(validRaw)+"\n{}") + ")",
		"process failure":  "raise ValueError()",
	} {
		t.Run(name, func(t *testing.T) {
			observed, err := observePreparationTargetCPU(context.Background(), K3sPins().Version, func(context.Context) error { return nil }, script)
			if !errors.Is(err, ErrPreparationConfig) || observed != (PreparationTargetCPU{}) {
				t.Fatal("invalid process output accepted", observed, err)
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if observed, err := observePreparationTargetCPU(ctx, K3sPins().Version, func(context.Context) error { return nil }, "while True: pass"); !errors.Is(err, ErrSessionAuthority) || observed != (PreparationTargetCPU{}) || time.Since(start) > 2*time.Second {
		t.Fatal("cancelled observer was not bounded and reaped", observed, err)
	}
}
