package clusterbootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
)

const HostPackageInventoryLimit = 2 << 20

type HostPackageState struct {
	Name, Architecture, State, Version string
}

// This projection selects only absent packages from the compiled release plan.
// It proves no dependency resolution, installation, readiness or authority.
// Native APT simulation still proves compatible installed packages stay intact.
type HostPackageSelection struct {
	PlanSHA256      string   `json:"plan_sha256"`
	InventorySHA256 string   `json:"inventory_sha256"`
	Missing         []string `json:"missing"`
}

func (s HostPackageSelection) Clone() HostPackageSelection {
	s.Missing = slices.Clone(s.Missing)
	return s
}

func (s HostPackageSelection) Pins() ([]HostPackagePin, error) {
	plan, digest, err := HostPackagePins()
	if err != nil || s.PlanSHA256 != digest || !digestPattern.MatchString(s.InventorySHA256) || s.InventorySHA256 == strings.Repeat("0", 64) || s.Missing == nil || len(s.Missing) > len(plan.Packages) {
		return nil, ErrPreparationConfig
	}
	pins := make([]HostPackagePin, 0, len(s.Missing))
	for i, name := range s.Missing {
		if i > 0 && s.Missing[i-1] >= name {
			return nil, ErrPreparationConfig
		}
		index := slices.IndexFunc(plan.Packages, func(p HostPackagePin) bool { return p.Name == name })
		if index < 0 {
			return nil, ErrPreparationConfig
		}
		pins = append(pins, plan.Packages[index])
	}
	return pins, nil
}

func (s HostPackageSelection) Validate() error {
	_, err := s.Pins()
	return err
}

// Same bounded dpkg-query contract used by the native transaction. Neither
// package descriptions nor private configuration cross this parser boundary.
func ParseHostPackageState(raw []byte) (map[string]HostPackageState, error) {
	if len(raw) == 0 || len(raw) > HostPackageInventoryLimit || raw[len(raw)-1] != '\n' {
		return nil, ErrPreparationConfig
	}
	states := map[string]HostPackageState{}
	required := map[string]bool{"apt": false, "dpkg": false, "base-files": false}
	for _, line := range strings.Split(string(raw[:len(raw)-1]), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 || !hostPackageName.MatchString(f[0]) || !hostPackageName.MatchString(f[1]) ||
			!hostPackageVersion.MatchString(f[3]) || f[2] != "install ok installed" && f[2] != "deinstall ok config-files" {
			return nil, ErrPreparationConfig
		}
		key := f[0] + ":" + f[1]
		if _, exists := states[key]; exists {
			return nil, ErrPreparationConfig
		}
		states[key] = HostPackageState{f[0], f[1], f[2], f[3]}
		if _, exists := required[f[0]]; exists {
			if required[f[0]] || f[2] != "install ok installed" || f[1] != "amd64" && f[1] != "all" {
				return nil, ErrPreparationConfig
			}
			required[f[0]] = true
		}
	}
	for _, present := range required {
		if !present {
			return nil, ErrPreparationConfig
		}
	}
	return states, nil
}

func SelectHostPackages(raw []byte) (HostPackageSelection, error) {
	states, err := ParseHostPackageState(raw)
	plan, digest, pinErr := HostPackagePins()
	if err != nil || pinErr != nil {
		return HostPackageSelection{}, ErrPreparationConfig
	}
	hash := sha256.Sum256(raw)
	selection := HostPackageSelection{PlanSHA256: digest, InventorySHA256: hex.EncodeToString(hash[:]), Missing: []string{}}
	for _, pin := range plan.Packages {
		present := false
		for _, state := range states {
			if state.Name != pin.Name {
				continue
			}
			if present || state.Architecture != pin.Architecture || state.State != "install ok installed" {
				return HostPackageSelection{}, ErrPreparationConfig
			}
			present = true
		}
		if !present {
			selection.Missing = append(selection.Missing, pin.Name)
		}
	}
	slices.Sort(selection.Missing)
	return selection, nil
}
