package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type sourceStageSQLModel struct {
	mu                        sync.Mutex
	f                         sshPreparationAuthorityFixture
	claim                     clusterSSHPreparationRecoveryClaim
	secret                    map[string]any
	mode                      string
	seen                      map[string]bool
	absent                    map[string]bool
	actions                   []string
	opened, closed, cleaned   int
	violation                 string
	escaped                   func(context.Context) error
	entered, closing, release chan struct{}
	enterOnce, closeOnce      sync.Once
}

type sourceStageSQLClient struct {
	model  *sourceStageSQLModel
	index  int
	action string
	closed bool
}

func (m *sourceStageSQLModel) connect(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHPreparationRecoverySourceStageClient, error) {
	if err := recoveryProgressSQLPoolAvailable(ctx, m.f); err != nil {
		return nil, err
	}
	for i, item := range m.f.targets {
		if target.Address == item.Binding.Address && target.Port == item.Binding.Port {
			if credential == nil || !reflect.DeepEqual(key, item.Key) {
				m.mu.Lock()
				m.violation = "original credential or pin changed"
				m.mu.Unlock()
				return nil, errClusterConflict
			}
			m.mu.Lock()
			m.opened++
			m.mu.Unlock()
			return &sourceStageSQLClient{model: m, index: i}, nil
		}
	}
	m.mu.Lock()
	m.violation = "unknown target dialed"
	m.mu.Unlock()
	return nil, errClusterConflict
}

func (c *sourceStageSQLClient) begin(ctx context.Context, action string, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected, check func(context.Context) error) error {
	m := c.model
	if err := recoveryProgressSQLPoolAvailable(ctx, m.f); err != nil {
		return err
	}
	lease := m.claim.Claims[c.index].Lease
	if expected.Target.TargetID != lease.TargetID || expected.Target.Generation != lease.Generation || expected.Target.HolderID != lease.Holder || expected.Source != m.claim.Baseline {
		m.mu.Lock()
		m.violation = "native action lost actual issued Q+2/source binding"
		m.mu.Unlock()
		return errClusterConflict
	}
	raw, err := inputs.Configuration(ctx, expected, check)
	clear(raw)
	if err != nil {
		return err
	}
	if _, err := inputs.SourceBundle(ctx, expected, check); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.action != "" || c.closed {
		m.violation = "native connection reused after action/close"
		return errClusterConflict
	}
	c.action = action
	m.escaped = check
	if action != "inspect" && len(m.seen) != len(m.claim.Claims) {
		m.violation = "journal proof preceded complete cohort observation"
		return errClusterConflict
	}
	m.actions = append(m.actions, lease.TargetID+":"+action)
	return nil
}

func (c *sourceStageSQLClient) InspectPreparationProgress(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected, _ []byte, check func(context.Context) error) (clusterbootstrap.PreparationProgress, error) {
	if err := c.begin(ctx, "inspect", inputs, expected, check); err != nil {
		return clusterbootstrap.PreparationProgress{}, err
	}
	m := c.model
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.claim.Claims[c.index].Lease.TargetID
	m.seen[id] = true
	if m.mode == "empty first" && c.index == 0 {
		return clusterbootstrap.PreparationProgress{Step: "empty"}, nil
	}
	if m.mode == "later second" && c.index == 1 {
		return clusterbootstrap.PreparationProgress{Step: "prepare_host"}, nil
	}
	if m.mode == "recorded absence" || m.absent[id] {
		if m.mode == "absence readback drift" {
			return clusterbootstrap.PreparationProgress{Step: "stage_source"}, nil
		}
		return clusterbootstrap.PreparationProgress{Step: "source_absent"}, nil
	}
	return clusterbootstrap.PreparationProgress{Step: "stage_source"}, nil
}

