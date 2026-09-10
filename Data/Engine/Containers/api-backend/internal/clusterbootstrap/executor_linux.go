package clusterbootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrExecutorContainment = errors.New("bootstrap executor supervision unavailable or inconsistent")

const ExecutorWatchdogWindow = 2 * time.Second

// ExecutorIdentity is public process ownership, not permission to mutate or
// proof that a previous executor has stopped. Future journal dispatch must
// persist this identity before effects and independently observe quiescence.
type ExecutorIdentity struct {
	Unit         string `json:"unit"`
	InvocationID string `json:"invocation_id"`
	BootID       string `json:"boot_id"`
	ControlGroup string `json:"control_group"`
}

func ExecutorUnit(nonce string) (string, error) {
	if !digestPattern.MatchString(nonce) {
		return "", ErrExecutorContainment
	}
	return "borealis-bootstrap-" + nonce + ".service", nil
}

// ExecutorArguments launches only the same already root-copied/verified helper.
// No command, environment or property comes from the session request. --pipe
// carries the existing private stdin protocol; no credential enters the unit
// command line, environment or journal. systemd-run waits through unit cleanup.
func ExecutorArguments(executable, nonce string) ([]string, error) {
	unit, err := ExecutorUnit(nonce)
	if err != nil || !filepath.IsAbs(executable) || filepath.Clean(executable) != executable ||
		strings.ContainsAny(executable, "\x00\r\n$") {
		return nil, ErrExecutorContainment
	}
	return []string{"--quiet", "--pipe", "--wait", "--collect", "--service-type=notify", "--slice=system.slice", "--unit=" + unit,
		"--property=Restart=no", "--property=KillMode=control-group", "--property=SendSIGKILL=yes", "--property=FinalKillSignal=SIGKILL",
		"--property=TimeoutStartSec=20s", "--property=TimeoutStopSec=2s", "--property=RuntimeMaxSec=6min",
		"--property=WatchdogSec=2s", "--property=WatchdogSignal=SIGKILL", "--property=NotifyAccess=main",
		"--property=LimitCORE=0", "--property=UMask=0077", "--property=Delegate=no", "--property=ProtectControlGroups=yes",
		"--", executable, "bootstrap-session-contained", "--nonce", nonce}, nil
}

var executorProperties = []string{"Id", "InvocationID", "MainPID", "ControlGroup", "LoadState", "ActiveState", "SubState", "Transient",
	"Type", "NotifyAccess", "KillMode", "SendSIGKILL", "FinalKillSignal", "WatchdogSignal", "WatchdogUSec", "TimeoutStopUSec", "RuntimeMaxUSec", "Restart", "Delegate", "ProtectControlGroups"}

func executorPropertyMap(raw []byte) (map[string]string, error) {
	if len(raw) > 16<<10 || bytes.ContainsRune(raw, '\x00') {
		return nil, ErrExecutorContainment
	}
	values := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, ErrExecutorContainment
		}
		if _, exists := values[key]; exists {
			return nil, ErrExecutorContainment
		}
		values[key] = value
	}
	if len(values) != len(executorProperties) {
		return nil, ErrExecutorContainment
	}
	for _, name := range executorProperties {
		if _, exists := values[name]; !exists {
			return nil, ErrExecutorContainment
		}
	}
	return values, nil
}

func verifyExecutorIdentity(nonce, invocation, boot, cgroup string, pid int, values map[string]string) (ExecutorIdentity, error) {
	unit, err := ExecutorUnit(nonce)
	invalid := ExecutorIdentity{}
	if err != nil || pid < 1 || !sessionMachineID.MatchString(invocation) || invocation == strings.Repeat("0", 32) || !sessionUUID.MatchString(boot) {
		return invalid, ErrExecutorContainment
	}
	group := "/system.slice/" + unit
	if cgroup != "0::"+group+"\n" {
		return invalid, ErrExecutorContainment
	}
	for key, want := range map[string]string{"Id": unit, "InvocationID": invocation, "MainPID": strconv.Itoa(pid), "ControlGroup": group,
		"LoadState": "loaded", "Transient": "yes", "Type": "notify", "NotifyAccess": "main", "KillMode": "control-group", "SendSIGKILL": "yes",
		"FinalKillSignal": "9", "WatchdogSignal": "9", "WatchdogUSec": "2s", "TimeoutStopUSec": "2s", "RuntimeMaxUSec": "6min", "Restart": "no", "Delegate": "no", "ProtectControlGroups": "yes"} {
		if values[key] != want {
			return invalid, ErrExecutorContainment
		}
	}
	if !(values["ActiveState"] == "activating" && values["SubState"] == "start" || values["ActiveState"] == "active" && values["SubState"] == "running") {
		return invalid, ErrExecutorContainment
	}
	return ExecutorIdentity{unit, invocation, boot, group}, nil
}

