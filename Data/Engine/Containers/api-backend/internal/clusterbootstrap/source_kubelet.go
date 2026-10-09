package clusterbootstrap

import (
	"path"
	"strings"
)

// SourceKubelet binds an observed PodResources listener pathname to the current
// k3s.service process and its reviewed executable backing bytes. It does not
// attest process memory, socket listener ownership, CSI health or persistence.
type SourceKubelet struct {
	CSISocket        SourceCSISocket `json:"csi_socket"`
	Root             string          `json:"root"`
	PID              uint64          `json:"pid"`
	StartTicks       uint64          `json:"start_ticks"`
	Invocation       string          `json:"invocation"`
	ListenerInode    uint64          `json:"listener_inode"`
	NetworkNamespace uint64          `json:"network_namespace"`
	MountNamespace   uint64          `json:"mount_namespace"`
	ExecutableDevice uint64          `json:"executable_device"`
	ExecutableInode  uint64          `json:"executable_inode"`
	ExecutableSHA256 string          `json:"executable_sha256"`
	HostRootDevice   uint64          `json:"host_root_device"`
	HostRootInode    uint64          `json:"host_root_inode"`
}

const KubeletPodResourcesSuffix = "/pod-resources/kubelet.sock"
const KubeletCSIDirectorySuffix = "/plugins/driver.longhorn.io"
const KubeletCSISocketSuffix = KubeletCSIDirectorySuffix + "/csi.sock"

// SourceCSISocket identifies the selected directory and socket filesystem
// objects. PathSHA256 freezes every held path component's mount/inode/type and
// ownership/mode, not directory contents or a listening process.
type SourceCSISocket struct {
	DirectoryDevice uint64 `json:"directory_device"`
	DirectoryInode  uint64 `json:"directory_inode"`
	DirectoryMount  uint64 `json:"directory_mount"`
	SocketDevice    uint64 `json:"socket_device"`
	SocketInode     uint64 `json:"socket_inode"`
	SocketMount     uint64 `json:"socket_mount"`
	PathSHA256      string `json:"path_sha256"`
}

func (s SourceCSISocket) Validate() error {
	if !digestPattern.MatchString(s.PathSHA256) || s.PathSHA256 == strings.Repeat("0", 64) {
		return ErrPreparationConfig
	}
	for _, n := range []uint64{s.DirectoryDevice, s.DirectoryInode, s.DirectoryMount, s.SocketDevice, s.SocketInode, s.SocketMount} {
		if n == 0 || n > 1<<53-1 {
			return ErrPreparationConfig
		}
	}
	return nil
}
func ValidSourceKubeletRoot(root string) bool {
	if len(root) < 2 || len(root)+len(KubeletPodResourcesSuffix) > 107 || root[0] != '/' || path.Clean(root) != root {
		return false
	}
	for _, b := range []byte(root) {
		if b < 32 || b > 126 {
			return false
		}
	}
	return true
}

func (k SourceKubelet) Validate() error {
	// Linux sockaddr_un has 108 bytes including its terminating NUL. The observed
	// pathname, not a default or a caller-supplied target, selects the root.
	if !digestPattern.MatchString(k.ExecutableSHA256) || k.ExecutableSHA256 != K3sPins().ServerExecutable.SHA256 ||
		!ValidSourceKubeletRoot(k.Root) || k.CSISocket.Validate() != nil ||
		!sessionMachineID.MatchString(k.Invocation) || k.Invocation == strings.Repeat("0", 32) || k.PID < 2 || k.PID > 2147483647 {
		return ErrPreparationConfig
	}
	for _, n := range []uint64{k.StartTicks, k.ListenerInode, k.NetworkNamespace, k.MountNamespace, k.ExecutableDevice, k.ExecutableInode, k.HostRootDevice, k.HostRootInode} {
		if n == 0 || n > 1<<53-1 {
			return ErrPreparationConfig
		}
	}
	return nil
}
