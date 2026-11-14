package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

func reissuedSourceFixture(t *testing.T, replacement bool, rounds int) (clusterSSHPreparationRecoveryReissuedProofAuthority, string, map[string]any, func(context.Context, string, any) error, clusterSSHSourceNetworkRead) {
	t.Helper()
	original, payload, secret, get, network := recoverySourceFixture(t, replacement)
	// Older configuration-only fixtures use a placeholder inspection digest.
	// This typed proof needs the actual retained original inspection and its
	// dependent plan/configuration receipt, matching the SQL issuer contract.
	proof := clusterSSHParentProof{Version: 1, Cohort: original.Cohort, Source: original.Source}
	var err error
	original.Authorization.InspectionSHA256, err = clusterSSHPreparationDigest(proof)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := clusterSSHPreparationPlanDigest(original.Authorization)
	if err != nil {
		t.Fatal(err)
	}
	work := clusterSSHPreparationWork{Authorization: original.Authorization, Baseline: original.Baseline, Proof: proof,
		Payload: marshalClusterJSON(map[string]any{"source_k3s_version": clusterbootstrap.K3sPins().Version})}
	approved, err := newClusterSSHPreparationConfigurationRead(work, func(ctx context.Context) error { return ctx.Err() }, get, network)(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	var receipts [6]string
	for i := range receipts {
		receipts[i] = strings.Repeat("a", 64)
	}
	receipts[4] = approved
	payload = marshalClusterJSON(map[string]any{"source_k3s_version": clusterbootstrap.K3sPins().Version, "ssh_preparation_readiness": clusterSSHPreparationReadiness(plan, receipts)})
	base, _, err := clusterSSHPreparationRecoveryPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	original.Reservation.PlanSHA256, original.Reservation.PayloadSHA256 = plan, clusterSSHPreparationRecoveryPayloadDigest(base)
	payload = sshRecoveryPayload(t, payload, original.Reservation)
	value := clusterSSHPreparationRecoveryReissuedProofAuthority{Lineage: clusterSSHPreparationRecoveryLineage{Version: 2, Original: original.Reservation},
		Baseline: original.Baseline, Cohort: original.Cohort, Source: original.Source, Authorization: original.Authorization, IdentitySource: original.IdentitySource,
		Leases: slices.Clone(original.Leases), ExpiresAt: map[string]int64{}}
	for range rounds {
		value.Lineage = appendReissueProofFixture(t, value.Lineage, payload)
		entry := &value.Lineage.Requests[len(value.Lineage.Requests)-1]
		for i := range entry.Targets {
			entry.Targets[i].PreviousHolder = value.Leases[i].Holder
			value.Leases[i].Holder, value.Leases[i].Generation = entry.Holder, entry.Targets[i].Generation
			value.ExpiresAt[value.Leases[i].TargetID] = 1000
		}
		payload = reissueProofPayload(t, payload, value.Lineage)
	}
	return value, payload, secret, get, network
}

func TestClusterSSHPreparationRecoveryReissuedSourceExactTip(t *testing.T) {
	for _, mode := range []string{"Q3 pair", "Q3 replacement", "Q4 pair", "Q4 replacement", "copied input"} {
		t.Run(mode, func(t *testing.T) {
			rounds := 1
			if strings.HasPrefix(mode, "Q4") {
				rounds = 2
			}
			value, payload, _, get, network := reissuedSourceFixture(t, strings.Contains(mode, "replacement"), rounds)
			want := slices.Clone(value.Leases)
			read := newClusterSSHPreparationRecoveryReissuedSourceRead(value, payload, func(ctx context.Context) error { return ctx.Err() }, get, network)
			if mode == "copied input" {
				value.Lineage.Requests[0].Targets[0].PreviousHolder = "caller changed"
				value.Cohort.Targets[0].Report.BootID = newClusterUUID()
				value.Leases[0].Generation++
				value.ExpiresAt[value.Leases[0].TargetID] = 0
			}
			result, err := read(context.Background())
			if err != nil || len(result.Expected) != len(want) {
				t.Fatal("original source", err)
			}
			for i, expected := range result.Expected {
				if expected.Target.Generation != want[i].Generation || expected.Target.HolderID != want[i].Holder || expected.Target.TargetID != want[i].TargetID {
					t.Fatal("expected reissued tip changed")
				}
			}
			result.Settings["POSTGRES_PASSWORD"] = "caller changed"
			again, err := read(context.Background())
			if err != nil || again.Settings["POSTGRES_PASSWORD"] == "caller changed" || !reflect.DeepEqual(again.Expected, result.Expected) {
				t.Fatal("mutable source output escaped", err)
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryReissuedSourceRejectsForeignTip(t *testing.T) {
	for _, mode := range []string{"v1", "old generation", "future generation", "wrong holder", "mixed holder", "target", "cohort", "inspection bytes", "source identity", "operation", "attempt", "controller", "initial phase", "missing sibling", "missing expiry", "extra expiry", "expired", "wrong payload", "history digest", "original source", "missing receipt"} {
		t.Run(mode, func(t *testing.T) {
			value, payload, _, get, network := reissuedSourceFixture(t, false, 2)
			switch mode {
			case "v1":
				value.Lineage.Requests = nil
				payload = reissueProofPayload(t, payload, value.Lineage)
			case "old generation":
				value.Leases[0].Generation--
			case "future generation":
				value.Leases[0].Generation++
			case "wrong holder":
				for i := range value.Leases {
					value.Leases[i].Holder = value.Lineage.Requests[0].Holder
				}
			case "mixed holder":
				value.Leases[1].Holder = newClusterUUID()
			case "target":
				value.Leases[0].TargetID = newClusterUUID()
			case "cohort":
				value.Cohort.Targets[0].Binding.TargetID = newClusterUUID()
			case "inspection bytes":
				value.Cohort.Targets[0].Report.BootID = newClusterUUID()
			case "source identity":
				value.Source.Members[0].NodeUID = newClusterUUID()
			case "operation":
				value.Leases[0].OperationID = newClusterUUID()
			case "attempt":
				value.Leases[0].OperationAttempt++
			case "controller":
				value.Leases[0].ControllerHolder = "changed"
			case "initial phase":
				value.Leases[0].OperationStep, value.Leases[0].Step = clusterSSHPreparationOperationStep, "stage_source"
			case "missing sibling":
				value.Leases = value.Leases[:1]
			case "missing expiry":
				delete(value.ExpiresAt, value.Leases[0].TargetID)
			case "extra expiry":
				value.ExpiresAt[newClusterUUID()] = 1
			case "expired":
				value.ExpiresAt[value.Leases[0].TargetID] = 0
			case "wrong payload":
				value.Lineage.Requests = value.Lineage.Requests[:1]
			case "history digest":
				value.Lineage.Requests[0].PayloadSHA256 = strings.Repeat("b", 64)
				payload = reissueProofPayload(t, payload, value.Lineage)
			case "original source":
				value.Lineage.Original.PlanSHA256 = strings.Repeat("c", 64)
			case "missing receipt":
				payload = `{}`
			}
			calls := 0
			read := newClusterSSHPreparationRecoveryReissuedSourceRead(value, payload, func(ctx context.Context) error { return ctx.Err() }, func(ctx context.Context, path string, out any) error {
				calls++
				return get(ctx, path, out)
			}, network)
			result, err := read(context.Background())
			if err == nil || calls != 0 || len(result.Expected) != 0 || result.Settings != nil {
				t.Fatal("foreign authority reached source read")
			}
			if _, err := buildClusterSSHPreparationRecoveryReissuedExpected(value, payload, 0, clusterbootstrap.K3sPins().Version, "10.42.0.0/16", "10.43.0.0/16"); err == nil {
				t.Fatal("foreign authority built native inputs")
			}
		})
	}
	value, payload, _, get, network := reissuedSourceFixture(t, false, 1)
	old := clusterSSHPreparationRecoveryAuthority{Reservation: value.Lineage.Original, Baseline: value.Baseline, Cohort: value.Cohort, Source: value.Source, Authorization: value.Authorization, Leases: value.Leases}
	if _, err := newClusterSSHPreparationRecoverySourceRead(old, payload, func(ctx context.Context) error { return ctx.Err() }, get, network)(context.Background()); err == nil {
		t.Fatal("old constructor accepted new lineage")
	}
	if _, err := buildClusterSSHPreparationRecoveryExpected(old, 0, clusterbootstrap.K3sPins().Version, "10.42.0.0/16", "10.43.0.0/16"); err == nil {
		t.Fatal("old Q+2 builder accepted reissued generation")
	}
}

func TestClusterSSHPreparationRecoveryReissuedSourcePoisonsChangedEvidence(t *testing.T) {
	for _, mode := range []string{"runtime bytes", "Secret UID", "Secret final revision", "source boot", "source final boot", "network namespace", "network final namespace", "authority", "final authority", "closed proof", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			value, payload, secret, originalGet, originalNetwork := reissuedSourceFixture(t, false, 2)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, nodes, secrets, networks := 0, 0, 0, 0
			enabled, denied := true, mode == "closed proof"
			get := func(ctx context.Context, path string, out any) error {
				calls++
				if path == clusterSSHRuntimeSecretPath {
					secrets++
					if enabled {
						switch mode {
						case "runtime bytes":
							if secrets == 1 {
								raw, err := base64.StdEncoding.DecodeString(secret["data"].(map[string]string)["POSTGRES_PASSWORD"])
								if err != nil {
									return errClusterConflict
								}
								password := string(raw) + "-changed"
								data := secret["data"].(map[string]string)
								databaseURL, err := base64.StdEncoding.DecodeString(data["BOREALIS_DATABASE_URL"])
								if err != nil {
									return errClusterConflict
								}
								parsed, err := url.Parse(string(databaseURL))
								if err != nil || parsed.User == nil {
									return errClusterConflict
								}
								parsed.User = url.UserPassword(parsed.User.Username(), password)
								data["POSTGRES_PASSWORD"] = base64.StdEncoding.EncodeToString([]byte(password))
								data["BOREALIS_DATABASE_URL"] = base64.StdEncoding.EncodeToString([]byte(parsed.String()))
							}
						case "Secret UID":
							if secrets == 1 {
								secret["metadata"].(map[string]any)["uid"] = newClusterUUID()
							}
						case "Secret final revision":
							if secrets == 2 {
								secret["metadata"].(map[string]any)["resourceVersion"] = "changed"
							}
						case "final authority":
							if secrets == 2 {
								denied = true
							}
						case "canceled":
							cancel()
						}
					}
				}
				if path == "/api/v1/nodes" {
					nodes++
					if enabled && (mode == "source boot" || mode == "source final boot" && nodes == 2) {
						var object map[string]any
						if originalGet(ctx, path, &object) != nil {
							return errClusterConflict
						}
						object["items"].([]any)[0].(map[string]any)["status"].(map[string]any)["nodeInfo"].(map[string]any)["bootID"] = newClusterUUID()
						wire, _ := json.Marshal(object)
						return json.Unmarshal(wire, out)
					}
				}
				return originalGet(ctx, path, out)
			}
			network := func(ctx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
				networks++
				n, err := originalNetwork(ctx, member)
				if enabled && (mode == "network namespace" || mode == "network final namespace" && networks > len(value.Source.Members)) {
					n.ManagementLink.NetworkNamespace++
					n.Kubelet.NetworkNamespace++
				}
				return n, err
			}
			check := func(ctx context.Context) error {
				if denied || enabled && mode == "authority" && calls > 1 {
					return errClusterConflict
				}
				return ctx.Err()
			}
			read := newClusterSSHPreparationRecoveryReissuedSourceRead(value, payload, check, get, network)
			result, err := read(ctx)
			if err == nil || len(result.Expected) != 0 || result.Settings != nil {
				t.Fatal("changed evidence produced inputs")
			}
			beforeCalls, beforeNetworks := calls, networks
			enabled, denied = false, false
			if _, err := read(context.Background()); err == nil || calls != beforeCalls || networks != beforeNetworks {
				t.Fatal("failed reader resumed after restored evidence")
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryReissuedSourceSerialCustody(t *testing.T) {
	value, payload, _, get, network := reissuedSourceFixture(t, false, 1)
	var mu sync.Mutex
	active := 0
	read := newClusterSSHPreparationRecoveryReissuedSourceRead(value, payload, func(ctx context.Context) error { return ctx.Err() }, func(ctx context.Context, path string, out any) error {
		mu.Lock()
		active++
		if active != 1 {
			t.Error("overlapping native source reads")
		}
		mu.Unlock()
		err := get(ctx, path, out)
		mu.Lock()
		active--
		mu.Unlock()
		return err
	}, network)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := read(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	// Cancellation of another use poisons the shared reader. A blocked earlier
	// read cannot later expose evidence after that uncertainty.
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var enteredOnce, releaseOnce sync.Once
	read = newClusterSSHPreparationRecoveryReissuedSourceRead(value, payload, func(ctx context.Context) error { return ctx.Err() }, func(ctx context.Context, path string, out any) error {
		enteredOnce.Do(func() { close(entered); <-release })
		return get(ctx, path, out)
	}, network)
	go func() { _, err := read(context.Background()); done <- err }()
	joined := false
	defer func() {
		releaseOnce.Do(func() { close(release) })
		if !joined {
			<-done
		}
	}()
	select {
	case <-entered:
	case err := <-done:
		joined = true
		t.Fatal("reader failed before controlled boundary", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := read(ctx); err == nil {
		t.Fatal("canceled concurrent read accepted")
	}
	releaseOnce.Do(func() { close(release) })
	err := <-done
	joined = true
	if err == nil {
		t.Fatal("poisoned in-flight read exposed inputs")
	}
}
