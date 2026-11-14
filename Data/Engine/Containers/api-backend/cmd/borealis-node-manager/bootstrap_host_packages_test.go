package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const bootstrapHostPackageBefore = "apt\tamd64\tinstall ok installed\t2.8.3\ndpkg\tamd64\tinstall ok installed\t1.22.6ubuntu6.5\nbase-files\tamd64\tinstall ok installed\t13ubuntu10.3\n"

func bootstrapHostPackagePlanFixture(t *testing.T, phase bootstrapHostCommand, existing bool) (*bootstrapHostPackagePlan, []byte) {
	t.Helper()
	raw := []byte(bootstrapHostPackageBefore)
	if existing {
		pins, _, err := clusterbootstrap.HostPackagePins()
		if err != nil {
			t.Fatal(err)
		}
		want := "base"
		if phase == bootstrapHostInstallDocker {
			want = "docker"
		}
		for _, pin := range pins.Packages {
			if pin.Phase == want {
				raw = append(raw, []byte(pin.Name+"\t"+pin.Architecture+"\tinstall ok installed\t999.0\n")...)
				break
			}
		}
	}
	p, err := newBootstrapHostPackagePlan("/opt/borealis-bootstrap-root.aB12345678/packages", phase, raw)
	if err != nil {
		t.Fatal(err)
	}
	return p, raw
}

func bootstrapHostPackageSimulationFixture(p *bootstrapHostPackagePlan) []byte {
	var out strings.Builder
	out.WriteString("Reading package lists...\nBuilding dependency tree...\nReading state information...\n")
	if p.needsInstall() {
		out.WriteString("Suggested packages:\n  unrelated-suggestion | alternate:amd64\nThe following NEW packages will be installed:\n")
		for _, pin := range p.selected {
			fmt.Fprintf(&out, "  %s\n", pin.Name)
		}
	}
	fmt.Fprintf(&out, "0 upgraded, %d newly installed, 0 to remove and 0 not upgraded.\n", len(p.selected))
	for _, pin := range p.selected {
		fmt.Fprintf(&out, "Inst %s (%s local-deb [%s])\nConf %s (%s local-deb [%s])\n", pin.Name, pin.Version, pin.Architecture, pin.Name, pin.Version, pin.Architecture)
	}
	return []byte(out.String())
}

func bootstrapHostPackageResultFixture(p *bootstrapHostPackagePlan, before []byte) []byte {
	out := slices.Clone(before)
	for _, pin := range p.selected {
		out = append(out, []byte(pin.Name+"\t"+pin.Architecture+"\tinstall ok installed\t"+pin.Version+"\n")...)
	}
	return out
}

