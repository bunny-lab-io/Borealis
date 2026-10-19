package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const sshBrokerTestSecret = "0123456789abcdef0123456789abcdef"

func sshBrokerFixture(t *testing.T) (clusterSSHSourceBrokerRequest, clusterSSHPreparationAuthority, clusterSSHPreparationSnapshot) {
	t.Helper()
	cohort, source, lease, baseline := sshPreparationFixture(t)
	expected, err := buildClusterSSHPreparationExpected(cohort, source, lease, baseline, "v1.36.3+k3s1", "10.42.0.0/16", "10.43.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	r := clusterSSHSourceBrokerRequest{Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 35,
		Lease: lease, Baseline: baseline, Binding: cohort.Targets[0].Binding, Generation: "private-generation", Ciphertext: aegisEnvelopePrefix + "private-credential"}
	a := clusterSSHPreparationAuthority{Cohort: cohort, Source: source, Lease: lease, Baseline: baseline, K3sVersion: "v1.36.3+k3s1"}
	s := clusterSSHPreparationSnapshot{Expected: expected, Settings: sshPreparationRuntimeFixture(), Observation: strings.Repeat("a", 64), Sources: []clusterbootstrap.SourceNetwork{sshSourceNetworkFixture(source.Members[0], a.K3sVersion)}}
	s.Storage = sshStorageSnapshotFixture(t, a)
	s.DesiredWorkerSites = []int64{}
	s.Storage.Requirements.SiteWorkers = []clusterSSHSiteWorkerResource{sshSiteWorkerResourceValue(source.Members[0].Name)}
	return r, a, s
}

func sshBrokerClient(t *testing.T, peers ...string) *clusterSSHSourceBrokerClient {
	t.Helper()
	request, _ := clusterSSHSourceBrokerCipher(sshBrokerTestSecret, "request")
	response, _ := clusterSSHSourceBrokerCipher(sshBrokerTestSecret, "response")
	return &clusterSSHSourceBrokerClient{requestCipher: request, responseCipher: response, httpClient: &http.Client{Timeout: time.Second},
		peers: func(context.Context) ([]string, error) { return peers, nil }}
}

