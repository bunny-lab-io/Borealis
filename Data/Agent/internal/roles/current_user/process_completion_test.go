package currentuser

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/scripts"
)

func TestCurrentUserProcessCompletionEvidence(t *testing.T) {
	for _, mode := range []string{"success", "nonzero", "exit259", "timeout", "cancel", "terminate_race", "unknown_wait", "exit_query", "capture", "cleanup"} {
		t.Run(mode, func(t *testing.T) {
			var order []string
			waits := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			process := currentUserProcess{
				wait: func(waitCtx context.Context, timeout int) error {
					waits++
					order = append(order, "wait")
					if waits == 1 {
						if waitCtx != ctx || timeout != 7 {
							t.Fatal("original wait inputs changed")
						}
						switch mode {
						case "timeout", "terminate_race", "unknown_wait":
							return errors.New("initial wait unavailable")
						case "cancel":
							cancel()
							return ctx.Err()
						}
					} else {
						if waitCtx.Err() != nil || waitCtx == ctx || timeout != 0 {
							t.Fatal("post-terminate wait reused execution cancellation")
						}
						if mode == "unknown_wait" {
							return errors.New("original handle never signaled")
						}
					}
					return nil
				},
				terminate: func() error {
					order = append(order, "terminate")
					if mode == "terminate_race" {
						return errors.New("already exited")
					}
					return nil
				},
				exitCode: func() (uint32, error) {
					order = append(order, "exit")
					if mode == "exit_query" {
						return 0, errors.New("query failed")
					}
					if mode == "exit259" {
						return 259, nil
					}
					if mode == "nonzero" {
						return 2, nil
					}
					return 0, nil
				},
				capture: func() (string, string, error) {
					order = append(order, "capture")
					if mode == "capture" {
						return "", "", errors.New("capture failed")
					}
					return "original output", "", nil
				},
				cleanup: func() error {
					order = append(order, "cleanup")
					if mode == "cleanup" {
						return errors.New("cleanup failed")
					}
					return nil
				},
			}
			out := completeCurrentUserProcess(ctx, 7, process)
			called := 0
			accepted, detail := deliverCurrentUserCompletion(out, func(result scripts.Result) {
				called++
				order = append(order, "callback")
				if result != out.result {
					t.Fatal("callback changed result")
				}
			})
			if !accepted || detail != "" {
				t.Fatal("launched outcome became prework rejection")
			}
			if mode == "unknown_wait" {
				if out.joined || called != 0 || !reflect.DeepEqual(order, []string{"wait", "terminate", "wait", "cleanup"}) {
					t.Fatal("unknown Wait gained terminal proof", order, out)
				}
				return
			}
			if !out.joined || called != 1 || order[len(order)-2] != "cleanup" {
				t.Fatal("terminal preceded owned cleanup", order, out)
			}
			if mode == "success" && out.result.ReturnCode != 0 || mode != "success" && out.result.ReturnCode == 0 {
				t.Fatal("result status lost", mode, out)
			}
			if mode == "exit259" && out.result.ReturnCode != 259 {
				t.Fatal("signaled exit259 became liveness evidence")
			}
		})
	}
}

func TestCurrentUserProcessJoinAndCleanupBlockTerminal(t *testing.T) {
	for _, blocked := range []string{"join", "cleanup"} {
		t.Run(blocked, func(t *testing.T) {
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer func() { unblock(); <-done }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cancel()
			terminal := make(chan struct{}, 1)
			waits := 0
			process := currentUserProcess{
				wait: func(waitCtx context.Context, _ int) error {
					waits++
					if waits == 1 {
						return ctx.Err()
					}
					if waitCtx.Err() != nil {
						return waitCtx.Err()
					}
					if blocked == "join" {
						close(entered)
						<-release
					}
					return nil
				},
				terminate: func() error { return nil }, exitCode: func() (uint32, error) { return 1, nil }, capture: func() (string, string, error) { return "", "", nil },
				cleanup: func() error {
					if blocked == "cleanup" {
						close(entered)
						<-release
					}
					return nil
				},
			}
			go func() {
				defer close(done)
				out := completeCurrentUserProcess(ctx, 1, process)
				deliverCurrentUserCompletion(out, func(scripts.Result) { terminal <- struct{}{} })
			}()
			select {
			case <-entered:
			case <-terminal:
				t.Fatal("terminal preceded owned join/cleanup checkpoint")
			case <-time.After(3 * time.Second):
				t.Fatal("missing join/cleanup checkpoint")
			}
			select {
			case <-terminal:
				t.Fatal("premature terminal while owned work blocked")
			case <-time.After(25 * time.Millisecond):
			}
			unblock()
			select {
			case <-terminal:
			case <-time.After(3 * time.Second):
				t.Fatal("joined terminal missing")
			}
		})
	}
}

func TestCurrentUserPrelaunchRejectionNeverCallsCompletion(t *testing.T) {
	called := false
	accepted, detail := deliverCurrentUserCompletion(currentUserProcessCompletion{detail: "no interactive session"}, func(scripts.Result) { called = true })
	if accepted || detail != "no interactive session" || called {
		t.Fatal("prelaunch outcome changed", accepted, detail, called)
	}
}
