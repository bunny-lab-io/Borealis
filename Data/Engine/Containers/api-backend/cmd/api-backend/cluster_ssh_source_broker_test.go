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
	s := clusterSSHPreparationSnapshot{Expected: expected, Settings: sshPreparationRuntimeFixture(), Observation: strings.Repeat("a", 64)}
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
	for _, mode := range []string{"fresh repeated", "secret changed", "target changed", "settings invalid", "source changed", "locked before", "locked after", "wrong authority lease"} {
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
			read := client.preparationRead(authority, r.Lease, r.Baseline, r.sealed())
			for i := 0; i < 2; i++ {
				expected, settings, err := read(context.Background())
				wantOK := mode == "fresh repeated" || (mode == "secret changed" && i == 0)
				if wantOK {
					if err != nil || !reflect.DeepEqual(expected, snapshot.Expected) || !reflect.DeepEqual(settings, snapshot.Settings) {
						t.Fatal("valid broker source rejected")
					}
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
	for _, mode := range []string{"redirect", "lost POST", "status", "plaintext", "oversize", "wrong nonce", "response duplicate", "wrong direction", "unknown status", "missing snapshot", "expired while reading"} {
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
				value := clusterSSHSourceBrokerResponse{Version: 1, ID: r.ID, Status: "ok", Snapshot: &snapshot}
				if mode == "wrong nonce" {
					value.ID = newClusterUUID()
				}
				if mode == "unknown status" {
					value.Status = "replay"
				}
				if mode == "missing snapshot" {
					value.Snapshot = nil
				}
				raw, _ := json.Marshal(value)
				if mode == "response duplicate" {
					raw = bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
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
