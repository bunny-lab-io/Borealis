package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClusterSSHJoiningCustodyBroker(t *testing.T) {
	for _, mode := range []string{"pair", "empty", "foreign", "zero UID", "duplicate UID", "source UID", "wrong phase", "locked", "legacy path"} {
		t.Run(mode, func(t *testing.T) {
			b, request, authority, proof := sshPrejoinBrokerFixture(t)
			nodes := map[string]string{}
			for _, target := range authority.Cohort.Targets {
				nodes[target.Binding.TargetID] = newClusterUUID()
			}
			id := authority.Cohort.Targets[0].Binding.TargetID
			switch mode {
			case "empty":
				nodes = nil
			case "foreign":
				delete(nodes, id)
				nodes[newClusterUUID()] = newClusterUUID()
			case "zero UID":
				nodes[id] = "00000000-0000-0000-0000-000000000000"
			case "duplicate UID":
				nodes[id] = nodes[authority.Cohort.Targets[1].Binding.TargetID]
			case "source UID":
				nodes[id] = authority.Source.Members[0].NodeUID
			}
			joiningCalls, prejoinCalls := 0, 0
			b.readJoining = func(ctx context.Context, r clusterSSHPrejoinCustodyRequest) (clusterSSHJoiningSourceCustody, error) {
				joiningCalls++
				if !r.Request.Joining || r.Source != request.Source {
					t.Error("wrong phase/source")
				}
				return clusterSSHJoiningSourceCustody{proof, nodes}, nil
			}
			b.read = func(context.Context, clusterSSHPrejoinCustodyRequest) (clusterSSHPrejoinSourceCustody, error) {
				prejoinCalls++
				return proof, nil
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode != "wrong phase" {
					b.handle(w, r)
					return
				}
				recorder := httptest.NewRecorder()
				b.handle(recorder, r)
				var response clusterSSHPrejoinCustodyResponse
				if openClusterSSHVIP(b.responseCipher, clusterSSHPrejoinCustodyPath, recorder.Body.Bytes(), &response, clusterSSHSourceBrokerLimit) != nil {
					t.Error("response")
					return
				}
				response.Joining = false
				raw, _ := sealClusterSSHVIP(b.responseCipher, clusterSSHPrejoinCustodyPath, response, clusterSSHSourceBrokerLimit)
				w.Header().Set("Content-Type", clusterSSHSourceBrokerMedia)
				_, _ = w.Write(raw)
			}))
			defer server.Close()
			client := &clusterSSHPrejoinCustodyClient{clusterSSHSourceBrokerClient{requestCipher: b.requestCipher, responseCipher: b.responseCipher, httpClient: server.Client(), peers: func(context.Context) ([]string, error) { return []string{server.URL}, nil }}}
			reads := 0
			read := func(context.Context) (clusterSSHPreparationAuthority, error) {
				reads++
				if mode == "locked" && reads == 2 {
					return authority, errors.New("private Aegis error")
				}
				return authority, nil
			}
			if mode == "legacy path" {
				if _, err := client.observe(context.Background(), request.Source, request.Request.Lease, request.Request.Baseline, request.Request.sealed(), read); err != nil || joiningCalls != 0 || prejoinCalls != 1 {
					t.Fatal("strict phase changed", err)
				}
				return
			}
			got, err := client.observeJoining(context.Background(), request.Source, request.Request.Lease, request.Request.Baseline, request.Request.sealed(), read)
			valid := mode == "pair" || mode == "empty"
			if (err == nil) != valid || joiningCalls != 1 || prejoinCalls != 0 {
				t.Fatal("wrong broker phase", err)
			}
			if !valid && (got.Registered != nil || got.Original != (clusterSSHPrejoinSourceCustody{})) {
				t.Fatal("partial result escaped")
			}
		})
	}
}

func TestClusterSSHJoiningCustodyPinsAcrossRequests(t *testing.T) {
	_, _, a, proof := sshPrejoinBrokerFixture(t)
	id, uid := a.Cohort.Targets[0].Binding.TargetID, newClusterUUID()
	nodes := map[string]string{}
	read := newClusterSSHJoiningCustodyRead(func(context.Context) (clusterSSHJoiningSourceCustody, error) {
		return clusterSSHJoiningSourceCustody{proof, nodes}, nil
	})
	if _, err := read(context.Background()); err != nil {
		t.Fatal(err)
	}
	nodes[id] = uid
	if _, err := read(context.Background()); err != nil {
		t.Fatal("registration growth", err)
	}
	// Mutating producer's map cannot rewrite retained identity.
	nodes[id] = newClusterUUID()
	if _, err := read(context.Background()); err != clusterbootstrap.ErrSessionAuthority {
		t.Fatal("replacement", err)
	}
	nodes[id] = uid
	if _, err := read(context.Background()); err != clusterbootstrap.ErrSessionAuthority {
		t.Fatal("failure not latched", err)
	}
	fresh := newClusterSSHJoiningCustodyRead(func(context.Context) (clusterSSHJoiningSourceCustody, error) {
		return clusterSSHJoiningSourceCustody{proof, nodes}, nil
	})
	if _, err := fresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	delete(nodes, id)
	if _, err := fresh(context.Background()); err == nil {
		t.Fatal("disappearance")
	}
}

func TestClusterSSHJoiningSnapshotBrokerPhase(t *testing.T) {
	for _, mode := range []string{"joining", "prejoin", "wrong phase", "qualification"} {
		t.Run(mode, func(t *testing.T) {
			r, current, snapshot := sshBrokerFixture(t)
			b := newClusterSSHSourceBroker(nil, nil, r.Lease.ControllerHolder, sshBrokerTestSecret)
			calls := 0
			b.read = func(ctx context.Context, request clusterSSHSourceBrokerRequest) (clusterSSHPreparationSnapshot, error) {
				calls++
				if request.Joining != (mode != "prejoin") {
					t.Error("joining mode lost")
				}
				return snapshot, nil
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if mode != "wrong phase" {
					b.handle(w, request)
					return
				}
				recorder := httptest.NewRecorder()
				b.handle(recorder, request)
				var response clusterSSHSourceBrokerResponse
				if openClusterSSHSourceBroker(b.responseCipher, recorder.Body.Bytes(), &response) != nil {
					t.Error("response")
					return
				}
				response.Joining = false
				raw, _ := sealClusterSSHSourceBroker(b.responseCipher, response)
				w.Header().Set("Content-Type", clusterSSHSourceBrokerMedia)
				_, _ = w.Write(raw)
			}))
			defer server.Close()
			if mode == "qualification" {
				current.Lease.OperationStep, current.Lease.Step = clusterSSHQualificationStep, "qualify"
				r.Lease = current.Lease
			}
			client := sshBrokerClient(t, server.URL)
			read := client.snapshotReadFor(func(context.Context) (clusterSSHPreparationAuthority, error) { return current, nil }, r.Lease, r.Baseline, r.sealed(), mode != "prejoin")
			_, err := read(context.Background())
			valid := mode == "joining" || mode == "prejoin"
			if (err == nil) != valid || mode == "qualification" && calls != 0 {
				t.Fatal("wrong snapshot phase", err, calls)
			}
		})
	}
}
