package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
				if proof.Packages != nil {
					wanted.ProfileSHA256 = proof.Packages.ProfileSHA256
				}
				if nativeErr != nil || selectErr != nil || parseErr != nil || proof.Packages == nil || !reflect.DeepEqual(*proof.Packages, wanted) {
					t.Fatalf("native/shared selection mismatch: native=%v select=%v parse=%v", nativeErr, selectErr, parseErr)
				}
			} else if nativeErr == nil || len(wire) != 0 {
				t.Fatal("invalid or changing selection produced native proof")
			}
		})
	}
}

func TestPreparationProposalObservedUbuntuSuite(t *testing.T) {
	for _, version := range []string{"26.04", "24.04"} {
		t.Run(version, func(t *testing.T) {
			root, write, prelude := preparationNativeFixture(t)
			write("etc/os-release", "ID=ubuntu\nVERSION_ID="+version+"\nVERSION_CODENAME=resolute\n")
			q := preparationProposalFixture()
			raw, err := runPreparationNativeFixture(t, root, prelude, q)
			if version != "26.04" {
				if err == nil {
					t.Fatal("mismatched version and suite accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			proof, err := parsePreparationProposalProof(raw, q)
			if err != nil || proof.Packages == nil {
				t.Fatal("missing observed package selection", err)
			}
			plan, _, err := proof.Packages.Plan()
			if err != nil || plan.OSVersion != version || plan.Suite != "resolute" {
				t.Fatal("unexpected selected package plan", err)
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

func TestPreparationPackageProfileMatchesFilesystem(t *testing.T) {
	root, write, fixture := preparationNativeFixture(t)
	write("etc/apt/apt.conf.d/70test", "private-fixture-A")
	proposal := preparationProposalFixture()
	wire, err := runPreparationNativeFixture(t, root, fixture, proposal)
	if err != nil || strings.Contains(string(wire), "private-fixture") {
		t.Fatalf("proposal failed or disclosed raw settings: %v", err)
	}
	proof, err := parsePreparationProposalProof(wire, proposal)
	if err != nil || proof.Packages == nil || !proposalDigest(proof.Packages.ProfileSHA256) {
		t.Fatal("missing approved package profile", err)
	}
	library, err := persistentFilesystemLibraries("24.04")
	if err != nil {
		t.Fatal(err)
	}
	read := func(override string) (string, error) {
		t.Helper()
		script := library + "\nLIMIT = 2 << 20\nEXPECTED_UID = os.getuid()\n" + fixture + "\n" + override +
			"\nfd = os.open(sys.argv[1], FS_OPEN)\nprint(package_install_profile(fd))\n"
		out, err := exec.Command("/usr/bin/python3", "-I", "-B", "-c", script, root).Output()
		return strings.TrimSpace(string(out)), err
	}
	match := proof.Packages.ProfileSHA256
	if observed, err := read(""); err != nil || observed != match {
		t.Fatalf("approval and filesystem disagree: %v", err)
	}
	changed := func(override string) string {
		t.Helper()
		observed, err := read(override)
		if err != nil || !proposalDigest(observed) || observed == match {
			t.Fatalf("profile drift missed: %v", err)
		}
		return observed
	}
	changed("def bounded_command(args): return b'APT::Fixture 1;\\n'")
	row := `["rpcbind.service","RPC","loaded","active","running","","/unit",0,"","/"]`
	unit := func(row string) string {
		return `def bus_reply(*args): return {"type":"a(ssssssouso)","data":[[` + row + `]]}`
	}
	changed(unit(row))
	if _, err := read(unit(strings.Replace(row, ",0,", ",12,", 1))); err == nil {
		t.Fatal("accepted pending service job")
	}
	write("etc/apt/apt.conf.d/70test", "private-fixture-B")
	changed("")
	write("etc/apt/apt.conf.d/70test", "private-fixture-A")
	if err := os.MkdirAll(filepath.Join(root, "var/lib/nfs"), 0700); err != nil {
		t.Fatal(err)
	}
	empty := changed("")
	write("var/lib/nfs/state", "private")
	if changed("") == empty {
		t.Fatal("empty and populated NFS state produced same profile")
	}
	if err := os.Symlink("70test", filepath.Join(root, "etc/apt/apt.conf.d/71test")); err != nil {
		t.Fatal(err)
	}
	if _, err := read(""); err == nil {
		t.Fatal("accepted symlinked configuration")
	}
}
