package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"compress/gzip"
	"context"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Expected metadata and simulated journal outcomes only. The native fixture
// owns no preparation authority and never converts a reissued claim to Q+2.
type reissuedSourceStageSQLTarget struct {
	lease  clusterSSHTargetLease
	target clusterremote.Target
	key    clusterremote.HostKey
}

type reissuedSourceStageSQLModel struct {
	mu                             sync.Mutex
	f                              sshPreparationAuthorityFixture
	baseline                       clusterbootstrap.Expected
	targets                        []reissuedSourceStageSQLTarget
	secret                         map[string]any
	mode                           string
	seen, inspectClosed, absent    map[int]bool
	actions                        []string
	opened, closed, cleaned, final int
	violation                      string
	injected                       bool
	escaped                        func(context.Context) error
	entered, closing, release      chan struct{}
	enterOnce, closeOnce           sync.Once
}

type reissuedSourceStageSQLClient struct {
	model  *reissuedSourceStageSQLModel
	index  int
	action string
	closed bool
}

func (m *reissuedSourceStageSQLModel) reject(message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.violation == "" {
		m.violation = message
	}
	return errors.New(message)
}

func (m *reissuedSourceStageSQLModel) connect(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHPreparationRecoverySourceStageClient, error) {
	if recoveryProgressSQLPoolAvailable(ctx, m.f) != nil {
		return nil, m.reject("native connect retained SQL")
	}
	for i, expected := range m.targets {
		if expected.target == target {
			if credential == nil || !reflect.DeepEqual(key, expected.key) {
				return nil, m.reject("native connect changed original credential or host key")
			}
			m.mu.Lock()
			m.opened++
			m.mu.Unlock()
			return &reissuedSourceStageSQLClient{model: m, index: i}, nil
		}
	}
	return nil, m.reject("native connect selected unknown original target")
}

func (c *reissuedSourceStageSQLClient) act(ctx context.Context, action string, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected, check func(context.Context) error) (clusterbootstrap.PreparationProgress, error) {
	m := c.model
	lease := m.targets[c.index].lease
	if recoveryProgressSQLPoolAvailable(ctx, m.f) != nil || expected.Target.TargetID != lease.TargetID || expected.Target.Generation != lease.Generation || expected.Target.HolderID != lease.Holder || expected.Source != m.baseline {
		return clusterbootstrap.PreparationProgress{}, m.reject("native action lost issued tip or released SQL pool")
	}
	raw, err := inputs.Configuration(ctx, expected, check)
	clear(raw)
	if err != nil {
		return clusterbootstrap.PreparationProgress{}, m.reject("original private configuration unavailable")
	}
	if _, err := inputs.SourceBundle(ctx, expected, check); err != nil {
		return clusterbootstrap.PreparationProgress{}, m.reject("original verified Bundle unavailable")
	}
	m.mu.Lock()
	if c.closed || c.action != "" {
		m.mu.Unlock()
		return clusterbootstrap.PreparationProgress{}, m.reject("native session reused after action or close")
	}
	if action != "inspect" && (len(m.seen) != len(m.targets) || len(m.inspectClosed) != len(m.targets) || m.cleaned < len(m.targets)) {
		m.mu.Unlock()
		return clusterbootstrap.PreparationProgress{}, m.reject("journal action preceded complete cohort inspection and cleanup")
	}
	c.action, m.escaped = action, check
	m.actions = append(m.actions, strconv.Itoa(c.index)+":"+action)
	if action == "inspect" {
		m.seen[c.index] = true
		step := "stage_source"
		if m.absent[c.index] {
			step = "source_absent"
		}
		if m.mode == "sibling cursor blocked" && c.index == 1 {
			step, m.injected = "prepare_host", true
		}
		m.mu.Unlock()
		return clusterbootstrap.PreparationProgress{Step: step}, nil
	}
	if action == "absent" {
		if m.mode != "replacement Q4 absence" {
			m.mu.Unlock()
			return clusterbootstrap.PreparationProgress{}, m.reject("uncertain cleanup was reused for absence proof")
		}
		m.absent[c.index] = true
		m.mu.Unlock()
		return clusterbootstrap.PreparationProgress{}, nil
	}
	mode := m.mode
	if mode == "late sibling cleanup" && c.index == 1 {
		m.injected = true
		m.mu.Unlock()
		return clusterbootstrap.PreparationProgress{}, errors.New("synthetic second journal reply lost")
	}
	m.mu.Unlock()
	if mode == "replacement Q4 absence" {
		return clusterbootstrap.PreparationProgress{}, errors.New("synthetic original publication unresolved")
	}
	if mode == "cancel proof" {
		m.enterOnce.Do(func() { close(m.entered) })
		<-ctx.Done()
		_ = check(context.Background())
	}
	return clusterbootstrap.PreparationProgress{}, nil
}

