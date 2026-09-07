package main

import (
	"borealis/api-backend/internal/clusterremote"
	"context"
	"errors"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

var errClusterSSHInspectionWorker = errors.New("SSH target inspection unavailable; target ownership and credentials must be rechecked")

// Deliberately limited public evidence. A successful inspection is never a
// clean-host, takeover, preparation, membership or public-readiness approval.
type clusterSSHInspectionReport struct {
	Version                int                      `json:"version"`
	Hostname               string                   `json:"hostname"`
	Kernel                 string                   `json:"kernel"`
	OSID                   string                   `json:"os_id"`
	OSVersion              string                   `json:"os_version"`
	Architecture           string                   `json:"architecture"`
	CPUCount               uint32                   `json:"cpu_count"`
	MemoryKiB              uint64                   `json:"memory_kib"`
	DiskTotalKiB           uint64                   `json:"disk_total_kib"`
	DiskFreeKiB            uint64                   `json:"disk_free_kib"`
	DiskScope              string                   `json:"disk_scope"`
	MachineID              string                   `json:"machine_id"`
	BootID                 string                   `json:"boot_id"`
	KubeSystemUID          string                   `json:"kube_system_uid"`
	NodesState             string                   `json:"nodes_state"`
	Nodes                  []clusterSSHObservedNode `json:"nodes"`
	SupportedPlatform      bool                     `json:"supported_platform"`
	NoExistingInstallation bool                     `json:"no_existing_installation"`
	ConnectedPrefix        string                   `json:"connected_prefix"`
	BorealisPath           string                   `json:"borealis_path"`
	BorealisConfig         string                   `json:"borealis_config"`
	K3sConfig              string                   `json:"k3s_config"`
	K3sData                string                   `json:"k3s_data"`
	K3sBinary              string                   `json:"k3s_binary"`
	K3sLoad                string                   `json:"k3s_load"`
	K3sActive              string                   `json:"k3s_active"`
	K3sAgentLoad           string                   `json:"k3s_agent_load"`
	K3sAgentActive         string                   `json:"k3s_agent_active"`
}

type clusterSSHObservedNode struct {
	Name string `json:"name"`
	UID  string `json:"uid"`
}

var clusterSSHReportText = regexp.MustCompile(`^[A-Za-z0-9._:+-]{1,128}$`)
var clusterSSHMachineID = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (r clusterSSHInspectionReport) valid() error {
	if r.Version != 1 || len(validateClusterNodeName("hostname", r.Hostname)) != 0 || r.Hostname != strings.ToLower(r.Hostname) ||
		!clusterSSHReportText.MatchString(r.Kernel) || !clusterSSHReportText.MatchString(r.OSID) || !clusterSSHReportText.MatchString(r.OSVersion) || !clusterSSHReportText.MatchString(r.Architecture) ||
		r.CPUCount == 0 || r.MemoryKiB == 0 || r.MemoryKiB > 1<<53-1 || r.DiskTotalKiB == 0 || r.DiskTotalKiB > 1<<53-1 || r.DiskFreeKiB > r.DiskTotalKiB ||
		!textInSet(r.DiskScope, "root", "opt") || !textInSet(r.NodesState, "not-present", "unknown", "observed") || len(r.Nodes) > 64 {
		return errClusterSSHInspectionWorker
	}
	if r.ConnectedPrefix != "" {
		prefix, err := netip.ParsePrefix(r.ConnectedPrefix)
		if err != nil || !prefix.Addr().Is4() || !prefix.Addr().IsPrivate() || prefix != prefix.Masked() || prefix.String() != r.ConnectedPrefix || prefix.Bits() > 30 {
			return errClusterSSHInspectionWorker
		}
	}
	for _, value := range []string{r.K3sLoad, r.K3sAgentLoad} {
		if !textInSet(value, "loaded", "not-found", "masked", "error", "bad-setting", "unknown") {
			return errClusterSSHInspectionWorker
		}
	}
	for _, value := range []string{r.K3sActive, r.K3sAgentActive} {
		if !textInSet(value, "active", "inactive", "activating", "deactivating", "failed", "reloading", "maintenance", "unknown") {
			return errClusterSSHInspectionWorker
		}
	}
	if r.SupportedPlatform != (clusterremote.HostFacts{Kernel: r.Kernel, Architecture: r.Architecture, OSID: r.OSID, OSVersion: r.OSVersion}).SupportedPlatform() {
		return errClusterSSHInspectionWorker
	}
	if r.MachineID != "unknown" && (!clusterSSHMachineID.MatchString(r.MachineID) || r.MachineID == strings.Repeat("0", 32)) {
		return errClusterSSHInspectionWorker
	}
	if r.BootID != "unknown" && !clusterUUIDRE.MatchString(r.BootID) {
		return errClusterSSHInspectionWorker
	}
	if !textInSet(r.KubeSystemUID, "not-present", "unknown") && !clusterUUIDRE.MatchString(r.KubeSystemUID) {
		return errClusterSSHInspectionWorker
	}
	for _, path := range []string{r.BorealisPath, r.BorealisConfig, r.K3sConfig, r.K3sData, r.K3sBinary} {
		if !textInSet(path, "absent", "directory", "regular", "symlink", "other", "unknown") {
			return errClusterSSHInspectionWorker
		}
	}
	seen := map[string]bool{}
	for _, node := range r.Nodes {
		if len(validateClusterNodeName("node", node.Name)) != 0 || !clusterUUIDRE.MatchString(node.UID) || seen[node.Name] {
			return errClusterSSHInspectionWorker
		}
		seen[node.Name] = true
	}
	if r.NodesState != "observed" && len(r.Nodes) != 0 {
		return errClusterSSHInspectionWorker
	}
	if r.NoExistingInstallation && !(clusterremote.PrivilegedFacts{MachineID: r.MachineID, BootID: r.BootID, KubeSystemUID: r.KubeSystemUID, NodesState: r.NodesState,
		BorealisPath: r.BorealisPath, BorealisConfig: r.BorealisConfig, K3sConfig: r.K3sConfig, K3sData: r.K3sData, K3sBinary: r.K3sBinary,
		K3sLoad: r.K3sLoad, K3sActive: r.K3sActive, K3sAgentLoad: r.K3sAgentLoad, K3sAgentActive: r.K3sAgentActive}).NoExistingInstallation() {
		return errClusterSSHInspectionWorker
	}
	return nil
}

func clusterSSHReport(host clusterremote.HostFacts, privileged clusterremote.PrivilegedFacts, address string) (clusterSSHInspectionReport, error) {
	report := clusterSSHInspectionReport{Version: 1, Hostname: host.Hostname, Kernel: host.Kernel, OSID: host.OSID, OSVersion: host.OSVersion, Architecture: host.Architecture, CPUCount: host.CPUCount, MemoryKiB: host.MemoryKiB,
		DiskTotalKiB: host.DiskTotalKiB, DiskFreeKiB: host.DiskFreeKiB, DiskScope: host.DiskScope, MachineID: privileged.MachineID, BootID: privileged.BootID, KubeSystemUID: privileged.KubeSystemUID, NodesState: privileged.NodesState, Nodes: []clusterSSHObservedNode{},
		SupportedPlatform: host.SupportedPlatform(), NoExistingInstallation: host.BorealisPath == "absent" && host.K3sUnit == "not-found" && privileged.NoExistingInstallation(), BorealisPath: privileged.BorealisPath, BorealisConfig: privileged.BorealisConfig, K3sConfig: privileged.K3sConfig, K3sData: privileged.K3sData, K3sBinary: privileged.K3sBinary,
		K3sLoad: privileged.K3sLoad, K3sActive: privileged.K3sActive, K3sAgentLoad: privileged.K3sAgentLoad, K3sAgentActive: privileged.K3sAgentActive}
	if prefix, err := privileged.ConnectedManagementNetwork(address, nil); err == nil {
		report.ConnectedPrefix = prefix.String()
	}
	for _, node := range privileged.Nodes {
		report.Nodes = append(report.Nodes, clusterSSHObservedNode{Name: node.Name, UID: node.UID})
	}
	if report.valid() != nil {
		return clusterSSHInspectionReport{}, errClusterSSHInspectionWorker
	}
	return report, nil
}

type clusterSSHInspectionStore interface {
	renewClusterSSHTargetGeneration(context.Context, clusterSSHTargetLease, string) error
	loadClusterSSHTargetCredentials(context.Context, clusterSSHTargetLease) (sealedClusterSSHCredentials, error)
	loadClusterSSHTargetKey(context.Context, clusterSSHTargetLease) (clusterremote.HostKey, error)
	completeClusterSSHInspection(context.Context, clusterSSHTargetLease, string, clusterSSHInspectionReport) error
}

type clusterSSHWorkerAegis interface {
	openClusterSSHCredentials(context.Context, sealedClusterSSHCredentials, clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error)
	verifyClusterSSHGeneration(context.Context, string) error
}

type clusterSSHInspectionClient interface {
	Inspect(context.Context) (clusterremote.HostFacts, error)
	InspectPrivileged(context.Context, []byte) (clusterremote.PrivilegedFacts, error)
	Close() error
}

type clusterSSHInspectionWorker struct {
	store         clusterSSHInspectionStore
	aegis         clusterSSHWorkerAegis
	connect       func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHInspectionClient, error)
	renewInterval time.Duration
}

