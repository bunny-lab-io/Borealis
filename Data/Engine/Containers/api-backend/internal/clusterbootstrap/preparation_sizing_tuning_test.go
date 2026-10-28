package clusterbootstrap

import (
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func preparationSizingRuntimeFixture() (PreparationExpected, map[string]string) {
	e, settings := preparationFixture()
	// The generic transfer fixture intentionally carries rank-1 naptime30s,
	// which is syntactically valid but conflicts with the rendered20s value.
	// Sizing fixtures derive omitted tuning unless they test an explicit value.
	delete(settings, "BOREALIS_POSTGRES_AUTOVACUUM_NAPTIME")
	return e, settings
}

func TestPreparationSizingProfileTuningRuntimeParity(t *testing.T) {
	functions := []string{"profile_rank_for_cpu", "profile_rank_for_memory", "profile_name_for_rank",
		"clamp_mib", "format_pg_memory_mib", "format_docker_memory_mib", "load_profile_tuning"}
	raw, err := os.ReadFile("../../../../../../Engine.sh")
	if err != nil {
		t.Fatal(err)
	}
	// Discover the actual unconditional environment bindings. A newly rendered
	// tuning field must acquire compatibility coverage, not disappear silently.
	bindings := map[string]string{}
	for _, match := range regexp.MustCompile(`(?m)^(BOREALIS_[A-Z_]+)=\$\{(PROFILE_[A-Z_]+)\}$`).FindAllStringSubmatch(string(raw), -1) {
		if _, tuning := preparationTuning[match[1]]; tuning {
			if bindings[match[1]] != "" {
				t.Fatal("duplicate profile environment binding", match[1])
			}
			bindings[match[1]] = match[2]
		}
	}
	if len(bindings) != 23 {
		t.Fatal("profile-derived settings changed", len(bindings))
	}
	var script strings.Builder
	script.WriteString("load_profile_tuning 64 999999999 \"$1\" \"$2\"\n")
	for _, key := range slices.Sorted(maps.Keys(bindings)) {
		fmt.Fprintf(&script, "printf '%%s=%%s\\n' '%s' \"$%s\"\n", key, bindings[key])
	}
	for rank, references := range [][]uint64{
		{1, 4095, 4096, 6553, 6554, 8195, 16384, 19660, 19661, 999999999},
		{16384, 16387, 26214, 26215, 32768, 999999999},
		{32768, 32771, 52428, 52429, 65536, 999999999},
		{65536, 65539, 98304, 104857, 104858, 999999999},
	} {
		for _, reference := range references {
			t.Run(fmt.Sprintf("rank%d/reference%d", rank, reference), func(t *testing.T) {
				rankText, referenceText := strconv.Itoa(rank), strconv.FormatUint(reference, 10)
				output := preparationSizingBash(t, functions, script.String(), rankText, referenceText)
				want := map[string]string{}
				for _, line := range strings.Split(output, "\n") {
					key, value, ok := strings.Cut(line, "=")
					if !ok || want[key] != "" {
						t.Fatal("invalid runtime tuning output")
					}
					want[key] = value
				}
				e, settings := preparationSizingRuntimeFixture()
				settings["BOREALIS_CLUSTER_SIZING_RANK"] = rankText
				settings["BOREALIS_CLUSTER_SIZING_MEMORY_MIB"] = referenceText
				settings["BOREALIS_POSTGRES_DB_MEMORY_LIMIT"] = "32g"
				maps.Copy(settings, want)
				check := func() (PreparationSizing, error) {
					c, err := NewPreparationConfiguration(e, settings)
					if err != nil {
						t.Fatal("generic tuning configuration rejected", err)
					}
					return c.Sizing()
				}
				s, err := check()
				if err != nil || !maps.Equal(s.profileTuning(), want) || s.Fits(64, 999999999*1024) != nil {
					t.Fatal("inherited profile differs from actual renderer", err)
				}
				for _, key := range slices.Sorted(maps.Keys(want)) {
					delete(settings, key)
					if _, err := check(); err != nil {
						t.Fatal("omitted optional tuning lost derivation compatibility", key)
					}
					settings[key] = map[string]string{"integer": "1", "quantity": "1MB", "duration": "1s", "number": "0.5", "compression": "off"}[preparationTuning[key]]
					if settings[key] == "" || settings[key] == want[key] {
						t.Fatal("test did not change tuning", key)
					}
					if _, err := check(); err == nil {
						t.Fatal("profile override accepted", key)
					}
					settings[key] = want[key]
				}
			})
		}
	}
}

func TestPreparationSizingProfileTuningCanonicalAndExplicitPrecedence(t *testing.T) {
	e, settings := preparationSizingRuntimeFixture()
	settings["BOREALIS_CLUSTER_SIZING_RANK"] = "1"
	settings["BOREALIS_CLUSTER_SIZING_MEMORY_MIB"] = "16384"
	settings["BOREALIS_POSTGRES_DB_MEMORY_LIMIT"] = "6144m"
	for _, key := range []string{"BOREALIS_DB_CONNECT_TIMEOUT", "BOREALIS_DB_IDLE_IN_TXN_TIMEOUT_MS", "BOREALIS_SITE_WORKER_ANSIBLE_CONCURRENCY", "BOREALIS_SITE_WORKER_PIDS_LIMIT"} {
		settings[key] = "9"
	}
	c, err := NewPreparationConfiguration(e, settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Sizing(); err != nil {
		t.Fatal("explicit-precedence setting became profile-derived", err)
	}
	// Compatibility selects the renderer's spelling only; no PostgreSQL parser
	// or approximate numeric conversion can silently expand the qualified set.
	for _, field := range []struct{ key, alternate string }{
		{"BOREALIS_POSTGRES_SHARED_BUFFERS", "4096MB"},
		{"BOREALIS_POSTGRES_CHECKPOINT_TIMEOUT", "900s"},
		{"BOREALIS_POSTGRES_CHECKPOINT_COMPLETION_TARGET", "0.900000"},
	} {
		settings[field.key] = field.alternate
		c, err := NewPreparationConfiguration(e, settings)
		if err != nil {
			t.Fatal("generic syntax lost compatibility", err)
		}
		if _, err := c.Sizing(); err == nil {
			t.Fatal("unqualified tuning spelling accepted", field.key)
		}
		delete(settings, field.key)
	}
}

func TestPreparationSizingPostgresDurationSyntax(t *testing.T) {
	for _, value := range []string{"1min", "15min", "1440min", "1s", "30s", "15m", "24h", "0.5s"} {
		if !validPreparationTuning("duration", value) {
			t.Fatal("supported duration rejected", value)
		}
	}
	for _, value := range []string{"", "0min", "00min", "01min", "-1min", "+1min", "1.5min", "1441min", "999999999min", "1000000000min", "9223372036854775807min", "15 min", " 15min", "15min ", "15min\n", "15MIN", "15minute", "15mins", "1h15min", "25h", "0s"} {
		if validPreparationTuning("duration", value) {
			t.Fatal("invalid or unbounded duration accepted", value)
		}
	}
	e, settings := preparationSizingRuntimeFixture()
	settings["BOREALIS_POSTGRES_CHECKPOINT_TIMEOUT"] = "15min"
	c, err := NewPreparationConfiguration(e, settings)
	if err != nil {
		t.Fatal("actual renderer default rejected", err)
	}
	raw, err := c.Export()
	if err != nil {
		t.Fatal(err)
	}
	imported, err := ImportPreparationConfiguration(raw, e)
	if err != nil {
		t.Fatal("minute syntax lost in private transfer", err)
	}
	env, err := imported.Environment()
	if err != nil || !strings.Contains(string(env), "BOREALIS_POSTGRES_CHECKPOINT_TIMEOUT=15min\n") {
		t.Fatal("PostgreSQL duration rewritten for transport", err)
	}
}
