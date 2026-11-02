package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	filemanagement "github.com/bunny-lab-io/borealis/go-agent/internal/roles/file_management"
	"github.com/bunny-lab-io/borealis/go-agent/internal/transport"
)

type workerOperationTestServer struct {
	mu                  sync.Mutex
	grants              map[string]string
	committed           map[string]string
	receipts            []string
	preflights          int
	dropTerminal        bool
	preflightError      error
	changeProof         func(map[string]any)
	changeTerminalProof func(map[string]any)
	terminalEmitted     chan struct{}
}

func (s *workerOperationTestServer) Emit(string, any) error { return nil }
func (s *workerOperationTestServer) EmitWithAck(ctx context.Context, event string, payload any, timeout time.Duration) ([]any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	operationContext, _ := body["context"].(map[string]any)
	correlation, _ := operationContext["worker_operation"].(map[string]any)
	token, _ := correlation["token"].(string)
	s.mu.Lock()
	defer s.mu.Unlock()
	ack := map[string]any{"job_id": body["job_id"], "worker_operation": correlation}
	if event == "quick_job_progress" {
		s.preflights++
		preflight, _ := body["worker_operation_preflight"].(map[string]any)
		nonce, _ := preflight["nonce"].(string)
		ack["worker_operation_preflight"] = preflight
		ack["status"] = "deny"
		if _, terminal := s.committed[token]; !terminal && (s.grants[token] == "" || s.grants[token] == nonce) {
			s.grants[token] = nonce
			ack["status"] = "execute"
		}
		if s.changeProof != nil {
			s.changeProof(ack)
		}
		return []any{ack}, s.preflightError
	}
	if event != "quick_job_result" {
		return nil, fmt.Errorf("unexpected event %s", event)
	}
	s.receipts = append(s.receipts, string(raw))
	if s.terminalEmitted != nil {
		defer func() { s.terminalEmitted <- struct{}{} }()
	}
	if prior, ok := s.committed[token]; ok && prior != string(raw) {
		return nil, errors.New("conflicting terminal")
	}
	s.committed[token] = string(raw)
	if s.dropTerminal {
		return nil, errors.New("commit reply lost")
	}
	ack["status"] = "committed"
	if s.changeTerminalProof != nil {
		s.changeTerminalProof(ack)
	}
	return []any{ack}, nil
}
func workerOperationTestFixture(t *testing.T) (*workerOperationConnection, *workerOperationTestServer, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.json")
	server := &workerOperationTestServer{grants: map[string]string{}, committed: map[string]string{}}
	outbox := &workerOperationOutbox{pending: map[string]workerOperationTerminal{}, logger: log.New(io.Discard, "", 0)}
	return workerOperationTestConnection(t, path, server, outbox), server, path
}
func workerOperationTestConnection(t *testing.T, path string, emitter workerOperationEmitter, outbox *workerOperationOutbox) *workerOperationConnection {
	t.Helper()
	journal, err := agentconfig.OpenWorkerOperations(path, "agent-identity", "https://engine.example")
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.PruneCommitted(); err != nil {
		t.Fatal(err)
	}
	return &workerOperationConnection{journal: journal, emitter: emitter, outbox: outbox, active: map[string]agentconfig.WorkerOperationKey{}}
}
func workerOperationTestPayload() map[string]any {
	return map[string]any{"job_id": 17, "action": "roots", "context": map[string]any{"worker_operation": workerOperationCorrelation(strings.Repeat("a", 32))}}
}
func workerOperationTestRecord(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "worker-operations.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]any
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	records := file["records"].([]any)
	if len(records) != 1 {
		t.Fatalf("records=%s", raw)
	}
	return records[0].(map[string]any)
}
func workerOperationTestPending(t *testing.T, c *workerOperationConnection, want int) {
	t.Helper()
	records, err := c.journal.Pending()
	if err != nil || len(records) != want {
		t.Fatalf("pending %v err %v", records, err)
	}
}

