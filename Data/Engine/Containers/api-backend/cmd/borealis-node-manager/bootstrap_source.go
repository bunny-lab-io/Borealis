package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var errBootstrapSource = errors.New("source preparation evidence unavailable or changed; private data withheld")

func bootstrapSourceSessionContained(args []string) {
	nonce := bootstrapSessionNonce(args)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	guard, _, err := clusterbootstrap.NewExecutorGuard(ctx, nonce)
	if err != nil {
		fatalf("source preparation executor invalid")
	}
	defer guard.Close()
	verify := func(ctx context.Context, request clusterbootstrap.PreparationSessionRequest) error {
		return verifyBootstrapSource(ctx, request, true)
	}
	receive := func(ctx context.Context, request clusterbootstrap.PreparationSessionRequest, input io.Reader) error {
		expected, err := request.Expected()
		if err != nil || request.Session.Nonce != nonce {
			return errBootstrapSource
		}
		check := func(ctx context.Context) error {
			if guard.CheckAuthority(ctx) != nil {
				return errBootstrapSource
			}
			return nil
		}
		executable, err := os.Executable()
		if err != nil {
			return errBootstrapSource
		}
		return clusterbootstrap.ConsumePreparation(ctx, filepath.Dir(executable), expected, request.Session.ManagerSHA256, input, check,
			func(ctx context.Context, inputs *clusterbootstrap.PreparationInputs) error {
				if check(ctx) != nil || verify(ctx, request) != nil || check(ctx) != nil {
					return errBootstrapSource
				}
				// The supervised root helper owns only this fixed private journal.
				// Parent safety is checked independently before creating its leaf.
				if bootstrapSourceSafeDirectory("/var/lib") != nil || ensureBootstrapMutationRoot("/var/lib") != nil {
					return errBootstrapSource
				}
				authority := func(ctx context.Context) error {
					if check(ctx) != nil || verifyBootstrapSource(ctx, request, false) != nil || check(ctx) != nil {
						return errBootstrapSource
					}
					return nil
				}
				journal, err := clusterbootstrap.OpenMutationJournal(ctx, bootstrapMutationRoot, expected.Target, expected.Source, nonce, authority)
				if err != nil {
					return errBootstrapSource
				}
				switch request.Action {
				case "stage_source":
					_, err = inputs.PublishSource(ctx, journal)
				case "reconcile_source":
					_, err = inputs.ReconcileSource(ctx, journal)
				case "prepare_runtime_identity":
					_, err = inputs.PrepareRuntimeIdentity(ctx, journal, clusterbootstrap.RuntimeIdentityActions{
						Prepare: func(ctx context.Context, lock *os.File) error {
							return prepareBootstrapRuntimeIdentity(ctx, lock, authority)
						}, Prove: proveBootstrapRuntimeAccount,
					})
				case "reconcile_runtime_identity":
					_, err = inputs.ReconcileRuntimeIdentity(ctx, journal, proveBootstrapRuntimeAccount)
				default:
					err = errBootstrapSource
				}
				return errors.Join(err, journal.Close())
			})
	}
	if clusterbootstrap.ServePreparationSession(ctx, os.Stdin, os.Stdout, nonce, verify, receive, guard.ObserveDeadline) != nil {
		fatalf("source preparation failed; inspect journal before any write retry")
	}
}

// This is a narrow source-publication boundary, not complete host qualification.
// Controller still owns network/storage/cohort/configuration/admission proof.
// No services, installation scripts, Docker, K3s or identity consumers run here.
func verifyBootstrapSource(ctx context.Context, request clusterbootstrap.PreparationSessionRequest, destination bool) error {
	if _, err := request.Expected(); err != nil || ctx.Err() != nil {
		return errBootstrapSource
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return errBootstrapSource
	}
	defer root.Close()
	return verifyBootstrapSourceWith(ctx, request, destination, bootstrapSourceObservers{
		host: verifyBootstrapSessionHost,
		boot: readBootstrapSourceBoot,
		paths: func(action string, destination bool) error {
			return verifyBootstrapSourcePaths(root, action, destination)
		},
		unit: observeBootstrapAbsentUnit,
	})
}

type bootstrapSourceObservers struct {
	host  func(context.Context, clusterbootstrap.SessionRequest) error
	boot  func() (string, error)
	paths func(string, bool) error
	unit  func(context.Context, string) error
}

