package clusterbootstrap

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSourceReceiptRejectsPrivateAmbiguousAndReplayedResults(t *testing.T) {
	raw, identity := sourceNetworkFixture(t)
	network, err := ParseSourceNetwork(raw, identity)
	if err != nil {
		t.Fatal(err)
	}
	nonce, job, pod := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	action, _ := json.Marshal(map[string]any{"ok": true, "verb": "InspectSourceNetwork", "result": map[string]any{"source_network": network}})
	receipt, err := NewSourceNetworkReceipt(action, nonce, job, pod)
	if err != nil || len(receipt) >= SourceNetworkReceiptLimit {
		t.Fatalf("receipt: %v", err)
	}
	got, err := ParseSourceNetworkReceipt(receipt, nonce, job, pod)
	if err != nil || got != network {
		t.Fatalf("projection: %v", err)
	}
	for _, ids := range [][3]string{{pod, job, pod}, {nonce, pod, pod}, {nonce, job, job}, {"", job, pod}, {strings.Repeat("0", 8) + "-0000-0000-0000-000000000000", job, pod}} {
		if _, err := ParseSourceNetworkReceipt(receipt, ids[0], ids[1], ids[2]); err == nil {
			t.Fatal("rebound receipt accepted")
		}
	}
	for name, bad := range map[string][]byte{
		"unknown":        bytes.Replace(receipt, []byte(`"version":1`), []byte(`"version":1,"private":"do-not-publish"`), 1),
		"nested unknown": bytes.Replace(receipt, []byte(`"hostname":`), []byte(`"private":"do-not-publish","hostname":`), 1),
		"duplicate":      bytes.Replace(receipt, []byte(`"version":1`), []byte(`"version":2,"version":1`), 1),
		"case alias":     bytes.Replace(receipt, []byte(`"pod_uid":`), []byte(`"POD_UID":`), 1),
		"nested alias":   bytes.Replace(receipt, []byte(`"hostname":`), []byte(`"Hostname":`), 1),
		"missing":        bytes.Replace(receipt, []byte(`"version":1,`), nil, 1),
		"null":           bytes.Replace(receipt, []byte(`"version":1`), []byte(`"version":null`), 1),
		"version":        bytes.Replace(receipt, []byte(`"version":1`), []byte(`"version":2`), 1),
		"bad network":    bytes.Replace(receipt, []byte("10.42.0.0/16"), []byte("10.42.0.1/16"), 1),
		"truncated":      receipt[:len(receipt)-1], "trailing": append(append([]byte(nil), receipt...), []byte(`{}`)...),
		"oversize": append(append([]byte(nil), receipt...), bytes.Repeat([]byte(" "), SourceNetworkReceiptLimit)...),
		"encoding": append(append([]byte(nil), receipt...), 0xff),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseSourceNetworkReceipt(bad, nonce, job, pod)
			if err != ErrPreparationConfig || got != (SourceNetwork{}) {
				t.Fatal("invalid result accepted or disclosed")
			}
		})
	}
	for _, bad := range [][]byte{
		bytes.Replace(action, []byte(`"ok":true`), []byte(`"ok":false`), 1),
		bytes.Replace(action, []byte(`"ok":true`), []byte(`"ok":true,"output":"private"`), 1),
		bytes.Replace(action, []byte(`"source_network":`), []byte(`"private":"sensitive","source_network":`), 1),
		bytes.Replace(action, []byte("InspectSourceNetwork"), []byte("InspectHealth"), 1),
	} {
		if out, err := NewSourceNetworkReceipt(bad, nonce, job, pod); err != ErrPreparationConfig || out != nil {
			t.Fatal("private manager response copied")
		}
	}
}
