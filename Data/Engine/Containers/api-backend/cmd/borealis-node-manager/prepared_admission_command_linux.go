package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Fixed admission commands use synchronous Bash/Python/kubectl descendants.
// Give that command tree its own process group; killing only Bash/Python can
// leave kubectl applying workloads after the journal lock has been released.
// This is not containment for arbitrary programs that deliberately call setsid
// or move into a separate service. Existing systemd actions retain their own
// service/journal reconciliation requirements.
func runPreparedAdmissionCommand(ctx context.Context, workdir, binary string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command := exec.Command(binary, args...)
	command.Dir = workdir
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		return nil, err
	}
	pid := command.Process.Pid
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		case <-stop:
		}
	}()
	// Observe exit without reaping. The original group leader remains ours,
	// preventing PID/PGID reuse until group cleanup and cancellation have joined.
	var waitErr error
	for {
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EINTR) {
			waitErr = errPreparedAdmission
			// Unknown exit is not permission to release the enclosing journal.
			// Retain custody until the kernel can establish original-child exit.
			time.Sleep(100 * time.Millisecond)
		}
	}
	close(stop)
	<-stopped
	waitErr = errors.Join(waitErr, stopPreparedAdmissionGroup(pid, preparedAdmissionGroupLive))
	err := command.Wait() // Reap leader and join output readers only after stop proof.
	return output.Bytes(), errors.Join(err, waitErr, ctx.Err())
}

func stopPreparedAdmissionGroup(pid int, observe func(int) (bool, error)) error {
	var result error
	// A pre-stop snapshot diagnoses escaped descendants, but is never absence
	// proof: a descendant can fork and exit between ReadDir and its stat read.
	if live, err := observe(pid); live || err != nil {
		result = errPreparedAdmission
	}
	// Always stop the original group while its unreaped leader pins the PGID.
	// Even a missed descendant must receive SIGKILL before any no-live snapshot
	// can allow journal release. Python's private child pipes cannot prove this.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		result = errPreparedAdmission
	}
	for {
		live, err := observe(pid)
		if err == nil && !live {
			break
		}
		result = errPreparedAdmission
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		// Cleanup intentionally outlives caller cancellation. If a process is
		// uninterruptible or procfs proof fails, keep the existing journal held;
		// bounded controller reconciliation must remain blocked, never successful.
		time.Sleep(10 * time.Millisecond)
	}
	return result
}

func preparedAdmissionGroupLive(group int) (bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return false, err
		}
		end := strings.LastIndexByte(string(raw), ')')
		if end < 0 {
			return false, errPreparedAdmission
		}
		fields := strings.Fields(string(raw[end+1:]))
		if len(fields) < 3 {
			return false, errPreparedAdmission
		}
		pgid, err := strconv.Atoi(fields[2])
		if err != nil {
			return false, err
		}
		if pgid != group {
			continue
		}
		if len(fields) < 18 {
			return false, errPreparedAdmission
		}
		threads, err := strconv.Atoi(fields[17])
		if err != nil || threads < 1 {
			return false, errPreparedAdmission
		}
		// A zombie leader can retain live sibling threads. Root procfs entries
		// enumerate leaders only, so its state alone cannot prove group exit.
		if threads > 1 || (fields[0] != "Z" && fields[0] != "X") {
			return true, nil
		}
	}
	return false, nil
}