func currentExecutor(ctx context.Context, nonce string) (ExecutorIdentity, error) {
	invalid := ExecutorIdentity{}
	unit, err := ExecutorUnit(nonce)
	if err != nil || os.Getenv("NOTIFY_SOCKET") != "/run/systemd/notify" || os.Getenv("WATCHDOG_USEC") != "2000000" ||
		(os.Getenv("WATCHDOG_PID") != "" && os.Getenv("WATCHDOG_PID") != strconv.Itoa(os.Getpid())) {
		return invalid, ErrExecutorContainment
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/systemctl", "show", "--no-pager", "--property="+strings.Join(executorProperties, ","), "--", unit)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	output := &boundedOutput{limit: 16 << 10}
	command.Stdout, command.Stderr = output, io.Discard
	command.WaitDelay = time.Second
	if command.Run() != nil {
		return invalid, ErrExecutorContainment
	}
	values, err := executorPropertyMap(output.Bytes())
	if err != nil {
		return invalid, err
	}
	// These kernel-generated public files are small; never read host secrets.
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || len(boot) > 64 {
		return invalid, ErrExecutorContainment
	}
	cgroup, err := os.ReadFile("/proc/self/cgroup")
	if err != nil || len(cgroup) > 1024 {
		return invalid, ErrExecutorContainment
	}
	return verifyExecutorIdentity(nonce, os.Getenv("INVOCATION_ID"), strings.TrimSpace(string(boot)), string(cgroup), os.Getpid(), values)
}

// ExecutorGuard feeds systemd only while the session's current authority
// deadline is valid. A stuck process stops feeding its independent watchdog;
// main-process exit/crash triggers control-group cleanup, including setsid
// descendants. This is containment, not a read-only absence reconciliation.
type ExecutorGuard struct {
	mu       sync.Mutex
	ctx      context.Context
	deadline time.Time
	ready    bool
	notify   func(string) error
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewExecutorGuard(ctx context.Context, nonce string) (*ExecutorGuard, ExecutorIdentity, error) {
	identity, err := currentExecutor(ctx, nonce)
	if err != nil {
		return nil, ExecutorIdentity{}, err
	}
	guard := newExecutorGuard(ctx, notifyExecutor, 500*time.Millisecond)
	return guard, identity, nil
}

func newExecutorGuard(ctx context.Context, notify func(string) error, cadence time.Duration) *ExecutorGuard {
	ctx, cancel := context.WithCancel(ctx)
	guard := &ExecutorGuard{ctx: ctx, notify: notify, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(guard.done)
		ticker := time.NewTicker(cadence)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				guard.mu.Lock()
				ready, valid := guard.ready, time.Now().Before(guard.deadline)
				var err error
				if ready && valid {
					err = notify("WATCHDOG=1")
				}
				guard.mu.Unlock()
				if ready && (!valid || err != nil) {
					cancel()
					return
				}
			}
		}
	}()
	return guard
}

func (g *ExecutorGuard) ObserveDeadline(deadline time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.done:
		return ErrExecutorContainment
	default:
	}
	if g.ctx.Err() != nil || g.ready && !time.Now().Before(g.deadline) || !time.Now().Before(deadline) || time.Until(deadline) > SessionLeaseWindow {
		return ErrExecutorContainment
	}
	g.deadline = deadline
	if !g.ready {
		if g.notify("READY=1\nWATCHDOG=1") != nil {
			g.cancel()
			return ErrExecutorContainment
		}
		g.ready = true
	}
	return nil
}

func (g *ExecutorGuard) Close() {
	g.cancel()
	<-g.done
}

func notifyExecutor(message string) error {
	connection, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: "/run/systemd/notify", Net: "unixgram"})
	if err != nil {
		return ErrExecutorContainment
	}
	defer connection.Close()
	if connection.SetWriteDeadline(time.Now().Add(250*time.Millisecond)) != nil {
		return ErrExecutorContainment
	}
	if _, err := connection.Write([]byte(message)); err != nil {
		return ErrExecutorContainment
	}
	return nil
}
