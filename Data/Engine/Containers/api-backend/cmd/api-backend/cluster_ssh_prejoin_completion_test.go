package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func sshPrejoinCompletionAuthorityFixture(t *testing.T, replacement bool) clusterSSHPreparationAuthority {
	a, _, _ := sshPrejoinSourceFixture(t, replacement)
	proof := clusterSSHParentProof{Version: 1, Cohort: a.Cohort, Source: a.Source}
	a.Queue = clusterSSHQueueSource{ClusterID: a.Source.ClusterID, ActiveOperationID: a.Cohort.OperationID, Release: a.Baseline.Release, SHA: a.Baseline.SourceSHA}
	report := newClusterSSHQualificationReport(a.Cohort.Attempt)
	for i := 0; i < len(report.Checks)-1; i++ {
		report.Checks[i].State = "passed"
	}
	authorization := sshPreparationAuthorizationFixture(t, proof, a.Queue, &report)
	confirmation := sshPreparationConfirmationFixture(t, &authorization, a.Source)
	plan, _ := clusterSSHPreparationPlanDigest(authorization)
	var receipts [6]string
	for i := range receipts {
		receipts[i] = strings.Repeat("a", 64)
	}
	digest, _ := clusterSSHPreparationDigest(proof)
	a.payload = marshalClusterJSON(map[string]any{"baseline_release": a.Baseline.Release, "baseline_sha": a.Baseline.SourceSHA, "source_k3s_version": a.K3sVersion, "target_count": len(a.Cohort.Targets),
		"ssh_inspection": map[string]any{"proof": proof, "sha256": digest, "qualification_required": true}, "ssh_preparation_authorization": authorization, "ssh_qualification": report, "ssh_preparation_confirmation": confirmation, "ssh_preparation_readiness": clusterSSHPreparationReadiness(plan, receipts)})
	a.PayloadSHA256 = preparationReceiptHash(a.payload)
	return a
}

func sshPrejoinCompletionNodes(a clusterSSHPreparationAuthority, uids map[string]string) func(context.Context, string, any) error {
	return func(_ context.Context, path string, out any) error {
		var value any
		switch path {
		case "/api/v1/namespaces/kube-system":
			value = map[string]any{"metadata": map[string]any{"uid": a.Source.KubeSystemUID}}
		case "/api/v1/nodes":
			var nodes []any
			for _, member := range a.Source.Members {
				node := sshSourceKubernetesFixture(member)
				node["apiVersion"], node["kind"] = "v1", "Node"
				node["metadata"].(map[string]any)["labels"] = map[string]any{"borealis.io/engine-node": "true", "borealis.io/application-state": "active"}
				nodes = append(nodes, node)
			}
			for _, target := range a.Cohort.Targets {
				if uid := uids[target.Binding.TargetID]; uid != "" {
					nodes = append(nodes, sshJoinedNodeFixture(target, a.K3sVersion, uid))
				}
			}
			value = map[string]any{"apiVersion": "v1", "kind": "NodeList", "metadata": map[string]any{}, "items": nodes}
		default:
			return errClusterConflict
		}
		raw, _ := json.Marshal(value)
		return json.Unmarshal(raw, out)
	}
}

func TestClusterSSHPrejoinCompletionProof(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "missing sibling", "Node replaced", "native member changed", "controller lost", "authority drift", "wrong source", "wrong generation", "cancelled", "private error", "not ready", "sibling drift"} {
		t.Run(mode, func(t *testing.T) {
			a := sshPrejoinCompletionAuthorityFixture(t, mode == "replacement")
			expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
			if err != nil {
				t.Fatal(err)
			}
			receipt := sshPrejoinReceiptFixture(expected, *a.IdentitySource)
			uids := map[string]string{}
			for _, target := range a.Cohort.Targets {
				uids[target.Binding.TargetID] = newClusterUUID()
			}
			uids[a.Lease.TargetID] = receipt.Node.NodeUID
			switch mode {
			case "missing sibling":
				delete(uids, a.Cohort.Targets[1].Binding.TargetID)
			case "Node replaced":
				uids[a.Lease.TargetID] = newClusterUUID()
			case "native member changed":
				receipt.MemberID++
			case "wrong source":
				receipt.Source.SourceUID = newClusterUUID()
			case "wrong generation":
				expected.Target.Generation++
			case "not ready":
				receipt.Node.Ready = false
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads := 0
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
				reads++
				if mode == "controller lost" {
					return a, errClusterConflict
				}
				if mode == "authority drift" && reads > 1 {
					a.PayloadSHA256 = strings.Repeat("f", 64)
				}
				return a, nil
			}
			cohortNodes := map[string]clusterbootstrap.K3sJoinedNode{}
			for _, target := range a.Cohort.Targets {
				cohortNodes[target.Binding.TargetID] = clusterbootstrap.K3sJoinedNode{NodeUID: uids[target.Binding.TargetID], Hostname: target.Report.Hostname, MachineID: target.Report.MachineID, BootID: target.Report.BootID, Address: target.Binding.Address, K3sVersion: a.K3sVersion, Ready: true}
			}
			if mode == "sibling drift" {
				uids[a.Cohort.Targets[1].Binding.TargetID] = newClusterUUID()
			}
			reader := sshPrejoinCompletionNodes(a, uids)
			getJSON := func(ctx context.Context, path string, out any) error {
				if mode == "private error" {
					return errClusterUnavailable
				}
				if mode == "cancelled" {
					cancel()
				}
				return reader(ctx, path, out)
			}
			result, err := verifyClusterSSHPrejoinCompletion(ctx, authority, expected, receipt, getJSON, cohortNodes)
			success := mode == "pair" || mode == "replacement"
			if (err == nil) != success {
				t.Fatal("wrong independent completion decision", err)
			}
			if success {
				if result.Result.Version != 2 || result.Result.JoinClusterSHA256 != "" || result.Result.Prejoin.Receipt != receipt || !result.Result.valid(a.Lease) {
					t.Fatal("wrong prejoin semantics")
				}
				proof := clusterSSHParentProof{Version: 1, Cohort: a.Cohort, Source: a.Source}
				// One-target parser fixture keeps original target identity while testing version separation.
				proof.Cohort.Targets = proof.Cohort.Targets[:1]
				saved := clusterSSHPreparationReceipt{Version: 2, Attempt: a.Lease.OperationAttempt, ConfirmationSHA256: result.Result.ConfirmationSHA256, Targets: []clusterSSHPreparationResult{result.Result}}
				raw := marshalClusterJSON(map[string]any{"ssh_preparation": saved})
				if _, err := parseClusterSSHPreparationReceipt(raw, proof); err == nil {
					t.Fatal("activated identity entered legacy repair")
				}
				if _, err := parseClusterSSHPreparationReceiptVersion(raw, proof, 2); err != nil {
					t.Fatal("version2 receipt rejected", err)
				}
				changed := result.Result
				changed.JoinClusterSHA256 = changed.Prejoin.Receipt.Steps[10].ResultSHA256
				if changed.valid(a.Lease) {
					t.Fatal("activation relabeled as legacy join")
				}
			} else if result.Result.Version != 0 {
				t.Fatal("failed independent proof leaked result")
			}
		})
	}
}