func TestBootstrapHostPackageCommandsAreFixedAndOffline(t *testing.T) {
	for _, phase := range []bootstrapHostCommand{bootstrapHostInstallBase, bootstrapHostInstallDocker} {
		p, before := bootstrapHostPackagePlanFixture(t, phase, true)
		if !p.needsInstall() || p.validateBefore(before) != nil {
			t.Fatal("valid package selection unavailable")
		}
		for _, simulate := range []bool{true, false} {
			spec, err := p.command(simulate)
			if err != nil || spec.program != "/usr/bin/apt-get" || spec.timeout != 2*time.Minute {
				t.Fatal("wrong command boundary", spec, err)
			}
			for _, required := range []string{"APT::Keep-Fds::=3", "APT::Keep-Fds::=4", "Dpkg::Use-Pty=0", "DPkg::Lock::Timeout=15",
				"Dir::Cache::archives=" + p.directory + "/", "Dir::Cache::pkgcache=", "Dir::Cache::srcpkgcache=",
				"Dir::Etc::sourcelist=", "Dir::Etc::sourceparts=",
				"--no-download", "--no-remove", "--no-upgrade", "--no-install-recommends"} {
				if !slices.Contains(spec.args, required) {
					t.Fatal("missing fixed package boundary", required)
				}
			}
			if slices.Contains(spec.args, "--simulate") != simulate || slices.Contains(spec.args, "--yes") == simulate {
				t.Fatal("simulation and mutation crossed")
			}
			for _, forbidden := range []string{"Debug::NoLocking=true", "update", "upgrade", "autoremove", "--allow-unauthenticated", "--allow-downgrades", "--allow-remove-essential", "--force-yes", "--reinstall"} {
				if slices.Contains(spec.args, forbidden) {
					t.Fatal("unsafe package command", forbidden)
				}
			}
			index := slices.Index(spec.args, "--")
			if index < 1 || spec.args[index-1] != "install" || len(spec.args[index+1:]) != len(p.selected) {
				t.Fatal("package selection changed")
			}
			for i, pin := range p.selected {
				if spec.args[index+1+i] != filepath.Join(p.directory, pin.File) {
					t.Fatal("caller supplied a package or option")
				}
			}
			spec.args[0] = "changed"
			again, err := p.command(simulate)
			if err != nil || slices.Contains(again.args, "changed") {
				t.Fatal("caller mutated retained command")
			}
		}
		before[0] = '!'
		if p.validateBefore([]byte(bootstrapHostPackageBefore)) == nil {
			t.Fatal("lost existing package baseline")
		}
	}
	for _, path := range []string{"", "/", "relative", "/opt/../tmp", "/opt//packages", "/opt/packages/", "/opt/a\npackages", "/opt/é", "/" + strings.Repeat("x", 1024)} {
		if p, err := newBootstrapHostPackagePlan(path, bootstrapHostInstallBase, []byte(bootstrapHostPackageBefore)); err == nil || p != nil {
			t.Fatal("invalid extraction directory accepted", path)
		}
	}
	for _, phase := range []bootstrapHostCommand{0, bootstrapHostAPTUpdate, bootstrapHostEnableDocker} {
		if p, err := newBootstrapHostPackagePlan("/opt/packages", phase, []byte(bootstrapHostPackageBefore)); err == nil || p != nil {
			t.Fatal("unknown package phase accepted")
		}
	}
	var p *bootstrapHostPackagePlan
	if _, err := p.simulationCommand(); err == nil {
		t.Fatal("missing package plan accepted")
	}
}

func TestBootstrapHostPackagePreflightCannotInstall(t *testing.T) {
	p, err := newBootstrapHostPackagePreflightPlan("/opt/packages", []byte(bootstrapHostPackageBefore))
	if err != nil || p.needsInstall() {
		t.Fatal("preflight became a mutation plan", err)
	}
	pins, _, err := clusterbootstrap.HostPackagePins()
	if err != nil || !reflect.DeepEqual(p.selected, pins.Packages) {
		t.Fatal("preflight omitted future phase dependencies", err)
	}
	spec, err := p.simulationCommand()
	if err != nil || !slices.Contains(spec.args, "--simulate") || p.validateSimulation(bootstrapHostPackageSimulationFixture(p)) != nil {
		t.Fatal("whole host preflight unavailable", err)
	}
	for _, required := range []string{"Dir::Etc::sourcelist=", "Dir::Etc::sourceparts=", "Dir::Cache::pkgcache=", "Dir::Cache::srcpkgcache="} {
		if !slices.Contains(spec.args, required) {
			t.Fatal("whole host preflight can consume ambient repository candidates", required)
		}
	}
	if _, err := p.installCommand(); err == nil {
		t.Fatal("union preflight allowed install")
	}
	if p.validateResult(bootstrapHostPackageResultFixture(p, []byte(bootstrapHostPackageBefore))) == nil {
		t.Fatal("preflight supplied phase completion evidence")
	}
}

