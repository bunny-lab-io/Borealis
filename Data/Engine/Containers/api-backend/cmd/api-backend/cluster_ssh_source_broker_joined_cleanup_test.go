package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterSSHSourceBrokerJoinedStopsPeerAfterFailedClose(t *testing.T) {
	request, _, _ := sshBrokerFixture(t)
	request.Joined = true
	client := sshBrokerClient(t, "http://10.42.0.1:8090", "http://10.42.0.2:8090")
	calls := 0
	client.httpClient.Transport = bootstrapRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		wire, err := sealClusterSSHSourceBroker(client.responseCipher, clusterSSHSourceBrokerResponse{Version: 54, ID: request.ID, Joined: true, Status: "not_owner"})
		if err != nil {
			return nil, err
		}
		body := &cohortMembershipBody{ReadCloser: io.NopCloser(bytes.NewReader(wire)), close: func() error { return errors.New("synthetic cleanup failure") }}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {clusterSSHSourceBrokerMedia}}, Body: body, Request: r}, nil
	})
	if _, err := client.exchange(context.Background(), request); err == nil || calls != 1 {
		t.Fatal("failed joined cleanup advanced peer discovery", err, calls)
	}
}

func TestClusterSSHSourceBrokerJoinedCleanup(t *testing.T) {
	for _, side := range []string{"API exchange", "controller native"} {
		for _, mode := range []string{"success", "failed close", "blocked close cancellation", "child expiry"} {
			t.Run(side+"/"+mode, func(t *testing.T) {
				a, _, _ := sshPrejoinSourceFixture(t, false)
				uids := map[string]string{}
				for _, target := range a.Cohort.Targets {
					uids[target.Binding.TargetID] = newClusterUUID()
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				var active, closed, authorityCalls atomic.Int64
				var fired atomic.Bool
				reached, release := make(chan struct{}), make(chan struct{})
				var unblock sync.Once
				defer unblock.Do(func() { close(release) })
				wrap := func(request *http.Request, body io.ReadCloser) io.ReadCloser {
					active.Add(1)
					return &cohortMembershipBody{ReadCloser: body, close: func() error {
						defer active.Add(-1)
						defer closed.Add(1)
						if !fired.CompareAndSwap(false, true) {
							return nil
						}
						switch mode {
						case "failed close":
							return errors.New("private synthetic close failure")
						case "blocked close cancellation":
							close(reached)
							<-release
						case "child expiry":
							<-request.Context().Done()
						}
						return nil
					}}
				}
				read := func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
					if active.Load() != 0 {
						t.Error("authority read preceded owned response cleanup")
					}
					authorityCalls.Add(1)
					return a, ctx.Err()
				}
				var invoke func(context.Context) (clusterSSHJoinedTargets, error)
				if side == "API exchange" {
					client := sshBrokerClient(t, "http://10.42.0.1:8090")
					client.httpClient.Timeout = time.Second
					if mode == "child expiry" {
						client.httpClient.Timeout = 50 * time.Millisecond
					}
					client.httpClient.Transport = bootstrapRoundTripper(func(request *http.Request) (*http.Response, error) {
						wire, err := io.ReadAll(request.Body)
						if err != nil {
							return nil, err
						}
						var input clusterSSHSourceBrokerRequest
						if openClusterSSHSourceBroker(client.requestCipher, wire, &input) != nil || !input.Joined {
							t.Error("wrong joined request")
						}
						proof, err := readClusterSSHJoinedTargets(request.Context(), func(context.Context) (clusterSSHPreparationAuthority, error) { return a, nil }, sshPrejoinCompletionNodes(a, uids))
						if err != nil {
							return nil, err
						}
						wire, err = sealClusterSSHSourceBroker(client.responseCipher, clusterSSHSourceBrokerResponse{Version: 54, ID: input.ID, Joined: true, Status: "joined_nodes_ok", Targets: &proof})
						if err != nil {
							return nil, err
						}
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {clusterSSHSourceBrokerMedia}}, Body: wrap(request, io.NopCloser(bytes.NewReader(wire))), Request: request}, nil
					})
					claim := clusterSSHNetworkTargetClaim{Lease: a.Lease, Sealed: sealedClusterSSHCredentials{binding: a.Cohort.Targets[0].Binding, generation: "fixture-generation", ciphertext: aegisEnvelopePrefix + "fixture"}}
					invoke = func(ctx context.Context) (clusterSSHJoinedTargets, error) {
						return client.joinedTargetsRead(ctx, read, claim, a.Baseline)
					}
				} else {
					server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						_ = json.NewEncoder(w).Encode(sshPreparedNativeFixture(a, uids, r.URL.Path))
					}))
					defer server.Close()
					client := server.Client()
					client.Timeout = time.Second
					if mode == "child expiry" {
						client.Timeout = 50 * time.Millisecond
					}
					underlying := client.Transport
					client.Transport = bootstrapRoundTripper(func(request *http.Request) (*http.Response, error) {
						response, err := underlying.RoundTrip(request)
						if response != nil && response.Body != nil {
							response.Body = wrap(request, response.Body)
						}
						return response, err
					})
					kube := &kubernetesAPIClient{baseURL: server.URL, httpClient: client}
					invoke = func(ctx context.Context) (clusterSSHJoinedTargets, error) {
						return readClusterSSHJoinedTargetsNative(ctx, kube, read)
					}
				}
				var value clusterSSHJoinedTargets
				var err error
				if mode == "blocked close cancellation" {
					done := make(chan struct{})
					go func() { value, err = invoke(ctx); close(done) }()
					defer func() {
						cancel()
						unblock.Do(func() { close(release) })
						select {
						case <-done:
						case <-time.After(time.Second):
							t.Error("owned close survived test cleanup")
						}
					}()
					select {
					case <-reached:
					case <-done:
						t.Fatal("returned before close barrier", err)
					case <-ctx.Done():
						t.Fatal("close barrier not reached")
					}
					cancel()
					select {
					case <-done:
						t.Fatal("cancellation escaped blocked close")
					case <-time.After(20 * time.Millisecond):
					}
					unblock.Do(func() { close(release) })
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Fatal("close not joined")
					}
				} else {
					value, err = invoke(ctx)
				}
				if (err == nil) != (mode == "success") || active.Load() != 0 || closed.Load() == 0 {
					t.Fatal("joined cleanup result", err, active.Load(), closed.Load())
				}
				if mode == "child expiry" && ctx.Err() != nil {
					t.Fatal("child expiry fixture canceled outer scope")
				}
				if mode != "success" && (value.AuthoritySHA256 != "" || len(value.Nodes) != 0 || authorityCalls.Load() != 1) {
					t.Fatal("failed cleanup reached final authority or returned proof", authorityCalls.Load())
				}
				// Failure belongs to the owned reader, not its borrowed transport.
				if next, err := invoke(context.Background()); err != nil || len(next.Nodes) != len(a.Cohort.Targets) || active.Load() != 0 {
					t.Fatal("owned failure poisoned independent sibling", err)
				}
			})
		}
	}
}

