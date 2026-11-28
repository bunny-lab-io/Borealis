package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClusterSSHSourceBrokerJoinedProof(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "missing target", "not ready", "changed authority", "wrong target", "changed source", "wrong response mode"} {
		t.Run(mode, func(t *testing.T) {
			a, _, _ := sshPrejoinSourceFixture(t, mode == "replacement")
			claim := clusterSSHNetworkTargetClaim{Lease: a.Lease, Sealed: sealedClusterSSHCredentials{binding: a.Cohort.Targets[0].Binding, generation: "private-generation", ciphertext: aegisEnvelopePrefix + "private-ciphertext"}}
			broker := newClusterSSHSourceBroker(nil, nil, a.Lease.ControllerHolder, sshBrokerTestSecret)
			uids := map[string]string{}
			for _, target := range a.Cohort.Targets {
				uids[target.Binding.TargetID] = newClusterUUID()
			}
			reads := 0
			broker.read = func(context.Context, clusterSSHSourceBrokerRequest) (clusterSSHPreparationSnapshot, error) {
				t.Fatal("joined read reached generic source projection")
				return clusterSSHPreparationSnapshot{}, errClusterConflict
			}
			broker.readJoined = func(ctx context.Context, r clusterSSHSourceBrokerRequest) (clusterSSHJoinedTargets, error) {
				reads++
				if !r.Joined || r.Joining || r.Lease != claim.Lease || r.Baseline != a.Baseline || r.sealed() != claim.Sealed {
					t.Fatal("joined request changed original authority")
				}
				value, err := readClusterSSHJoinedTargets(ctx, func(context.Context) (clusterSSHPreparationAuthority, error) { return a, nil }, sshPrejoinCompletionNodes(a, uids))
				if err != nil {
					t.Fatal("native source observation", err)
				}
				switch mode {
				case "missing target":
					delete(value.Nodes, claim.Lease.TargetID)
				case "not ready":
					n := value.Nodes[claim.Lease.TargetID]
					n.Ready = false
					value.Nodes[claim.Lease.TargetID] = n
				case "changed authority":
					value.AuthoritySHA256 = strings.Repeat("f", 64)
				case "wrong target":
					n := value.Nodes[claim.Lease.TargetID]
					n.MachineID = strings.Repeat("f", 32)
					value.Nodes[claim.Lease.TargetID] = n
				case "changed source":
					a.PayloadSHA256 = strings.Repeat("f", 64)
				}
				return value, nil
			}
			server := httptest.NewServer(http.HandlerFunc(broker.handle))
			defer server.Close()
			client := sshBrokerClient(t, server.URL)
			if mode == "wrong response mode" {
				broker.readJoined = nil
			}
			value, err := client.joinedTargetsRead(context.Background(), func(context.Context) (clusterSSHPreparationAuthority, error) { return a, nil }, claim, a.Baseline)
			success := mode == "pair" || mode == "replacement"
			if (err == nil) != success {
				t.Fatal("joined proof result", err)
			}
			if success && (reads != 1 || len(value.Nodes) != len(a.Cohort.Targets)) {
				t.Fatal("incomplete cohort proof")
			}
		})
	}
}

func TestClusterSSHSourceBrokerJoinedRequestBoundary(t *testing.T) {
	r, _, _ := sshBrokerFixture(t)
	r.Joined = true
	if !r.validSource(time.Now()) || r.valid(time.Now()) || r.validWithin(time.Now(), 90) {
		t.Fatal("preparation joined request rejected")
	}
	r.Joining = true
	if r.validSource(time.Now()) {
		t.Fatal("ambiguous source/joined modes accepted")
	}
	r.Joining = false
	r.Lease.Step = "qualify"
	r.Lease.OperationStep = clusterSSHQualificationStep
	if r.validSource(time.Now()) {
		t.Fatal("qualification borrowed joined observation")
	}
}
