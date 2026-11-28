package clusterbootstrap

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func joinSourceRequestFixture(t *testing.T) (K3sJoinSourceRequest, time.Time) {
	t.Helper()
	expected, source, _, _ := joinMaterialFixture(t)
	_, network := sourceNetworkFixture(t)
	network.NodeUID = source.SourceUID
	network.PodCIDR, network.ServiceCIDR = expected.PodCIDR, expected.ServiceCIDR
	for _, peer := range expected.PeerAddresses {
		if peer != expected.Target.Address {
			network.ManagementLink.Address = peer + "/24"
			break
		}
	}
	now := time.Unix(1800000000, 0)
	r := K3sJoinSourceRequest{1, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", now.Unix() + 60, expected, source, network}
	if err := r.Validate(now); err != nil {
		t.Fatal(err)
	}
	return r, now
}

func TestK3sJoinSourceRequestBoundary(t *testing.T) {
	r, now := joinSourceRequestFixture(t)
	raw, _ := json.Marshal(r)
	if _, err := ParseK3sJoinSourceRequest(raw, now); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*K3sJoinSourceRequest){
		"version":                func(r *K3sJoinSourceRequest) { r.Version++ },
		"nonce":                  func(r *K3sJoinSourceRequest) { r.ID = strings.Repeat("0", 36) },
		"expired":                func(r *K3sJoinSourceRequest) { r.ExpiresAt = now.Unix() },
		"long expiry":            func(r *K3sJoinSourceRequest) { r.ExpiresAt = now.Unix() + 91 },
		"source UID":             func(r *K3sJoinSourceRequest) { r.Network.NodeUID = r.ID },
		"version mismatch":       func(r *K3sJoinSourceRequest) { r.Network.K3sVersion = "v1.36.4+k3s1" },
		"network mismatch":       func(r *K3sJoinSourceRequest) { r.Network.PodCIDR = "10.44.0.0/16" },
		"target as source":       func(r *K3sJoinSourceRequest) { r.Network.ManagementLink.Address = r.Expected.Target.Address + "/24" },
		"foreign source address": func(r *K3sJoinSourceRequest) { r.Network.ManagementLink.Address = "192.168.3.244/24" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := r
			mutate(&bad)
			if bad.Validate(now) == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
	for _, bad := range [][]byte{nil, []byte("null"), append(bytes.Clone(raw), ' '), append([]byte(`{"version":1,`), raw[1:]...), bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"unknown":true`), 1), bytes.Repeat([]byte{' '}, MaxK3sJoinSourceRequestBytes+1)} {
		if _, err := ParseK3sJoinSourceRequest(bad, now); err == nil {
			t.Fatal("noncanonical request accepted")
		}
	}
}

func TestK3sJoinSourceDeliveryAndReceipt(t *testing.T) {
	r, now := joinSourceRequestFixture(t)
	secret := bytes.Repeat([]byte{'x'}, 32)
	frame := []byte("private source frame")
	job, pod := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	wire, receipt, err := SealK3sJoinSourceDelivery(secret, r, job, pod, frame, now)
	if err != nil || bytes.Contains(wire, frame) {
		t.Fatal("seal failed or leaked plaintext", err)
	}
	other, _, err := SealK3sJoinSourceDelivery(secret, r, job, pod, frame, now)
	if err != nil || bytes.Equal(wire, other) {
		t.Fatal("random nonce not used")
	}
	got, observed, err := OpenK3sJoinSourceDelivery(secret, r, wire, now)
	if err != nil || !bytes.Equal(got, frame) || observed != receipt {
		t.Fatal("delivery lost binding", err)
	}
	clear(got)
	if !bytes.Equal(frame, []byte("private source frame")) {
		t.Fatal("returned frame aliases input")
	}
	public, _ := json.Marshal(receipt)
	if _, err := ParseK3sJoinSourceReceipt(public, r, job, pod); err != nil || bytes.Contains(public, frame) {
		t.Fatal("public receipt invalid", err)
	}
	if _, err := ParseK3sJoinSourceReceipt(public, r, pod, job); err == nil {
		t.Fatal("foreign Job accepted")
	}
	if _, err := ParseK3sJoinSourceReceipt(append(public, ' '), r, job, pod); err == nil {
		t.Fatal("noncanonical receipt accepted")
	}
	for name, mutate := range map[string]func(*K3sJoinSourceRequest, *[]byte, *[]byte, *time.Time){
		"wrong key": func(_ *K3sJoinSourceRequest, s, _ *[]byte, _ *time.Time) { (*s)[0]++ },
		"tamper":    func(_ *K3sJoinSourceRequest, _, w *[]byte, _ *time.Time) { (*w)[len(*w)-1]++ },
		"truncated": func(_ *K3sJoinSourceRequest, _, w *[]byte, _ *time.Time) { *w = (*w)[:10] },
		"oversize": func(_ *K3sJoinSourceRequest, _, w *[]byte, _ *time.Time) {
			*w = make([]byte, MaxK3sJoinSourceDeliveryBytes+1)
		},
		"expired":         func(_ *K3sJoinSourceRequest, _, _ *[]byte, n *time.Time) { *n = n.Add(time.Minute) },
		"foreign request": func(r *K3sJoinSourceRequest, _, _ *[]byte, _ *time.Time) { r.ID = job },
		"foreign claim":   func(r *K3sJoinSourceRequest, _, _ *[]byte, _ *time.Time) { r.Expected.Target.Generation++ },
	} {
		t.Run(name, func(t *testing.T) {
			request, key, body, clock := r, bytes.Clone(secret), bytes.Clone(wire), now
			mutate(&request, &key, &body, &clock)
			if got, _, err := OpenK3sJoinSourceDelivery(key, request, body, clock); err == nil || got != nil {
				t.Fatal("invalid delivery exposed frame")
			}
		})
	}
	for _, input := range [][]byte{nil, make([]byte, MaxK3sJoinTokenBytes+MaxK3sJoinCABytes+1025)} {
		if _, _, err := SealK3sJoinSourceDelivery(secret, r, job, pod, input, now); err == nil {
			t.Fatal("unbounded frame accepted")
		}
	}
}
