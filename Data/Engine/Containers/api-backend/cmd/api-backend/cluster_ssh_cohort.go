package main

import (
	"borealis/api-backend/internal/clusterremote"
	"errors"
	"net/netip"
)

const clusterSSHInspectionLifetimeSeconds int64 = 300

var (
	errClusterSSHCohort         = errors.New("SSH inspection cohort is incomplete, stale or no longer owned by this operation")
	errClusterSSHCohortIdentity = errors.New("SSH target identity conflicts with another target or existing member")
	errClusterSSHCohortNetwork  = errors.New("SSH targets, members and VIPs lack a consistent usable management subnet")
	errClusterSSHCohortExisting = errors.New("SSH target contains existing or uncertain installation state; reconcile membership or approve a separate takeover plan")
	errClusterSSHCohortPlatform = errors.New("SSH target platform is unsupported")
)

// Inspection evidence is an observation, never permission to install or join.
// The controller must still verify sizing, storage, persistent static network,
// Layer2 reachability, source release/configuration and operator confirmation.
// Recheck all identities and authority when committing any subsequent step.
type clusterSSHInspectedTarget struct {
	Binding     clusterSSHCredentialBinding
	Key         clusterremote.HostKey
	Ordinal     int64
	InspectedAt int64
	Attempt     int64
	Generation  int64
	Report      clusterSSHInspectionReport
}

type clusterSSHInspectionCohort struct {
	ClusterID        string
	OperationID      string
	ControllerHolder string
	Attempt          int64
	ObservedAt       int64 // Database clock, same snapshot as all target rows.
	Targets          []clusterSSHInspectedTarget
}

// Caller supplies freshly reconciled source membership from the existing
// controller and actual Kubernetes metadata. No identity may be synthesized
// from an IP, hostname or provisioning UUID. SSH fingerprints are optional for
// legacy members with no approved SSH record; machine and Node IDs are required.
type clusterSSHSourceMember struct {
	NodeID         string
	NodeUID        string
	Name           string
	Address        string
	MachineID      string
	BootID         string
	SSHFingerprint string
}

type clusterSSHSourceCohort struct {
	ClusterID       string
	KubeSystemUID   string
	ActiveSize      int64
	DesiredSize     int64
	Status          string
	HMRState        string
	ControlPlaneVIP string
	EdgeVIP         string
	Members         []clusterSSHSourceMember
}

func validateClusterSSHInspectionCohort(cohort clusterSSHInspectionCohort, source clusterSSHSourceCohort) error {
	expected, err := currentReleaseAdmissionBatchSize(source.ActiveSize, source.DesiredSize, source.Status)
	if err != nil || !clusterUUIDRE.MatchString(cohort.ClusterID) || cohort.ClusterID != source.ClusterID ||
		!clusterUUIDRE.MatchString(cohort.OperationID) || !clusterUUIDRE.MatchString(cohort.ControllerHolder) || cohort.Attempt < 1 || cohort.ObservedAt < 1 ||
		!clusterUUIDRE.MatchString(source.KubeSystemUID) || source.HMRState != "inactive" || len(source.Members) != int(source.ActiveSize) || len(cohort.Targets) != expected {
		return errClusterSSHCohort
	}
	addresses, names, machines, boots, fingerprints, identities := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	add := func(values map[string]bool, value string) bool {
		if value == "" || values[value] {
			return false
		}
		values[value] = true
		return true
	}
	for _, member := range source.Members {
		if !clusterUUIDRE.MatchString(member.NodeID) || !clusterUUIDRE.MatchString(member.NodeUID) ||
			len(validateClusterNodeName("node", member.Name)) != 0 || !clusterSSHMachineID.MatchString(member.MachineID) ||
			member.MachineID == "00000000000000000000000000000000" || !clusterUUIDRE.MatchString(member.BootID) ||
			(clusterremote.Target{Address: member.Address, Port: 22}).Validate() != nil {
			return errClusterSSHCohort
		}
		if !add(identities, member.NodeID) || !add(identities, member.NodeUID) || !add(addresses, member.Address) || !add(names, member.Name) ||
			!add(machines, member.MachineID) || !add(boots, member.BootID) || (member.SSHFingerprint != "" && !add(fingerprints, member.SSHFingerprint)) {
			return errClusterSSHCohortIdentity
		}
	}
	// The same VIP can serve both roles. Neither may be a host address.
	for _, vip := range []string{source.ControlPlaneVIP, source.EdgeVIP} {
		if (clusterremote.Target{Address: vip, Port: 22}).Validate() != nil || addresses[vip] {
			return errClusterSSHCohortNetwork
		}
	}
	prefix := netip.Prefix{}
	for index, target := range cohort.Targets {
		r := target.Report
		if !target.Binding.valid() || target.Binding.ClusterID != cohort.ClusterID || target.Binding.OperationID != cohort.OperationID || target.Ordinal != int64(index+1) ||
			target.Key.Validate() != nil || target.Key.Fingerprint != target.Binding.Fingerprint || target.Attempt != cohort.Attempt || target.Generation < 1 ||
			target.InspectedAt <= 0 || target.InspectedAt > cohort.ObservedAt || cohort.ObservedAt-target.InspectedAt >= clusterSSHInspectionLifetimeSeconds || r.valid() != nil {
			return errClusterSSHCohort
		}
		if !r.SupportedPlatform {
			return errClusterSSHCohortPlatform
		}
		if !r.NoExistingInstallation {
			return errClusterSSHCohortExisting
		}
		if !add(identities, target.Binding.TargetID) || !add(addresses, target.Binding.Address) || !add(names, r.Hostname) ||
			!add(machines, r.MachineID) || !add(boots, r.BootID) || !add(fingerprints, target.Binding.Fingerprint) {
			return errClusterSSHCohortIdentity
		}
		observed, err := netip.ParsePrefix(r.ConnectedPrefix)
		if err != nil || (prefix.IsValid() && prefix != observed) {
			return errClusterSSHCohortNetwork
		}
		prefix = observed
	}
	if addresses[source.ControlPlaneVIP] || addresses[source.EdgeVIP] {
		return errClusterSSHCohortNetwork
	}
	addresses[source.ControlPlaneVIP], addresses[source.EdgeVIP] = true, true
	for address := range addresses {
		ip, err := netip.ParseAddr(address)
		if err != nil || !clusterremote.UsableManagementAddress(prefix, ip) {
			return errClusterSSHCohortNetwork
		}
	}
	return nil
}
