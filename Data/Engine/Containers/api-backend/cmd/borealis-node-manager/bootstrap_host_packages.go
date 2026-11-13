package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"maps"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type bootstrapHostPackageState struct {
	name, architecture, state, version string
}

// This is an in-scope command selection, not a receipt or permission to install.
// The caller must own and reprove the extracted archive set, package snapshot,
// both borrowed fences and durable host intent around simulation and execution.
type bootstrapHostPackagePlan struct {
	directory string
	before    map[string]bootstrapHostPackageState
	selected  []clusterbootstrap.HostPackagePin
	preflight bool
}

func parseBootstrapHostPackageState(raw []byte) (map[string]bootstrapHostPackageState, error) {
	if len(raw) == 0 || len(raw) > bootstrapDockerInventoryLimit || raw[len(raw)-1] != '\n' {
		return nil, errBootstrapSource
	}
	states := map[string]bootstrapHostPackageState{}
	required := map[string]bool{"apt": false, "dpkg": false, "base-files": false}
	for _, line := range strings.Split(string(raw[:len(raw)-1]), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 || !bootstrapDockerPackage.MatchString(f[0]) || !bootstrapDockerPackage.MatchString(f[1]) ||
			!bootstrapHostPackageVersion.MatchString(f[3]) || f[2] != "install ok installed" && f[2] != "deinstall ok config-files" {
			return nil, errBootstrapSource
		}
		key := f[0] + ":" + f[1]
		if _, exists := states[key]; exists {
			return nil, errBootstrapSource
		}
		states[key] = bootstrapHostPackageState{f[0], f[1], f[2], f[3]}
		if _, exists := required[f[0]]; exists {
			if required[f[0]] || f[2] != "install ok installed" || f[1] != "amd64" && f[1] != "all" {
				return nil, errBootstrapSource
			}
			required[f[0]] = true
		}
	}
	for _, present := range required {
		if !present {
			return nil, errBootstrapSource
		}
	}
	return states, nil
}

func newBootstrapHostPackagePlan(directory string, phase bootstrapHostCommand, installed []byte) (*bootstrapHostPackagePlan, error) {
	want := ""
	switch phase {
	case bootstrapHostInstallBase:
		want = "base"
	case bootstrapHostInstallDocker:
		want = "docker"
	default:
		return nil, errBootstrapSource
	}
	return newBootstrapHostPackagePlanFor(directory, want, installed, false)
}

// Both fixed phases must resolve together before the first package effect. Docker
// cannot be simulated alone on a fresh host whose base dependencies are absent.
// This union is observation-only; it can never produce an install command.
func newBootstrapHostPackagePreflightPlan(directory string, installed []byte) (*bootstrapHostPackagePlan, error) {
	return newBootstrapHostPackagePlanFor(directory, "", installed, true)
}

func newBootstrapHostPackagePlanFor(directory, phase string, installed []byte, preflight bool) (*bootstrapHostPackagePlan, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || directory == "/" || len(directory) > 1024 ||
		(preflight && phase != "") || (!preflight && phase != "base" && phase != "docker") {
		return nil, errBootstrapSource
	}
	for _, c := range directory {
		if c < ' ' || c > '~' {
			return nil, errBootstrapSource
		}
	}
	before, err := parseBootstrapHostPackageState(installed)
	if err != nil {
		return nil, err
	}
	pins, _, err := clusterbootstrap.HostPackagePins()
	if err != nil {
		return nil, errBootstrapSource
	}
	p := &bootstrapHostPackagePlan{directory: directory, before: before, preflight: preflight}
	for _, pin := range pins.Packages {
		if !preflight && pin.Phase != phase {
			continue
		}
		present := false
		for _, state := range before {
			if state.name != pin.Name {
				continue
			}
			if present || state.architecture != pin.Architecture || state.state != "install ok installed" {
				return nil, errBootstrapSource
			}
			present = true
		}
		// Existing configured packages retain their versions. The native solver
		// must leave them unchanged while satisfying every selected dependency.
		// Residual or ambiguous selected-package state is not a fresh install.
		if !present {
			p.selected = append(p.selected, pin)
		}
	}
	return p, nil
}

func (p *bootstrapHostPackagePlan) needsInstall() bool {
	return p != nil && !p.preflight && len(p.selected) != 0
}

func (p *bootstrapHostPackagePlan) simulationCommand() (bootstrapHostCommandSpec, error) {
	return p.command(true)
}

func (p *bootstrapHostPackagePlan) installCommand() (bootstrapHostCommandSpec, error) {
	return p.command(false)
}

func (p *bootstrapHostPackagePlan) command(simulate bool) (bootstrapHostCommandSpec, error) {
	if p == nil || p.directory == "" || len(p.before) == 0 || !simulate && !p.needsInstall() {
		return bootstrapHostCommandSpec{}, errBootstrapSource
	}
	// No repositories are updated and no archives can be downloaded. APT may
	// only consume the verified private cache; normal package/cache locks stay
	// enabled for installation. Empty pkgcache paths prevent persistent caches.
	args := []string{"-o", "APT::Keep-Fds::=3", "-o", "APT::Keep-Fds::=4", "-o", "Dpkg::Use-Pty=0",
		"-o", "DPkg::Lock::Timeout=15", "-o", "Acquire::Retries=0", "-o", "Acquire::http::Timeout=30", "-o", "Acquire::https::Timeout=30",
		"-o", "Dir::Cache::archives=" + p.directory + "/", "-o", "Dir::Cache::pkgcache=", "-o", "Dir::Cache::srcpkgcache=",
		"--no-download", "--no-remove", "--no-upgrade", "--no-install-recommends"}
	if simulate {
		args = append(args, "--simulate")
	} else {
		args = append(args, "--yes")
	}
	args = append(args, "install", "--")
	for _, pin := range p.selected {
		args = append(args, filepath.Join(p.directory, pin.File))
	}
	return bootstrapHostCommandSpec{program: "/usr/bin/apt-get", args: args, timeout: 2 * time.Minute}, nil
}