func TestWorkerOperationAsyncAcceptanceAndFirstTerminalProof(t *testing.T) {
	c, server, path := workerOperationTestFixture(t)
	server.dropTerminal = true
	var callback func(error)
	accepted := c.async("service_control_action", func(_ context.Context, _ any, complete func(error)) (any, error) {
		callback = complete
		return map[string]any{"ok": true, "status": "accepted"}, nil
	})
	if _, err := accepted(context.Background(), workerOperationTestPayload()); err != nil {
		t.Fatal(err)
	}
	if workerOperationTestRecord(t, path)["state"] != "granted" || len(server.receipts) != 0 {
		t.Fatal("accepted became terminal")
	}
	callback(errors.New("actual command failed after completion"))
	first := server.receipts[0]
	var group sync.WaitGroup
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() { defer group.Done(); callback(nil) }()
	}
	group.Wait()
	if len(server.receipts) != 1 || !strings.Contains(first, `"status":"Failed"`) {
		t.Fatal("duplicate callback replaced first proof", server.receipts)
	}
	workerOperationTestPending(t, c, 1)
	server.dropTerminal = false
	c.replay(context.Background())
	if len(server.receipts) != 2 || first != server.receipts[1] {
		t.Fatal("replay changed receipt")
	}
	workerOperationTestPending(t, c, 0)
	if len(c.outbox.pending) != 0 {
		t.Fatal("duplicate callback left conflicting outbox")
	}
}

func TestWorkerOperationAmbiguousReturnNeverProvesTermination(t *testing.T) {
	for _, mode := range []string{"accepted", "pending", "error", "unknown", "async_error"} {
		t.Run(mode, func(t *testing.T) {
			c, server, path := workerOperationTestFixture(t)
			blocked := make(chan struct{})
			done := make(chan struct{})
			handler := func(context.Context, any) (any, error) {
				go func() { defer close(done); <-blocked }()
				if mode == "error" || mode == "async_error" {
					return nil, errors.New("uncertain")
				}
				if mode == "unknown" {
					return nil, nil
				}
				status := "accepted"
				if mode == "pending" {
					status = "pending"
				}
				return map[string]any{"ok": true, "status": status}, nil
			}
			var wrapped transport.Handler
			if mode == "async_error" {
				wrapped = c.async("service_control_action", func(ctx context.Context, payload any, _ func(error)) (any, error) { return handler(ctx, payload) })
			} else {
				wrapped = c.synchronous("file_management_request", handler, workerOperationSynchronousFile)
			}
			_, _ = wrapped(context.Background(), workerOperationTestPayload())
			if len(server.receipts) != 0 || workerOperationTestRecord(t, path)["state"] != "granted" {
				t.Fatal("return released live operation")
			}
			close(blocked)
			<-done
			if len(server.receipts) != 0 {
				t.Fatal("absence of callback became terminal")
			}
		})
	}
}

func TestWorkerOperationSynchronousAndUnsupportedContracts(t *testing.T) {
	for _, mode := range []string{"completed", "rejected", "unsupported", "untracked"} {
		t.Run(mode, func(t *testing.T) {
			c, server, _ := workerOperationTestFixture(t)
			calls := 0
			payload := workerOperationTestPayload()
			supported := workerOperationSynchronousFile
			if mode == "unsupported" {
				payload["action"] = "upload_start"
			}
			if mode == "untracked" {
				delete(payload, "context")
				payload["action"] = "upload_start"
			}
			wrapped := c.synchronous("file_management_request", func(context.Context, any) (any, error) { calls++; return map[string]any{"ok": mode != "rejected"}, nil }, supported)
			_, _ = wrapped(context.Background(), payload)
			if mode == "unsupported" {
				if calls != 0 {
					t.Fatal("unsupported effect ran")
				}
			} else if calls != 1 {
				t.Fatal("handler omitted")
			}
			if mode == "untracked" {
				if server.preflights != 0 || len(server.receipts) != 0 {
					t.Fatal("untracked behavior changed")
				}
				return
			}
			if len(server.receipts) != 1 {
				t.Fatal("terminal proof missing")
			}
			want := "Completed"
			if mode != "completed" {
				want = "Failed"
			}
			if !strings.Contains(server.receipts[0], `"status":"`+want+`"`) {
				t.Fatal(server.receipts)
			}
		})
	}
	for _, action := range []string{"upload_start", "download_start", "future_async", ""} {
		payload := workerOperationTestPayload()
		payload["action"] = action
		if workerOperationSynchronousFile(payload) {
			t.Fatalf("unsupported file action %q", action)
		}
	}
}