func (c *sourceStageSQLClient) ReconcilePreparation(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected, _ []byte, check func(context.Context) error) error {
	if err := c.begin(ctx, "reconcile", inputs, expected, check); err != nil {
		return err
	}
	m := c.model
	switch m.mode {
	case "absent replacement", "absence proof failed", "absence readback drift", "absence ACK lost", "native error plus close":
		return errors.New("synthetic original publication unresolved")
	case "late second failure":
		if c.index == 1 {
			return errors.New("synthetic second proof lost")
		}
	case "ignored credential loss":
		if _, err := m.f.c.store.db.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, m.claim.Claims[len(m.claim.Claims)-1].Lease.TargetID); err != nil {
			return err
		}
		_ = check(ctx) // An ignored lost-authority error must poison the entire scope.
	case "Aegis locked":
		m.f.aegis.mu.Lock()
		clear(m.f.aegis.key)
		m.f.aegis.key = nil
		m.f.aegis.mu.Unlock()
	case "original Secret changed":
		m.secret["metadata"].(map[string]any)["resourceVersion"] = "changed"
	case "canceled proof":
		m.enterOnce.Do(func() { close(m.entered) })
		<-ctx.Done()
		_ = check(context.Background())
		return nil
	}
	return nil
}

func (c *sourceStageSQLClient) ReconcilePreparationAbsent(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected, _ []byte, check func(context.Context) error) error {
	if err := c.begin(ctx, "absent", inputs, expected, check); err != nil {
		return err
	}
	m := c.model
	if m.mode == "absence proof failed" || m.mode == "late second failure" {
		return errors.New("synthetic absence/stopped-producer proof unavailable")
	}
	m.mu.Lock()
	m.absent[m.claim.Claims[c.index].Lease.TargetID] = true
	m.mu.Unlock()
	if m.mode == "absence ACK lost" {
		return errors.New("synthetic native receipt lost after persistence")
	}
	return nil
}

func (c *sourceStageSQLClient) Close() error {
	m := c.model
	m.mu.Lock()
	if c.closed {
		m.violation = "duplicate connection close"
		m.mu.Unlock()
		return errClusterConflict
	}
	c.closed = true
	m.closed++
	action := c.action
	m.mu.Unlock()
	if m.mode == "canceled proof" && action == "reconcile" {
		m.closeOnce.Do(func() { close(m.closing) })
		<-m.release
	}
	if m.mode == "native error plus close" && action == "reconcile" {
		return errors.New("synthetic cleanup uncertain")
	}
	return nil
}

func newSourceStageSQLModel(f sshPreparationAuthorityFixture, claim clusterSSHPreparationRecoveryClaim, secret map[string]any, mode string) *sourceStageSQLModel {
	return &sourceStageSQLModel{f: f, claim: claim, secret: secret, mode: mode, seen: map[string]bool{}, absent: map[string]bool{}, entered: make(chan struct{}), closing: make(chan struct{}), release: make(chan struct{})}
}

func sourceStageSQLDependencies(t *testing.T, m *sourceStageSQLModel, manifest map[string]any, archive []byte) clusterSSHPreparationRecoverySourceStageDependencies {
	t.Helper()
	return clusterSSHPreparationRecoverySourceStageDependencies{
		source: recoveryProgressSQLSource(t, m.f, m.secret),
		download: func(ctx context.Context, expected clusterbootstrap.Expected, scratch string) (*clusterbootstrap.Bundle, error) {
			if err := recoveryProgressSQLPoolAvailable(ctx, m.f); err != nil {
				return nil, err
			}
			return recoveryProgressStageFixture(t, ctx, expected, manifest, archive, scratch)
		}, connect: m.connect,
		closeInputs: func(inputs *clusterbootstrap.PreparationInputs) error {
			err := inputs.Close()
			m.mu.Lock()
			m.cleaned++
			n := m.cleaned
			m.mu.Unlock()
			if m.mode == "input cleanup" && n > len(m.claim.Claims) {
				return errors.New("synthetic input cleanup uncertain")
			}
			return err
		},
	}
}

