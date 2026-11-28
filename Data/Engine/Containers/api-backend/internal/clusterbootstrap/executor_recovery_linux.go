package clusterbootstrap

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

func (e ExecutorIdentity) valid() bool {
	nonce := strings.TrimSuffix(strings.TrimPrefix(e.Unit, "borealis-bootstrap-"), ".service")
	unit, err := ExecutorUnit(nonce)
	return err == nil && e.Unit == unit && e.ControlGroup == "/system.slice/"+unit &&
		sessionMachineID.MatchString(e.InvocationID) && e.InvocationID != strings.Repeat("0", 32) && sessionUUID.MatchString(e.BootID)
}

var executorRecoveryProperties = []string{"Id", "InvocationID", "MainPID", "ControlGroup", "LoadState", "ActiveState", "SubState", "Job", "Restart", "Transient"}

// A missing unit is only one observation. Kernel cgroup emptiness and a stable
// second manager/boot observation are independently required. Never stop/reset
// a unit here: recovery must observe quiescence, not manufacture it.
func executorStopped(prior ExecutorIdentity, boot string, values map[string]string) bool {
	if !prior.valid() || !sessionUUID.MatchString(boot) || len(values) != len(executorRecoveryProperties) {
		return false
	}
	for _, key := range executorRecoveryProperties {
		if _, ok := values[key]; !ok {
			return false
		}
	}
	if values["Id"] != prior.Unit || values["MainPID"] != "0" || values["Job"] != "" || values["Restart"] != "no" {
		return false
	}
	if values["LoadState"] == "not-found" {
		return values["ActiveState"] == "inactive" && values["SubState"] == "dead" && values["InvocationID"] == "" &&
			values["ControlGroup"] == "" && values["Transient"] == "no"
	}
	return boot == prior.BootID && values["LoadState"] == "loaded" && values["Transient"] == "yes" &&
		values["InvocationID"] == prior.InvocationID && (values["ControlGroup"] == "" || values["ControlGroup"] == prior.ControlGroup) &&
		(values["ActiveState"] == "inactive" && values["SubState"] == "dead" || values["ActiveState"] == "failed" && values["SubState"] == "failed")
}

// populated covers descendants recursively; an empty cgroup.procs does not.
// Permit future kernel fields, but reject duplicate, malformed or oversized
// observations rather than treating parse/permission errors as emptiness.
func executorCgroupEmpty(raw []byte) bool {
	if len(raw) > 4096 || !strings.HasSuffix(string(raw), "\n") {
		return false
	}
	seen := map[string]bool{}
	empty := false
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 || seen[parts[0]] || (parts[1] != "0" && parts[1] != "1") {
			return false
		}
		seen[parts[0]] = true
		if parts[0] == "populated" {
			empty = parts[1] == "0"
		}
	}
	return empty
}

func readExecutorBoot() (string, error) {
	raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	boot := strings.TrimSpace(string(raw))
	if err != nil || len(raw) > 64 || !sessionUUID.MatchString(boot) {
		return "", ErrExecutorContainment
	}
	return boot, nil
}

func readStoppedExecutor(ctx context.Context, prior ExecutorIdentity) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", "show", "--no-pager", "--property="+strings.Join(executorRecoveryProperties, ","), "--", prior.Unit)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	output := &boundedOutput{limit: 16 << 10}
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = output, io.Discard, time.Second
	if cmd.Run() != nil {
		return nil, ErrExecutorContainment
	}
	return executorPropertyMapFor(output.Bytes(), executorRecoveryProperties)
}

func readEmptyExecutorCgroup(prior ExecutorIdentity) error {
	const cgroup2Magic = 0x63677270
	root, err := os.OpenRoot("/sys/fs/cgroup")
	if err != nil {
		return ErrExecutorContainment
	}
	defer root.Close()
	parent, err := root.OpenFile("system.slice", os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return ErrExecutorContainment
	}
	defer parent.Close()
	var stat syscall.Statfs_t
	if syscall.Fstatfs(int(parent.Fd()), &stat) != nil || stat.Type != cgroup2Magic {
		return ErrExecutorContainment
	}
	// Pin the directory descriptor, so removal during observation is distinct
	// from an absent events file in an existing/masked/foreign directory.
	descriptor, err := syscall.Openat(int(parent.Fd()), prior.Unit, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err == syscall.ENOENT {
		return nil
	}
	if err != nil {
		return ErrExecutorContainment
	}
	defer syscall.Close(descriptor)
	if syscall.Fstatfs(descriptor, &stat) != nil || stat.Type != cgroup2Magic {
		return ErrExecutorContainment
	}
	eventsFD, err := syscall.Openat(descriptor, "cgroup.events", syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return ErrExecutorContainment
	}
	events := os.NewFile(uintptr(eventsFD), "executor-cgroup-events")
	defer events.Close()
	raw, err := io.ReadAll(io.LimitReader(events, 4097))
	if err != nil || !executorCgroupEmpty(raw) {
		return ErrExecutorContainment
	}
	return nil
}

type executorRecoveryReader struct {
	boot  func() (string, error)
	unit  func(context.Context, ExecutorIdentity) (map[string]string, error)
	empty func(ExecutorIdentity) error
}

func observeExecutorQuiescent(ctx context.Context, prior ExecutorIdentity) error {
	return (executorRecoveryReader{readExecutorBoot, readStoppedExecutor, readEmptyExecutorCgroup}).check(ctx, prior)
}

func (r executorRecoveryReader) check(ctx context.Context, prior ExecutorIdentity) error {
	if !prior.valid() || ctx.Err() != nil {
		return ErrExecutorContainment
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var boot string
	var previous map[string]string
	for pass := 0; pass < 2; pass++ {
		current, err := r.boot()
		if err != nil || !sessionUUID.MatchString(current) || pass > 0 && current != boot {
			return ErrExecutorContainment
		}
		boot = current
		values, err := r.unit(ctx, prior)
		if err != nil || !executorStopped(prior, boot, values) {
			return ErrExecutorContainment
		}
		if pass > 0 {
			for key, value := range previous {
				if values[key] != value {
					return ErrExecutorContainment
				}
			}
		}
		if r.empty(prior) != nil || ctx.Err() != nil {
			return ErrExecutorContainment
		}
		previous = values
	}
	return nil
}