func TestClusterSSHSourceBrokerJoinedStrictNative(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "namespace deleting", "namespace deletion alias", "namespace identity alias", "Node status alias"} {
		t.Run(mode, func(t *testing.T) {
			a, _, _ := sshPrejoinSourceFixture(t, mode == "replacement")
			uids := map[string]string{}
			for _, target := range a.Cohort.Targets {
				uids[target.Binding.TargetID] = newClusterUUID()
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				object := sshPreparedNativeFixture(a, uids, r.URL.Path)
				if r.URL.Path == "/api/v1/namespaces/kube-system" {
					metadata := clusterSSHStorageMap(object, "metadata")
					switch mode {
					case "namespace deleting":
						metadata["deletionTimestamp"] = "2026-11-09T00:00:00Z"
					case "namespace deletion alias":
						metadata["DeletionTimestamp"] = "2026-11-09T00:00:00Z"
					case "namespace identity alias":
						metadata["UID"] = metadata["uid"]
						delete(metadata, "uid")
					}
				} else if mode == "Node status alias" {
					node := anySlice(object["items"])[0].(map[string]any)
					node["Status"] = node["status"]
					delete(node, "status")
				}
				_ = json.NewEncoder(w).Encode(object)
			}))
			defer server.Close()
			kube := &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}
			proof, err := readClusterSSHJoinedTargetsNative(context.Background(), kube, func(context.Context) (clusterSSHPreparationAuthority, error) { return a, nil })
			success := mode == "pair" || mode == "replacement"
			if (err == nil) != success {
				t.Fatal("strict joined decoder outcome", err)
			}
			if !success && (proof.AuthoritySHA256 != "" || len(proof.Nodes) != 0) {
				t.Fatal("partial native evidence escaped")
			}
			if success && len(proof.Nodes) != len(a.Cohort.Targets) {
				t.Fatal("missing cohort")
			}
		})
	}
}
