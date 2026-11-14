package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPreparationProposalPackageSelectionParity(t *testing.T) {
	plan, _, err := clusterbootstrap.HostPackagePins()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"all missing", "base installed", "subset installed", "residual selected", "foreign architecture", "changed selection", "old native version"} {
		t.Run(mode, func(t *testing.T) {
			root, _, prelude := preparationNativeFixture(t)
			proposal := preparationProposalFixture()
			raw := []byte("apt\tamd64\tinstall ok installed\t2.7.14\ndpkg\tamd64\tinstall ok installed\t1.22.6\nbase-files\tamd64\tinstall ok installed\t13\n")
			var chosen clusterbootstrap.HostPackagePin
			for i, pin := range plan.Packages {
				if pin.Phase != "base" {
					continue
				}
				chosen = pin
				if mode == "base installed" || mode == "subset installed" && i%2 == 0 {
					raw = append(raw, []byte(pin.Name+"\t"+pin.Architecture+"\tinstall ok installed\t0.1-compatible\n")...)
				}
			}
			if mode == "residual selected" {
				raw = append(raw, []byte(chosen.Name+"\t"+chosen.Architecture+"\tdeinstall ok config-files\t1\n")...)
			}
			if mode == "foreign architecture" {
				raw = append(raw, []byte(chosen.Name+"\tarm64\tinstall ok installed\t1\n")...)
			}
			prelude += "fixture_packages = base64.b64decode('" + base64.StdEncoding.EncodeToString(raw) + "')\n"
			if mode == "changed selection" {
				prelude += "original_observe = observe\ndef observe(p):\n    global fixture_packages\n    result = original_observe(p)\n    fixture_packages += b'" + chosen.Name + "\\t" + chosen.Architecture + "\\tinstall ok installed\\t1\\n'\n    return result\n"
			}
			if mode == "old native version" {
				proposal.Version = 3
			}
			wire, nativeErr := runPreparationNativeFixture(t, root, prelude, proposal)
			wanted, selectErr := clusterbootstrap.SelectHostPackages(raw)
			positive := mode == "all missing" || mode == "base installed" || mode == "subset installed"
			if positive {
				proof, parseErr := parsePreparationProposalProof(wire, proposal)
				if nativeErr != nil || selectErr != nil || parseErr != nil || proof.Packages == nil || !reflect.DeepEqual(*proof.Packages, wanted) {
					t.Fatalf("native/shared selection mismatch: native=%v select=%v parse=%v", nativeErr, selectErr, parseErr)
				}
			} else if nativeErr == nil || len(wire) != 0 {
				t.Fatal("invalid or changing selection produced native proof")
			}
		})
	}
}

func TestPreparationProposalRejectsAbsentOrMalformedPackageSelection(t *testing.T) {
	p := preparationProposalFixture()
	proof := preparationProofFixture(p)
	raw, _ := json.Marshal(proof)
	packageRaw, _ := json.Marshal(proof.Packages)
	for _, bad := range []string{
		strings.Replace(string(raw), `,"packages":`+string(packageRaw), "", 1),
		strings.Replace(string(raw), string(packageRaw), "null", 1),
		strings.Replace(string(raw), `"missing":[`, `"missing":null,"ignored":[`, 1),
		strings.Replace(string(raw), `"missing":[`, `"Missing":[`, 1),
		strings.Replace(string(raw), `"missing":[`, `"missing":[],"missing":[`, 1),
		strings.Replace(string(raw), `"missing":[`, `"missing":["foreign-package",`, 1),
	} {
		if _, err := parsePreparationProposalProof([]byte(bad), p); err == nil {
			t.Fatal("missing or malformed selection accepted")
		}
	}
}