func newClusterSSHInspectionWorker(store *postgresOperatorStore, aegis *goAegisService) *clusterSSHInspectionWorker {
	return &clusterSSHInspectionWorker{store: store, aegis: aegis, renewInterval: 5 * time.Second, connect: func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHInspectionClient, error) {
		return (clusterremote.Transport{}).Connect(ctx, target, key, credential)
	}}
}

// Caller claims under the existing controller operation. This executor owns
// only read-only inspection and its fenced public result; it cannot authorize
// preparation or change membership. Queue/controller dispatch is separate.
func (w *clusterSSHInspectionWorker) inspect(parent context.Context, lease clusterSSHTargetLease) error {
	if w == nil || w.store == nil || w.aegis == nil || w.connect == nil || lease.Step != "inspect" {
		return errClusterSSHInspectionWorker
	}
	ctx, cancel := context.WithTimeout(parent, time.Minute)
	defer cancel()
	sealed, err := w.store.loadClusterSSHTargetCredentials(ctx, lease)
	if err != nil {
		return errClusterSSHInspectionWorker
	}
	check := func(parent context.Context) error {
		ctx, cancel := context.WithTimeout(parent, 3*time.Second)
		defer cancel()
		if w.aegis.verifyClusterSSHGeneration(ctx, sealed.generation) != nil || w.store.renewClusterSSHTargetGeneration(ctx, lease, sealed.generation) != nil || ctx.Err() != nil {
			return errClusterSSHInspectionWorker
		}
		return nil
	}
	if check(ctx) != nil {
		return errClusterSSHInspectionWorker
	}
	guard := startClusterControllerLeaseGuardWithGrace(ctx, w.renewInterval, time.Millisecond, func(ctx context.Context) (bool, error) { return check(ctx) == nil, nil })
	defer guard.Close()
	ctx = guard.Context()
	key, err := w.store.loadClusterSSHTargetKey(ctx, lease)
	if err != nil || key.Fingerprint != sealed.binding.Fingerprint {
		return errClusterSSHInspectionWorker
	}
	envelope, err := w.aegis.openClusterSSHCredentials(ctx, sealed, sealed.binding)
	if err != nil {
		return errClusterSSHInspectionWorker
	}
	var credential *clusterremote.Credential
	switch envelope.Method {
	case "password":
		credential, err = clusterremote.PasswordCredential(envelope.Username, []byte(envelope.Password))
	case "private_key":
		credential, err = clusterremote.KeyCredential(envelope.Username, []byte(envelope.PrivateKey), []byte(envelope.Passphrase))
	default:
		return errClusterSSHInspectionWorker
	}
	if err != nil {
		return errClusterSSHInspectionWorker
	}
	defer credential.Destroy()
	sudo := []byte(envelope.SudoPassword)
	defer clear(sudo)
	envelope = clusterSSHCredentialEnvelope{}
	if check(ctx) != nil {
		return errClusterSSHInspectionWorker
	}
	client, err := w.connect(ctx, clusterremote.Target{Address: sealed.binding.Address, Port: sealed.binding.Port}, key, credential)
	if err != nil {
		return errClusterSSHInspectionWorker
	}
	defer client.Close()
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	if check(ctx) != nil {
		return errClusterSSHInspectionWorker
	}
	host, err := client.Inspect(ctx)
	if err != nil || check(ctx) != nil {
		return errClusterSSHInspectionWorker
	}
	privileged, err := client.InspectPrivileged(ctx, sudo)
	if err != nil || check(ctx) != nil {
		return errClusterSSHInspectionWorker
	}
	report, err := clusterSSHReport(host, privileged, sealed.binding.Address)
	if err != nil || w.store.completeClusterSSHInspection(ctx, lease, sealed.generation, report) != nil {
		return errClusterSSHInspectionWorker
	}
	return nil
}