func (c *reissuedSourceStageSQLClient) InspectPreparationProgress(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected, _ []byte, check func(context.Context) error) (clusterbootstrap.PreparationProgress, error) {
	return c.act(ctx, "inspect", inputs, expected, check)
}

func (c *reissuedSourceStageSQLClient) ReconcilePreparation(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected, _ []byte, check func(context.Context) error) error {
	_, err := c.act(ctx, "apply", inputs, expected, check)
	return err
}

func (c *reissuedSourceStageSQLClient) ReconcilePreparationAbsent(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected, _ []byte, check func(context.Context) error) error {
	_, err := c.act(ctx, "absent", inputs, expected, check)
	return err
}

func (c *reissuedSourceStageSQLClient) Close() error {
	m := c.model
	if recoveryProgressSQLPoolAvailable(m.f.ctx, m.f) != nil {
		return m.reject("native cleanup retained SQL")
	}
	m.mu.Lock()
	if c.closed {
		m.mu.Unlock()
		return m.reject("native connection closed twice")
	}
	c.closed = true
	m.closed++
	if c.action == "inspect" {
		m.inspectClosed[c.index] = true
	}
	mode, action := m.mode, c.action
	m.mu.Unlock()
	if mode == "cancel proof" && action == "apply" {
		m.closeOnce.Do(func() { close(m.closing) })
		<-m.release
		return errors.New("synthetic canceled native cleanup uncertain")
	}
	if mode == "late sibling cleanup" && c.index == 1 && action == "apply" {
		return errors.New("synthetic second native cleanup uncertain")
	}
	return nil
}

