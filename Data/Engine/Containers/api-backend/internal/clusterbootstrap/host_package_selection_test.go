package clusterbootstrap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const hostPackageSelectionBase = "apt\tamd64\tinstall ok installed\t2.7.14\ndpkg\tamd64\tinstall ok installed\t1.22.6\nbase-files\tamd64\tinstall ok installed\t13\n"

func TestHostPackageSelectionExactMissingPins(t *testing.T) {
	plan, digest, err := HostPackagePins()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"all missing", "compatible installed subset", "none missing", "unrelated inventory"} {
		t.Run(mode, func(t *testing.T) {
			raw := []byte(hostPackageSelectionBase)
			wanted := make([]string, 0, len(plan.Packages))
			for i, pin := range plan.Packages {
				present := mode == "none missing" || mode == "compatible installed subset" && i%2 == 0
				if present {
					raw = append(raw, []byte(pin.Name+"\t"+pin.Architecture+"\tinstall ok installed\t0.1-compatible\n")...)
				} else {
					wanted = append(wanted, pin.Name)
				}
			}
			if mode == "unrelated inventory" {
				raw = append(raw, []byte("unrelated\tamd64\tdeinstall ok config-files\t1\n")...)
			}
			slices.Sort(wanted)
			got, err := SelectHostPackages(raw)
			hash := sha256.Sum256(raw)
			if err != nil || got.Validate() != nil || got.PlanSHA256 != digest || got.InventorySHA256 != hex.EncodeToString(hash[:]) || !reflect.DeepEqual(got.Missing, wanted) {
				t.Fatalf("selection did not preserve exact inventory and missing set: %v", err)
			}
			pins, err := got.Pins()
			if err != nil || len(pins) != len(wanted) {
				t.Fatal("compiled pins unavailable", err)
			}
			for i, pin := range pins {
				original := plan.Packages[slices.IndexFunc(plan.Packages, func(p HostPackagePin) bool { return p.Name == wanted[i] })]
				if !reflect.DeepEqual(pin, original) {
					t.Fatal("pin not derived from compiled plan")
				}
			}
			copy := got.Clone()
			if len(copy.Missing) > 0 {
				copy.Missing[0] = "foreign"
				if reflect.DeepEqual(copy, got) {
					t.Fatal("borrowed missing list")
				}
			}
			if mode == "none missing" {
				wire, _ := json.Marshal(got)
				if !bytes.Contains(wire, []byte(`"missing":[]`)) {
					t.Fatal("empty selection lost explicit presence")
				}
			}
		})
	}
}

func TestHostPackageSelectionRejectsMalformedInventoryAndProjection(t *testing.T) {
	plan, _, _ := HostPackagePins()
	pin := plan.Packages[0]
	for _, raw := range []string{
		"", strings.TrimSuffix(hostPackageSelectionBase, "\n"), hostPackageSelectionBase + hostPackageSelectionBase,
		strings.Replace(hostPackageSelectionBase, "apt\t", "foreign\t", 1),
		hostPackageSelectionBase + pin.Name + "\tarm64\tinstall ok installed\t1\n",
		hostPackageSelectionBase + pin.Name + "\t" + pin.Architecture + "\tdeinstall ok config-files\t1\n",
		hostPackageSelectionBase + pin.Name + "\t" + pin.Architecture + "\tinstall ok installed\t1\n" + pin.Name + "\tarm64\tinstall ok installed\t1\n",
		hostPackageSelectionBase + "other\tamd64\tinstall ok unpacked\t1\n",
		strings.Repeat("x", HostPackageInventoryLimit) + "\n",
	} {
		if got, err := SelectHostPackages([]byte(raw)); err == nil || got.Missing != nil {
			t.Fatal("invalid inventory yielded projection")
		}
	}
	good, err := SelectHostPackages([]byte(hostPackageSelectionBase))
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*HostPackageSelection){
		func(s *HostPackageSelection) { s.Missing = nil },
		func(s *HostPackageSelection) { s.Missing = []string{"foreign"} },
		func(s *HostPackageSelection) { s.Missing = []string{good.Missing[0], good.Missing[0]} },
		func(s *HostPackageSelection) { s.Missing = []string{good.Missing[1], good.Missing[0]} },
		func(s *HostPackageSelection) { s.PlanSHA256 = strings.Repeat("a", 64) },
		func(s *HostPackageSelection) { s.InventorySHA256 = strings.Repeat("0", 64) },
	} {
		bad := good.Clone()
		mutate(&bad)
		if pins, err := bad.Pins(); err == nil || pins != nil {
			t.Fatal("malformed projection yielded pins")
		}
	}
}