func TestWorkerOperationPreflightUnknownAndMalformedBinding(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"token": func(p map[string]any) {
			p["context"].(map[string]any)["worker_operation"].(map[string]any)["token"] = "bad"
		},
		"version": func(p map[string]any) {
			p["context"].(map[string]any)["worker_operation"].(map[string]any)["version"] = 2
		},
		"extra": func(p map[string]any) {
			p["context"].(map[string]any)["worker_operation"].(map[string]any)["extra"] = true
		},
		"fraction": func(p map[string]any) { p["job_id"] = 17.5 },
		"large":    func(p map[string]any) { p["job_id"] = int64(1 << 53) },
		"context":  func(p map[string]any) { p["context"] = "bad" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			c, s, _ := workerOperationTestFixture(t)
			p := workerOperationTestPayload()
			mutate(p)
			calls := 0
			_, _ = c.synchronous("test", func(context.Context, any) (any, error) { calls++; return nil, nil }, nil)(context.Background(), p)
			if calls != 0 || s.preflights != 0 {
				t.Fatal("malformed binding reached effects")
			}
		})
	}
	for _, mode := range []string{"timeout", "nonce", "job", "token", "extra", "status"} {
		t.Run(mode, func(t *testing.T) {
			c, s, path := workerOperationTestFixture(t)
			if mode == "timeout" {
				s.preflightError = context.DeadlineExceeded
			} else {
				s.changeProof = func(ack map[string]any) {
					switch mode {
					case "nonce":
						ack["worker_operation_preflight"].(map[string]any)["nonce"] = strings.Repeat("b", 32)
					case "job":
						ack["job_id"] = 18
					case "token":
						ack["worker_operation"].(map[string]any)["token"] = strings.Repeat("b", 32)
					case "extra":
						ack["other"] = true
					case "status":
						ack["status"] = "accepted"
					}
				}
			}
			calls := 0
			wrapped := c.synchronous("test", func(context.Context, any) (any, error) { calls++; return map[string]any{"ok": true}, nil }, nil)
			_, _ = wrapped(context.Background(), workerOperationTestPayload())
			_, _ = wrapped(context.Background(), workerOperationTestPayload())
			if calls != 0 || s.preflights != 1 || workerOperationTestRecord(t, path)["state"] != "intent" {
				t.Fatal("unknown intent retried or executed")
			}
		})
	}
}

func TestWorkerOperationLostJournalCannotExecuteAgain(t *testing.T) {
	c, s, path := workerOperationTestFixture(t)
	calls := 0
	handler := func(context.Context, any) (any, error) {
		calls++
		return map[string]any{"ok": true, "status": "accepted"}, nil
	}
	_, _ = c.synchronous("test", handler, nil)(context.Background(), workerOperationTestPayload())
	firstNonce := s.grants[strings.Repeat("a", 32)]
	if calls != 1 || firstNonce == "" {
		t.Fatal("initial grant absent")
	}
	if err := os.Remove(filepath.Join(filepath.Dir(path), "worker-operations.json")); err != nil {
		t.Fatal(err)
	}
	replacement := workerOperationTestConnection(t, path, s, c.outbox)
	for i := 0; i < 80; i++ {
		_, _ = replacement.synchronous("test", handler, nil)(context.Background(), workerOperationTestPayload())
	}
	if calls != 1 || s.grants[strings.Repeat("a", 32)] != firstNonce || s.preflights != 81 || len(s.receipts) != 0 {
		t.Fatal("lost journal/repeated denial reexecuted or fabricated completion")
	}
	workerOperationTestPending(t, replacement, 0)
}

func TestWorkerOperationReconnectReplaysExactReceipt(t *testing.T) {
	c, s, path := workerOperationTestFixture(t)
	s.dropTerminal = true
	_, _ = c.synchronous("test", func(context.Context, any) (any, error) { return map[string]any{"ok": true}, nil }, nil)(context.Background(), workerOperationTestPayload())
	first := s.receipts[0]
	c.close()
	nextServer := &workerOperationTestServer{grants: s.grants, committed: s.committed}
	next := workerOperationTestConnection(t, path, nextServer, c.outbox)
	next.replay(context.Background())
	if len(nextServer.receipts) != 1 || nextServer.receipts[0] != first {
		t.Fatal("new socket did not replay exact receipt")
	}
	workerOperationTestPending(t, next, 0)
	next.startReplay(context.Background())
	next.close()
	next.startReplay(context.Background())
	_, _, _, execute, err := c.begin(context.Background(), "test", workerOperationTestPayload())
	if execute || err == nil {
		t.Fatal("closed connection accepted work")
	}
}

