//go:build windows

package currentuser

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/bunny-lab-io/borealis/go-agent/internal/scripts"
)

// Disposable child of this test binary; no SYSTEM token, active-session launch,
// helper deployment, installed Agent or Engine service is involved.
func TestCurrentUserNativeProcessChild(t *testing.T) {
	mode := os.Args[len(os.Args)-1]
	switch mode {
	case "current-user-child-exit259":
		os.Exit(259)
	case "current-user-child-exit0":
		os.Exit(0)
	case "current-user-child-wait":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
}

func TestCurrentUserNativeOriginalHandleCompletion(t *testing.T) {
	for _, mode := range []string{"exit0", "exit259", "cancel", "termination_race"} {
		t.Run(mode, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			childMode := "exit0"
			if mode == "exit259" {
				childMode = "exit259"
			}
			if mode == "cancel" {
				childMode = "wait"
			}
			app, err := windows.UTF16PtrFromString(exe)
			if err != nil {
				t.Fatal(err)
			}
			command, err := windows.UTF16PtrFromString(windowsCommandLine([]string{exe, "-test.run=^TestCurrentUserNativeProcessChild$", "--", "current-user-child-" + childMode}))
			if err != nil {
				t.Fatal(err)
			}
			startup := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
			var child windows.ProcessInformation
			if err := windows.CreateProcess(app, command, nil, nil, false, windows.CREATE_NO_WINDOW, nil, nil, &startup, &child); err != nil {
				t.Fatal(err)
			}
			var closeOnce sync.Once
			var closed atomic.Bool
			var closeErr error
			closeHandles := func() error {
				closeOnce.Do(func() {
					closeErr = errors.Join(windows.CloseHandle(child.Thread), windows.CloseHandle(child.Process))
					closed.Store(true)
				})
				return closeErr
			}
			t.Cleanup(func() {
				if !closed.Load() {
					_ = windows.TerminateProcess(child.Process, 1)
					_, _ = windows.WaitForSingleObject(child.Process, 5000)
					_ = closeHandles()
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			waits, terminated := 0, 0
			cleaned := false
			process := currentUserProcess{
				wait: func(waitCtx context.Context, timeout int) error {
					waits++
					if mode == "termination_race" && waits == 1 {
						return context.DeadlineExceeded
					}
					return waitForProcess(waitCtx, child.Process, timeout)
				},
				terminate: func() error {
					terminated++
					if mode == "termination_race" {
						event, err := windows.WaitForSingleObject(child.Process, 5000)
						if err != nil || event != windows.WAIT_OBJECT_0 {
							return errors.New("disposable child did not exit")
						}
					}
					return windows.TerminateProcess(child.Process, 1)
				},
				exitCode: func() (uint32, error) {
					var code uint32
					err := windows.GetExitCodeProcess(child.Process, &code)
					return code, err
				},
				capture: func() (string, string, error) { return "disposable original handle", "", nil },
				cleanup: func() error {
					event, err := windows.WaitForSingleObject(child.Process, 0)
					if err != nil || event != windows.WAIT_OBJECT_0 {
						return errors.New("cleanup preceded original handle signal")
					}
					cleaned = true
					return closeHandles()
				},
			}
			out := completeCurrentUserProcess(ctx, 5, process)
			called := false
			accepted, _ := deliverCurrentUserCompletion(out, func(result scripts.Result) {
				called = true
				if !cleaned || !closed.Load() {
					t.Error("native receipt preceded owned handle cleanup")
				}
				if mode == "exit259" && result.ReturnCode != 259 {
					t.Error("signaled259 misclassified")
				}
			})
			if !out.joined || !accepted || !called || !cleaned || closeErr != nil {
				t.Fatal("original native handle completion failed", out, closeErr)
			}
			if mode == "cancel" || mode == "termination_race" {
				if waits != 2 || terminated != 1 || out.result.ReturnCode == 0 {
					t.Fatal("stop request lacked original post-termination wait", waits, terminated, out)
				}
			} else if waits != 1 || terminated != 0 {
				t.Fatal("natural exit changed process path", waits, terminated)
			}
			if strings.Contains(out.result.Stderr, "cleanup preceded") {
				t.Fatal(out.result.Stderr)
			}
		})
	}
}
