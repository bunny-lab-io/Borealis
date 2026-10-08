package clusterbootstrap

import (
	"path"
	"strings"
)

// SourceKubelet binds an observed PodResources listener pathname to the current
// k3s.service process. It does not attest filesystem/socket contents, executable
// digest, CSI service health or persistent configuration.
type SourceKubelet struct {
	Root             string `json:"root"`
	PID              uint64 `json:"pid"`
	StartTicks       uint64 `json:"start_ticks"`
	Invocation       string `json:"invocation"`
	ListenerInode    uint64 `json:"listener_inode"`
	NetworkNamespace uint64 `json:"network_namespace"`
	MountNamespace   uint64 `json:"mount_namespace"`
	ExecutableDevice uint64 `json:"executable_device"`
	ExecutableInode  uint64 `json:"executable_inode"`
	HostRootDevice   uint64 `json:"host_root_device"`
	HostRootInode    uint64 `json:"host_root_inode"`
}

const KubeletPodResourcesSuffix = "/pod-resources/kubelet.sock"

func (k SourceKubelet) Validate() error {
	// Linux sockaddr_un has 108 bytes including its terminating NUL. The observed
	// pathname, not a default or a caller-supplied target, selects the root.
	if len(k.Root) < 2 || len(k.Root)+len(KubeletPodResourcesSuffix) > 107 || k.Root[0] != '/' || path.Clean(k.Root) != k.Root ||
		!sessionMachineID.MatchString(k.Invocation) || k.Invocation == strings.Repeat("0", 32) || k.PID < 2 || k.PID > 2147483647 {
		return ErrPreparationConfig
	}
	for _, b := range []byte(k.Root) {
		if b < 32 || b > 126 {
			return ErrPreparationConfig
		}
	}
	for _, n := range []uint64{k.StartTicks, k.ListenerInode, k.NetworkNamespace, k.MountNamespace, k.ExecutableDevice, k.ExecutableInode, k.HostRootDevice, k.HostRootInode} {
		if n == 0 || n > 1<<53-1 {
			return ErrPreparationConfig
		}
	}
	return nil
}