func (p *bootstrapHostPackagePlan) validateBefore(raw []byte) error {
	current, err := parseBootstrapHostPackageState(raw)
	if p == nil || len(p.before) == 0 || err != nil || !maps.Equal(p.before, current) {
		return errBootstrapSource
	}
	return nil
}

func (p *bootstrapHostPackagePlan) validateResult(raw []byte) error {
	current, err := parseBootstrapHostPackageState(raw)
	if p == nil || p.preflight || len(p.before) == 0 || err != nil {
		return errBootstrapSource
	}
	want := maps.Clone(p.before)
	for _, pin := range p.selected {
		want[pin.Name+":"+pin.Architecture] = bootstrapHostPackageState{pin.Name, pin.Architecture, "install ok installed", pin.Version}
	}
	if !maps.Equal(want, current) {
		return errBootstrapSource
	}
	return nil
}

var bootstrapHostPackageSimulationRow = regexp.MustCompile(`^(Inst|Conf) ([a-z0-9][a-z0-9+.-]{0,199})(?::(amd64|all))? \(([0-9][A-Za-z0-9.+:~\-]{0,199}) local-deb \[(amd64|all)\]\)$`)
var bootstrapHostPackageSimulationSummary = regexp.MustCompile(`^0 upgraded, ([0-9]+) newly installed, 0 to remove and ([0-9]+) not upgraded\.$`)
var bootstrapHostPackageSuggestion = regexp.MustCompile(`^[a-z0-9][a-z0-9+.:\-]{0,220}$`)

// Consume only successful bounded stdout from the fixed C-locale simulation.
// Inst rows with an old version (upgrade/downgrade/reinstall), removal actions,
// foreign origins, missing/repeated actions and unselected packages all fail.
// The caller must reobserve the complete before snapshot before any mutation;
// a simulation does not acquire dpkg custody or make later execution atomic.
func (p *bootstrapHostPackagePlan) validateSimulation(raw []byte) error {
	if p == nil || len(p.before) == 0 || len(raw) == 0 || len(raw) > bootstrapDockerInventoryLimit || raw[len(raw)-1] != '\n' {
		return errBootstrapSource
	}
	for _, c := range raw {
		if c != '\n' && (c < ' ' || c > '~') {
			return errBootstrapSource
		}
	}
	lines := strings.Split(string(raw[:len(raw)-1]), "\n")
	prefix := []string{"Reading package lists...", "Building dependency tree...", "Reading state information..."}
	if len(lines) < len(prefix)+1 {
		return errBootstrapSource
	}
	for i, expected := range prefix {
		if lines[i] != expected {
			return errBootstrapSource
		}
	}
	want := make(map[string]clusterbootstrap.HostPackagePin, len(p.selected))
	for _, pin := range p.selected {
		want[pin.Name] = pin
	}
	installed, configured := map[string]bool{}, map[string]bool{}
	summary, list := false, false
	for _, line := range lines[len(prefix):] {
		if !summary {
			if row := bootstrapHostPackageSimulationSummary.FindStringSubmatch(line); row != nil {
				count, err := strconv.Atoi(row[1])
				held, heldErr := strconv.Atoi(row[2])
				if err != nil || heldErr != nil || count != len(p.selected) || held > len(p.before) {
					return errBootstrapSource
				}
				summary = true
				continue
			}
			switch line {
			case "Suggested packages:", "Recommended packages:", "The following NEW packages will be installed:", "The following additional packages will be installed:", "The following packages were automatically installed and are no longer required:":
				list = true
				continue
			case "Use 'apt autoremove' to remove them.":
				list = false
				continue
			}
			if strings.HasPrefix(line, "  ") && list {
				fields := strings.Fields(line)
				if len(fields) == 0 {
					return errBootstrapSource
				}
				for _, field := range fields {
					if field != "|" && !bootstrapHostPackageSuggestion.MatchString(field) {
						return errBootstrapSource
					}
				}
				continue
			}
			note := false
			for _, pin := range p.selected {
				if line == "Note, selecting '"+pin.Name+"' instead of '"+filepath.Join(p.directory, pin.File)+"'" {
					note = true
					break
				}
			}
			if note {
				continue
			}
			return errBootstrapSource
		}
		row := bootstrapHostPackageSimulationRow.FindStringSubmatch(line)
		if row == nil {
			return errBootstrapSource
		}
		pin, ok := want[row[2]]
		if !ok || pin.Version != row[4] || pin.Architecture != row[5] || row[3] != "" && row[3] != pin.Architecture {
			return errBootstrapSource
		}
		if row[1] == "Inst" {
			if installed[pin.Name] || configured[pin.Name] {
				return errBootstrapSource
			}
			installed[pin.Name] = true
		} else {
			if !installed[pin.Name] || configured[pin.Name] {
				return errBootstrapSource
			}
			configured[pin.Name] = true
		}
	}
	if !summary || len(installed) != len(want) || len(configured) != len(want) {
		return errBootstrapSource
	}
	return nil
}
