package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestClusterSSHPrejoinWorkerDispatch(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "source slice ownership", "native slice ownership", "wrong confirmation", "wrong source", "qualification claim", "acquisition failure", "missing callback", "duplicate callback", "source cleanup failure", "native failure", "invalid receipt", "wrong expected", "authority drift before dispatch", "authority drift after dispatch", "sibling drift", "cohort renewal failure", "ignored authority failure", "input failure", "late input failure", "ignored join failure", "cancelled", "source error redaction"} {
		t.Run(mode, func(t *testing.T) {
			a := sshPrejoinCompletionAuthorityFixture(t, mode == "replacement")
			_, process, _ := existingIdentitySourceFixture(t)
			process.NodeName = a.Source.Members[len(a.Source.Members)-1].Name
			source := clusterSSHPrejoinSource{Binding: *a.IdentitySource, Process: process}
			claim := clusterSSHNetworkTargetClaim{Lease: a.Lease, Sealed: sealedClusterSSHCredentials{binding: a.Cohort.Targets[0].Binding}}
			var authorization clusterSSHPreparationAuthorization
			if clusterBootstrapObject([]byte(a.payload), map[string]any{"ssh_preparation_authorization": &authorization}) != nil {
				t.Fatal("fixture authorization")
			}
			confirmation := authorization.ConfirmationSHA256
			expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
			if err != nil {
				t.Fatal(err)
			}
			owned := expected
			owned.PeerAddresses = slices.Clone(expected.PeerAddresses)
			receipt := sshPrejoinReceiptFixture(expected, source.Binding)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			acquired, dispatched, closed := false, false, false
			inputs := &clusterbootstrap.PreparationInputs{}
			artifacts := &clusterbootstrap.PreparationArtifacts{}
			postgres := clusterbootstrap.PostgresImageRepository + "@sha256:" + strings.Repeat("a", 64)
			var escaped clusterSSHPreparationChecks
			var escapedJoin func(context.Context) error
			inputCalls, renewals := 0, 0
			checks := clusterSSHPreparationChecks{
				Inputs: func(context.Context) error {
					inputCalls++
					if mode == "input failure" || mode == "late input failure" && dispatched {
						return errors.New("private configuration")
					}
					return nil
				},
				Authority: func(context.Context) error { return nil },
				K3sJoin: func(ctx context.Context, binding engineidentity.Binding, consume clusterSSHJoinMaterialConsumer) error {
					if binding != source.Binding {
						t.Fatal("join source replaced")
					}
					if mode == "ignored join failure" {
						return errors.New("private join material")
					}
					return consume(ctx, &clusterbootstrap.K3sJoinMaterial{}, func(context.Context) error { return nil })
				},
			}
			acquire := func(ctx context.Context, consume clusterSSHPrejoinWorkerSourceConsumer) error {
				acquired = true
				defer func() { closed = true }()
				if mode == "acquisition failure" || mode == "source error redaction" {
					return errors.New("private source credential")
				}
				if mode == "missing callback" {
					return nil
				}
				if mode == "wrong expected" {
					expected.Target.Generation++
				}
				if mode == "authority drift before dispatch" {
					a.PayloadSHA256 = strings.Repeat("f", 64)
				}
				if mode == "sibling drift" {
					a.Cohort.Targets[1].Report.BootID = newClusterUUID()
				}
				if err := consume(ctx, inputs, artifacts, postgres, clusterSSHPreparedImageCapacity{}, expected, checks); err != nil {
					return err
				}
				if mode == "duplicate callback" {
					_ = consume(ctx, inputs, artifacts, postgres, clusterSSHPreparedImageCapacity{}, expected, checks)
				}
				if mode == "source slice ownership" {
					expected.PeerAddresses[0] = "192.0.2.99"
				}
				if mode == "source cleanup failure" {
					return errors.New("private scratch path")
				}
				return nil
			}
			native := func(ctx context.Context, p *clusterbootstrap.PreparationInputs, suppliedArtifacts *clusterbootstrap.PreparationArtifacts, pg string,
				e clusterbootstrap.PreparationExpected, c clusterSSHPreparationChecks) (clusterbootstrap.PrejoinPreparationReceipt, error) {
				dispatched = true
				escaped = c
				if !acquired || closed || p != inputs || suppliedArtifacts != artifacts || pg != postgres || !reflect.DeepEqual(e, owned) {
					t.Fatal("native dispatch lost scoped immutable inputs")
				}
				if c.Authority(ctx) != nil {
					return receipt, errClusterConflict
				}
				joinErr := c.K3sJoin(ctx, source.Binding, func(ctx context.Context, _ *clusterbootstrap.K3sJoinMaterial, check func(context.Context) error) error {
					escapedJoin = check
					return check(ctx)
				})
				if joinErr != nil && mode != "ignored join failure" {
					return receipt, joinErr
				}
				switch mode {
				case "native failure":
					return receipt, errors.New("private remote diagnostic")
				case "invalid receipt":
					receipt.MemberID++
				case "ignored authority failure":
					failed, stop := context.WithCancel(ctx)
					stop()
					_ = c.Authority(failed)
				case "cancelled":
					cancel()
				case "native slice ownership":
					e.PeerAddresses[0] = "192.0.2.99"
				case "authority drift after dispatch":
					a.PayloadSHA256 = strings.Repeat("f", 64)
				}
				return receipt, nil
			}
			switch mode {
			case "wrong confirmation":
				confirmation = strings.Repeat("f", 64)
			case "wrong source":
				source.Binding.SourceUID = newClusterUUID()
			case "qualification claim":
				claim.Lease.OperationStep, claim.Lease.Step = clusterSSHQualificationStep, "qualify"
			}
			result, err := dispatchClusterSSHPrejoinWorker(ctx, claim, a.Baseline, confirmation, source,
				func(context.Context) (clusterSSHPreparationAuthority, error) { return a, nil }, acquire, native,
				func(context.Context) error {
					renewals++
					if mode == "cohort renewal failure" && dispatched {
						return errors.New("private sibling lease")
					}
					return nil
				})
			success := mode == "pair" || mode == "replacement" || mode == "source slice ownership" || mode == "native slice ownership"
			if (err == nil) != success {
				t.Fatalf("incorrect dispatch outcome: %v", err)
			}
			if success {
				if !closed || !reflect.DeepEqual(result.Expected, owned) || result.Receipt != receipt || inputCalls < 2 || renewals < 2 {
					t.Fatal("receipt escaped without final source/cohort proof and cleanup")
				}
			} else if !reflect.DeepEqual(result, clusterSSHPrejoinDispatchResult{}) || err != clusterbootstrap.ErrSessionAuthority {
				t.Fatal("failed scope leaked evidence or private error")
			}
			if escaped.Authority != nil && (escaped.Authority(context.Background()) == nil || escaped.Inputs(context.Background()) == nil ||
				escaped.K3sJoin(context.Background(), source.Binding, func(context.Context, *clusterbootstrap.K3sJoinMaterial, func(context.Context) error) error {
					t.Fatal("escaped join acquisition")
					return nil
				}) == nil) {
				t.Fatal("native callback escaped completed scope")
			}
			if escapedJoin != nil && escapedJoin(context.Background()) == nil {
				t.Fatal("join callback escaped completed scope")
			}
		})
	}
}

