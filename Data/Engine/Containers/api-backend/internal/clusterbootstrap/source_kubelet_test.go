package clusterbootstrap

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSourceKubeletPublicReceiptContract(t *testing.T) {
	raw, identity := sourceNetworkFixture(t)
	network, err := ParseSourceNetwork(raw, identity)
	if err != nil {
		t.Fatal(err)
	}
	nonce, job, pod := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	for _, mode := range []string{"custom root", "longest root", "empty", "relative", "traversal", "slash", "long", "control", "zero pid", "large pid", "zero start", "large inode", "bad invocation", "zero namespace", "namespace mismatch", "missing executable digest", "wrong executable digest", "launcher digest"} {
		t.Run(mode, func(t *testing.T) {
			value := network
			switch mode {
			case "custom root":
				value.Kubelet.Root = "/srv/custom root"
			case "longest root":
				value.Kubelet.Root = "/" + strings.Repeat("x", 106-len(KubeletPodResourcesSuffix))
			case "missing executable digest":
				value.Kubelet.ExecutableSHA256 = ""
			case "wrong executable digest":
				value.Kubelet.ExecutableSHA256 = strings.Repeat("a", 64)
			case "launcher digest":
				value.Kubelet.ExecutableSHA256 = K3sPins().Binary.SHA256
			case "empty":
				value.Kubelet = SourceKubelet{}
			case "relative":
				value.Kubelet.Root = "relative"
			case "traversal":
				value.Kubelet.Root = "/var/../kubelet"
			case "slash":
				value.Kubelet.Root = "/"
			case "long":
				value.Kubelet.Root = "/" + strings.Repeat("x", 107-len(KubeletPodResourcesSuffix))
			case "control":
				value.Kubelet.Root = "/var/\nkubelet"
			case "zero pid":
				value.Kubelet.PID = 0
			case "large pid":
				value.Kubelet.PID = 1 << 31
			case "zero start":
				value.Kubelet.StartTicks = 0
			case "large inode":
				value.Kubelet.ListenerInode = 1 << 53
			case "bad invocation":
				value.Kubelet.Invocation = strings.Repeat("A", 32)
			case "zero namespace":
				value.Kubelet.MountNamespace = 0
			case "namespace mismatch":
				value.Kubelet.NetworkNamespace++
			}
			action, _ := json.Marshal(map[string]any{"ok": true, "verb": "InspectSourceNetwork", "result": map[string]any{"source_network": value}})
			receipt, err := NewSourceNetworkReceipt(action, nonce, job, pod)
			if mode == "custom root" || mode == "longest root" {
				got, parseErr := ParseSourceNetworkReceipt(receipt, nonce, job, pod)
				if err != nil || parseErr != nil || got != value || len(receipt) > SourceNetworkReceiptLimit {
					t.Fatal("public projection", err, parseErr)
				}
			} else if err != ErrPreparationConfig || receipt != nil {
				t.Fatal("invalid kubelet proof published")
			}
		})
	}
	action, _ := json.Marshal(map[string]any{"ok": true, "verb": "InspectSourceNetwork", "result": map[string]any{"source_network": network}})
	receipt, err := NewSourceNetworkReceipt(action, nonce, job, pod)
	if err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string][]byte{
		"private":   bytes.Replace(receipt, []byte(`"root":`), []byte(`"private":"not allowed","root":`), 1),
		"alias":     bytes.Replace(receipt, []byte(`"root":`), []byte(`"Root":`), 1),
		"duplicate": bytes.Replace(receipt, []byte(`"pid":42`), []byte(`"pid":42,"pid":42`), 1),
		"missing":   bytes.Replace(receipt, []byte(`"pid":42,`), nil, 1),
		"null":      bytes.Replace(receipt, []byte(`"pid":42`), []byte(`"pid":null`), 1),
		"legacy":    bytes.Replace(receipt, []byte(`"version":6`), []byte(`"version":4`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := ParseSourceNetworkReceipt(bad, nonce, job, pod); err != ErrPreparationConfig || got != (SourceNetwork{}) {
				t.Fatal("unsafe receipt")
			}
		})
	}
}