func TestClusterSSHSourceBrokerEncryptedReadAndRetainedObservation(t *testing.T) {
	for _, mode := range []string{"Snapshotter resources missing", "Snapshotter role swapped", "Snapshotter resources changed", "Snapshotter replicas changed", "Snapshotter observed changed", "Resizer resources missing", "Resizer role swapped", "Resizer resources changed", "Resizer replicas changed", "Resizer observed changed", "Provisioner resources missing", "Provisioner role swapped", "Provisioner resources changed", "Provisioner replicas changed", "Provisioner observed changed", "Attacher resources missing", "Attacher role swapped", "Attacher resources changed", "Attacher replicas changed", "Attacher observed changed", "driver resources missing", "driver role swapped", "driver resources changed", "driver replicas changed", "driver observed changed", "driver wait missing", "driver wait role swapped", "driver wait changed", "fresh repeated", "UI resources missing", "UI resources changed", "UI replicas changed", "UI observed changed", "UI role swapped", "prepull resources missing", "prepull resources changed", "prepull role swapped", "manager resources missing", "manager resources changed", "manager role swapped", "kube-vip resources missing", "kube-vip resources changed", "kube-vip role swapped", "cainjector resources missing", "cainjector resources changed", "cainjector replicas changed", "cainjector role swapped", "webhook resources missing", "webhook resources changed", "webhook replicas changed", "webhook role swapped", "cert-manager resources missing", "cert-manager resources changed", "cert-manager replicas changed", "snapshot resources missing", "snapshot resources changed", "snapshot replicas changed", "upgrade resources missing", "upgrade resources changed", "upgrade replicas changed", "operator resources missing", "operator resources changed", "operator replicas changed", "census missing", "census changed", "worker policy changed", "workers missing", "workers changed", "worker identity changed", "worker node invalid", "postgres resources missing", "postgres resources changed", "applications missing", "applications changed", "cert-manager runtime changed", "cert-manager runtime missing", "snapshot runtime changed", "snapshot runtime missing", "kubelet missing", "kubelet CSI missing", "kubelet CSI inode changed", "kubelet CSI mount changed", "kubelet CSI ancestry changed", "CSI peer process changed", "CSI peer and Pod changed", "kubelet executable digest", "kubelet root mismatch", "kubelet process changed", "kubelet listener changed", "kubelet root changed coherently", "kube-vip interface mismatch", "bootstrap image changed", "postgres image changed", "storage receipt changed", "storage requirements changed", "volume images changed", "driver runtime changed", "upgrade runtime changed", "CNPG runtime changed", "CNPG runtime missing", "driver init changed", "provisioner runtime changed", "resizer runtime changed", "snapshotter runtime changed", "CSI socket path changed", "attacher runtime changed", "UI runtime changed", "replica images changed", "storage missing", "storage invalid", "secret changed", "link changed", "missing sources", "extra source", "wrong source Node", "wrong source address", "wrong source ranges", "target changed", "settings invalid", "source changed", "locked before", "locked after", "wrong authority lease"} {
		t.Run(mode, func(t *testing.T) {
			r, current, snapshot := sshBrokerFixture(t)
			b := newClusterSSHSourceBroker(nil, nil, r.Lease.ControllerHolder, sshBrokerTestSecret)
			var calls atomic.Int64
			b.read = func(_ context.Context, got clusterSSHSourceBrokerRequest) (clusterSSHPreparationSnapshot, error) {
				if got.Lease != r.Lease || got.Binding != r.Binding || got.Ciphertext != r.Ciphertext {
					t.Error("request binding lost")
				}
				n := calls.Add(1)
				value := snapshot
				value.Sources = slices.Clone(snapshot.Sources)
				value.Storage.Requirements.Applications = slices.Clone(snapshot.Storage.Requirements.Applications)
				value.Storage.Requirements.SiteWorkers = slices.Clone(snapshot.Storage.Requirements.SiteWorkers)
				value.Storage.Requirements.Volumes = slices.Clone(snapshot.Storage.Requirements.Volumes)
				value.Storage.Requirements.ReplicaImages = slices.Clone(snapshot.Storage.Requirements.ReplicaImages)
				value.Storage.Requirements.LonghornPlugin = slices.Clone(snapshot.Storage.Requirements.LonghornPlugin)
				switch mode {
				case "driver resources missing":
					value.Storage.Requirements.LonghornDriverResources = clusterSSHLonghornDriverResources{}
				case "driver role swapped":
					value.Storage.Requirements.LonghornDriverResources.Main.Name = "longhorn-manager"
				case "driver resources changed":
					if n > 1 {
						value.Storage.Requirements.LonghornDriverResources.Main.CPULimitMilli++
					}
				case "driver replicas changed":
					if n > 1 {
						value.Storage.Requirements.LonghornDriverResources.Replicas++
					}
				case "driver observed changed":
					if n > 1 {
						value.Storage.Requirements.LonghornDriverResources.Observed++
					}
				case "driver wait missing":
					value.Storage.Requirements.LonghornDriverResources.Wait = clusterSSHApplicationResource{}
				case "driver wait role swapped":
					value.Storage.Requirements.LonghornDriverResources.Wait.Name = "longhorn-driver-deployer"
				case "driver wait changed":
					if n > 1 {
						value.Storage.Requirements.LonghornDriverResources.Wait.CPULimitMilli++
					}
				case "Snapshotter resources missing":
					value.Storage.Requirements.LonghornSnapshotterResources = clusterSSHLonghornSnapshotterResources{}
				case "Snapshotter role swapped":
					value.Storage.Requirements.LonghornSnapshotterResources.Envelope.Name = "csi-resizer"
				case "Snapshotter resources changed":
					if n > 1 {
						value.Storage.Requirements.LonghornSnapshotterResources.Envelope.CPULimitMilli++
					}
				case "Snapshotter replicas changed":
					if n > 1 {
						value.Storage.Requirements.LonghornSnapshotterResources.Replicas++
					}
				case "Snapshotter observed changed":
					if n > 1 {
						value.Storage.Requirements.LonghornSnapshotterResources.Observed++
					}
				case "Resizer resources missing":
					value.Storage.Requirements.LonghornResizerResources = clusterSSHLonghornResizerResources{}
				case "Resizer role swapped":
					value.Storage.Requirements.LonghornResizerResources.Envelope.Name = "csi-provisioner"
				case "Resizer resources changed":
					if n > 1 {
						value.Storage.Requirements.LonghornResizerResources.Envelope.CPULimitMilli++
					}
				case "Resizer replicas changed":
					if n > 1 {
						value.Storage.Requirements.LonghornResizerResources.Replicas++
					}
				case "Resizer observed changed":
					if n > 1 {
						value.Storage.Requirements.LonghornResizerResources.Observed++
					}
				case "Provisioner resources missing":
					value.Storage.Requirements.LonghornProvisionerResources = clusterSSHLonghornProvisionerResources{}
				case "Provisioner role swapped":
					value.Storage.Requirements.LonghornProvisionerResources.Envelope.Name = "csi-attacher"
				case "Provisioner resources changed":
					if n > 1 {
						value.Storage.Requirements.LonghornProvisionerResources.Envelope.CPULimitMilli++
					}
				case "Provisioner replicas changed":
					if n > 1 {
						value.Storage.Requirements.LonghornProvisionerResources.Replicas++
					}
				case "Provisioner observed changed":
					if n > 1 {
						value.Storage.Requirements.LonghornProvisionerResources.Observed++
					}
				case "Attacher resources missing":
					value.Storage.Requirements.LonghornAttacherResources = clusterSSHLonghornAttacherResources{}
				case "Attacher role swapped":
					value.Storage.Requirements.LonghornAttacherResources.Envelope.Name = "longhorn-ui"
				case "Attacher resources changed":
					if n > 1 {
						value.Storage.Requirements.LonghornAttacherResources.Envelope.CPULimitMilli++
					}
				case "Attacher replicas changed":
					if n > 1 {
						value.Storage.Requirements.LonghornAttacherResources.Replicas++
					}
				case "Attacher observed changed":
					if n > 1 {
						value.Storage.Requirements.LonghornAttacherResources.Observed++
					}
				case "UI resources missing":
					value.Storage.Requirements.LonghornUIResources = clusterSSHLonghornUIResources{}
				case "UI role swapped":
					value.Storage.Requirements.LonghornUIResources.Envelope.Name = "longhorn-manager"
				case "UI resources changed":
					if n > 1 {
						value.Storage.Requirements.LonghornUIResources.Envelope.CPULimitMilli++
					}
				case "UI replicas changed":
					if n > 1 {
						value.Storage.Requirements.LonghornUIResources.Replicas++
					}
				case "UI observed changed":
					if n > 1 {
						value.Storage.Requirements.LonghornUIResources.Observed++
					}
				case "manager resources missing":
					value.Storage.Requirements.LonghornManagerResources.Manager = clusterSSHApplicationResource{}
				case "manager resources changed":
					if n > 1 {
						value.Storage.Requirements.LonghornManagerResources.Manager.CPULimitMilli++
					}
				case "manager role swapped":
					value.Storage.Requirements.LonghornManagerResources.Manager.Name = "postgres"
				case "prepull resources missing":
					value.Storage.Requirements.LonghornManagerResources.PrePull = clusterSSHApplicationResource{}
				case "prepull resources changed":
					if n > 1 {
						value.Storage.Requirements.LonghornManagerResources.PrePull.CPULimitMilli++
					}
				case "prepull role swapped":
					value.Storage.Requirements.LonghornManagerResources.PrePull.Name = "postgres"
				case "kube-vip resources missing":
					value.Storage.Requirements.KubeVIP.Resources = clusterSSHApplicationResource{}
				case "kube-vip resources changed":
					if n > 1 {
						value.Storage.Requirements.KubeVIP.Resources.CPULimitMilli++
					}
				case "kube-vip role swapped":
					value.Storage.Requirements.KubeVIP.Resources.Name = "postgres"
				case "cainjector resources missing":
					value.Storage.Requirements.CertManagerCainjectorResources = clusterSSHCertManagerResources{}
				case "cainjector resources changed":
					if n > 1 {
						value.Storage.Requirements.CertManagerCainjectorResources.Envelope.CPULimitMilli++
					}
				case "cainjector replicas changed":
					if n > 1 {
						value.Storage.Requirements.CertManagerCainjectorResources.Replicas = 3
					}
				case "cainjector role swapped":
					value.Storage.Requirements.CertManagerCainjectorResources = value.Storage.Requirements.CertManagerResources
				case "webhook resources missing":
					value.Storage.Requirements.CertManagerWebhookResources = clusterSSHCertManagerResources{}
				case "webhook resources changed":
					if n > 1 {
						value.Storage.Requirements.CertManagerWebhookResources.Envelope.CPULimitMilli++
					}
				case "webhook replicas changed":
					if n > 1 {
						value.Storage.Requirements.CertManagerWebhookResources.Replicas = 3
					}
				case "webhook role swapped":
					value.Storage.Requirements.CertManagerWebhookResources = value.Storage.Requirements.CertManagerResources
				case "cert-manager resources missing":
					value.Storage.Requirements.CertManagerResources = clusterSSHCertManagerResources{}
				case "cert-manager resources changed":
					if n > 1 {
						value.Storage.Requirements.CertManagerResources.Envelope.CPULimitMilli++
					}
				case "cert-manager replicas changed":
					if n > 1 {
						value.Storage.Requirements.CertManagerResources.Replicas = 3
					}
				case "snapshot resources missing":
					value.Storage.Requirements.SnapshotResources = clusterSSHSnapshotResources{}
				case "snapshot resources changed":
					if n > 1 {
						value.Storage.Requirements.SnapshotResources.Envelope.CPULimitMilli++
					}
				case "snapshot replicas changed":
					if n > 1 {
						value.Storage.Requirements.SnapshotResources.Replicas = 3
					}
				case "upgrade resources missing":
					value.Storage.Requirements.SystemUpgradeResources = clusterSSHSystemUpgradeResources{}
				case "upgrade resources changed":
					if n > 1 {
						value.Storage.Requirements.SystemUpgradeResources.Envelope.CPULimitMilli++
					}
				case "upgrade replicas changed":
					if n > 1 {
						value.Storage.Requirements.SystemUpgradeResources.Replicas = 2
					}
				case "operator resources missing":
					value.Storage.Requirements.CNPGOperatorResources = clusterSSHCNPGOperatorResources{}
				case "operator resources changed":
					if n > 1 {
						value.Storage.Requirements.CNPGOperatorResources.Envelope.CPULimitMilli++
					}
				case "operator replicas changed":
					if n > 1 {
						value.Storage.Requirements.CNPGOperatorResources.Replicas = 2
					}
				case "census missing":
					value.DesiredWorkerSites = nil
				case "census changed":
					if n > 1 {
						value.DesiredWorkerSites = []int64{2}
					}
				case "worker policy changed":
					if n > 1 {
						value.Storage.Requirements.WorkerPolicy.Profile = "large"
					}
				case "workers missing":
					value.Storage.Requirements.SiteWorkers = nil
				case "workers changed":
					if n > 1 {
						value.Storage.Requirements.SiteWorkers[0].Resources.CPULimitMilli++
					}
				case "worker identity changed":
					if n > 1 {
						value.Storage.Requirements.SiteWorkers[0].UID = newClusterUUID()
					}
				case "worker node invalid":
					value.Storage.Requirements.SiteWorkers[0].Node = "failed-member"
				case "postgres resources missing":
					value.Storage.Requirements.PostgresResources = clusterSSHApplicationResource{}
				case "postgres resources changed":
					if n > 1 {
						value.Storage.Requirements.PostgresResources.CPULimitMilli++
					}
				case "applications missing":
					value.Storage.Requirements.Applications = nil
				case "applications changed":
					if n > 1 {
						value.Storage.Requirements.Applications[0].CPULimitMilli++
					}
				case "kube-vip interface mismatch":
					value.Storage.Requirements.KubeVIP.Interface = "ens19"
				case "bootstrap image changed":
					if n > 1 {
						value.Storage.Requirements.PostgresImage.Bootstrap.Configured = value.Storage.Requirements.PostgresImage.Bootstrap.Resolved
					}
				case "postgres image changed":
					if n > 1 {
						value.Storage.Requirements.PostgresImage.Resolved = clusterSSHPostgresRepository + "@sha256:" + strings.Repeat("c", 64)
					}
				case "cert-manager runtime missing":
					value.Storage.Requirements.CertManagerImages.ControllerResolved = ""
				case "cert-manager runtime changed":
					if n > 1 {
						value.Storage.Requirements.CertManagerImages.ControllerResolved = "quay.io/jetstack/cert-manager-controller@" + sshCertManagerPin("controller").ManifestDigest
					}
				case "snapshot runtime missing":
					value.Storage.Requirements.SnapshotControllerResolved = ""
				case "snapshot runtime changed":
					if n > 1 {
						value.Storage.Requirements.SnapshotControllerResolved = clusterSSHSnapshotControllerRepository + "@" + sshSnapshotControllerPin().ManifestDigest
					}
				case "CNPG runtime missing":
					value.Storage.Requirements.CNPGOperatorResolved = ""
				case "CNPG runtime changed":
					if n > 1 {
						value.Storage.Requirements.CNPGOperatorResolved = clusterSSHCNPGRepository + "@" + sshCNPGRuntimePin().ManifestDigest
					}
				case "upgrade runtime changed":
					if n > 1 {
						value.Storage.Requirements.SystemUpgradeImages.Resolved = "docker.io/rancher/system-upgrade-controller@" + sshSystemUpgradePin("system-upgrade-controller").IndexDigest
					}
				case "driver runtime changed", "driver init changed":
					if n > 1 {
						image := "docker.io/longhornio/longhorn-manager@" + sshLonghornDriverPin("longhorn-manager").IndexDigest
						if mode == "driver runtime changed" {
							value.Storage.Requirements.LonghornDriverImages.Resolved = image
						} else {
							value.Storage.Requirements.LonghornDriverImages.InitResolved = image
						}
					}
				case "provisioner runtime changed":
					if n > 1 {
						value.Storage.Requirements.LonghornCSIImages.Provisioner.Resolved = "docker.io/longhornio/csi-provisioner@" + sshLonghornDriverPin("csi-provisioner").IndexDigest
					}
				case "resizer runtime changed":
					if n > 1 {
						value.Storage.Requirements.LonghornCSIImages.Resizer.Resolved = "docker.io/longhornio/csi-resizer@" + sshLonghornDriverPin("csi-resizer").IndexDigest
					}
				case "snapshotter runtime changed":
					if n > 1 {
						value.Storage.Requirements.LonghornCSIImages.Snapshotter.Resolved = "docker.io/longhornio/csi-snapshotter@" + sshLonghornDriverPin("csi-snapshotter").IndexDigest
					}
				case "CSI socket path changed":
					if n > 1 {
						value.Storage.Requirements.LonghornCSISocketPath = "/srv/kubelet/plugins/driver.longhorn.io"
					}
				case "attacher runtime changed":
					if n > 1 {
						value.Storage.Requirements.LonghornAttacherImage.Resolved = "docker.io/longhornio/csi-attacher@" + sshLonghornDriverPin("csi-attacher").IndexDigest
					}
				case "UI runtime changed":
					if n > 1 {
						value.Storage.Requirements.LonghornUIResolved = "docker.io/longhornio/longhorn-ui@" + sshLonghornDriverPin("longhorn-ui").IndexDigest
					}
				case "replica images changed":
					if n > 1 {
						image := "docker.io/longhornio/longhorn-engine@" + sshLonghornDriverPin("longhorn-engine").ManifestDigest
						value.Storage.Requirements.ReplicaImages[0].Image = image
						value.Storage.Requirements.ReplicaImages[0].CurrentImage = image
					}
				case "volume images changed":
					if n > 1 {
						image := "docker.io/longhornio/longhorn-engine@" + sshLonghornDriverPin("longhorn-engine").ManifestDigest
						value.Storage.Requirements.Volumes[0].EngineImage = image
						value.Storage.Requirements.Volumes[0].CurrentEngineImage = image
					}
				case "storage receipt changed":
					if n > 1 {
						value.Storage.Observation = strings.Repeat("b", 64)
					}
				case "storage requirements changed":
					if n > 1 {
						value.Storage.Requirements.Volumes[len(value.Storage.Requirements.Volumes)-1].Bytes++
					}
				case "storage missing":
					value.Storage = clusterSSHStorageSnapshot{}
				case "storage invalid":
					value.Storage.Requirements.Volumes[1].Node = "foreign"
				}
				switch mode {
				case "kubelet CSI missing":
					value.Sources[0].Kubelet.CSISocket = clusterbootstrap.SourceCSISocket{}
				case "kubelet CSI inode changed":
					if n > 1 {
						value.Sources[0].Kubelet.CSISocket.SocketInode++
					}
				case "kubelet CSI mount changed":
					if n > 1 {
						value.Sources[0].Kubelet.CSISocket.DirectoryMount++
					}
				case "CSI peer process changed", "CSI peer and Pod changed":
					if n > 1 {
						value.Sources[0].Kubelet.CSISocket.Listener.IdentitySHA256 = strings.Repeat("f", 64)
						if mode == "CSI peer and Pod changed" {
							value.Sources[0].Kubelet.CSISocket.Listener.ContainerID = strings.Repeat("f", 64)
							value.Storage.Requirements.LonghornPlugin[0].ContainerID = strings.Repeat("f", 64)
						}
					}
				case "kubelet CSI ancestry changed":
					if n > 1 {
						value.Sources[0].Kubelet.CSISocket.PathSHA256 = strings.Repeat("d", 64)
					}
				case "kubelet executable digest":
					value.Sources[0].Kubelet.ExecutableSHA256 = clusterbootstrap.K3sPins().Binary.SHA256
				case "kubelet missing":
					value.Sources[0].Kubelet = clusterbootstrap.SourceKubelet{}
				case "kubelet root mismatch":
					value.Sources[0].Kubelet.Root = "/other"
				case "kubelet process changed":
					if n > 1 {
						value.Sources[0].Kubelet.StartTicks++
					}
				case "kubelet listener changed":
					if n > 1 {
						value.Sources[0].Kubelet.ListenerInode++
					}
				case "kubelet root changed coherently":
					if n > 1 {
						value.Sources[0].Kubelet.Root = "/other"
						value.Storage.Requirements.LonghornCSISocketPath = "/other/plugins/driver.longhorn.io"
					}
				case "link changed":
					if n > 1 {
						value.Sources[0].ManagementLink.MAC = "02:00:00:00:00:ff"
					}
				case "missing sources":
					value.Sources = nil
				case "extra source":
					value.Sources = append(value.Sources, value.Sources[0])
				case "wrong source Node":
					value.Sources[0].NodeUID = newClusterUUID()
				case "wrong source address":
					value.Sources[0].ManagementLink.Address = "192.168.90.99/24"
				case "wrong source ranges":
					value.Sources[0].PodCIDR = "10.44.0.0/16"
				}
				if mode == "secret changed" && n > 1 {
					value.Observation = strings.Repeat("b", 64)
				}
				if mode == "target changed" {
					value.Expected.Target.HolderID = newClusterUUID()
				}
				if mode == "settings invalid" {
					value.Settings = map[string]string{"private": "unexpected"}
				}
				return value, nil
			}
			server := httptest.NewServer(http.HandlerFunc(b.handle))
			defer server.Close()
			follower := newClusterSSHSourceBroker(nil, nil, "different-controller", sshBrokerTestSecret)
			follower.read = func(context.Context, clusterSSHSourceBrokerRequest) (clusterSSHPreparationSnapshot, error) {
				t.Error("follower performed source work")
				return snapshot, nil
			}
			other := httptest.NewServer(http.HandlerFunc(follower.handle))
			defer other.Close()
			client := sshBrokerClient(t, other.URL, server.URL)
			reads := 0
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
				reads++
				if mode == "locked before" || (mode == "locked after" && reads > 1) {
					return clusterSSHPreparationAuthority{}, errors.New("private key error")
				}
				copy := current
				copy.Cohort.ObservedAt += int64(reads)
				if mode == "wrong authority lease" {
					copy.Lease.Holder = newClusterUUID()
				}
				if mode == "source changed" && reads > 1 {
					copy.Source.KubeSystemUID = newClusterUUID()
				}
				return copy, nil
			}
			read := client.snapshotRead(authority, r.Lease, r.Baseline, r.sealed())
			for i := 0; i < 2; i++ {
				started := time.Now()
				value, err := read(context.Background())
				expected, settings := value.Expected, value.Settings
				wantOK := mode == "fresh repeated" || ((mode == "Snapshotter resources changed" || mode == "Snapshotter replicas changed" || mode == "Snapshotter observed changed" || mode == "Resizer resources changed" || mode == "Resizer replicas changed" || mode == "Resizer observed changed" || mode == "Provisioner resources changed" || mode == "Provisioner replicas changed" || mode == "Provisioner observed changed" || mode == "Attacher resources changed" || mode == "Attacher replicas changed" || mode == "Attacher observed changed" || mode == "driver resources changed" || mode == "driver replicas changed" || mode == "driver observed changed" || mode == "driver wait changed" || mode == "UI resources changed" || mode == "UI replicas changed" || mode == "UI observed changed" || mode == "prepull resources changed" || mode == "manager resources changed" || mode == "kube-vip resources changed" || mode == "cainjector resources changed" || mode == "cainjector replicas changed" || mode == "webhook resources changed" || mode == "webhook replicas changed" || mode == "cert-manager resources changed" || mode == "cert-manager replicas changed" || mode == "snapshot resources changed" || mode == "snapshot replicas changed" || mode == "upgrade resources changed" || mode == "upgrade replicas changed" || mode == "operator resources changed" || mode == "operator replicas changed" || mode == "census changed" || mode == "worker policy changed" || mode == "workers changed" || mode == "worker identity changed" || mode == "postgres resources changed" || mode == "applications changed" || mode == "CSI peer process changed" || mode == "CSI peer and Pod changed" || mode == "kubelet CSI inode changed" || mode == "kubelet CSI mount changed" || mode == "kubelet CSI ancestry changed" || mode == "kubelet process changed" || mode == "kubelet listener changed" || mode == "kubelet root changed coherently" || mode == "secret changed" || mode == "link changed" || mode == "postgres image changed" || mode == "bootstrap image changed" || mode == "storage receipt changed" || mode == "storage requirements changed" || mode == "volume images changed" || mode == "replica images changed" || mode == "provisioner runtime changed" || mode == "resizer runtime changed" || mode == "snapshotter runtime changed" || mode == "CSI socket path changed" || mode == "attacher runtime changed" || mode == "UI runtime changed" || mode == "cert-manager runtime changed" || mode == "snapshot runtime changed" || mode == "CNPG runtime changed" || mode == "upgrade runtime changed" || mode == "driver runtime changed" || mode == "driver init changed") && i == 0)
				if wantOK {
					if err != nil || !reflect.DeepEqual(expected, snapshot.Expected) || !reflect.DeepEqual(settings, snapshot.Settings) || !slices.Equal(value.Sources, snapshot.Sources) || value.started.Before(started) || !reflect.DeepEqual(value.Storage, snapshot.Storage) {
						t.Fatal("valid broker source rejected")
					}
					value.Sources[0].ManagementLink.MAC = "02:00:00:00:00:ff"
					value.Storage.Requirements.Applications[0].CPURequestMilli = 1
					value.Storage.Requirements.SiteWorkers[0].Resources.CPURequestMilli = 1
					value.Storage.Requirements.Volumes[0].Bytes = 1
					value.Storage.Requirements.ReplicaImages[0].Image = "caller mutation"
				} else if err != clusterbootstrap.ErrPreparationConfig || settings != nil || expected.Target.TargetID != "" {
					t.Fatal("unsafe/private source returned")
				}
			}
			if (mode == "locked before" || mode == "wrong authority lease") && calls.Load() != 0 {
				t.Fatal("network preceded worker authority")
			}
			if mode == "fresh repeated" && calls.Load() != 2 {
				t.Fatal("cached source receipt reused")
			}
		})
	}
}