func TestClusterSSHPrejoinWorkerDispatchJoinsOutstandingCheck(t *testing.T) {
	a := sshPrejoinCompletionAuthorityFixture(t, false)
	_, process, _ := existingIdentitySourceFixture(t)
	process.NodeName = a.Source.Members[0].Name
	source := clusterSSHPrejoinSource{Binding: *a.IdentitySource, Process: process}
	claim := clusterSSHNetworkTargetClaim{Lease: a.Lease, Sealed: sealedClusterSSHCredentials{binding: a.Cohort.Targets[0].Binding}}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(a.payload), &raw)
	var authorization clusterSSHPreparationAuthorization
	_ = json.Unmarshal(raw["ssh_preparation_authorization"], &authorization)
	expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	started, joined := make(chan struct{}), make(chan struct{})
	checks := clusterSSHPreparationChecks{Inputs: func(context.Context) error { return nil }, Authority: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, K3sJoin: func(context.Context, engineidentity.Binding, clusterSSHJoinMaterialConsumer) error { return nil }}
	acquire := func(ctx context.Context, consume clusterSSHPrejoinWorkerSourceConsumer) error {
		return consume(ctx, &clusterbootstrap.PreparationInputs{}, &clusterbootstrap.PreparationArtifacts{},
			clusterbootstrap.PostgresImageRepository+"@sha256:"+strings.Repeat("a", 64), clusterSSHPreparedImageCapacity{}, expected, checks)
	}
	native := func(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ *clusterbootstrap.PreparationArtifacts, _ string,
		_ clusterbootstrap.PreparationExpected, c clusterSSHPreparationChecks) (clusterbootstrap.PrejoinPreparationReceipt, error) {
		go func() { defer close(joined); _ = c.Authority(context.Background()) }()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("outstanding check never started")
		}
		return sshPrejoinReceiptFixture(expected, source.Binding), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := dispatchClusterSSHPrejoinWorker(ctx, claim, a.Baseline, authorization.ConfirmationSHA256, source,
		func(context.Context) (clusterSSHPreparationAuthority, error) { return a, nil }, acquire, native, func(context.Context) error { return nil })
	if err == nil || !reflect.DeepEqual(result, clusterSSHPrejoinDispatchResult{}) {
		t.Fatal("in-flight authority check became completion")
	}
	select {
	case <-joined:
	default:
		t.Fatal("dispatch returned before joining authority check")
	}
}

func TestClusterSSHPrejoinWorkerBudget(t *testing.T) {
	perTarget := clusterSSHStartupDispatchBudget(true, true, true) + 20*time.Minute
	for _, targets := range []int{1, 2} {
		if clusterSSHPreparationWorkerBudget(true, targets) != time.Duration(targets)*perTarget || clusterSSHPreparationWorkerBudget(false, targets) != 10*time.Minute {
			t.Fatal("worker truncated native/acquisition budget or changed legacy ceiling")
		}
	}
	for _, targets := range []int{-1, 0, 3} {
		if clusterSSHPreparationWorkerBudget(true, targets) != 0 {
			t.Fatal("unsupported cohort received execution budget")
		}
	}
	if newClusterSSHNativePrejoinPreparationWorker(nil, nil, "", clusterExistingIdentitySourceProcess{}, nil) != nil {
		t.Fatal("missing runtime dependencies accepted")
	}
}
