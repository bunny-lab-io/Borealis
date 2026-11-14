package clusterbootstrap

import (
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestPreparationTargetCPUArgumentsAndPlan(t *testing.T) {
	version := K3sPins().Version
	want := []string{
		"kube-reserved=cpu=0",
		"system-reserved=cpu=0",
		"reserved-cpus=",
		"cpu-manager-policy=none",
		"enforce-node-allocatable=pods",
	}
	args, err := PreparationTargetCPUArguments(version)
	if err != nil || !slices.Equal(args, want) {
		t.Fatal("fixed fresh-host CPU defaults changed", args, err)
	}
	plan, err := PreparationTargetCPUPlanDigest(version)
	// Independently calculated JSON/SHA-256 vector. A release, binary, server,
	// argument or wire-version change requires review of the native CPU proof.
	const wantPlan = "5cdadca45d34e1da3d115c22232e16116a23381419a37165d428f92f14354361"
	if err != nil || plan != wantPlan {
		t.Fatal("pinned CPU plan changed", plan, err)
	}
	args[0] = "kube-reserved=cpu=8"
	again, err := PreparationTargetCPUArguments(version)
	if err != nil || !slices.Equal(again, want) {
		t.Fatal("returned argument mutation escaped into fixed plan", again, err)
	}
	againPlan, err := PreparationTargetCPUPlanDigest(version)
	if err != nil || againPlan != plan {
		t.Fatal("returned argument mutation changed plan digest", againPlan, err)
	}
	for _, unsupported := range []string{"", "v1.36.3", "v1.36.3+k3s2", " " + version, version + "\n"} {
		if args, err := PreparationTargetCPUArguments(unsupported); args != nil || !errors.Is(err, ErrPreparationConfig) {
			t.Fatal("unsupported release produced CPU arguments", unsupported, args, err)
		}
		if digest, err := PreparationTargetCPUPlanDigest(unsupported); digest != "" || !errors.Is(err, ErrPreparationConfig) {
			t.Fatal("unsupported release produced CPU digest", unsupported, digest, err)
		}
	}
}

func preparationTargetCPUFixture(t *testing.T) PreparationTargetCPU {
	t.Helper()
	plan, err := PreparationTargetCPUPlanDigest(K3sPins().Version)
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic receipt checks wire validation only, not a native observation.
	return PreparationTargetCPU{PreparationTargetCPUVersion, plan, "0-3", 4000, 33554432, strings.Repeat("a", 64)}
}

func TestPreparationTargetCPUValidate(t *testing.T) {
	base := preparationTargetCPUFixture(t)
	for _, sample := range []struct {
		list  string
		milli uint64
	}{
		{"0", 1000},
		{"4095", 1000},
		{"0-3", 4000},
		{"0,2-4,6,4094-4095", 7000},
		{"0-4095", 4096000},
	} {
		cpu := base
		cpu.OnlineCPUs, cpu.AllocatableMilli = sample.list, sample.milli
		if err := cpu.Validate(K3sPins().Version); err != nil {
			t.Fatal("canonical bounded CPU evidence rejected", sample, err)
		}
	}
	var sparse []string
	for id := 0; id <= 4095; id += 2 {
		sparse = append(sparse, strconv.Itoa(id))
	}
	cpu := base
	cpu.OnlineCPUs, cpu.AllocatableMilli = strings.Join(sparse, ","), 2048000
	if err := cpu.Validate(K3sPins().Version); err != nil {
		t.Fatal("bounded maximally sparse CPU list rejected", err)
	}
	for _, memory := range []uint64{1, (1<<53 - 1) / 1024} {
		cpu := base
		cpu.MemoryKiB = memory
		if err := cpu.Validate(K3sPins().Version); err != nil {
			t.Fatal("bounded positive MemTotal rejected", memory, err)
		}
	}
	for name, mutate := range map[string]func(*PreparationTargetCPU){
		"missing version":     func(c *PreparationTargetCPU) { c.Version = 0 },
		"unknown version":     func(c *PreparationTargetCPU) { c.Version++ },
		"missing plan":        func(c *PreparationTargetCPU) { c.PlanSHA256 = "" },
		"different plan":      func(c *PreparationTargetCPU) { c.PlanSHA256 = strings.Repeat("b", 64) },
		"zero plan":           func(c *PreparationTargetCPU) { c.PlanSHA256 = strings.Repeat("0", 64) },
		"noncanonical plan":   func(c *PreparationTargetCPU) { c.PlanSHA256 = strings.ToUpper(c.PlanSHA256) },
		"missing observation": func(c *PreparationTargetCPU) { c.ObservationSHA256 = "" },
		"zero observation":    func(c *PreparationTargetCPU) { c.ObservationSHA256 = strings.Repeat("0", 64) },
		"upper observation":   func(c *PreparationTargetCPU) { c.ObservationSHA256 = strings.Repeat("A", 64) },
		"short observation":   func(c *PreparationTargetCPU) { c.ObservationSHA256 = strings.Repeat("a", 63) },
		"long observation":    func(c *PreparationTargetCPU) { c.ObservationSHA256 += "a" },
		"nonhex observation":  func(c *PreparationTargetCPU) { c.ObservationSHA256 = strings.Repeat("g", 64) },
		"missing allocatable": func(c *PreparationTargetCPU) { c.AllocatableMilli = 0 },
		"lower allocatable":   func(c *PreparationTargetCPU) { c.AllocatableMilli-- },
		"higher allocatable":  func(c *PreparationTargetCPU) { c.AllocatableMilli++ },
		"overflow amount":     func(c *PreparationTargetCPU) { c.AllocatableMilli = math.MaxUint64 },
		"missing memory":      func(c *PreparationTargetCPU) { c.MemoryKiB = 0 },
		"oversized memory":    func(c *PreparationTargetCPU) { c.MemoryKiB = (1<<53-1)/1024 + 1 },
		"overflow memory":     func(c *PreparationTargetCPU) { c.MemoryKiB = math.MaxUint64 },
	} {
		t.Run(name, func(t *testing.T) {
			cpu := base
			mutate(&cpu)
			if err := cpu.Validate(K3sPins().Version); !errors.Is(err, ErrPreparationConfig) {
				t.Fatal("invalid CPU evidence accepted", err)
			}
		})
	}
	if err := base.Validate("v1.36.3+k3s2"); !errors.Is(err, ErrPreparationConfig) {
		t.Fatal("CPU evidence accepted for different release", err)
	}
}

func TestPreparationTargetCPURejectsNoncanonicalLists(t *testing.T) {
	for _, list := range []string{
		"", " ", "0\n", " 0", "0 ", "+0", "-1", "00", "01", "0-01", "0-0", "1-0",
		",0", "0,", "0,,2", "0,0", "2,0", "0,1", "0-2,3", "0,1-3", "0-3,2-4",
		"0-3,3", "0-3,4-6", "0-", "-3", "0--3", "0-1-3", "0;2", "0, 2", "٠", "1.0",
		"4096", "0-4096", "18446744073709551616", strings.Repeat("0,", 10241),
	} {
		t.Run(strconv.QuoteToASCII(list[:min(len(list), 80)]), func(t *testing.T) {
			if count, err := preparationTargetCPUCount(list); count != 0 || !errors.Is(err, ErrPreparationConfig) {
				t.Fatal("CPU parser accepted invalid list", count, err)
			}
			cpu := preparationTargetCPUFixture(t)
			cpu.OnlineCPUs = list
			if err := cpu.Validate(K3sPins().Version); !errors.Is(err, ErrPreparationConfig) {
				t.Fatal("malformed, unbounded or noncanonical CPU list accepted", err)
			}
		})
	}
}
