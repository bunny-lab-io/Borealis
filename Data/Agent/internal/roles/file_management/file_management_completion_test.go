package filemanagement

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

type transferTestTransport func(*http.Request) (*http.Response, error)

func (f transferTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func transferTestResponse(r *http.Request, status int, body io.ReadCloser) *http.Response {
	return &http.Response{StatusCode: status, Body: body, Header: make(http.Header), Request: r}
}
func transferTestJSON(r *http.Request, value any) *http.Response {
	raw, _ := json.Marshal(value)
	return transferTestResponse(r, http.StatusOK, io.NopCloser(bytes.NewReader(raw)))
}
func transferTestManager(t *testing.T, roundTrip transferTestTransport) *Manager {
	t.Helper()
	root := t.TempDir()
	cfg := agentconfig.Default()
	cfg.ServerURL = "http://transfer.fixture"
	cfg.Agent.GUID = "11111111-1111-4111-8111-111111111111"
	cfg.Tokens.AccessToken = "synthetic-access"
	cfg.Tokens.RefreshToken = "synthetic-refresh"
	cfg.Tokens.AccessExpiresAt = time.Now().Add(time.Hour).Unix()
	client, err := auth.NewClient(filepath.Join(root, "agent.json"), &cfg, "system", auth.WithHostname("host"))
	if err != nil {
		t.Fatal(err)
	}
	m := New(client, "host")
	m.tempRoot = filepath.Join(root, "transfers")
	m.httpClient = &http.Client{Transport: roundTrip, Timeout: time.Second}
	return m
}
func transferTestWait(t *testing.T, channel <-chan struct{}) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(3 * time.Second):
		t.Fatal("transfer barrier timed out")
	}
}
func transferTestNotDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("terminal callback before owned work ended: %v", err)
	default:
	}
}
func transferTestDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("terminal callback missing")
		return nil
	}
}

type transferTestBody struct {
	reader                                               io.Reader
	readStarted, readRelease, closeStarted, closeRelease chan struct{}
	readOnce, closeOnce                                  sync.Once
	closed                                               atomic.Bool
}

func (b *transferTestBody) Read(p []byte) (int, error) {
	b.readOnce.Do(func() {
		if b.readStarted != nil {
			close(b.readStarted)
		}
		if b.readRelease != nil {
			<-b.readRelease
		}
	})
	return b.reader.Read(p)
}
func (b *transferTestBody) Close() error {
	b.closeOnce.Do(func() {
		if b.closeStarted != nil {
			close(b.closeStarted)
		}
		if b.closeRelease != nil {
			<-b.closeRelease
		}
		b.closed.Store(true)
	})
	return nil
}

func TestTrackedUploadCompletesAfterStreamCleanupAndFinalReport(t *testing.T) {
	target := t.TempDir()
	body := &transferTestBody{reader: strings.NewReader("complete upload"), readStarted: make(chan struct{}), readRelease: make(chan struct{}), closeStarted: make(chan struct{}), closeRelease: make(chan struct{})}
	reportStarted, reportRelease := make(chan struct{}), make(chan struct{})
	m := transferTestManager(t, func(r *http.Request) (*http.Response, error) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		if strings.Contains(r.URL.Path, "/upload-item/") {
			return transferTestResponse(r, 200, body), nil
		}
		if strings.HasSuffix(r.URL.Path, "/progress") {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["status"] == "completed" {
				close(reportStarted)
				<-reportRelease
			}
		}
		return transferTestJSON(r, map[string]any{"status": "running"}), nil
	})
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	response, err := m.HandleRequestWithCompletion(ctx, map[string]any{"action": "upload_start", "transfer_id": "upload-1", "target_path": target, "items": []map[string]any{{"item_id": "item-1", "relative_path": "result.txt", "size_bytes": 15}}}, func(err error) {
		_ = m.Health()
		if m.activeTransfers.Load() != 0 || !body.closed.Load() {
			done <- errors.New("callback preceded owned cleanup")
			return
		}
		done <- err
	})
	if err != nil || response.(map[string]any)["status"] != "accepted" {
		t.Fatal(response, err)
	}
	transferTestWait(t, body.readStarted)
	cancel()
	transferTestNotDone(t, done)
	close(body.readRelease)
	transferTestWait(t, body.closeStarted)
	transferTestNotDone(t, done)
	close(body.closeRelease)
	transferTestWait(t, reportStarted)
	transferTestNotDone(t, done)
	close(reportRelease)
	if err := transferTestDone(t, done); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(target, "result.txt"))
	if err != nil || string(raw) != "complete upload" {
		t.Fatal(string(raw), err)
	}
	files, _ := filepath.Glob(filepath.Join(target, ".borealis-upload-*.tmp"))
	if len(files) != 0 {
		t.Fatal("upload temporary files retained", files)
	}
}

