package patchmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func policyPayload() map[string]any {
	return map[string]any{"hostname": "fixture", "mode": "managed", "request_id": "original-policy"}
}
func policyResult() commandResult {
	return commandResult{Stdout: `{"ok":true,"status":"applied","mode":"managed","request_id":"original-policy"}`}
}

func TestTrackedPatchPolicyRequiresOwnedExitAndMatchingResult(t *testing.T) {
	for _, mode := range []string{"success", "start_failed", "unknown_wait", "exit_failed", "missing_result", "wrong_request", "wrong_mode", "wrong_status", "false_ok", "wrong_ok_type", "canceled_joined"} {
		t.Run(mode, func(t *testing.T) {
			m := New(nil, "fixture", "system")
			m.supported = true
			m.runner = func(context.Context, time.Duration, string, ...string) (commandResult, error) {
				t.Fatal("tracked policy used legacy runner")
				return commandResult{}, nil
			}
			m.policyRunner = func(_ context.Context, timeout time.Duration, name string, args ...string) (commandResult, commandCompletion, error) {
				if timeout != 45*time.Second || name != "powershell.exe" || len(args) != 6 || args[5] != windowsPatchPolicyEnforcementScript("managed", "original-policy") {
					t.Fatal("fixed original policy command changed")
				}
				result, owned, err := policyResult(), commandCompletion{started: true, joined: true}, error(nil)
				switch mode {
				case "start_failed":
					owned = commandCompletion{}
					result.ExitCode = -1
					err = errors.New("start failed")
				case "unknown_wait":
					owned.joined = false
				case "exit_failed":
					result.ExitCode = 9
					err = errors.New("joined command failed")
				case "canceled_joined":
					err = context.Canceled
				case "missing_result":
					result.Stdout = "no result"
				default:
					var parsed map[string]any
					_ = json.Unmarshal([]byte(result.Stdout), &parsed)
					switch mode {
					case "wrong_request":
						parsed["request_id"] = "other"
					case "wrong_mode":
						parsed["mode"] = "unmanaged"
					case "wrong_status":
						parsed["status"] = "accepted"
					case "false_ok":
						parsed["ok"] = false
					case "wrong_ok_type":
						parsed["ok"] = "true"
					}
					raw, _ := json.Marshal(parsed)
					result.Stdout = string(raw)
				}
				return result, owned, err
			}
			calls := 0
			var completionErr error
			response, err := m.HandlePolicyEnforcementRequestWithCompletion(context.Background(), policyPayload(), func(err error) { calls++; completionErr = err })
			if err != nil {
				t.Fatal(err)
			}
			body := response.(map[string]any)
			if mode == "unknown_wait" {
				if calls != 0 || body["status"] != "pending" {
					t.Fatal("unconfirmed process became terminal", calls, body)
				}
				return
			}
			if calls != 1 || (completionErr == nil) != (mode == "success") {
				t.Fatal("owned completion mismatch", calls, completionErr)
			}
			if mode != "success" && (body["ok"] != false || body["status"] != "failed") {
				t.Fatal("joined failure became success", body)
			}
		})
	}
}

func TestTrackedPatchPolicyPrelaunchValidationAndLegacyEnvelope(t *testing.T) {
	for _, mode := range []string{"invalid", "wrong_host", "unsupported", "wrong_mode", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			m := New(nil, "fixture", "system")
			m.supported = true
			m.policyRunner = func(context.Context, time.Duration, string, ...string) (commandResult, commandCompletion, error) {
				t.Fatal("prework or legacy path reached tracked command")
				return commandResult{}, commandCompletion{}, nil
			}
			legacyCalls := 0
			m.runner = func(context.Context, time.Duration, string, ...string) (commandResult, error) {
				legacyCalls++
				return policyResult(), nil
			}
			var payload any = policyPayload()
			switch mode {
			case "invalid":
				payload = "invalid"
			case "wrong_host":
				payload.(map[string]any)["hostname"] = "other"
			case "unsupported":
				m.supported = false
			case "wrong_mode":
				payload.(map[string]any)["mode"] = "other"
			}
			var response any
			var err error
			if mode == "legacy" {
				response, err = m.HandlePolicyEnforcementRequest(context.Background(), payload)
			} else {
				response, err = m.HandlePolicyEnforcementRequestWithCompletion(context.Background(), payload, func(error) { t.Fatal("validation manufactured command proof") })
			}
			if err != nil {
				t.Fatal(err)
			}
			body := response.(map[string]any)
			if mode == "legacy" {
				if legacyCalls != 1 || body["ok"] != true || body["status"] != "applied" {
					t.Fatal("legacy envelope changed", body)
				}
			} else if body["ok"] != false || body["status"] != nil || legacyCalls != 0 {
				t.Fatal("prework rejection contract changed", body)
			}
		})
	}
}