func TestClusterSSHSourceBrokerRejectsWireAndRequestAmbiguity(t *testing.T) {
	for _, mode := range []string{"tamper", "wrong key", "reflection", "duplicate", "case alias", "unknown", "missing", "null", "UTF8", "oversize", "expired", "future", "zero nonce", "wrong binding", "inspect lease", "wrong repository", "query", "media", "method", "encoding"} {
		t.Run(mode, func(t *testing.T) {
			r, _, snapshot := sshBrokerFixture(t)
			b := newClusterSSHSourceBroker(nil, nil, r.Lease.ControllerHolder, sshBrokerTestSecret)
			b.read = func(context.Context, clusterSSHSourceBrokerRequest) (clusterSSHPreparationSnapshot, error) {
				t.Error("invalid request reached source")
				return snapshot, nil
			}
			switch mode {
			case "expired":
				r.ExpiresAt = time.Now().Unix()
			case "future":
				r.ExpiresAt = time.Now().Unix() + 1000
			case "zero nonce":
				r.ID = "00000000-0000-0000-0000-000000000000"
			case "wrong binding":
				r.Binding.TargetID = newClusterUUID()
			case "inspect lease":
				r.Lease.Step = "inspect"
			case "wrong repository":
				r.Baseline.Repository = "other/repo"
			}
			raw, _ := json.Marshal(r)
			switch mode {
			case "duplicate":
				raw = bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
			case "case alias":
				raw = bytes.Replace(raw, []byte(`"Holder":`), []byte(`"holder":`), 1)
			case "unknown":
				raw = bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"private":"hidden"`), 1)
			case "missing":
				raw = bytes.Replace(raw, []byte(`"version":1,`), nil, 1)
			case "null":
				raw = bytes.Replace(raw, []byte(`"Holder":"`+r.Lease.Holder+`"`), []byte(`"Holder":null`), 1)
			case "UTF8":
				raw = append([]byte{0xff}, raw...)
			case "oversize":
				raw = append(raw, bytes.Repeat([]byte(" "), clusterSSHSourceBrokerLimit)...)
			}
			aead := b.requestCipher
			if mode == "wrong key" {
				aead, _ = clusterSSHSourceBrokerCipher(strings.Repeat("z", 32), "request")
			}
			if mode == "reflection" {
				aead = b.responseCipher
			}
			wire := aead.Seal(nil, nil, raw, []byte(clusterSSHSourceBrokerPath))
			if mode == "tamper" {
				wire[len(wire)-1] ^= 1
			}
			request := httptest.NewRequest("POST", clusterSSHSourceBrokerPath, bytes.NewReader(wire))
			request.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
			if mode == "query" {
				request.URL.RawQuery = "source=secret"
			}
			if mode == "media" {
				request.Header.Set("Content-Type", "application/json")
			}
			if mode == "method" {
				request.Method = "GET"
			}
			if mode == "encoding" {
				request.Header.Set("Content-Encoding", "gzip")
			}
			response := httptest.NewRecorder()
			b.handle(response, request)
			if response.Code < 400 || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "private") {
				t.Fatal("unsafe request response")
			}
		})
	}
}

func TestClusterSSHSourceBrokerReplayCapacityAndCancellation(t *testing.T) {
	r, _, snapshot := sshBrokerFixture(t)
	b := newClusterSSHSourceBroker(nil, nil, r.Lease.ControllerHolder, sshBrokerTestSecret)
	var calls atomic.Int64
	b.read = func(ctx context.Context, _ clusterSSHSourceBrokerRequest) (clusterSSHPreparationSnapshot, error) {
		calls.Add(1)
		return snapshot, ctx.Err()
	}
	server := httptest.NewServer(http.HandlerFunc(b.handle))
	defer server.Close()
	client := sshBrokerClient(t, server.URL)
	if _, err := client.fetch(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := client.fetch(context.Background(), r); err == nil || calls.Load() != 1 {
		t.Fatal("replay reached source")
	}
	b.mu.Lock()
	b.active = 2
	b.mu.Unlock()
	r.ID = newClusterUUID()
	if _, err := client.fetch(context.Background(), r); err == nil || calls.Load() != 1 {
		t.Fatal("capacity bypass")
	}
	b.mu.Lock()
	b.active = 0
	for i := 0; i < 128; i++ {
		b.seen[fmt.Sprint(i)] = r.ExpiresAt
	}
	b.mu.Unlock()
	if _, err := client.fetch(context.Background(), r); err == nil || calls.Load() != 1 {
		t.Fatal("replay cache evicted live receipt")
	}
	b.mu.Lock()
	for id := range b.seen {
		b.seen[id] = time.Now().Unix() - 1
	}
	b.mu.Unlock()
	if _, err := client.fetch(context.Background(), r); err != nil || calls.Load() != 2 {
		t.Fatal("expired replay entries not released")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.ID = newClusterUUID()
	if _, err := client.fetch(ctx, r); err == nil || calls.Load() != 2 {
		t.Fatal("canceled read dispatched")
	}
}

func TestClusterSSHSourceBrokerClientRejectsResponseAndNeverReplays(t *testing.T) {
	for _, mode := range []string{"redirect", "lost POST", "status", "plaintext", "oversize", "wrong nonce", "response duplicate", "legacy response", "wrong direction", "unknown status", "missing snapshot", "expired while reading"} {
		t.Run(mode, func(t *testing.T) {
			r, _, snapshot := sshBrokerFixture(t)
			client := sshBrokerClient(t, "http://10.42.0.1:8090", "http://10.42.0.2:8090")
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client.httpClient.Transport = bootstrapRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Path != clusterSSHSourceBrokerPath || req.Method != "POST" || req.GetBody != nil || req.Header.Get("Authorization") != "" {
					t.Error("request boundary drift")
				}
				wire, _ := io.ReadAll(req.Body)
				if bytes.Contains(wire, []byte(r.Ciphertext)) || bytes.Contains(wire, []byte(r.Generation)) {
					t.Error("plaintext secret on wire")
				}
				if mode == "lost POST" {
					return nil, errors.New("private transport")
				}
				value := clusterSSHSourceBrokerResponse{Version: 50, ID: r.ID, Status: "ok", Snapshot: &snapshot}
				if mode == "wrong nonce" {
					value.ID = newClusterUUID()
				}
				if mode == "unknown status" {
					value.Status = "replay"
				}
				if mode == "missing snapshot" {
					value.Snapshot = nil
				}
				if mode == "legacy response" {
					value.Version = 49
				}
				raw, _ := json.Marshal(value)
				if mode == "response duplicate" {
					altered := bytes.Replace(raw, []byte(`"version":50`), []byte(`"version":50,"version":50`), 1)
					if bytes.Equal(raw, altered) {
						t.Fatal("duplicate response fixture unchanged")
					}
					raw = altered
				}
				aead := client.responseCipher
				if mode == "wrong direction" {
					aead = client.requestCipher
				}
				body := aead.Seal(nil, nil, raw, []byte(clusterSSHSourceBrokerPath))
				if mode == "plaintext" {
					body = raw
				}
				if mode == "oversize" {
					body = bytes.Repeat([]byte("x"), clusterSSHSourceBrokerLimit+1)
				}
				status := 200
				if mode == "redirect" {
					status = 307
				}
				if mode == "status" {
					status = 502
				}
				if mode == "expired while reading" {
					cancel()
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {clusterSSHSourceBrokerMedia}, "Location": {"http://10.42.0.3:8090"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: req}, nil
			})
			if _, err := client.fetch(ctx, r); err != clusterbootstrap.ErrPreparationConfig || calls != 1 {
				t.Fatal("unsafe response accepted or POST replayed")
			}
		})
	}
}

func TestClusterSSHSourceBrokerConcurrentReadsReleaseOnCancellation(t *testing.T) {
	r, _, _ := sshBrokerFixture(t)
	b := newClusterSSHSourceBroker(nil, nil, r.Lease.ControllerHolder, sshBrokerTestSecret)
	started := make(chan struct{}, 2)
	stopped := make(chan struct{}, 2)
	b.read = func(ctx context.Context, _ clusterSSHSourceBrokerRequest) (clusterSSHPreparationSnapshot, error) {
		started <- struct{}{}
		<-ctx.Done()
		stopped <- struct{}{}
		return clusterSSHPreparationSnapshot{}, ctx.Err()
	}
	server := httptest.NewServer(http.HandlerFunc(b.handle))
	defer server.Close()
	client := sshBrokerClient(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		request := r
		request.ID = newClusterUUID()
		go func() { _, err := client.fetch(ctx, request); done <- err }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("source did not start")
		}
	}
	if _, err := client.fetch(ctx, r); err != clusterbootstrap.ErrPreparationConfig {
		t.Fatal("third active source read accepted")
	}
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != clusterbootstrap.ErrPreparationConfig {
				t.Fatal("canceled response accepted")
			}
		case <-time.After(time.Second):
			t.Fatal("worker did not cancel")
		}
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("controller retained canceled work")
		}
	}
}