func TestWorkerOperationPersistenceFailureRetainsTerminal(t *testing.T) {
	c, s, path := workerOperationTestFixture(t)
	var callback func(error)
	_, _ = c.async("service_control_action", func(_ context.Context, _ any, complete func(error)) (any, error) {
		callback = complete
		return map[string]any{"ok": true, "status": "accepted"}, nil
	})(context.Background(), workerOperationTestPayload())
	journalPath := filepath.Join(filepath.Dir(path), "worker-operations.json")
	granted, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journalPath, []byte("corrupt fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	callback(nil)
	if len(s.receipts) != 0 || len(c.outbox.pending) != 1 {
		t.Fatal("receipt emitted without durable terminal")
	}
	callback(errors.New("late conflicting callback"))
	if err := os.WriteFile(journalPath, granted, 0600); err != nil {
		t.Fatal(err)
	}
	next := workerOperationTestConnection(t, path, s, c.outbox)
	next.replay(context.Background())
	if len(s.receipts) != 1 || !strings.Contains(s.receipts[0], `"status":"Completed"`) || len(c.outbox.pending) != 0 {
		t.Fatal("retained terminal lost or replaced")
	}
}

func TestWorkerOperationScriptOutputAndUnsupportedCurrentUser(t *testing.T) {
	for _, mode := range []string{"system", "currentuser", "current_user", "interactive", "user", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			c, s, _ := workerOperationTestFixture(t)
			p := workerOperationTestPayload()
			p["run_mode"] = mode
			calls := 0
			handler := c.scripts(func(_ context.Context, payload any) (any, error) {
				calls++
				body := payload.(map[string]any)
				return nil, c.Emit("quick_job_result", map[string]any{"job_id": body["job_id"], "status": "Success", "stdout": strings.Repeat("\x00<&世", 100000), "stderr": strings.Repeat("\xff", 300000), "context": body["context"]})
			}, "host")
			_, _ = handler(context.Background(), p)
			if (mode == "system") != (calls == 1) || len(s.receipts) != 1 {
				t.Fatal("script effect support mismatch")
			}
			raw := s.receipts[0]
			if len(raw) > agentconfig.WorkerOperationReceiptLimit || !utf8.ValidString(raw) {
				t.Fatal("receipt size/UTF8 violated")
			}
			var result map[string]any
			_ = json.Unmarshal([]byte(raw), &result)
			for _, field := range []string{"stdout", "stderr"} {
				if len(result[field].(string)) > 256<<10 {
					t.Fatal("output bound violated")
				}
			}
		})
	}
}

func TestWorkerOperationConcurrentDuplicateDelivery(t *testing.T) {
	c, s, _ := workerOperationTestFixture(t)
	var calls atomic.Int32
	var group sync.WaitGroup
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, _ = c.synchronous("test", func(context.Context, any) (any, error) { calls.Add(1); return map[string]any{"ok": true}, nil }, nil)(context.Background(), workerOperationTestPayload())
		}()
	}
	group.Wait()
	if calls.Load() != 1 || len(s.receipts) != 1 {
		t.Fatalf("effects=%d receipts=%d", calls.Load(), len(s.receipts))
	}
}

func TestWorkerOperationTerminalACKRequiresExactBinding(t *testing.T) {
	for _, mode := range []string{"job", "token", "status", "extra"} {
		t.Run(mode, func(t *testing.T) {
			c, s, _ := workerOperationTestFixture(t)
			s.changeTerminalProof = func(ack map[string]any) {
				switch mode {
				case "job":
					ack["job_id"] = 18
				case "token":
					ack["worker_operation"] = workerOperationCorrelation(strings.Repeat("b", 32))
				case "status":
					ack["status"] = "accepted"
				case "extra":
					ack["other"] = true
				}
			}
			_, _ = c.synchronous("test", func(context.Context, any) (any, error) { return map[string]any{"ok": true}, nil }, nil)(context.Background(), workerOperationTestPayload())
			workerOperationTestPending(t, c, 1)
			first := s.receipts[0]
			s.changeTerminalProof = nil
			c.replay(context.Background())
			if s.receipts[1] != first {
				t.Fatal("terminal ACK retry changed bytes")
			}
			workerOperationTestPending(t, c, 0)
		})
	}
}