func TestClusterSSHPreparationRecoverySourceStagePostgresCustody(t *testing.T) {
	// The declared interface cannot dispatch source delivery or later host work.
	contract := reflect.TypeOf((*clusterSSHPreparationRecoverySourceStageClient)(nil)).Elem()
	var methods []string
	for i := 0; i < contract.NumMethod(); i++ {
		methods = append(methods, contract.Method(i).Name)
	}
	if !slices.Equal(methods, []string{"Close", "InspectPreparationProgress", "ReconcilePreparation", "ReconcilePreparationAbsent"}) {
		t.Fatal("effect issuer entered reconciliation transport", methods)
	}
	baseline, manifest, members := recoveryProgressArchiveFixture(t)
	archive := recoveryProgressPack(t, members)
	for _, mode := range []string{"applied pair", "applied replacement", "absent replacement", "recorded absence", "empty first", "later second", "late second failure", "absence proof failed", "absence readback drift", "absence ACK lost", "ignored credential loss", "Aegis locked", "original Secret changed", "native error plus close", "input cleanup", "initial claim", "unissued claim"} {
		t.Run(mode, func(t *testing.T) {
			replacement := strings.Contains(mode, "replacement")
			f, claim, secret := recoveryProgressClaimFixture(t, replacement, baseline)
			f = recoveryTransportSQLExercise(t, f)
			f.c.store.db.SetMaxOpenConns(1)
			model := newSourceStageSQLModel(f, claim, secret, mode)
			deps := sourceStageSQLDependencies(t, model, manifest, archive)
			scratch := t.TempDir()
			if mode == "initial claim" {
				claim.Claims[0].Lease.OperationStep = clusterSSHPreparationOperationStep
				claim.Claims[0].Lease.Step = "stage_source"
			}
			if mode == "unissued claim" {
				claim.custody = nil
			}
			events := f.events(t)
			got, err := f.c.recoverClusterSSHPreparationSourceStageWith(f.ctx, f.aegis, claim, scratch, deps)
			// Inspect retained state even if the exercise was canceled. This is a
			// fresh assertion-only context, never permission for native work.
			assertCtx, stopAssertions := context.WithTimeout(context.WithoutCancel(f.ctx), 5*time.Second)
			defer stopAssertions()
			f.ctx = assertCtx
			valid := mode == "applied pair" || mode == "applied replacement" || mode == "absent replacement" || mode == "recorded absence"
			if (err == nil) != valid || (!valid && len(got) != 0) {
				t.Fatal("uncertain/partial reconciliation escaped", mode, got, err)
			}
			if model.violation != "" {
				t.Fatal(model.violation)
			}
			if model.opened != model.closed {
				t.Fatal("native connection not joined", model.opened, model.closed)
			}
			reconciles, absences := 0, 0
			for _, action := range model.actions {
				if strings.HasSuffix(action, ":reconcile") {
					reconciles++
				}
				if strings.HasSuffix(action, ":absent") {
					absences++
				}
			}
			if mode == "empty first" || mode == "later second" || mode == "initial claim" || mode == "unissued claim" {
				if reconciles != 0 || absences != 0 {
					t.Fatal("invalid cohort reached journal action")
				}
			}
			if mode == "native error plus close" && absences != 0 {
				t.Fatal("cleanup loss treated as native-only failure")
			}
			switch mode {
			case "native error plus close", "input cleanup", "ignored credential loss", "Aegis locked", "original Secret changed":
				if reconciles != 1 {
					t.Fatal("boundary failure did not follow exactly one original proof", reconciles)
				}
			case "late second failure":
				if reconciles != 2 || absences != 1 {
					t.Fatal("late pair failure did not retain first proof and independently reject second", reconciles, absences)
				}
			case "absence proof failed", "absence readback drift", "absence ACK lost":
				if reconciles != 1 || absences != 1 {
					t.Fatal("uncertain source did not attempt exactly one separate absence proof", reconciles, absences)
				}
			}
			if valid {
				if len(got) != len(claim.Claims) {
					t.Fatal("missing cohort result")
				}
				for i, result := range got {
					want := "applied"
					if mode == "absent replacement" || mode == "recorded absence" {
						want = "not_applied"
					}
					if result.TargetID != claim.Claims[i].Lease.TargetID || result.Resolution != want {
						t.Fatal("resolution rebound", result)
					}
				}
				if mode == "recorded absence" && (reconciles != 0 || absences != len(claim.Claims)) {
					t.Fatal("absence record replayed another action")
				}
				if mode == "absent replacement" && (reconciles != 1 || absences != 1) {
					t.Fatal("absence lacked distinct independent proof")
				}
				if strings.HasPrefix(mode, "applied ") && (reconciles != len(claim.Claims) || absences != 0) {
					t.Fatal("applied proof issued unexpected absence action")
				}
			}
			if model.escaped != nil && model.escaped(context.Background()) == nil {
				t.Fatal("reconciliation authority escaped scope")
			}
			before := len(model.actions)
			if _, err := f.c.recoverClusterSSHPreparationSourceStageWith(f.ctx, f.aegis, claim, scratch, deps); err == nil || len(model.actions) != before {
				t.Fatal("lost acknowledgement reexecuted issued claim")
			}
			if _, err := f.c.observeClusterSSHPreparationRecoveryProgressWith(f.ctx, f.aegis, claim, scratch, clusterSSHPreparationRecoveryProgressDependencies{}); err == nil {
				t.Fatal("reconciliation claim replayed as observation")
			}
			if entries, _ := os.ReadDir(scratch); len(entries) != 0 {
				t.Fatal("owned Bundle scratch retained")
			}
			if events != f.events(t) || f.c.store.db.Stats().InUse != 0 {
				t.Fatal("reconciliation emitted transition or retained connection")
			}
			var state, step, payload, active string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT o.state,o.current_step,o.payload_json,c.active_operation_id FROM engine.cluster_operations o CROSS JOIN engine.cluster_state c WHERE o.id=$1 AND c.id=1`, f.op.ID).Scan(&state, &step, &payload, &active) != nil || state != "running" || step != clusterSSHPreparationRecoveryOperationStep || payload != model.claim.payload || active != f.op.ID {
				t.Fatal("source proof settled parent")
			}
			for _, target := range model.claim.Claims {
				var targetState, phase string
				var generation int64
				if f.c.store.db.QueryRowContext(f.ctx, `SELECT state,current_step,lease_generation FROM engine.cluster_onboarding_targets WHERE id=$1`, target.Lease.TargetID).Scan(&targetState, &phase, &generation) != nil || targetState != "running" || phase != clusterSSHPreparationRecoveryClaimStep || generation != target.Lease.Generation {
					t.Fatal("absence created retry lineage or settled target")
				}
			}
		})
	}
}

func TestClusterSSHPreparationRecoverySourceStagePostgresJoinsCancellation(t *testing.T) {
	baseline, manifest, members := recoveryProgressArchiveFixture(t)
	archive := recoveryProgressPack(t, members)
	f, claim, secret := recoveryProgressClaimFixture(t, true, baseline)
	f = recoveryTransportSQLExercise(t, f)
	f.c.store.db.SetMaxOpenConns(1)
	model := newSourceStageSQLModel(f, claim, secret, "canceled proof")
	deps := sourceStageSQLDependencies(t, model, manifest, archive)
	scratch := t.TempDir()
	ctx, cancel := context.WithTimeout(f.ctx, 45*time.Second)
	defer cancel()
	done := make(chan error, 1)
	var release sync.Once
	joined := false
	defer func() {
		cancel()
		release.Do(func() { close(model.release) })
		if !joined {
			<-done
		}
	}()
	go func() {
		result := errors.New("native callback exited before returning")
		defer func() { done <- result }()
		got, err := f.c.recoverClusterSSHPreparationSourceStageWith(ctx, f.aegis, claim, scratch, deps)
		if len(got) != 0 {
			result = errors.New("canceled proof escaped")
			return
		}
		result = err
	}()
	select {
	case <-model.entered:
	case err := <-done:
		joined = true
		t.Fatal("proof entry", err)
	case <-ctx.Done():
		t.Fatal("proof entry deadline")
	}
	cancel()
	<-model.closing
	select {
	case <-done:
		joined = true
		t.Fatal("returned before owned native cleanup joined")
	default:
	}
	release.Do(func() { close(model.release) })
	err := <-done
	joined = true
	if err == nil || model.violation != "" || model.opened != model.closed || model.escaped == nil || model.escaped(context.Background()) == nil {
		t.Fatal("cancellation released invalid proof", err, model.violation)
	}
	if f.c.store.db.Stats().InUse != 0 {
		t.Fatal("canceled proof retained connection")
	}
	if entries, _ := os.ReadDir(scratch); len(entries) != 0 {
		t.Fatal("canceled Bundle retained")
	}
}
