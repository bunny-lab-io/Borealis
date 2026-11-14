package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Reexec only this harmless fixture. The child has private output pipes, just
// like reconcile-node-workloads.py's kubectl child; parent exit does not close
// or join the descendant through the Go runner's CombinedOutput pipes.
func TestPreparedAdmissionCommandProcess(t *testing.T) {
	if len(os.Args) < 4 || os.Args[len(os.Args)-3] != "prepared-admission-child" {
		return
	}
	mode, dir := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
	if mode == "descendant" {
		pidPath := filepath.Join(dir, "descendant")
		if err := os.WriteFile(pidPath+".tmp", []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(2)
		}
		// Both PID readers and the orphan's existence check need complete content.
		if err := os.Rename(pidPath+".tmp", pidPath); err != nil {
			os.Exit(2)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(dir, "release")); err == nil {
				if os.WriteFile(filepath.Join(dir, "effect"), []byte("late workload apply"), 0600) != nil {
					os.Exit(3)
				}
				os.Exit(0)
			}
			time.Sleep(10 * time.Millisecond)
		}
		os.Exit(4)
	}
	if mode == "complete" {
		fmt.Print("complete")
		os.Exit(0)
	}
	if mode == "failure" {
		fmt.Fprint(os.Stderr, "failed")
		os.Exit(7)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestPreparedAdmissionCommandProcess$", "--", "prepared-admission-child", "descendant", dir)
	var out, diagnostic bytes.Buffer
	child.Stdout, child.Stderr = &out, &diagnostic
	if child.Start() != nil {
		os.Exit(5)
	}
	if mode == "orphan" {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(dir, "descendant")); err == nil {
				os.Exit(0)
			}
			time.Sleep(10 * time.Millisecond)
		}
		os.Exit(6)
	}
	if child.Wait() != nil {
		os.Exit(8)
	}
	os.Exit(0)
}

func TestPreparedAdmissionCommandStopsDescendants(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"cancel", "orphan", "legacy direct-child control"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx = context.WithValue(ctx, preparedAdmissionCheckKey{}, func(ctx context.Context) error { return ctx.Err() })
			// The legacy control proves this fixture catches the original bug,
			// and confirms unrelated unguarded command behavior stays unchanged.
			if mode == "legacy direct-child control" {
				ctx = context.WithValue(ctx, preparedAdmissionCheckKey{}, nil)
			}
			childMode := "parent"
			if mode == "orphan" {
				childMode = "orphan"
			}
			done := make(chan error, 1)
			go func() {
				_, err := run(ctx, dir, executable, "-test.run=^TestPreparedAdmissionCommandProcess$", "--", "prepared-admission-child", childMode, dir)
				done <- err
			}()
			t.Cleanup(func() { _ = os.WriteFile(filepath.Join(dir, "release"), nil, 0600) })
			var pid int
			for pid == 0 {
				raw, err := os.ReadFile(filepath.Join(dir, "descendant"))
				if err == nil {
					pid, err = strconv.Atoi(string(raw))
					if err != nil {
						t.Fatal(err)
					}
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("child never started")
				case <-time.After(10 * time.Millisecond):
				}
			}
			pidfd, err := unix.PidfdOpen(pid, 0)
			if err != nil {
				// An orphan may already be reaped after production cleanup.
				if mode != "orphan" || !errors.Is(err, unix.ESRCH) {
					t.Fatal(err)
				}
				pidfd = -1
			} else {
				defer unix.Close(pidfd)
			}
			if mode != "orphan" {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancelled or escaped command accepted")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("command did not join stop")
			}
			if pidfd >= 0 {
				fds := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
				_, err = unix.Poll(fds, 0)
				if err != nil {
					t.Fatal(err)
				}
				stopped := fds[0].Revents&unix.POLLIN != 0
				if stopped == (mode == "legacy direct-child control") {
					t.Fatal("descendant stop proof", stopped)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			if mode == "legacy direct-child control" {
				fds := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
				if _, err := pollPreparedAdmissionCommandWait(fds, 2000); err != nil || fds[0].Revents&unix.POLLIN == 0 {
					t.Fatal("legacy control child did not finish", err)
				}
			}
			_, err = os.Stat(filepath.Join(dir, "effect"))
			if (err == nil) != (mode == "legacy direct-child control") {
				t.Fatal("late effect result", err)
			}
		})
	}
}

func pollPreparedAdmissionCommandWait(fds []unix.PollFd, timeout int) (int, error) {
	deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
	for {
		count, err := unix.Poll(fds, timeout)
		if !errors.Is(err, unix.EINTR) {
			return count, err
		}
		// Signals may interrupt a wait; retries retain its original deadline.
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, nil
		}
		timeout = int(remaining / time.Millisecond)
	}
}