// Harmless disposable child; never executes policy PowerShell or touches registry.
func TestPatchPolicyCommandChild(t *testing.T) {
	if len(os.Args) < 3 {
		return
	}
	mode := os.Args[len(os.Args)-2]
	path := os.Args[len(os.Args)-1]
	if !strings.HasPrefix(mode, "policy-child-") {
		return
	}
	switch mode {
	case "policy-child-result":
		fmt.Println(policyResult().Stdout)
		os.Exit(0)
	case "policy-child-fail":
		fmt.Fprintln(os.Stderr, "joined failure")
		os.Exit(9)
	case "policy-child-output-parent":
		exe, _ := os.Executable()
		child := exec.Command(exe, "-test.run=^TestPatchPolicyCommandChild$", "--", "policy-child-output", path)
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		fmt.Println(policyResult().Stdout)
		os.Exit(0)
	case "policy-child-output", "policy-child-wait":
		_ = os.WriteFile(path+".entered", []byte("started"), 0600)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(path); err == nil {
				fmt.Println("owned output completed")
				os.Exit(0)
			}
			time.Sleep(5 * time.Millisecond)
		}
		os.Exit(4)
	}
}

func TestTrackedPatchPolicyActualCommandJoin(t *testing.T) {
	for _, mode := range []string{"success", "start_failed", "exit_failed", "cancel", "output_join"} {
		t.Run(mode, func(t *testing.T) {
			m := New(nil, "fixture", "system")
			m.supported = true
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			release := filepath.Join(dir, "release")
			var once sync.Once
			unblock := func() { once.Do(func() { _ = os.WriteFile(release, []byte("release"), 0600) }) }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			terminal := make(chan error, 1)
			done := make(chan struct{})
			defer func() { unblock(); cancel(); <-done }()
			m.policyRunner = func(ctx context.Context, timeout time.Duration, _ string, _ ...string) (commandResult, commandCompletion, error) {
				name, childMode := exe, "policy-child-result"
				switch mode {
				case "start_failed":
					name = filepath.Join(dir, "missing-command")
				case "exit_failed":
					childMode = "policy-child-fail"
				case "cancel":
					childMode = "policy-child-wait"
				case "output_join":
					childMode = "policy-child-output-parent"
				}
				result, owned, err := runCommandWithCompletion(ctx, timeout, name, "-test.run=^TestPatchPolicyCommandChild$", "--", childMode, release)
				if mode == "output_join" && owned.joined && !strings.Contains(result.Stdout, "owned output completed") {
					t.Error("command returned before complete owned output")
				}
				return result, owned, err
			}
			go func() {
				defer close(done)
				_, _ = m.HandlePolicyEnforcementRequestWithCompletion(ctx, policyPayload(), func(err error) { terminal <- err })
			}()
			if mode == "cancel" || mode == "output_join" {
				deadline := time.Now().Add(3 * time.Second)
				for {
					if _, err := os.Stat(release + ".entered"); err == nil {
						break
					}
					select {
					case err := <-terminal:
						t.Fatal("terminal preceded child ownership checkpoint", err)
					default:
					}
					if time.Now().After(deadline) {
						t.Fatal("child never entered")
					}
					time.Sleep(5 * time.Millisecond)
				}
				select {
				case err := <-terminal:
					t.Fatal("terminal preceded owned child/output join", err)
				case <-time.After(25 * time.Millisecond):
				}
				if mode == "cancel" {
					cancel()
				} else {
					unblock()
				}
			}
			select {
			case err := <-terminal:
				if (err == nil) != (mode == "success" || mode == "output_join") {
					t.Fatal("native completion mismatch", mode, err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("native completion missing")
			}
		})
	}
}

func TestPatchPolicyLegacyRunnerParity(t *testing.T) {
	for _, mode := range []string{"success", "start_failed", "exit_failed", "canceled_before_start"} {
		t.Run(mode, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			childMode := "policy-child-result"
			switch mode {
			case "start_failed":
				exe = filepath.Join(t.TempDir(), "missing-command")
			case "exit_failed":
				childMode = "policy-child-fail"
			case "canceled_before_start":
				cancel()
			}
			args := []string{"-test.run=^TestPatchPolicyCommandChild$", "--", childMode, "unused"}
			// Compare against the previous Cmd.Run path using the same harmless child.
			cmd := exec.CommandContext(ctx, exe, args...)
			var stdout, stderr strings.Builder
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			oldErr := cmd.Run()
			got, gotErr := runCommand(ctx, 10*time.Second, exe, args...)
			wantExit := 0
			if oldErr != nil {
				wantExit = -1
				if exitErr, ok := oldErr.(*exec.ExitError); ok {
					wantExit = exitErr.ExitCode()
				}
			}
			if got.Stdout != stdout.String() || got.Stderr != stderr.String() || got.ExitCode != wantExit || fmt.Sprint(gotErr) != fmt.Sprint(oldErr) {
				t.Fatal("legacy command outcome changed", got, gotErr, stdout.String(), stderr.String(), wantExit, oldErr)
			}
		})
	}
}