func TestTrackedTransferCancellationAndFailureWaitForReporting(t *testing.T) {
	for _, mode := range []string{"upload_canceled", "download_canceled", "upload_read_failed", "download_send_failed"} {
		t.Run(mode, func(t *testing.T) {
			target := t.TempDir()
			source := filepath.Join(target, "source.txt")
			if err := os.WriteFile(source, []byte("download content"), 0600); err != nil {
				t.Fatal(err)
			}
			reportStarted, reportRelease := make(chan struct{}), make(chan struct{})
			var reportOnce sync.Once
			var observedStatus string
			m := transferTestManager(t, func(r *http.Request) (*http.Response, error) {
				if r.Body != nil {
					defer r.Body.Close()
				}
				if strings.HasSuffix(r.URL.Path, "/progress") {
					var payload map[string]any
					_ = json.NewDecoder(r.Body).Decode(&payload)
					if payload["status"] == "failed" || payload["status"] == "canceled" {
						observedStatus = payload["status"].(string)
						reportOnce.Do(func() { close(reportStarted) })
						<-reportRelease
						return transferTestJSON(r, map[string]any{}), nil
					}
				}
				if strings.Contains(mode, "canceled") {
					return transferTestJSON(r, map[string]any{"cancel_requested": true}), nil
				}
				if strings.Contains(r.URL.Path, "/upload-item/") {
					return transferTestResponse(r, 200, io.NopCloser(transferFailReader{})), nil
				}
				if strings.HasSuffix(r.URL.Path, "/content") {
					return nil, context.DeadlineExceeded
				}
				return transferTestJSON(r, map[string]any{"status": "running"}), nil
			})
			payload := map[string]any{"action": "upload_start", "transfer_id": "transfer-1", "target_path": target, "items": []map[string]any{{"item_id": "item-1", "relative_path": "result.txt", "size_bytes": 10}}}
			if strings.HasPrefix(mode, "download") {
				payload["action"] = "download_start"
				payload["items"] = []map[string]any{{"path": source, "name": "source.txt"}}
			}
			done := make(chan error, 1)
			response, err := m.HandleRequestWithCompletion(context.Background(), payload, func(err error) {
				_ = m.Health()
				if m.activeTransfers.Load() != 0 {
					done <- errors.New("active transfer count retained at completion")
					return
				}
				done <- err
			})
			if err != nil || response.(map[string]any)["status"] != "accepted" {
				t.Fatal(response, err)
			}
			transferTestWait(t, reportStarted)
			transferTestNotDone(t, done)
			close(reportRelease)
			if err := transferTestDone(t, done); err == nil {
				t.Fatal("failed/canceled transfer completed successfully")
			}
			want := "failed"
			if strings.Contains(mode, "canceled") {
				want = "canceled"
			}
			if observedStatus != want {
				t.Fatal(observedStatus, want)
			}
			files, _ := filepath.Glob(filepath.Join(target, ".borealis-upload-*.tmp"))
			if len(files) != 0 {
				t.Fatal("upload cleanup incomplete", files)
			}
			files, _ = filepath.Glob(filepath.Join(m.tempRoot, "*"))
			if len(files) != 0 {
				t.Fatal("download cleanup incomplete", files)
			}
		})
	}
}

type transferFailReader struct{}

func (transferFailReader) Read([]byte) (int, error) {
	return 0, errors.New("transfer fixture read failure")
}

func TestTrackedDownloadCompletesAfterArtifactCloseAndTempCleanup(t *testing.T) {
	for _, archive := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "archive"}[archive], func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "source.txt")
			if err := os.WriteFile(source, []byte("download content"), 0600); err != nil {
				t.Fatal(err)
			}
			body := &transferTestBody{reader: strings.NewReader("{}"), closeStarted: make(chan struct{}), closeRelease: make(chan struct{})}
			var content []byte
			m := transferTestManager(t, func(r *http.Request) (*http.Response, error) {
				if r.Body != nil {
					defer r.Body.Close()
				}
				if strings.HasSuffix(r.URL.Path, "/content") {
					var err error
					content, err = io.ReadAll(r.Body)
					if err != nil {
						return nil, err
					}
					return transferTestResponse(r, 200, body), nil
				}
				return transferTestJSON(r, map[string]any{"status": "running"}), nil
			})
			done := make(chan error, 1)
			response, err := m.HandleRequestWithCompletion(context.Background(), map[string]any{"action": "download_start", "transfer_id": "download-1", "items": []map[string]any{{"path": source, "name": "source.txt"}}, "archive_required": archive}, func(err error) {
				_ = m.Health()
				if m.activeTransfers.Load() != 0 || !body.closed.Load() {
					done <- errors.New("callback preceded artifact cleanup")
					return
				}
				done <- err
			})
			if err != nil || response.(map[string]any)["status"] != "accepted" {
				t.Fatal(response, err)
			}
			transferTestWait(t, body.closeStarted)
			transferTestNotDone(t, done)
			temporary, _ := filepath.Glob(filepath.Join(m.tempRoot, "*"))
			want := 1
			if archive {
				want = 2
			}
			if len(temporary) != want {
				t.Fatalf("owned temporary files %v want %d", temporary, want)
			}
			close(body.closeRelease)
			if err := transferTestDone(t, done); err != nil {
				t.Fatal(err)
			}
			if len(content) == 0 {
				t.Fatal("artifact not sent")
			}
			temporary, _ = filepath.Glob(filepath.Join(m.tempRoot, "*"))
			if len(temporary) != 0 {
				t.Fatal("temporary files remain after callback", temporary)
			}
		})
	}
}