func TestWorkerOperationShellCallbackCapturesOriginalConnection(t *testing.T) {
	c, s, path := workerOperationTestFixture(t)
	payload := workerOperationTestPayload()
	delete(payload, "action")
	payload["type"] = "worker_operation_bind"
	payload["session_id"] = "session-1"
	callback, err := c.bindShell(context.Background(), payload)
	if err != nil || callback == nil {
		t.Fatal(err)
	}
	if workerOperationTestRecord(t, path)["state"] != "granted" || len(s.receipts) != 0 {
		t.Fatal("bind became terminal")
	}
	c.close()
	s.dropTerminal = true
	replacementServer := &workerOperationTestServer{grants: s.grants, committed: s.committed}
	replacement := workerOperationTestConnection(t, path, replacementServer, c.outbox)
	callback(nil)
	if len(s.receipts) != 1 || len(replacementServer.receipts) != 0 {
		t.Fatal("old callback switched emitters")
	}
	replacement.replay(context.Background())
	if len(replacementServer.receipts) != 1 || replacementServer.receipts[0] != s.receipts[0] {
		t.Fatal("replacement failed exact terminal replay")
	}
	callback(errors.New("late duplicate"))
	if len(c.outbox.pending) != 0 {
		t.Fatal("late callback overwrote committed receipt")
	}
}

func workerOperationFileManager(t *testing.T, baseURL string) *filemanagement.Manager {
	t.Helper()
	// Keep the role's temporary artifacts inside this disposable fixture.
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("TMP", t.TempDir())
	cfg := agentconfig.Default()
	cfg.ServerURL = baseURL
	cfg.Agent.GUID = "11111111-1111-4111-8111-111111111111"
	cfg.Tokens.AccessToken = "synthetic-access"
	cfg.Tokens.RefreshToken = "synthetic-refresh"
	cfg.Tokens.AccessExpiresAt = time.Now().Add(time.Hour).Unix()
	client, err := auth.NewClient(filepath.Join(t.TempDir(), "agent.json"), &cfg, "system", auth.WithHostname("host"))
	if err != nil {
		t.Fatal(err)
	}
	return filemanagement.New(client, "host")
}

func TestWorkerOperationFileTransfersRetainGrantUntilActualExitAndReplayReceipt(t *testing.T) {
	for _, action := range []string{"upload_start", "download_start"} {
		t.Run(action, func(t *testing.T) {
			c, s, path := workerOperationTestFixture(t)
			s.dropTerminal = true
			s.terminalEmitted = make(chan struct{}, 4)
			started, release := make(chan struct{}), make(chan struct{})
			var effects atomic.Int32
			target := t.TempDir()
			source := filepath.Join(target, "source.txt")
			if err := os.WriteFile(source, []byte("download data"), 0600); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/upload-item/") || strings.HasSuffix(r.URL.Path, "/content") {
					if workerOperationTestRecord(t, path)["state"] != "granted" {
						t.Error("file effect before durable grant")
					}
					effects.Add(1)
					if r.Body != nil {
						_, _ = io.Copy(io.Discard, r.Body)
						_ = r.Body.Close()
					}
					close(started)
					<-release
					if action == "upload_start" {
						_, _ = io.WriteString(w, "uploaded data")
						return
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "running"})
			}))
			defer server.Close()
			manager := workerOperationFileManager(t, server.URL)
			payload := workerOperationTestPayload()
			payload["action"] = action
			payload["transfer_id"] = "transfer-1"
			payload["target_path"] = target
			if action == "upload_start" {
				payload["items"] = []map[string]any{{"item_id": "item-1", "relative_path": "uploaded.txt", "size_bytes": 13}}
			} else {
				payload["items"] = []map[string]any{{"path": source, "name": "source.txt"}}
			}
			wrapped := c.async("file_management_request", manager.HandleRequestWithCompletion)
			response, err := wrapped(context.Background(), payload)
			if err != nil || response.(map[string]any)["status"] != "accepted" {
				t.Fatal(response, err)
			}
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("transfer did not start")
			}
			response, err = wrapped(context.Background(), payload)
			if err != nil || response.(map[string]any)["status"] != "pending" {
				t.Fatal("duplicate transfer not fenced", response, err)
			}
			select {
			case <-s.terminalEmitted:
				t.Fatal("accepted or duplicate ACK became terminal")
			default:
			}
			if workerOperationTestRecord(t, path)["state"] != "granted" {
				t.Fatal("in-flight grant lost")
			}
			c.close()
			replacement := workerOperationTestConnection(t, path, s, c.outbox)
			close(release)
			select {
			case <-s.terminalEmitted:
			case <-time.After(3 * time.Second):
				t.Fatal("transfer terminal missing")
			}
			workerOperationTestPending(t, replacement, 1)
			if manager.Health().Details["active_transfers"] != "0" {
				t.Fatal("callback preceded transfer exit")
			}
			s.mu.Lock()
			first := s.receipts[0]
			s.dropTerminal = false
			s.mu.Unlock()
			replacement.replay(context.Background())
			workerOperationTestPending(t, replacement, 0)
			s.mu.Lock()
			if len(s.receipts) != 2 || s.receipts[1] != first || !strings.Contains(first, `"status":"Completed"`) {
				t.Error("reconnect changed transfer receipt", s.receipts)
			}
			s.mu.Unlock()
			_, _ = replacement.async("file_management_request", manager.HandleRequestWithCompletion)(context.Background(), payload)
			if effects.Load() != 1 {
				t.Fatal("uncertain/committed transfer replayed", effects.Load())
			}
			if action == "upload_start" {
				raw, err := os.ReadFile(filepath.Join(target, "uploaded.txt"))
				if err != nil || string(raw) != "uploaded data" {
					t.Fatal(string(raw), err)
				}
			}
		})
	}
}

