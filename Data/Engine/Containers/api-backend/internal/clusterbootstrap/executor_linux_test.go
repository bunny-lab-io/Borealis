package clusterbootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func executorFixtureValues() map[string]string {
	unit, _ := ExecutorUnit(sessionFixture().Nonce)
	return map[string]string{"Id": unit, "InvocationID": strings.Repeat("a", 32), "MainPID": "123", "ControlGroup": "/system.slice/" + unit,
		"LoadState": "loaded", "ActiveState": "activating", "SubState": "start", "Transient": "yes", "Type": "notify", "NotifyAccess": "main",
		"KillMode": "control-group", "SendSIGKILL": "yes", "FinalKillSignal": "9", "WatchdogSignal": "9", "WatchdogUSec": "2s",
		"TimeoutStopUSec": "2s", "RuntimeMaxUSec": "6min", "Restart": "no", "Delegate": "no", "ProtectControlGroups": "yes"}
}

func TestExecutorIdentityRequiresActualSupervision(t *testing.T) {
	for _, changed := range append([]string{"valid", "active", "wrong cgroup", "wrong invocation", "wrong boot", "wrong pid"}, executorProperties...) {
		t.Run(changed, func(t *testing.T) {
			values := executorFixtureValues()
			invocation, boot, group, pid := strings.Repeat("a", 32), "11111111-1111-4111-8111-111111111111", "0::"+values["ControlGroup"]+"\n", 123
			switch changed {
			case "valid":
			case "active":
				values["ActiveState"], values["SubState"] = "active", "running"
			case "wrong cgroup":
				group = "0::/user.slice/unrelated.service\n"
			case "wrong invocation":
				invocation = strings.Repeat("b", 32)
			case "wrong boot":
				boot = "unknown"
			case "wrong pid":
				pid++
			default:
				values[changed] = "unsafe"
			}
			proof, err := verifyExecutorIdentity(sessionFixture().Nonce, invocation, boot, group, pid, values)
			if changed == "valid" || changed == "active" {
				if err != nil || proof.Unit != values["Id"] || proof.InvocationID != invocation || proof.BootID != boot {
					t.Fatalf("valid supervision refused: %v", err)
				}
			} else if !errors.Is(err, ErrExecutorContainment) || proof != (ExecutorIdentity{}) {
				t.Fatal("unsafe process received containment proof")
			}
		})
	}
}