func TestTrackedFileSynchronousCompletionPreservesResponses(t *testing.T) {
	m := New(nil, "host")
	root := t.TempDir()
	completed := 0
	response, err := m.HandleRequestWithCompletion(context.Background(), map[string]any{"action": "mkdir", "path": root, "name": "created"}, func(err error) {
		_ = m.Health()
		if err != nil {
			t.Error(err)
		}
		if _, err := os.Stat(filepath.Join(root, "created")); err != nil {
			t.Error("callback before mutation", err)
		}
		completed++
	})
	if err != nil || response.(map[string]any)["ok"] != true || completed != 1 {
		t.Fatal(response, err, completed)
	}
	response, err = m.HandleRequestWithCompletion(context.Background(), map[string]any{"action": "mkdir", "path": root, "name": "created"}, func(err error) {
		if err == nil {
			t.Error("conflict lacks completion error")
		}
		completed++
	})
	if err != nil || response.(map[string]any)["ok"] != false || completed != 2 {
		t.Fatal(response, err, completed)
	}
	response, err = m.HandleRequest(context.Background(), map[string]any{"action": "children", "path": root})
	if err != nil || response.(map[string]any)["ok"] != true {
		t.Fatal("legacy envelope changed", response, err)
	}
}

func TestTrackedDownloadWaitsForTransportRequestBodyClose(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "response", true: "timeout"}[failed], func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "source.txt")
			if err := os.WriteFile(source, []byte("download content"), 0600); err != nil {
				t.Fatal(err)
			}
			returned, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
			responseBody := &transferTestBody{reader: strings.NewReader("{}"), closeStarted: make(chan struct{})}
			m := transferTestManager(t, func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/content") {
					go func() {
						<-release
						_ = r.Body.Close()
						close(closed)
					}()
					close(returned)
					if failed {
						return nil, context.DeadlineExceeded
					}
					return transferTestResponse(r, 200, responseBody), nil
				}
				if r.Body != nil {
					_ = r.Body.Close()
				}
				return transferTestJSON(r, map[string]any{"status": "running"}), nil
			})
			done := make(chan error, 1)
			response, err := m.HandleRequestWithCompletion(context.Background(), map[string]any{"action": "download_start", "transfer_id": "download-1", "items": []map[string]any{{"path": source, "name": "source.txt"}}}, func(err error) { done <- err })
			if err != nil || response.(map[string]any)["status"] != "accepted" {
				t.Fatal(response, err)
			}
			transferTestWait(t, returned)
			if !failed {
				transferTestWait(t, responseBody.closeStarted)
			}
			transferTestNotDone(t, done)
			temporary, _ := filepath.Glob(filepath.Join(m.tempRoot, "*"))
			if len(temporary) != 1 || m.activeTransfers.Load() != 1 {
				t.Fatal("transport still owns body but transfer/artifact was released", temporary)
			}
			close(release)
			transferTestWait(t, closed)
			if err := transferTestDone(t, done); (err != nil) != failed {
				t.Fatal("wrong final transfer result", err)
			}
			temporary, _ = filepath.Glob(filepath.Join(m.tempRoot, "*"))
			if len(temporary) != 0 {
				t.Fatal("artifact remains after transport close", temporary)
			}
		})
	}
}

func TestTransferRequestBodyWaitIncludesRedirectAndRetryBodies(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://transfer.fixture/progress", strings.NewReader("request"))
	if err != nil {
		t.Fatal(err)
	}
	wait := waitTransferRequestBodies(req)
	retry, err := req.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	if err := req.Body.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { wait(); close(done) }()
	select {
	case <-done:
		t.Fatal("original close released retry body")
	default:
	}
	if err := retry.Close(); err != nil {
		t.Fatal(err)
	}
	if err := retry.Close(); err != nil {
		t.Fatal(err)
	}
	transferTestWait(t, done)
}
