package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func recoverySourceFixture(t *testing.T, replacement bool) (clusterSSHPreparationRecoveryAuthority, string, map[string]any, func(context.Context, string, any) error, clusterSSHSourceNetworkRead) {
	t.Helper()
	work := sshPreparationConfigurationFixture(t, replacement)
	secret := sshPreparationConfigurationSecret()
	get := func(ctx context.Context, path string, out any) error {
		var value any
		switch path {
		case clusterSSHRuntimeSecretPath:
			value = secret
		case "/api/v1/namespaces/kube-system":
			value = map[string]any{"metadata": map[string]any{"uid": work.Proof.Source.KubeSystemUID}}
		case "/api/v1/nodes":
			var items []any
			for _, member := range work.Proof.Source.Members {
				items = append(items, sshSourceKubernetesFixture(member))
			}
			value = map[string]any{"items": items}
		default:
			return errClusterConflict
		}
		raw, _ := json.Marshal(value)
		return json.Unmarshal(raw, out)
	}
	network := func(ctx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
		return sshSourceNetworkFixture(member, clusterbootstrap.K3sPins().Version), ctx.Err()
	}
	plan, _ := clusterSSHPreparationPlanDigest(work.Authorization)
	// Exercise the unchanged waiting reader, not just the shared calculator.
	approved, err := newClusterSSHPreparationConfigurationRead(work, func(ctx context.Context) error { return ctx.Err() }, get, network)(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	var receipts [6]string
	for i := range receipts {
		receipts[i] = strings.Repeat("a", 64)
	}
	receipts[4] = approved
	base := marshalClusterJSON(map[string]any{"source_k3s_version": clusterbootstrap.K3sPins().Version, "ssh_preparation_readiness": clusterSSHPreparationReadiness(plan, receipts)})
	canonical, _, err := clusterSSHPreparationRecoveryPayload(base)
	if err != nil {
		t.Fatal(err)
	}
	record := clusterSSHPreparationRecoveryRecord{Version: 1, ID: newClusterUUID(), OperationID: work.Authorization.OperationID, Attempt: work.Authorization.Attempt,
		ControllerHolder: work.Authorization.ControllerHolder, PlanSHA256: plan, PayloadSHA256: clusterSSHPreparationRecoveryPayloadDigest(canonical)}
	old, holder := newClusterUUID(), newClusterUUID()
	value := clusterSSHPreparationRecoveryAuthority{Baseline: work.Baseline, Cohort: work.Proof.Cohort, Source: work.Proof.Source, Authorization: work.Authorization}
	for _, item := range work.Authorization.Targets {
		record.Targets = append(record.Targets, clusterSSHPreparationRecoveryTarget{TargetID: item.TargetID, PreviousHolder: old, PreviousGeneration: item.QualificationGeneration + 1, PreviousExpiresAt: 1, Generation: item.QualificationGeneration + 2})
		value.Leases = append(value.Leases, clusterSSHTargetLease{TargetID: item.TargetID, OperationID: record.OperationID, OperationAttempt: record.Attempt, ControllerHolder: record.ControllerHolder,
			OperationKind: "ssh_onboarding", OperationStep: clusterSSHPreparationRecoveryOperationStep, Step: clusterSSHPreparationRecoveryClaimStep, Holder: holder, Generation: item.QualificationGeneration + 2})
	}
	value.Reservation = record
	var payload map[string]any
	_ = json.Unmarshal([]byte(base), &payload)
	payload[clusterSSHPreparationRecoveryField] = record
	return value, marshalClusterJSON(payload), secret, get, network
}

func TestClusterSSHPreparationRecoverySourceOriginalReceipt(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "runtime bytes", "excluded bytes", "Secret UID", "Secret revision", "network namespace", "network UID", "source boot", "initial lease", "stale generation", "changed holder", "changed target", "changed payload", "missing receipt", "authority", "during Secret read", "copied input"} {
		t.Run(mode, func(t *testing.T) {
			value, payload, secret, get, network := recoverySourceFixture(t, mode == "replacement")
			expectedValue := value
			switch mode {
			case "runtime bytes":
				secret["data"].(map[string]string)["POSTGRES_PASSWORD"] = base64.StdEncoding.EncodeToString([]byte("changed"))
			case "excluded bytes":
				secret["data"].(map[string]string)["BOREALIS_REPO_ROOT"] = base64.StdEncoding.EncodeToString([]byte("changed"))
			case "Secret UID":
				secret["metadata"].(map[string]any)["uid"] = newClusterUUID()
			case "Secret revision":
				secret["metadata"].(map[string]any)["resourceVersion"] = "101"
			case "initial lease":
				value.Leases[0].OperationStep = clusterSSHPreparationOperationStep
				value.Leases[0].Step = "stage_source"
			case "stale generation":
				value.Leases[0].Generation--
			case "changed holder":
				value.Leases[0].Holder = value.Reservation.Targets[0].PreviousHolder
			case "changed target":
				value.Leases[0].TargetID = newClusterUUID()
			case "changed payload":
				payload = strings.Replace(payload, `"version":1`, `"version":2`, 1)
			case "missing receipt":
				payload = `{}`
			}
			originalNetwork := network
			network = func(ctx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
				n, err := originalNetwork(ctx, member)
				if mode == "network namespace" {
					n.ManagementLink.NetworkNamespace++
					n.Kubelet.NetworkNamespace++ // Internally valid, identical CIDRs; only original receipt differs.
				}
				if mode == "network UID" {
					n.NodeUID = newClusterUUID()
				}
				return n, err
			}
			calls := 0
			originalGet := get
			get = func(ctx context.Context, path string, out any) error {
				calls++
				if mode == "during Secret read" && path == clusterSSHRuntimeSecretPath {
					secret["metadata"].(map[string]any)["resourceVersion"] = "changed"
				}
				if mode == "source boot" && path == "/api/v1/nodes" {
					var nodes map[string]any
					_ = originalGet(ctx, path, &nodes)
					nodes["items"].([]any)[0].(map[string]any)["status"].(map[string]any)["nodeInfo"].(map[string]any)["bootID"] = newClusterUUID()
					raw, _ := json.Marshal(nodes)
					return json.Unmarshal(raw, out)
				}
				return originalGet(ctx, path, out)
			}
			check := func(ctx context.Context) error {
				if mode == "authority" && calls > 1 {
					return errClusterConflict
				}
				return ctx.Err()
			}
			read := newClusterSSHPreparationRecoverySourceRead(value, payload, check, get, network)
			if mode == "copied input" {
				value.Cohort.Targets[0].Report.BootID = newClusterUUID()
				value.Leases[0].Generation++
			}
			result, err := read(context.Background())
			valid := mode == "pair" || mode == "replacement" || mode == "copied input"
			if (err == nil) != valid {
				t.Fatal("receipt binding", err)
			}
			if !valid {
				before := calls
				if _, err := read(context.Background()); err == nil || calls != before {
					t.Fatal("failed source reader not poisoned")
				}
				return
			}
			if len(result.Expected) != len(expectedValue.Leases) || result.Expected[0].Target.Generation != expectedValue.Authorization.Targets[0].QualificationGeneration+2 {
				t.Fatal("recovery generation changed")
			}
			if _, err := buildClusterSSHPreparationExpected(expectedValue.Cohort, expectedValue.Source, expectedValue.Leases[0], expectedValue.Baseline, clusterbootstrap.K3sPins().Version, "10.42.0.0/16", "10.43.0.0/16"); err == nil {
				t.Fatal("initial builder admitted recovery")
			}
			// Returning a mutable settings map must not change later original reads.
			result.Settings["POSTGRES_PASSWORD"] = "caller changed"
			again, err := read(context.Background())
			if err != nil || again.Settings["POSTGRES_PASSWORD"] == "caller changed" {
				t.Fatal("source result aliases reader", err)
			}
		})
	}
}

func TestClusterSSHPreparationRecoverySourceSerialReads(t *testing.T) {
	value, payload, _, get, network := recoverySourceFixture(t, false)
	var mu sync.Mutex
	active := 0
	getSerial := func(ctx context.Context, path string, out any) error {
		mu.Lock()
		active++
		if active != 1 {
			t.Error("overlapping source reads")
		}
		mu.Unlock()
		err := get(ctx, path, out)
		mu.Lock()
		active--
		mu.Unlock()
		return err
	}
	read := newClusterSSHPreparationRecoverySourceRead(value, payload, func(ctx context.Context) error { return ctx.Err() }, getSerial, network)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := read(context.Background())
			if err != nil || len(r.Expected) != 2 {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	wanted, _ := buildClusterSSHPreparationRecoveryExpected(value, 0, clusterbootstrap.K3sPins().Version, "10.42.0.0/16", "10.43.0.0/16")
	result, err := read(context.Background())
	if err != nil || !reflect.DeepEqual(wanted, result.Expected[0]) {
		t.Fatal("serialized read drift", err)
	}
}
