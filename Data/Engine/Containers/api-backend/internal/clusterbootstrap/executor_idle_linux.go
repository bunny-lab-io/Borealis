package clusterbootstrap

import (
	"context"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ObserveExecutorIdle proves that this exact supervised helper is the sole
// process in its cgroup, with no child/dying cgroups. Use only at joined command
// boundaries, never as authority or as a substitute for prior-executor proof.
// It changes no service or process state, even after caller cancellation.
func ObserveExecutorIdle(ctx context.Context, identity ExecutorIdentity) error {
	nonce := strings.TrimSuffix(strings.TrimPrefix(identity.Unit, "borealis-bootstrap-"), ".service")
	return observeExecutorIdleWith(ctx, identity, func(ctx context.Context) (ExecutorIdentity, error) {
		return currentExecutor(ctx, nonce)
	}, func(identity ExecutorIdentity) error {
		return readIdleExecutorCgroup(identity, os.Getpid())
	})
}

func observeExecutorIdleWith(ctx context.Context, identity ExecutorIdentity, current func(context.Context) (ExecutorIdentity, error), idle func(ExecutorIdentity) error) error {
	if !identity.valid() || ctx.Err() != nil || current == nil || idle == nil {
		return ErrExecutorContainment
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for pass := 0; pass < 2; pass++ {
		actual, err := current(ctx)
		if err != nil || actual != identity || ctx.Err() != nil || idle(identity) != nil || ctx.Err() != nil {
			return ErrExecutorContainment
		}
	}
	return nil
}

func executorCgroupIdle(procs, stats []byte, pid int) bool {
	if pid <= 1 || string(procs) != strconv.Itoa(pid)+"\n" || len(stats) == 0 || len(stats) > 4096 || stats[len(stats)-1] != '\n' {
		return false
	}
	values := map[string]uint64{}
	for _, line := range strings.Split(string(stats[:len(stats)-1]), "\n") {
		key, text, ok := strings.Cut(line, " ")
		value, err := strconv.ParseUint(text, 10, 64)
		if !ok || key == "" || err != nil || strconv.FormatUint(value, 10) != text {
			return false
		}
		for _, value := range key {
			if value != '_' && (value < 'a' || value > 'z') && (value < '0' || value > '9') {
				return false
			}
		}
		if _, exists := values[key]; exists {
			return false
		}
		values[key] = value
	}
	descendants, present := values["nr_descendants"]
	dying, dyingPresent := values["nr_dying_descendants"]
	return present && dyingPresent && descendants == 0 && dying == 0
}

func readIdleExecutorCgroup(identity ExecutorIdentity, pid int) error {
	if !identity.valid() || pid <= 1 {
		return ErrExecutorContainment
	}
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
	var fs syscall.Statfs_t
	if syscall.Fstatfs(int(parent.Fd()), &fs) != nil || fs.Type != 0x63677270 {
		return ErrExecutorContainment
	}
	return readIdleExecutorDirectory(parent, identity, pid, true)
}

// Parent is the verified cgroup2 system.slice descriptor in production. Tests
// use an isolated ordinary directory; no production filesystem override exists.
func readIdleExecutorDirectory(parent *os.File, identity ExecutorIdentity, pid int, requireCgroup bool) error {
	if parent == nil || !identity.valid() || pid <= 1 {
		return ErrExecutorContainment
	}
	fd, err := syscall.Openat(int(parent.Fd()), identity.Unit, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return ErrExecutorContainment
	}
	defer syscall.Close(fd)
	if requireCgroup {
		var fs syscall.Statfs_t
		if syscall.Fstatfs(fd, &fs) != nil || fs.Type != 0x63677270 {
			return ErrExecutorContainment
		}
	}
	read := func(name string, limit int64) ([]byte, error) {
		child, err := syscall.Openat(fd, name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
		if err != nil {
			return nil, ErrExecutorContainment
		}
		file := os.NewFile(uintptr(child), "executor-idle-observation")
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			file.Close()
			return nil, ErrExecutorContainment
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, limit+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || int64(len(raw)) > limit {
			return nil, ErrExecutorContainment
		}
		return raw, nil
	}
	// Repeat both files from the same pinned cgroup. A disappearing/unknown
	// attribute is never interpreted as an idle executor.
	for pass := 0; pass < 2; pass++ {
		procs, err := read("cgroup.procs", 64)
		if err != nil {
			return ErrExecutorContainment
		}
		stats, err := read("cgroup.stat", 4096)
		if err != nil || !executorCgroupIdle(procs, stats, pid) {
			return ErrExecutorContainment
		}
	}
	return nil
}