func TestWorkerOperationFileCallbackCoversSyncRejectionAndCancellation(t *testing.T) {
	for _, mode := range []string{"sync", "rejection", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			c, s, _ := workerOperationTestFixture(t)
			s.terminalEmitted = make(chan struct{}, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"cancel_requested": true})
			}))
			defer server.Close()
			manager := workerOperationFileManager(t, server.URL)
			payload := workerOperationTestPayload()
			payload["target_path"] = t.TempDir()
			if mode != "sync" {
				payload["action"] = "upload_start"
			}
			if mode == "cancellation" {
				payload["transfer_id"] = "transfer-1"
				payload["items"] = []map[string]any{{"item_id": "item-1", "relative_path": "no-write.txt", "size_bytes": 1}}
			}
			response, err := c.async("file_management_request", manager.HandleRequestWithCompletion)(context.Background(), payload)
			if err != nil {
				t.Fatal(err)
			}
			if (mode != "rejection") != (response.(map[string]any)["ok"] == true) {
				t.Fatal("response envelope changed", response)
			}
			select {
			case <-s.terminalEmitted:
			case <-time.After(3 * time.Second):
				t.Fatal("terminal result missing")
			}
			c.outbox.delivery.Lock()
			c.outbox.delivery.Unlock()
			s.mu.Lock()
			want := "Failed"
			if mode == "sync" {
				want = "Completed"
			}
			if len(s.receipts) != 1 || !strings.Contains(s.receipts[0], `"status":"`+want+`"`) {
				t.Error("wrong terminal result", s.receipts)
			}
			s.mu.Unlock()
			if _, err := os.Stat(filepath.Join(payload["target_path"].(string), "no-write.txt")); !os.IsNotExist(err) {
				t.Fatal("canceled request wrote target")
			}
		})
	}
}

func TestWorkerOperationFileCapabilityRequiresUsableJournal(t *testing.T) {
	for _, mode := range []string{"ready", "unavailable", "closed", "corrupt", "missing"} {
		t.Run(mode, func(t *testing.T) {
			c, _, path := workerOperationTestFixture(t)
			switch mode {
			case "unavailable":
				c.journal = nil
			case "closed":
				c.close()
			case "corrupt":
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), "worker-operations.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(filepath.Join(filepath.Dir(path), "worker-operations.json")); err != nil {
					t.Fatal(err)
				}
			}
			payload := map[string]any{}
			c.addRegistrationCapabilities(payload)
			for _, name := range []string{"worker_operation_version", "worker_operation_file_transfer_version"} {
				value, exists := payload[name]
				if mode == "ready" && value != 1 || mode != "ready" && exists {
					t.Fatalf("%s capability in %s journal: %v", name, mode, payload)
				}
			}
		})
	}
}