func reissuedSourceStageSQLDependencies(t *testing.T, f sshPreparationAuthorityFixture, claim clusterSSHPreparationRecoveryReissuedProofClaim, secret map[string]any, mode string, manifest map[string]any, archive []byte) (*reissuedSourceStageSQLModel, clusterSSHPreparationRecoveryReissuedSourceStageDependencies) {
	t.Helper()
	m := &reissuedSourceStageSQLModel{f: f, baseline: claim.Baseline, secret: secret, mode: mode, seen: map[int]bool{}, inspectClosed: map[int]bool{}, absent: map[int]bool{}, entered: make(chan struct{}), closing: make(chan struct{}), release: make(chan struct{})}
	for i, target := range claim.Claims {
		original := f.targets[i]
		m.targets = append(m.targets, reissuedSourceStageSQLTarget{target.Lease, clusterremote.Target{Address: original.Binding.Address, Port: original.Binding.Port}, original.Key})
	}
	source := recoveryReissuedProgressSQLSource(f, secret)
	deps := clusterSSHPreparationRecoveryReissuedSourceStageDependencies{
		source: func(value clusterSSHPreparationRecoveryReissuedProofAuthority, payload string, check func(context.Context) error) clusterSSHPreparationRecoverySourceRead {
			read := source(value, payload, check)
			return func(ctx context.Context) (clusterSSHPreparationRecoverySource, error) {
				m.mu.Lock()
				if m.cleaned == 2*len(m.targets) {
					m.final++
				}
				m.mu.Unlock()
				return read(ctx)
			}
		},
		download: func(ctx context.Context, expected clusterbootstrap.Expected, scratch string) (*clusterbootstrap.Bundle, error) {
			if expected != claim.Baseline || recoveryProgressSQLPoolAvailable(ctx, f) != nil {
				return nil, m.reject("Bundle acquisition lost baseline or released SQL pool")
			}
			return recoveryProgressStageFixture(t, ctx, expected, manifest, archive, scratch)
		},
		connect: m.connect,
		closeInputs: func(inputs *clusterbootstrap.PreparationInputs) error {
			if err := inputs.Close(); err != nil {
				return m.reject("actual private input cleanup failed")
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			m.cleaned++
			if mode == "final Secret drift" && m.cleaned == 2*len(m.targets) {
				secret["metadata"].(map[string]any)["resourceVersion"] = "changed-after-final-source-input-cleanup"
				m.injected = true
			}
			return nil
		},
	}
	return m, deps
}

func TestClusterSSHPreparationRecoveryReissueSourceStagePostgresCustody(t *testing.T) {
	baseline, manifest, members := recoveryProgressArchiveFixture(t)
	archive := recoveryProgressPackWithCompressionLevel(t, members, gzip.NoCompression)
	cases := []struct {
		mode        string
		replacement bool
		resolution  string
		actions     []string
		cleaned     int
	}{
		{"pair Q3 applied", false, "applied", []string{"0:inspect", "1:inspect", "0:inspect", "0:apply", "1:inspect", "1:apply"}, 4},
		{"replacement Q4 absence", true, "not_applied", []string{"0:inspect", "0:inspect", "0:apply", "0:absent", "0:inspect"}, 2},
		{"sibling cursor blocked", false, "", []string{"0:inspect", "1:inspect"}, 2},
		{"late sibling cleanup", false, "", []string{"0:inspect", "1:inspect", "0:inspect", "0:apply", "1:inspect", "1:apply"}, 4},
		{"final Secret drift", true, "", []string{"0:inspect", "0:inspect", "0:apply"}, 2},
	}
	for _, test := range cases {
		t.Run(test.mode, func(t *testing.T) {
			f, claim, secret := recoveryReissuedProgressSQLFixture(t, test.replacement, test.replacement, baseline)
			copied := claim
			m, deps := reissuedSourceStageSQLDependencies(t, f, claim, secret, test.mode, manifest, archive)
			parent, events := t.TempDir(), f.events(t)
			got, err := f.c.recoverClusterSSHPreparationReissuedSourceStageWith(f.ctx, f.aegis, claim, parent, deps)
			valid := test.resolution != ""
			if m.violation != "" {
				t.Fatal(m.violation)
			}
			if (err == nil) != valid || !valid && (len(got) != 0 || !m.injected) || !reflect.DeepEqual(m.actions, test.actions) || m.opened != len(test.actions) || m.closed != m.opened || m.cleaned != test.cleaned {
				t.Fatal("incomplete source-only custody or partial result escaped", err, m.actions, m.opened, m.closed, m.cleaned)
			}
			if valid {
				if len(got) != len(claim.Claims) || m.final != 1 {
					t.Fatal("source result escaped complete post-cleanup cohort proof")
				}
				for i, result := range got {
					if result.TargetID != claim.Claims[i].Lease.TargetID || result.Resolution != test.resolution {
						t.Fatal("source-only result changed issued target or resolution")
					}
				}
			}
			if m.escaped == nil || m.escaped(context.Background()) == nil {
				t.Fatal("native source authority escaped closed scope")
			}
			// Reuse must reject before SQL, even when expired SQL state would also
			// reject a replay. The one original pool connection stays borrowed.
			blocked, err := f.c.store.db.Conn(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			replayCtx, cancel := context.WithTimeout(context.WithoutCancel(f.ctx), 250*time.Millisecond)
			waits, opened := f.c.store.db.Stats().WaitCount, m.opened
			live := replayCtx.Err() == nil
			again, replayErr := f.c.recoverClusterSSHPreparationReissuedSourceStageWith(replayCtx, f.aegis, copied, parent, deps)
			expired := replayCtx.Err()
			cancel()
			closeErr := blocked.Close()
			if !live || closeErr != nil || replayErr == nil || expired != nil || len(again) != 0 || f.c.store.db.Stats().WaitCount != waits || m.opened != opened {
				t.Fatal("copied reconciliation handle attempted SQL or native action")
			}
			assertRecoveryReissuedProgressSQLRetained(t, f, claim, events, parent)
		})
	}
}

func TestClusterSSHPreparationRecoveryReissueSourceStagePostgresJoinsCancellation(t *testing.T) {
	baseline, manifest, members := recoveryProgressArchiveFixture(t)
	archive := recoveryProgressPackWithCompressionLevel(t, members, gzip.NoCompression)
	f, claim, secret := recoveryReissuedProgressSQLFixture(t, false, true, baseline)
	m, deps := reissuedSourceStageSQLDependencies(t, f, claim, secret, "cancel proof", manifest, archive)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	parent, events := t.TempDir(), f.events(t)
	done := make(chan error, 1)
	var result []clusterSSHPreparationRecoverySourceStageResult
	var releaseOnce sync.Once
	go func() {
		err := errors.New("source reconciliation callback exited before return")
		defer func() { done <- err }()
		result, err = f.c.recoverClusterSSHPreparationReissuedSourceStageWith(ctx, f.aegis, claim, parent, deps)
	}()
	joined := false
	defer func() {
		cancel()
		releaseOnce.Do(func() { close(m.release) })
		if !joined {
			<-done
		}
	}()
	select {
	case <-m.entered:
	case err := <-done:
		joined = true
		t.Fatal("journal proof entry", err)
	case <-ctx.Done():
		t.Fatal("journal proof entry timeout")
	}
	cancel()
	select {
	case <-m.closing:
	case err := <-done:
		joined = true
		t.Fatal("scope returned before owned SSH close", err)
	case <-time.After(5 * time.Second):
		t.Fatal("canceled journal proof did not start cleanup")
	}
	select {
	case <-done:
		joined = true
		t.Fatal("source scope did not join owned cleanup")
	case <-time.After(25 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(m.release) })
	select {
	case err := <-done:
		joined = true
		if err == nil || len(result) != 0 || m.violation != "" || m.opened != 4 || m.closed != 4 || m.cleaned != 3 || m.escaped == nil || m.escaped(context.Background()) == nil || !claim.custody.failed.Load() || !reflect.DeepEqual(m.actions, []string{"0:inspect", "1:inspect", "0:inspect", "0:apply"}) {
			t.Fatal("canceled source proof released results or unjoined custody", err, m.violation)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("source reconciliation cleanup did not join")
	}
	assertRecoveryReissuedProgressSQLRetained(t, f, claim, events, parent)
}
