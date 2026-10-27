package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func bootstrapHostCommandLocks(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	root := t.TempDir()
	host, err := os.OpenFile(filepath.Join(root, "host-lock"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close() })
	identity, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { identity.Close() })
	for _, file := range []*os.File{host, identity} {
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal(err)
		}
	}
	return host, identity
}

func TestBootstrapHostCommandFixedPlan(t *testing.T) {
	for step := bootstrapHostAPTUpdate; step <= bootstrapHostEnableFirewall; step++ {
		spec, err := bootstrapHostCommandPlan(step)
		if err != nil || !filepath.IsAbs(spec.program) || len(spec.args) == 0 || spec.timeout <= 0 || spec.timeout > 2*time.Minute {
			t.Fatal("invalid fixed command", step, err)
		}
		for _, forbidden := range []string{"/bin/sh", "k3s.service", "--allow-unauthenticated", "--allow-remove-essential", "--force-yes", "upgrade", "autoremove"} {
			if spec.program == forbidden || slices.Contains(spec.args, forbidden) {
				t.Fatal("host command broadened", step, forbidden)
			}
		}
		if spec.program == "/usr/bin/apt-get" {
			for _, required := range []string{"APT::Keep-Fds::=3", "APT::Keep-Fds::=4", "DPkg::Lock::Timeout=15", "Acquire::Retries=0", "Dpkg::Use-Pty=0"} {
				if !slices.Contains(spec.args, required) {
					t.Fatal("APT containment missing", required)
				}
			}
			if step != bootstrapHostAPTUpdate && (!slices.Contains(spec.args, "--no-remove") || !slices.Contains(spec.args, "--no-upgrade")) {
				t.Fatal("package action can remove/upgrade unrelated dependencies")
			}
		}
		if step == bootstrapHostInstallDocker && strings.Join(spec.args[len(spec.args)-4:], " ") != "docker-ce docker-ce-cli containerd.io docker-buildx-plugin" {
			t.Fatal("Docker package family diverged")
		}
		spec.args[0] = "foreign"
		again, err := bootstrapHostCommandPlan(step)
		if err != nil || slices.Contains(again.args, "foreign") {
			t.Fatal("command plan aliases caller storage")
		}
	}
	for _, step := range []bootstrapHostCommand{0, 255} {
		if spec, err := bootstrapHostCommandPlan(step); err == nil || spec.program != "" {
			t.Fatal("unknown command accepted")
		}
	}
}

func TestBootstrapHostCommandScope(t *testing.T) {
	for _, mode := range []string{"valid", "initial authority", "initial busy", "command failure", "late authority", "cancel", "leftover child", "unknown cleanup", "nil check", "nil idle", "nil run", "nil abort", "invalid lock", "invalid command"} {
		t.Run(mode, func(t *testing.T) {
			host, identity := bootstrapHostCommandLocks(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			checks, idleCalls, commands, aborts := 0, 0, 0, 0
			check := func(context.Context) error {
				checks++
				if mode == "initial authority" || mode == "late authority" && checks == 3 {
					return errors.New("private authority")
				}
				return nil
			}
			idle := func(ctx context.Context) error {
				idleCalls++
				if idleCalls == 2 && ctx.Err() != nil {
					t.Fatal("cleanup reused cancelled authority context")
				}
				if mode == "initial busy" && idleCalls == 1 || (mode == "leftover child" || mode == "unknown cleanup") && idleCalls == 2 {
					return errors.New("private process inventory")
				}
				return nil
			}
			run := func(ctx context.Context, spec bootstrapHostCommandSpec, hostFD, identityFD *os.File) error {
				commands++
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 2*time.Minute || hostFD != host || identityFD != identity || spec.program != "/usr/bin/apt-get" || checks != 2 || idleCalls != 1 {
					t.Fatal("unfenced or unbounded command")
				}
				if mode == "command failure" {
					return errors.New("private command diagnostic")
				}
				if mode == "cancel" {
					cancel()
				}
				return nil
			}
			abort := func() { aborts++ }
			step := bootstrapHostInstallBase
			switch mode {
			case "nil check":
				check = nil
			case "nil idle":
				idle = nil
			case "nil run":
				run = nil
			case "nil abort":
				abort = nil
			case "invalid lock":
				identity = host
			case "invalid command":
				step = 255
			}
			err := runBootstrapHostCommandWith(ctx, step, host, identity, check, idle, run, abort)
			if (err == nil) != (mode == "valid") || err != nil && err != errBootstrapSource {
				t.Fatal("unsafe command acknowledgement or diagnostic leak", err)
			}
			if (aborts == 1) != (mode == "leftover child" || mode == "unknown cleanup") {
				t.Fatal("unknown descendants did not force stop", aborts)
			}
			if commands > 0 && (commands != 1 || idleCalls != 2) {
				t.Fatal("command replay or omitted cleanup proof", commands, idleCalls)
			}
		})
	}
}

// Child process fixture only; normal package invocation performs no effects.
// It inherits a temporary regular-file lock and directory lock, never host
// journals. No APT, service, module or firewall program runs in these tests.
func TestBootstrapHostCommandChild(t *testing.T) {
	if !slices.Contains(os.Args, "borealis-host-command-fixture") {
		return
	}
	var borrowed []*os.File
	for _, fd := range []int{3, 4} {
		file := os.NewFile(uintptr(fd), "borrowed-fixture-lock")
		borrowed = append(borrowed, file)
		info, err := file.Stat()
		if err != nil || fd == 3 && !info.Mode().IsRegular() || fd == 4 && !info.IsDir() {
			t.Fatal("borrowed descriptor missing")
		}
		path := "/proc/self/fd/3"
		if fd == 4 {
			path = "/proc/self/fd/4"
		}
		other, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != syscall.EWOULDBLOCK {
			other.Close()
			t.Fatal("inherited lock does not exclude independent descriptor", err)
		}
		other.Close()
	}
	if slices.Contains(os.Args, "wait-for-cancellation") {
		time.Sleep(time.Minute)
	}
	runtime.KeepAlive(borrowed)
}

func TestBootstrapHostCommandRealChildLocksAndCancellation(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, cancelChild := range []bool{false, true} {
		host, identity := bootstrapHostCommandLocks(t)
		limit := 5 * time.Second
		args := []string{"-test.run=^TestBootstrapHostCommandChild$", "--", "borealis-host-command-fixture"}
		if cancelChild {
			limit = 100 * time.Millisecond
			args = append(args, "wait-for-cancellation")
		}
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		err := executeBootstrapHostCommand(ctx, bootstrapHostCommandSpec{program: executable, args: args}, host, identity)
		cancel()
		if (err == nil) == cancelChild {
			t.Fatal("child lock or cancellation contract failed", err)
		}
		if _, err := host.Stat(); err != nil {
			t.Fatal("borrowed host lock closed")
		}
		if _, err := identity.Stat(); err != nil {
			t.Fatal("borrowed identity lock closed")
		}
	}
}