func TestBootstrapHostPackageSnapshotsPreserveUnchangedPackages(t *testing.T) {
	p, before := bootstrapHostPackagePlanFixture(t, bootstrapHostInstallBase, true)
	complete := bootstrapHostPackageResultFixture(p, before)
	if p.validateResult(complete) != nil || p.validateResult(before) == nil {
		t.Fatal("complete package delta not enforced")
	}
	for _, mode := range []string{"upgrade existing", "remove existing", "extra package", "missing selected", "wrong selected version", "wrong selected arch", "pending configure", "duplicate", "malformed", "missing apt", "no newline", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			raw := string(complete)
			pin := p.selected[0]
			switch mode {
			case "upgrade existing":
				raw = strings.Replace(raw, "999.0", "999.1", 1)
			case "remove existing":
				raw = strings.Replace(raw, "base-files\tamd64\tinstall ok installed\t13ubuntu10.3\n", "", 1)
			case "extra package":
				raw += "unlocked\tamd64\tinstall ok installed\t1.0\n"
			case "missing selected":
				raw = strings.Replace(raw, pin.Name+"\t"+pin.Architecture+"\tinstall ok installed\t"+pin.Version+"\n", "", 1)
			case "wrong selected version":
				raw = strings.Replace(raw, "\t"+pin.Version+"\n", "\t999.0\n", 1)
			case "wrong selected arch":
				raw = strings.Replace(raw, pin.Name+"\t"+pin.Architecture+"\t", pin.Name+"\tarm64\t", 1)
			case "pending configure":
				raw = strings.Replace(raw, "install ok installed", "install ok unpacked", 1)
			case "duplicate":
				raw += strings.SplitAfter(raw, "\n")[0]
			case "malformed":
				raw = strings.Replace(raw, "\t", " ", 1)
			case "missing apt":
				raw = strings.SplitN(raw, "\n", 2)[1]
			case "no newline":
				raw = strings.TrimSuffix(raw, "\n")
			case "overflow":
				raw = strings.Repeat("x", bootstrapDockerInventoryLimit+1)
			}
			if p.validateResult([]byte(raw)) == nil {
				t.Fatal("changed or incomplete package result accepted")
			}
		})
	}
	for _, state := range []string{"deinstall ok config-files", "install ok unpacked", "hold ok installed"} {
		pin := p.selected[0]
		initial := append(slices.Clone(before), []byte(pin.Name+"\t"+pin.Architecture+"\t"+state+"\t"+pin.Version+"\n")...)
		if _, err := newBootstrapHostPackagePlan(p.directory, bootstrapHostInstallBase, initial); err == nil {
			t.Fatal("retained/unfinished selected package treated as fresh", state)
		}
	}
	allInstalled, err := newBootstrapHostPackagePlan(p.directory, bootstrapHostInstallBase, complete)
	if err != nil || allInstalled.needsInstall() || allInstalled.validateSimulation(bootstrapHostPackageSimulationFixture(allInstalled)) != nil {
		t.Fatal("unchanged installed phase rejected", err)
	}
	if _, err := allInstalled.installCommand(); err == nil {
		t.Fatal("empty selection can launch mutating APT")
	}
}