func TestExecutorPropertiesRejectPartialAmbiguousOrOversizedOutput(t *testing.T) {
	var lines []string
	for _, key := range executorProperties {
		lines = append(lines, key+"="+executorFixtureValues()[key])
	}
	raw := strings.Join(lines, "\n") + "\n"
	if _, err := executorPropertyMap([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	for _, broken := range []string{raw + lines[0] + "\n", raw + "Extra=value\n", strings.Join(lines[1:], "\n"), "broken", strings.Repeat("x", 16<<10+1), raw + "\x00"} {
		if _, err := executorPropertyMap([]byte(broken)); err == nil {
			t.Fatal("untrusted property response accepted")
		}
	}
}

func TestExecutorLaunchUsesFixedPrivateStreamContract(t *testing.T) {
	args, err := ExecutorArguments("/var/tmp/borealis-bootstrap-root.ABC1234567/manager", sessionFixture().Nonce)
	if err != nil {
		t.Fatal(err)
	}
	command := strings.Join(args, "\n")
	for _, required := range []string{"--pipe", "--wait", "--collect", "--service-type=notify", "--property=KillMode=control-group", "--property=WatchdogSec=2s", "--property=WatchdogSignal=SIGKILL", "--property=TimeoutStopSec=2s", "--property=RuntimeMaxSec=6min", "--property=LimitCORE=0", "bootstrap-session-contained"} {
		if !strings.Contains(command, required+"\n") {
			t.Fatalf("missing lifecycle boundary %s", required)
		}
	}
	if args[len(args)-1] != sessionFixture().Nonce || strings.Contains(command, "--setenv") || strings.Contains(command, "--scope") || strings.Contains(command, "/bin/sh") {
		t.Fatal("launcher changed stdin/environment/service contract")
	}
	for _, path := range []string{"manager", "/var/tmp/../manager", "/var/tmp/$MANAGER", "/var/tmp/a\nmanager", "/var/tmp/manager\x00"} {
		if _, err := ExecutorArguments(path, sessionFixture().Nonce); err == nil {
			t.Fatal("unsafe executable path accepted")
		}
	}
	if _, err := ExecutorArguments("/manager", "../other.service"); err == nil {
		t.Fatal("unsafe unit identity accepted")
	}
}

func TestExecutorGuardExpiresAndStopsPinging(t *testing.T) {
	for _, mode := range []string{"expiry", "cancel", "notify failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var mu sync.Mutex
			var messages []string
			guard := newExecutorGuard(ctx, func(message string) error {
				mu.Lock()
				defer mu.Unlock()
				messages = append(messages, message)
				if mode == "notify failure" && len(messages) > 1 {
					return errors.New("private socket diagnostic")
				}
				return nil
			}, 5*time.Millisecond)
			defer guard.Close()
			if err := guard.ObserveDeadline(time.Now().Add(80 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			if mode == "cancel" {
				cancel()
			}
			select {
			case <-guard.done:
			case <-time.After(time.Second):
				t.Fatal("expired/cancelled/failed guard continued")
			}
			mu.Lock()
			count := len(messages)
			if messages[0] != "READY=1\nWATCHDOG=1" {
				t.Error("service readiness not explicitly armed")
			}
			mu.Unlock()
			if err := guard.ObserveDeadline(time.Now().Add(time.Second)); err == nil {
				t.Fatal("dead supervisor accepted another grant")
			}
			time.Sleep(15 * time.Millisecond)
			mu.Lock()
			defer mu.Unlock()
			if len(messages) != count {
				t.Fatal("watchdog continued after guard stopped")
			}
		})
	}
}

func TestExecutorGuardRejectsInvalidDeadlinesBeforeReady(t *testing.T) {
	calls := 0
	guard := newExecutorGuard(context.Background(), func(string) error { calls++; return nil }, time.Hour)
	defer guard.Close()
	for _, deadline := range []time.Time{{}, time.Now().Add(-time.Second), time.Now().Add(SessionLeaseWindow + time.Second)} {
		if guard.ObserveDeadline(deadline) == nil || calls != 0 {
			t.Fatal("invalid deadline armed watchdog")
		}
	}
}

// Tier3 helper only. Ordinary portable runs return without using systemd.
// Explicit lab runner starts this test binary unprivileged in a disposable
// transient unit. Only its validation directory is touched. The setsid child
// closes inherited lock FD and ignores TERM; cgroup SIGKILL must contain it.
func TestExecutorServiceFixture(t *testing.T) {
	mode := os.Getenv("BOREALIS_EXECUTOR_FIXTURE")
	if mode == "" {
		return
	}
	if mode == "arguments" {
		args, err := ExecutorArguments(os.Getenv("BOREALIS_EXECUTOR_BINARY"), os.Getenv("BOREALIS_EXECUTOR_NONCE"))
		if err != nil || json.NewEncoder(os.Stdout).Encode(args) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Geteuid() == 0 {
		t.Fatal("isolated fixture must run unprivileged")
	}
	root := os.Getenv("BOREALIS_EXECUTOR_FIXTURE_DIR")
	if !filepath.IsAbs(root) || !strings.Contains(root, "s01-executor-containment-") {
		t.Fatal("fixture path invalid")
	}
	if mode == "probe" {
		raw, err := os.ReadFile(filepath.Join(root, "started.json"))
		var started struct {
			Identity ExecutorIdentity `json:"identity"`
		}
		if err != nil || json.Unmarshal(raw, &started) != nil || !started.Identity.valid() {
			t.Fatal("fixture identity missing")
		}
		quiescent := observeExecutorQuiescent(context.Background(), started.Identity) == nil
		if json.NewEncoder(os.Stdout).Encode(map[string]bool{"quiescent": quiescent}) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if mode == "descendant" {
		if os.Getenv("BOREALIS_EXECUTOR_IGNORE_TERM") == "1" {
			signal.Ignore(syscall.SIGTERM)
		}
		if _, err := syscall.Setsid(); err != nil {
			t.Fatal(err)
		}
		_ = syscall.Close(3)
		if err := os.WriteFile(filepath.Join(root, "descendant.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			t.Fatal(err)
		}
		for {
			if err := os.WriteFile(filepath.Join(root, "descendant-heartbeat"), []byte(strconv.FormatInt(time.Now().UnixNano(), 10)), 0o600); err != nil {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	if mode != "normal" && mode != "orphan-timeout" && mode != "crash" && mode != "watchdog" && mode != "lease-expiry" {
		t.Fatal("fixture mode invalid")
	}
	unit, _ := ExecutorUnit(os.Getenv("BOREALIS_EXECUTOR_NONCE"))
	properties, _ := exec.Command("/usr/bin/systemctl", "show", "--property="+strings.Join(executorProperties, ","), "--", unit).Output()
	group, _ := os.ReadFile("/proc/self/cgroup")
	diagnostic, _ := json.Marshal(map[string]any{"properties": string(properties), "cgroup": string(group), "pid": os.Getpid(),
		"notify_socket": os.Getenv("NOTIFY_SOCKET"), "watchdog_usec": os.Getenv("WATCHDOG_USEC"), "watchdog_pid": os.Getenv("WATCHDOG_PID"), "invocation_id": os.Getenv("INVOCATION_ID")})
	if os.WriteFile(filepath.Join(root, "public-context.json"), diagnostic, 0o600) != nil {
		t.Fatal("fixture public context failed")
	}
	guard, identity, err := NewExecutorGuard(context.Background(), os.Getenv("BOREALIS_EXECUTOR_NONCE"))
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	if err := guard.ObserveDeadline(time.Now().Add(SessionLeaseWindow)); err != nil {
		t.Fatal(err)
	}
	journalRoot := filepath.Join(root, "journal")
	if os.Mkdir(journalRoot, 0o700) != nil {
		t.Fatal("fixture journal root failed")
	}
	owner, source := mutationFixture()
	journal, err := OpenMutationJournal(context.Background(), journalRoot, owner, source, os.Getenv("BOREALIS_EXECUTOR_NONCE"), mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	_, err = journal.Apply(context.Background(), "stage_source", mutationDigest("fixture only"), func(context.Context, *os.File) (string, error) {
		if readMutationFixture(t, journalRoot).Steps["stage_source"].Executor != identity {
			t.Fatal("actual executor missing before fixture effect")
		}
		return "", ErrMutationUnknown
	})
	if err != ErrMutationUnknown || journal.Close() != nil {
		t.Fatal("fixture intent retention failed")
	}
	lock, err := os.OpenFile(filepath.Join(root, "fixture.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil || syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		t.Fatal("fixture lock failed")
	}
	defer lock.Close()
	executable, _ := os.Executable()
	child := exec.Command(executable, "-test.run=^TestExecutorServiceFixture$")
	child.Env = append(os.Environ(), "BOREALIS_EXECUTOR_FIXTURE=descendant")
	if mode != "normal" {
		child.Env = append(child.Env, "BOREALIS_EXECUTOR_IGNORE_TERM=1")
	}
	child.ExtraFiles = []*os.File{lock}
	if child.Start() != nil {
		t.Fatal("fixture child failed")
	}
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "descendant.pid")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture descendant did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	raw, _ := json.Marshal(map[string]any{"identity": identity, "main_pid": os.Getpid(), "descendant_pid": child.Process.Pid, "mode": mode})
	if os.WriteFile(filepath.Join(root, "started.json"), raw, 0o600) != nil {
		t.Fatal("fixture evidence failed")
	}
	// The outer observer must reject quiescence even though the journal lock
	// was released above. Coordinate via public fixture files, never sleeps
	// that can make a slow runner confuse active and already-stopped states.
	deadline = time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "observer-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture observer did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if mode == "lease-expiry" {
		if err := guard.ObserveDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	switch mode {
	case "normal", "orphan-timeout":
		return
	case "crash":
		os.Exit(23)
	case "watchdog":
		_ = syscall.Kill(os.Getpid(), syscall.SIGSTOP)
	}
	time.Sleep(30 * time.Second)
	t.Fatal("independent watchdog failed to stop fixture")
}