func TestPreparedAdmissionCommandResults(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"complete", "failure", "cancel before", "start failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = context.WithValue(ctx, preparedAdmissionCheckKey{}, func(ctx context.Context) error { return ctx.Err() })
			binary := executable
			if mode == "cancel before" {
				cancel()
			}
			if mode == "start failure" {
				binary = filepath.Join(t.TempDir(), "absent")
			}
			out, err := run(ctx, t.TempDir(), binary, "-test.run=^TestPreparedAdmissionCommandProcess$", "--", "prepared-admission-child", mode, t.TempDir())
			if (err == nil) != (mode == "complete") {
				t.Fatal("command result", out, err)
			}
			if mode == "complete" && out != "complete" || mode == "failure" && out != "failed" {
				t.Fatal("command output", out)
			}
		})
	}
}

func TestPreparedAdmissionCommandStaleProcessSnapshot(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	command := exec.Command(executable, "-test.run=^TestPreparedAdmissionCommandProcess$", "--", "prepared-admission-child", "orphan", dir)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	// Retain the real exited leader exactly as production does. The descendant
	// still waits to apply its delayed effect with output private to its parent.
	var info unix.Siginfo
	if err := unix.Waitid(unix.P_PID, command.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		_ = command.Wait()
	})
	raw, err := os.ReadFile(filepath.Join(dir, "descendant"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pidfd)
	calls := 0
	_ = stopPreparedAdmissionGroup(command.Process.Pid, func(group int) (bool, error) {
		calls++
		if calls == 1 {
			// Simulate a /proc listing whose known parent exited and whose newly
			// forked descendant was absent from the initial directory snapshot.
			return false, nil
		}
		if calls == 2 {
			fds := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
			if _, err := pollPreparedAdmissionCommandWait(fds, 1000); err != nil || fds[0].Revents&unix.POLLIN == 0 {
				t.Error("group kill did not precede final absence scan", err)
			}
		}
		return preparedAdmissionGroupLive(group)
	})
	if calls < 2 {
		t.Fatal("pre-stop snapshot was accepted as final absence proof")
	}
	fds := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
	if _, err := unix.Poll(fds, 0); err != nil || fds[0].Revents&unix.POLLIN == 0 {
		t.Fatal("missed descendant survived group stop", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "effect")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missed descendant applied late effect", err)
	}
}

func TestPreparedAdmissionCommandZombieLeaderRetainsLiveThread(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python 3 is required for the native thread fixture:", err)
	}
	dir := t.TempDir()
	// pthread_exit terminates only the main thread, without an architecture-
	// specific syscall number. Its sibling remains a real potential producer.
	command := exec.Command(python, "-c", `
import ctypes
import pathlib
import sys
import threading
import time

root = pathlib.Path(sys.argv[1])
ready = threading.Event()

def producer():
    ready.set()
    while not (root / "release").exists():
        time.sleep(0.01)
    (root / "effect").write_text("late workload apply")

threading.Thread(target=producer).start()
ready.wait()
libc = ctypes.CDLL(None)
libc.pthread_exit.argtypes = [ctypes.c_void_p]
libc.pthread_exit.restype = None
libc.pthread_exit(None)
`, dir)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	pid := command.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = command.Wait()
	})
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pidfd)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			t.Fatal(err)
		}
		end := strings.LastIndexByte(string(raw), ')')
		fields := strings.Fields(string(raw[end+1:]))
		if end >= 0 && len(fields) >= 18 && fields[0] == "Z" && fields[17] == "2" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("fixture did not retain a zombie leader and live sibling: %s", raw)
		case <-time.After(10 * time.Millisecond):
		}
	}
	fds := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
	if _, err := unix.Poll(fds, 0); err != nil || fds[0].Revents&unix.POLLIN != 0 {
		t.Fatal("fixture did not retain a live thread group", err)
	}
	if live, err := preparedAdmissionGroupLive(pid); err != nil || !live {
		t.Fatalf("zombie leader hid live producer: live=%v err=%v", live, err)
	}
	if err := stopPreparedAdmissionGroup(pid, preparedAdmissionGroupLive); !errors.Is(err, errPreparedAdmission) {
		t.Fatal("escaped live producer was not rejected", err)
	}
	if _, err := unix.Poll(fds, 0); err != nil || fds[0].Revents&unix.POLLIN == 0 {
		t.Fatal("stop proof returned before every thread exited", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "effect")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stopped thread applied late effect", err)
	}
}