func TestBootstrapHostPackageSimulationBindsExactLocalPlan(t *testing.T) {
	for _, phase := range []bootstrapHostCommand{bootstrapHostInstallBase, bootstrapHostInstallDocker} {
		p, _ := bootstrapHostPackagePlanFixture(t, phase, false)
		valid := string(bootstrapHostPackageSimulationFixture(p))
		pin := p.selected[0]
		inst := fmt.Sprintf("Inst %s (%s local-deb [%s])\n", pin.Name, pin.Version, pin.Architecture)
		conf := strings.Replace(inst, "Inst", "Conf", 1)
		for _, mode := range []string{"valid", "qualified name", "note", "upgrade", "downgrade", "reinstall", "remove", "unlocked", "wrong version", "wrong architecture", "foreign origin", "mixed repository origin", "duplicate install", "duplicate configure", "configure before install", "missing install", "missing configure", "summary count", "duplicate summary", "error", "truncated", "prefix", "control", "oversized"} {
			t.Run(fmt.Sprintf("%d/%s", phase, mode), func(t *testing.T) {
				raw := valid
				switch mode {
				case "qualified name":
					raw = strings.ReplaceAll(raw, "st "+pin.Name+" (", "st "+pin.Name+":"+pin.Architecture+" (")
				case "note":
					note := "Note, selecting '" + pin.Name + "' instead of '" + filepath.Join(p.directory, pin.File) + "'\n"
					raw = strings.Replace(raw, "Suggested packages:\n", note+"Suggested packages:\n", 1)
				case "upgrade", "downgrade", "reinstall":
					raw = strings.Replace(raw, inst, strings.Replace(inst, " (", " [1.0] (", 1), 1)
				case "remove":
					raw += "Remv apt [2.8.3]\n"
				case "unlocked":
					raw = strings.ReplaceAll(raw, " "+pin.Name+" (", " unlocked (")
				case "wrong version":
					raw = strings.Replace(raw, "("+pin.Version+" local-deb", "(999.0 local-deb", 1)
				case "wrong architecture":
					raw = strings.Replace(raw, "["+pin.Architecture+"])", "[arm64])", 1)
				case "foreign origin":
					raw = strings.Replace(raw, "local-deb", "Ubuntu:noble", 1)
				case "mixed repository origin":
					raw = strings.Replace(raw, "local-deb", "Ubuntu:24.04/noble-updates, local-deb", 1)
				case "duplicate install":
					raw = strings.Replace(raw, inst, inst+inst, 1)
				case "duplicate configure":
					raw = strings.Replace(raw, conf, conf+conf, 1)
				case "configure before install":
					raw = strings.Replace(raw, inst+conf, conf+inst, 1)
				case "missing install":
					raw = strings.Replace(raw, inst, "", 1)
				case "missing configure":
					raw = strings.Replace(raw, conf, "", 1)
				case "summary count":
					raw = strings.Replace(raw, fmt.Sprintf("%d newly installed", len(p.selected)), "0 newly installed", 1)
				case "duplicate summary":
					raw += "0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n"
				case "error":
					raw += "E: Unmet dependencies\n"
				case "truncated":
					raw = strings.TrimSuffix(raw, "\n")
				case "prefix":
					raw = strings.TrimPrefix(raw, "Reading package lists...\n")
				case "control":
					raw += "\x00\n"
				case "oversized":
					raw = strings.Repeat("x", bootstrapDockerInventoryLimit+1)
				}
				err := p.validateSimulation([]byte(raw))
				if (err == nil) != (mode == "valid" || mode == "qualified name" || mode == "note") {
					t.Fatal("unexpected exact simulation result", err)
				}
			})
		}
	}
	// Optional retained native observations extend the always-run portable
	// cases above. Reading these files never starts APT or changes this host.
	read := func(directory, name string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if directory := os.Getenv("BOREALIS_TEST_HOST_PACKAGE_COMMAND_EVIDENCE"); directory != "" {
		before := read(directory, "before.tsv")
		for _, phase := range []struct {
			step        bootstrapHostCommand
			name, after string
		}{{bootstrapHostInstallBase, "base", "after-base.tsv"}, {bootstrapHostInstallDocker, "docker", "after-docker.tsv"}} {
			p, err := newBootstrapHostPackagePlan("/opt/package-cache", phase.step, before)
			if err != nil || p.validateBefore(before) != nil || p.validateSimulation(read(directory, phase.name+"-simulation.log")) != nil {
				t.Fatal("actual phase simulation rejected", phase.name, err)
			}
			after := read(directory, phase.after)
			if p.validateResult(after) != nil {
				t.Fatal("actual phase inventory result rejected", phase.name)
			}
			before = after
		}
	}
	if directory := os.Getenv("BOREALIS_TEST_HOST_PACKAGE_PREFLIGHT_EVIDENCE"); directory != "" {
		p, err := newBootstrapHostPackagePreflightPlan("/archives", read(directory, "before.tsv"))
		if err != nil || p.validateSimulation(read(directory, "simulation.log")) != nil {
			t.Fatal("actual whole-host simulation rejected", err)
		}
	}
}