func TestSourceCSISocketStrictProjection(t *testing.T) {
	raw, identity := sourceNetworkFixture(t)
	network, err := ParseSourceNetwork(raw, identity)
	if err != nil {
		t.Fatal(err)
	}
	nonce, job, pod := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	action, _ := json.Marshal(map[string]any{"ok": true, "verb": "InspectSourceNetwork", "result": map[string]any{"source_network": network}})
	receipt, err := NewSourceNetworkReceipt(action, nonce, job, pod)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"directory_device", "directory_inode", "directory_mount", "socket_device", "socket_inode", "socket_mount", "path_sha256"} {
		for _, mode := range []string{"missing", "null", "zero", "negative", "too large", "wrong type", "alias", "duplicate", "private"} {
			t.Run(field+"/"+mode, func(t *testing.T) {
				var wire map[string]any
				if json.Unmarshal(receipt, &wire) != nil {
					t.Fatal("fixture")
				}
				socket := wire["source_network"].(map[string]any)["kubelet"].(map[string]any)["csi_socket"].(map[string]any)
				switch mode {
				case "missing":
					delete(socket, field)
				case "null":
					socket[field] = nil
				case "zero":
					if field == "path_sha256" {
						socket[field] = strings.Repeat("0", 64)
					} else {
						socket[field] = 0
					}
				case "negative":
					socket[field] = -1
				case "too large":
					socket[field] = uint64(1 << 53)
				case "wrong type":
					socket[field] = true
				case "alias":
					socket[strings.ToUpper(field)] = socket[field]
					delete(socket, field)
				case "private":
					socket["private"] = "never publish"
				}
				bad, _ := json.Marshal(wire)
				if mode == "duplicate" {
					key := []byte(`"` + field + `":`)
					bad = bytes.Replace(bad, key, []byte(`"`+field+`":null,"`+field+`":`), 1)
				}
				if bytes.Equal(bad, receipt) {
					t.Fatal("mutation absent")
				}
				if got, err := ParseSourceNetworkReceipt(bad, nonce, job, pod); err != ErrPreparationConfig || got != (SourceNetwork{}) {
					t.Fatal("unsafe filesystem proof accepted")
				}
			})
		}
	}
	// Maximum representable identifiers and both longest text paths still fit
	// unchanged termination-receipt bound, including the embedded VIP projection.
	k := &network.Kubelet
	k.Root = "/" + strings.Repeat("x", 106-len(KubeletPodResourcesSuffix))
	k.PID = 2147483647
	k.CSISocket.Listener.UserID = ^uint32(0)
	for _, n := range []*uint64{&k.StartTicks, &k.ListenerInode, &k.NetworkNamespace, &k.MountNamespace, &k.ExecutableDevice, &k.ExecutableInode, &k.HostRootDevice, &k.HostRootInode, &k.CSISocket.DirectoryDevice, &k.CSISocket.DirectoryInode, &k.CSISocket.DirectoryMount, &k.CSISocket.SocketDevice, &k.CSISocket.SocketInode, &k.CSISocket.SocketMount} {
		*n = 1<<53 - 1
	}
	network.ManagementLink.NetworkNamespace = k.NetworkNamespace
	network.Hostname = strings.Repeat("a", 63)
	network.ManagementLink.Interface = strings.Repeat("e", 15)
	action, _ = json.Marshal(map[string]any{"ok": true, "verb": "InspectSourceNetwork", "result": map[string]any{"source_network": network}})
	if _, err = NewSourceNetworkReceipt(action, nonce, job, pod); err != nil {
		t.Fatal("maximum source proof", err)
	}
	vip := VIPAddress{Address: "192.168.90.250", Present: true, Interface: network.ManagementLink.Interface, Index: network.ManagementLink.Index, MAC: network.ManagementLink.MAC, NetworkNamespace: k.NetworkNamespace}
	value := SourceVIPNetwork{Network: network, VIP: vip}
	action, _ = json.Marshal(map[string]any{"ok": true, "verb": "InspectVIPNetwork", "result": map[string]any{"source_vip_network": value}})
	if _, err = NewSourceVIPReceipt(action, nonce, job, pod, vip.Address); err != nil {
		t.Fatal("maximum VIP proof", err)
	}
}

func TestSourceCSIListenerStrictReceipt(t *testing.T) {
	raw, identity := sourceNetworkFixture(t)
	network, err := ParseSourceNetwork(raw, identity)
	if err != nil {
		t.Fatal(err)
	}
	nonce, job, pod := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	action, _ := json.Marshal(map[string]any{"ok": true, "verb": "InspectSourceNetwork", "result": map[string]any{"source_network": network}})
	receipt, err := NewSourceNetworkReceipt(action, nonce, job, pod)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"pod_uid", "container_id", "identity_sha256", "user_id"} {
		for _, mode := range []string{"missing", "null", "alias", "private", "duplicate"} {
			t.Run(field+"/"+mode, func(t *testing.T) {
				var wire map[string]any
				_ = json.Unmarshal(receipt, &wire)
				listener := wire["source_network"].(map[string]any)["kubelet"].(map[string]any)["csi_socket"].(map[string]any)["listener"].(map[string]any)
				switch mode {
				case "missing":
					delete(listener, field)
				case "null":
					listener[field] = nil
				case "alias":
					listener[strings.ToUpper(field)] = listener[field]
					delete(listener, field)
				case "private":
					listener["private"] = "hidden"
				}
				bad, _ := json.Marshal(wire)
				if mode == "duplicate" {
					key := []byte(`"` + field + `":`)
					bad = bytes.Replace(bad, key, []byte(`"`+field+`":null,"`+field+`":`), 1)
				}
				if _, err := ParseSourceNetworkReceipt(bad, nonce, job, pod); err == nil {
					t.Fatal("invalid listener receipt accepted")
				}
			})
		}
	}
}
