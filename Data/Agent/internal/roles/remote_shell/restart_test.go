package remoteshell

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func restartManager(t *testing.T) (*Manager, *shellGeneration) {
	t.Helper()
	m := New("fixture", "system", filepath.Join(t.TempDir(), "agent.json"))
	m.host, m.port, m.shellKind, m.shellBin = "127.0.0.1", 0, "bash", "/fixture/shell"
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)
	t.Cleanup(func() { cancel(); m.Stop(context.Background()) })
	m.mu.Lock()
	run := m.run
	m.mu.Unlock()
	select {
	case err := <-run.ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listener startup missing")
	}
	return m, run
}

func restartSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("owned work did not reach checkpoint")
	}
}

func restartResult(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("restart completion missing")
		return nil
	}
}

func restartBlocked(t *testing.T, ch <-chan error) {
	t.Helper()
	select {
	case err := <-ch:
		t.Fatalf("restart completed before owned work joined: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
}

func restartRelease() (chan struct{}, func()) {
	ch := make(chan struct{})
	var once sync.Once
	return ch, func() { once.Do(func() { close(ch) }) }
}

func restartWaitingSession(t *testing.T, m *Manager, run *shellGeneration, complete func(error)) (*shellSession, func()) {
	t.Helper()
	agent, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	session := newShellSession(agent, "fixture", "bash", "", nil, m.onSessionClosed, m.activateSession)
	session.bound, session.complete, session.proc = true, complete, &exec.Cmd{}
	release, unblock := restartRelease()
	// Register cleanup before any assertion so failure never strands Wait.
	t.Cleanup(unblock)
	session.work.Add(1)
	go func() {
		defer session.work.Done()
		session.waitForTerminal(func() (error, bool) { <-release; return nil, true })
	}()
	m.startSession(run, session)
	m.activateSession(session)
	return session, unblock
}

func TestRestartJoinsSupersededWaitAndOriginalCompletion(t *testing.T) {
	m, old := restartManager(t)
	callbackEntered := make(chan struct{})
	callbackRelease, unblockCallback := restartRelease()
	t.Cleanup(unblockCallback)
	var originals atomic.Int32
	first, releaseFirst := restartWaitingSession(t, m, old, func(error) {
		close(callbackEntered)
		<-callbackRelease
		originals.Add(1)
	})
	_, releaseSecond := restartWaitingSession(t, m, old, func(error) { originals.Add(1) })
	if !first.isClosed() {
		t.Fatal("superseded session remained open")
	}
	done := make(chan error, 1)
	m.RestartWithCompletion(context.Background(), "fixture", func(err error) { done <- err })
	restartSignal(t, old.ctx.Done())
	restartBlocked(t, done)
	releaseFirst()
	restartSignal(t, callbackEntered)
	releaseSecond()
	restartBlocked(t, done)
	unblockCallback()
	if err := restartResult(t, done); err != nil {
		t.Fatal(err)
	}
	if originals.Load() != 2 {
		t.Fatal("restart replaced original completion callbacks")
	}
	m.mu.Lock()
	current, listening := m.run, m.listening
	m.mu.Unlock()
	if current == old || !listening {
		t.Fatal("restart receipt preceded replacement listener ownership")
	}
	m.sessionMu.Lock()
	active := m.session
	m.sessionMu.Unlock()
	if active != nil {
		t.Fatal("replacement inherited old shell session")
	}
}

func TestRestartJoinsPendingBindBeforeNewListener(t *testing.T) {
	m, old := restartManager(t)
	entered, canceled := make(chan struct{}), make(chan struct{})
	release, unblock := restartRelease()
	t.Cleanup(unblock)
	var completed atomic.Int32
	m.SetOperationBinder(func(ctx context.Context, _ map[string]any) (func(error), error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return func(error) { completed.Add(1) }, nil
	})
	agent, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	session := newShellSession(agent, "fixture", "bash", "/fixture/shell", nil, m.onSessionClosed, m.activateSession)
	m.startSession(old, session)
	writeJSON(t, peer, map[string]any{"type": "worker_operation_bind", "session_id": "original", "job_id": 1, "context": map[string]any{"worker_operation": map[string]any{"version": 1, "token": strings.Repeat("a", 32)}}})
	restartSignal(t, entered)
	done := make(chan error, 1)
	m.RestartWithCompletion(context.Background(), "fixture", func(err error) { done <- err })
	restartSignal(t, canceled)
	restartBlocked(t, done)
	unblock()
	if err := restartResult(t, done); err != nil || completed.Load() != 1 {
		t.Fatalf("pending original bind lost completion: %v/%d", err, completed.Load())
	}
}

func TestRestartUnknownWaitRetainsGenerationAndOutcome(t *testing.T) {
	m, old := restartManager(t)
	agent, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	session := newShellSession(agent, "fixture", "bash", "", nil, m.onSessionClosed, m.activateSession)
	session.proc = &exec.Cmd{}
	var original atomic.Int32
	session.bound, session.complete = true, func(error) { original.Add(1) }
	session.waitForTerminal(func() (error, bool) { return errors.New("unknown exit"), false })
	m.startSession(old, session)
	done := make(chan error, 1)
	m.RestartWithCompletion(context.Background(), "fixture", func(err error) { done <- err })
	restartSignal(t, old.done)
	restartBlocked(t, done)
	m.Start(context.Background())
	m.mu.Lock()
	retained, listening := m.run, m.listening
	m.mu.Unlock()
	if retained != old || listening || original.Load() != 0 {
		t.Fatal("unknown Wait acquired replacement or terminal proof")
	}
}

func TestRestartCancellationWaitsForOwnedWait(t *testing.T) {
	m, old := restartManager(t)
	_, release := restartWaitingSession(t, m, old, func(error) {})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	m.RestartWithCompletion(ctx, "fixture", func(err error) { done <- err })
	restartSignal(t, old.ctx.Done())
	cancel()
	restartBlocked(t, done)
	release()
	if err := restartResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled restart result: %v", err)
	}
	m.mu.Lock()
	run := m.run
	m.mu.Unlock()
	if run != nil {
		t.Fatal("canceled restart installed replacement")
	}
}

func TestRestartRejectsStaleGenerationAndRequiresFreshBind(t *testing.T) {
	m, old := restartManager(t)
	var binds atomic.Int32
	m.SetOperationBinder(func(context.Context, map[string]any) (func(error), error) {
		binds.Add(1)
		return func(error) {}, nil
	})
	done := make(chan error, 1)
	m.RestartWithCompletion(context.Background(), "fixture", func(err error) { done <- err })
	if err := restartResult(t, done); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	current, listener := m.run, m.listener
	m.mu.Unlock()
	if m.setState(old, false, "stale failure", nil) {
		t.Fatal("stale generation changed listener state")
	}
	agent, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	stale := newShellSession(agent, "fixture", "bash", "", nil, m.onSessionClosed, m.activateSession)
	m.startSession(old, stale)
	if !stale.isClosed() {
		t.Fatal("stale accept survived generation fence")
	}
	otherAgent, otherPeer := net.Pipe()
	t.Cleanup(func() { _ = otherPeer.Close() })
	staleReady := newShellSession(otherAgent, "fixture", "bash", "", nil, m.onSessionClosed, m.activateSession)
	defer staleReady.close()
	staleReady.generation = old
	// Even an independently delayed readiness callback cannot adopt old work.
	m.activateSession(staleReady)
	m.sessionMu.Lock()
	active := m.session
	m.sessionMu.Unlock()
	if active != nil {
		t.Fatal("stale readiness adopted old generation")
	}
	m.mu.Lock()
	unchanged := m.run == current && m.listener == listener && m.listening
	m.mu.Unlock()
	if !unchanged {
		t.Fatal("stale callback displaced replacement")
	}
	agent, peer = net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
	session := newShellSession(agent, "fixture", "bash", "/fixture/shell", nil, m.onSessionClosed, m.activateSession)
	m.startSession(current, session)
	if err := session.ensureProcessStarted(); err == nil || binds.Load() != 0 {
		t.Fatal("replacement inherited old binding")
	}
	writeJSON(t, peer, map[string]any{"type": "worker_operation_bind", "session_id": "new", "job_id": 2, "context": map[string]any{"worker_operation": map[string]any{"version": 1, "token": strings.Repeat("b", 32)}}})
	if ack := readJSON(t, bufio.NewReader(peer)); ack["type"] != "worker_operation_bound" || binds.Load() != 1 {
		t.Fatal("replacement did not obtain fresh binding")
	}
	_ = peer.Close()
}

func TestRestartCompletionCanStopReplacement(t *testing.T) {
	m, _ := restartManager(t)
	done := make(chan error, 1)
	m.RestartWithCompletion(context.Background(), "fixture", func(err error) {
		m.Stop(context.Background())
		done <- err
	})
	if err := restartResult(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestRestartListenerFailureJoinsFailedGeneration(t *testing.T) {
	m, _ := restartManager(t)
	m.Stop(context.Background())
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	m.port = busy.Addr().(*net.TCPAddr).Port
	done := make(chan error, 1)
	m.RestartWithCompletion(context.Background(), "fixture", func(err error) { done <- err })
	if err := restartResult(t, done); err == nil {
		t.Fatal("occupied listener reported restart success")
	}
	m.mu.Lock()
	run := m.run
	m.mu.Unlock()
	if run != nil {
		t.Fatal("failed listener retained retry producer after terminal")
	}
}

func TestRestartRequiresLiveRoleParent(t *testing.T) {
	for _, mode := range []string{"unstarted", "parent_canceled"} {
		t.Run(mode, func(t *testing.T) {
			m := New("fixture", "system", filepath.Join(t.TempDir(), "agent.json"))
			m.host, m.port, m.shellKind, m.shellBin = "127.0.0.1", 0, "bash", "/fixture/shell"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			t.Cleanup(func() { m.Stop(context.Background()) })
			if mode == "parent_canceled" {
				m.Start(ctx)
				cancel()
			}
			done := make(chan error, 1)
			m.RestartWithCompletion(context.Background(), "fixture", func(err error) { done <- err })
			if err := restartResult(t, done); err == nil {
				t.Fatal("restart installed listener without live role parent")
			}
			m.mu.Lock()
			run := m.run
			m.mu.Unlock()
			if run != nil {
				t.Fatal("ownerless restart retained generation")
			}
		})
	}
}

type restartBlockedClose struct {
	net.Conn
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *restartBlockedClose) Close() error {
	c.once.Do(func() { close(c.entered); <-c.release })
	return c.Conn.Close()
}

func TestRestartCancellationWaitsForOwnedClose(t *testing.T) {
	m, old := restartManager(t)
	agent, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	release, unblock := restartRelease()
	t.Cleanup(unblock)
	conn := &restartBlockedClose{Conn: agent, entered: make(chan struct{}), release: release}
	session := newShellSession(conn, "fixture", "bash", "", nil, m.onSessionClosed, m.activateSession)
	m.startSession(old, session)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	m.RestartWithCompletion(ctx, "fixture", func(err error) { done <- err })
	restartSignal(t, conn.entered)
	cancel()
	restartBlocked(t, done)
	unblock()
	if err := restartResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal("owned Close did not precede canceled completion", err)
	}
}

func TestRestartCoalescesSupervisorAndRejectsConcurrentTrackedRequest(t *testing.T) {
	m, old := restartManager(t)
	_, release := restartWaitingSession(t, m, old, func(error) {})
	first := make(chan error, 1)
	m.RestartWithCompletion(context.Background(), "first", func(err error) { first <- err })
	restartSignal(t, old.ctx.Done())
	contenders := make(chan error, 8)
	for i := 0; i < 8; i++ {
		m.Restart(context.Background(), "supervisor")
		m.RestartWithCompletion(context.Background(), "tracked contender", func(err error) { contenders <- err })
	}
	for i := 0; i < 8; i++ {
		if err := restartResult(t, contenders); err == nil {
			t.Fatal("competing restart completed active operation")
		}
	}
	restartBlocked(t, first)
	release()
	if err := restartResult(t, first); err != nil {
		t.Fatal(err)
	}
}

func TestRestartPreservesOriginalParentAcrossRepeatedStart(t *testing.T) {
	m := New("fixture", "system", filepath.Join(t.TempDir(), "agent.json"))
	m.host, m.port, m.shellKind, m.shellBin = "127.0.0.1", 0, "bash", "/fixture/shell"
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	m.Start(parent)
	t.Cleanup(func() { m.Stop(context.Background()) })
	other, cancelOther := context.WithCancel(context.Background())
	cancelOther()
	m.Start(other)
	done := make(chan error, 1)
	m.RestartWithCompletion(context.Background(), "fixture", func(err error) { done <- err })
	if err := restartResult(t, done); err != nil {
		t.Fatal("repeated Start replaced live original parent", err)
	}
	cancelParent()
	m.Start(context.Background())
	m.RestartWithCompletion(context.Background(), "fixture", func(err error) { done <- err })
	if err := restartResult(t, done); err == nil {
		t.Fatal("repeated Start bypassed canceled original parent")
	}
}

func TestRestartWithoutSocketBinderNeverStartsShell(t *testing.T) {
	m, run := restartManager(t)
	for _, stage := range []string{"initial", "restarted"} {
		t.Run(stage, func(t *testing.T) {
			if stage == "restarted" {
				done := make(chan error, 1)
				m.RestartWithCompletion(context.Background(), "fixture", func(err error) { done <- err })
				if err := restartResult(t, done); err != nil {
					t.Fatal(err)
				}
				m.mu.Lock()
				run = m.run
				m.mu.Unlock()
			}
			agent, peer := net.Pipe()
			defer peer.Close()
			session := newShellSession(agent, "fixture", "bash", "/fixture/shell", nil, m.onSessionClosed, m.activateSession)
			m.startSession(run, session)
			if !session.requireOperation || session.bindOperation != nil || session.bound {
				t.Fatal("no-binder generation did not retain binding fence")
			}
			if err := session.ensureProcessStarted(); err == nil || err.Error() != "shell operation unavailable" {
				t.Fatal("unbound generation reached shell startup", err)
			}
			session.procMu.Lock()
			proc := session.proc
			session.procMu.Unlock()
			if proc != nil {
				t.Fatal("no-binder generation started child")
			}
		})
	}
}
