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
	for _, mode := range []string{"custom root", "longest root", "empty", "relative", "traversal", "slash", "long", "control", "zero pid", "large pid", "zero start", "large inode", "bad invocation", "zero namespace", "namespace mismatch"} {
		t.Run(mode, func(t *testing.T) {
			value := network
			switch mode {
			case "custom root":
				value.Kubelet.Root = "/srv/custom root"
			case "longest root":
				value.Kubelet.Root = "/" + strings.Repeat("x", 106-len(KubeletPodResourcesSuffix))
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
		"legacy":    bytes.Replace(receipt, []byte(`"version":3`), []byte(`"version":2`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := ParseSourceNetworkReceipt(bad, nonce, job, pod); err != ErrPreparationConfig || got != (SourceNetwork{}) {
				t.Fatal("unsafe receipt")
			}
		})
	}
}