func verifyBootstrapSourceWith(ctx context.Context, request clusterbootstrap.PreparationSessionRequest, destination bool, observers bootstrapSourceObservers) error {
	expected, err := request.Expected()
	if err != nil || ctx.Err() != nil || observers.host == nil || observers.boot == nil || observers.paths == nil || observers.unit == nil {
		return errBootstrapSource
	}
	checkHost := func() error {
		if ctx.Err() != nil || observers.host(ctx, request.Session) != nil {
			return errBootstrapSource
		}
		boot, err := observers.boot()
		if err != nil || boot != expected.TargetBootID+"\n" || ctx.Err() != nil {
			return errBootstrapSource
		}
		return nil
	}
	if checkHost() != nil || observers.paths(request.Action, destination) != nil {
		return errBootstrapSource
	}
	for _, unit := range []string{"k3s.service", "k3s-agent.service", "borealis-node-manager.service"} {
		if ctx.Err() != nil || observers.unit(ctx, unit) != nil {
			return errBootstrapSource
		}
	}
	if ctx.Err() != nil || observers.paths(request.Action, destination) != nil || checkHost() != nil {
		return errBootstrapSource
	}
	return nil
}

func readBootstrapSourceBoot() (string, error) {
	boot, err := os.Open("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", errBootstrapSource
	}
	raw, readErr := io.ReadAll(io.LimitReader(boot, 65))
	closeErr := boot.Close()
	if readErr != nil || closeErr != nil || len(raw) != 37 {
		return "", errBootstrapSource
	}
	return string(raw), nil
}

func bootstrapSourceSafeDirectory(path string) error {
	root, err := os.OpenRoot("/")
	if err != nil {
		return errBootstrapSource
	}
	defer root.Close()
	absent, err := bootstrapSourcePath(root, strings.TrimPrefix(path, "/"))
	if err != nil || absent {
		return errBootstrapSource
	}
	return nil
}

func verifyBootstrapSourcePaths(root *os.Root, action string, destination bool) error {
	if action != "stage_source" && action != "reconcile_source" && action != "prepare_runtime_identity" && action != "reconcile_runtime_identity" {
		return errBootstrapSource
	}
	for _, path := range []string{"etc/borealis", "etc/rancher/k3s", "var/lib/rancher/k3s", "usr/local/bin/k3s", "var/lib/kubelet", "etc/kubernetes"} {
		absent, err := bootstrapSourcePath(root, path)
		if err != nil || !absent {
			return errBootstrapSource
		}
	}
	if destination {
		absent, err := bootstrapSourcePath(root, "opt/Borealis")
		if err != nil || absent != (action == "stage_source") {
			return errBootstrapSource
		}
	}
	return nil
}

// Walk ancestors without following links. Missing leaf/ancestor is absence;
// permissions, unexpected types and inaccessible metadata are never absence.
func bootstrapSourcePath(root *os.Root, path string) (bool, error) {
	current := ""
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false, errBootstrapSource
		}
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
			return false, errBootstrapSource
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(owner.Uid) != os.Geteuid() {
			return false, errBootstrapSource
		}
	}
	return false, nil
}

func observeBootstrapAbsentUnit(parent context.Context, unit string) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/systemctl", "show", "--no-pager", "--property=LoadState,ActiveState,SubState,MainPID,ControlPID,NeedDaemonReload,Job", "--", unit)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	command.WaitDelay = 250 * time.Millisecond
	output := &identityConsumerOutput{}
	command.Stdout, command.Stderr = output, io.Discard
	if command.Run() != nil || ctx.Err() != nil {
		return errBootstrapSource
	}
	return validateBootstrapAbsentUnit(output.buffer.String())
}

func validateBootstrapAbsentUnit(raw string) error {
	want := map[string]string{"LoadState": "not-found", "ActiveState": "inactive", "SubState": "dead", "MainPID": "0", "ControlPID": "0", "NeedDaemonReload": "no", "Job": ""}
	if len(raw) > 4096 || !strings.HasSuffix(raw, "\n") {
		return errBootstrapSource
	}
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		expected, exists := want[key]
		if !ok || !exists || value != expected {
			return errBootstrapSource
		}
		delete(want, key)
	}
	if len(want) != 0 {
		return errBootstrapSource
	}
	return nil
}
