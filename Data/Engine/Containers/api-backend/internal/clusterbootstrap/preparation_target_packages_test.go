package clusterbootstrap

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func preparationTargetPackagesFixture(t *testing.T) HostPackageSelection {
	t.Helper()
	selection, err := SelectHostPackages([]byte("apt\tamd64\tinstall ok installed\t2.8.3\ndpkg\tamd64\tinstall ok installed\t1.22.6ubuntu6.5\nbase-files\tamd64\tinstall ok installed\t13ubuntu10.3\n"))
	if err != nil {
		t.Fatal(err)
	}
	return selection
}

func TestPreparationTargetPackagesValidation(t *testing.T) {
	for _, mode := range []string{"approved", "legacy", "plan", "inventory", "missing null", "unknown package", "duplicate", "unsorted"} {
		t.Run(mode, func(t *testing.T) {
			expected, runtime := preparationFixture()
			selection := preparationTargetPackagesFixture(t)
			expected.TargetPackages = &selection
			switch mode {
			case "legacy":
				expected.TargetPackages = nil
			case "plan":
				selection.PlanSHA256 = strings.Repeat("a", 64)
			case "inventory":
				selection.InventorySHA256 = strings.Repeat("0", 64)
			case "missing null":
				selection.Missing = nil
			case "unknown package":
				selection.Missing = []string{"unapproved-package"}
			case "duplicate":
				selection.Missing = []string{selection.Missing[0], selection.Missing[0]}
			case "unsorted":
				selection.Missing[0], selection.Missing[1] = selection.Missing[1], selection.Missing[0]
			}
			valid := mode == "approved" || mode == "legacy"
			if (expected.Validate() == nil) != valid {
				t.Fatal("invalid target selection accepted")
			}
			config, err := NewPreparationConfiguration(expected, runtime)
			if (err == nil) != valid {
				t.Fatal("configuration lost selection validation", err)
			}
			if valid {
				raw, _ := config.Export()
				if _, err := ImportPreparationConfiguration(raw, expected); err != nil {
					t.Fatal("approved/legacy configuration cannot round trip", err)
				}
				if bytes.Contains(raw, []byte(`"target_packages"`)) != (mode == "approved") {
					t.Fatal("legacy encoding changed or selection omitted")
				}
			}
		})
	}
}

func TestPreparationTargetPackagesConfigurationOwnership(t *testing.T) {
	expected, runtime := preparationFixture()
	selection := preparationTargetPackagesFixture(t)
	expected.TargetPackages = &selection
	approved := selection.Clone()
	config, err := NewPreparationConfiguration(expected, runtime)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := config.Export()
	selection.InventorySHA256 = strings.Repeat("b", 64)
	selection.Missing[0] = "foreign-package"
	if !reflect.DeepEqual(*config.wire.Expected.TargetPackages, approved) {
		t.Fatal("caller mutation changed retained approved selection")
	}
	expected.TargetPackages = &approved
	if _, err := ImportPreparationConfiguration(raw, expected); err != nil {
		t.Fatal("owned configuration lost exact binding", err)
	}
	drift := approved.Clone()
	drift.InventorySHA256 = strings.Repeat("c", 64)
	expected.TargetPackages = &drift
	if _, err := ImportPreparationConfiguration(raw, expected); err == nil {
		t.Fatal("configuration accepted different approved inventory")
	}
	expected.TargetPackages = nil
	if _, err := ImportPreparationConfiguration(raw, expected); err == nil {
		t.Fatal("configuration dropped approved selection")
	}
	var wire preparationConfigWire
	if json.Unmarshal(raw, &wire) != nil || !reflect.DeepEqual(*wire.Expected.TargetPackages, approved) {
		t.Fatal("wire omitted approved selection")
	}
}

func TestPreparationTargetPackagesJoinMaterialOwnership(t *testing.T) {
	expected, source, token, ca := joinMaterialFixture(t)
	selection := preparationTargetPackagesFixture(t)
	expected.TargetPackages = &selection
	approved := selection.Clone()
	material, err := NewK3sJoinMaterial(expected, source, token, ca)
	if err != nil {
		t.Fatal(err)
	}
	defer material.Close()
	selection.InventorySHA256 = strings.Repeat("b", 64)
	selection.Missing[0] = "foreign-package"
	if !reflect.DeepEqual(*material.owned.expected.TargetPackages, approved) {
		t.Fatal("caller mutation changed retained join selection")
	}
}
