package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
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
	bootstrapSourceSessionContainedFor(args, false)
}

func bootstrapSourceSessionContainedFor(args []string, artifacts bool) {
	nonce := bootstrapSessionNonce(args)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	newGuard := clusterbootstrap.NewExecutorGuard
	if artifacts {
		newGuard = clusterbootstrap.NewArtifactExecutorGuard
	}
	guard, executor, err := newGuard(ctx, nonce)
	if err != nil {
		fatalf("source preparation executor invalid")
	}
	defer guard.Close()
	verify := func(ctx context.Context, request clusterbootstrap.PreparationSessionRequest) error {
		if verifyBootstrapSource(ctx, request, true) != nil {
			return errBootstrapSource
		}
		if request.IdentitySource != nil {
			_, err := bootstrapPrejoinQuiescence(ctx, request)
			return err
		}
		return nil
	}
	var progress clusterbootstrap.PreparationProgress
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
		consume := func(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, material *engineidentity.Material, assets *clusterbootstrap.PreparationArtifacts, join *clusterbootstrap.K3sJoinMaterial) error {
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
			openJournal := clusterbootstrap.OpenMutationJournal
			if artifacts {
				openJournal = clusterbootstrap.OpenArtifactMutationJournal
			}
			journal, err := openJournal(ctx, bootstrapMutationRoot, expected.Target, expected.Source, nonce, authority)
			if err != nil {
				return errBootstrapSource
			}
			if request.IdentitySource != nil {
				install, installErr := bootstrapPrejoinInstallation(ctx, request, authority)
				if installErr != nil {
					return errors.Join(installErr, journal.Close())
				}
				switch request.Action {
				case "verify_prepared_images", "reconcile_prepared_images":
					operation := inputs.VerifyPreparedImages
					if request.Action == "reconcile_prepared_images" {
						operation = inputs.ReconcilePreparedImages
					}
					_, err = operation(ctx, journal, install, material, assets, request.PostgresReference,
						func(ctx context.Context) error {
							return proveBootstrapPreparedK3sStartHost(ctx, expected, install.Binding, join, authority)
						}, join, bootstrapNativePreparedImageActions(expected, install.Binding, join, guard, executor, authority))
				case "start_prepared_k3s", "reconcile_prepared_k3s_start":
					operation := inputs.StartPreparedK3s
					if request.Action == "reconcile_prepared_k3s_start" {
						operation = inputs.ReconcilePreparedK3sStart
					}
					_, err = operation(ctx, journal, install, material, assets, request.PostgresReference,
						func(ctx context.Context) error {
							return proveBootstrapPreparedK3sStartHost(ctx, expected, install.Binding, join, authority)
						},
						join, bootstrapNativeK3sStartActions(expected, install.Binding, join, guard, executor, authority))
				case "publish_prejoin_artifacts":
					_, err = inputs.PublishPrejoinArtifacts(ctx, journal, install, material, assets, request.PostgresReference,
						func(ctx context.Context) error { return proveBootstrapPreparedHost(ctx, expected, authority) })
				case "reconcile_prejoin_artifacts":
					_, err = inputs.ReconcilePrejoinArtifacts(ctx, journal, install, material, assets, request.PostgresReference,
						func(ctx context.Context) error { return proveBootstrapPreparedHost(ctx, expected, authority) })
				case "install_prejoin_k3s":
					_, err = inputs.InstallPrejoinK3s(ctx, journal, install, material, assets, request.PostgresReference,
						func(ctx context.Context) error { return proveBootstrapPreparedHost(ctx, expected, authority) }, bootstrapNativeK3sActions(expected, guard, executor, authority))
				case "reconcile_prejoin_k3s":
					_, err = inputs.ReconcilePrejoinK3s(ctx, journal, install, material, assets, request.PostgresReference,
						func(ctx context.Context) error { return proveBootstrapPreparedHost(ctx, expected, authority) }, bootstrapNativeK3sActions(expected, guard, executor, authority).Prove)
				case "configure_prejoin_k3s", "reconcile_prejoin_k3s_configuration":
					operation := inputs.ConfigurePrejoinK3s
					if request.Action == "reconcile_prejoin_k3s_configuration" {
						operation = inputs.ReconcilePrejoinK3sConfiguration
					}
					_, err = operation(ctx, journal, install, material, assets, request.PostgresReference,
						func(ctx context.Context) error {
							return proveBootstrapPreparedK3sJoinHost(ctx, expected, install.Binding, join, authority)
						},
						join, bootstrapNativeK3sJoinActions(expected, install.Binding, join, guard, executor, authority))
				case "prepare_prejoin_identity_root":
					_, err = inputs.PreparePrejoinIdentityRoot(ctx, journal, install, material)
				case "reconcile_prejoin_identity_root":
					_, err = inputs.ReconcilePrejoinIdentityRoot(ctx, journal, install, material)
				case "install_prejoin_identity":
					_, err = inputs.InstallPreparedPrejoinIdentity(ctx, journal, install, material)
				case "reconcile_prejoin_identity":
					_, err = inputs.ReconcilePreparedPrejoinIdentity(ctx, journal, install, material)
				case "prepare_prejoin_host":
					_, err = inputs.PreparePrejoinHost(ctx, journal, install, material, bootstrapNativeHostActions(expected, guard, executor, authority))
				case "complete_prejoin_host":
					_, err = inputs.CompletePrejoinHost(ctx, journal, install, material, bootstrapNativeHostRecovery(expected, guard, executor, authority))
				case "complete_prejoin_firewall":
					_, err = inputs.CompletePrejoinFirewall(ctx, journal, install, material, bootstrapNativeFirewallRecovery(expected, guard, executor, authority))
				case "resume_fresh_prejoin_host":
					_, err = inputs.ResumeFreshPrejoinHost(ctx, journal, install, material, bootstrapNativeHostActions(expected, guard, executor, authority))
				case "resume_repository_prejoin_host":
					_, err = inputs.ResumeRepositoryPrejoinHost(ctx, journal, install, material, bootstrapNativeRepositoryHostActions(expected, guard, executor, authority))
				case "reconcile_prejoin_host":
					// Proof-only API deliberately receives no preparation callback.
					_, err = inputs.ReconcilePrejoinHost(ctx, journal, install, material,
						bootstrapNativeHostActions(expected, guard, executor, authority).Prove)
				default:
					err = errBootstrapSource
				}
				return errors.Join(err, journal.Close())
			}
			switch request.Action {
			case "inspect_preparation_progress":
				progress, err = inputs.InspectPreparationProgress(ctx, journal)
			case "stage_source":
				_, err = inputs.PublishSource(ctx, journal)
			case "reconcile_source":
				_, err = inputs.ReconcileSource(ctx, journal)
			case "reconcile_source_absent":
				_, err = inputs.ReconcileSourceAbsent(ctx, journal)
			case "prepare_runtime_identity":
				_, err = inputs.PrepareRuntimeIdentity(ctx, journal, clusterbootstrap.RuntimeIdentityActions{
					Prepare: func(ctx context.Context, lock *os.File) error {
						return prepareBootstrapRuntimeIdentity(ctx, lock, authority)
					}, Prove: proveBootstrapRuntimeAccount,
				})
			case "complete_runtime_identity":
				_, err = inputs.CompleteRuntimeIdentity(ctx, journal, clusterbootstrap.RuntimeIdentityRecoveryActions{
					ProveGroupOnly: proveBootstrapRuntimeGroupOnly,
					CompleteUser: func(ctx context.Context, lock *os.File) error {
						return completeBootstrapRuntimeIdentity(ctx, lock, authority)
					},
					Prove: proveBootstrapRuntimeAccount,
				})
			case "reconcile_runtime_identity":
				_, err = inputs.ReconcileRuntimeIdentity(ctx, journal, proveBootstrapRuntimeAccount)
			default:
				err = errBootstrapSource
			}
			return errors.Join(err, journal.Close())
		}
		if artifacts {
			if request.IdentitySource == nil || !bootstrapArtifactAction(request.Action) || bootstrapSourceSafeDirectory("/opt") != nil {
				return errBootstrapSource
			}
			// Bulk scratch shares the qualified /opt source filesystem; the uploaded
			// helper remains in its existing bounded private /var/tmp wrapper.
			if bootstrapK3sConfigurationAction(request.Action) || bootstrapK3sStartupAction(request.Action) {
				return clusterbootstrap.ConsumePrejoinK3sJoinPreparation(ctx, "/opt", expected, request.Session.ManagerSHA256, *request.IdentitySource, request.PostgresReference, input, check, consume)
			}
			return clusterbootstrap.ConsumePrejoinArtifactPreparation(ctx, "/opt", expected, request.Session.ManagerSHA256, *request.IdentitySource, request.PostgresReference, input, check,
				func(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, material *engineidentity.Material, assets *clusterbootstrap.PreparationArtifacts) error {
					return consume(ctx, inputs, material, assets, nil)
				})
		}
		if request.IdentitySource != nil {
			return clusterbootstrap.ConsumePrejoinPreparation(ctx, filepath.Dir(executable), expected, request.Session.ManagerSHA256, *request.IdentitySource, input, check, func(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, material *engineidentity.Material) error {
				return consume(ctx, inputs, material, nil, nil)
			})
		}
		return clusterbootstrap.ConsumePreparation(ctx, filepath.Dir(executable), expected, request.Session.ManagerSHA256, input, check,
			func(ctx context.Context, inputs *clusterbootstrap.PreparationInputs) error {
				return consume(ctx, inputs, nil, nil, nil)
			})
	}
	serve := clusterbootstrap.ServePreparationSessionWithProgress
	if artifacts {
		serve = clusterbootstrap.ServeArtifactPreparationSession
	}
	if serve(ctx, os.Stdin, os.Stdout, nonce, verify,
		func(ctx context.Context, request clusterbootstrap.PreparationSessionRequest, input io.Reader) (clusterbootstrap.PreparationProgress, error) {
			err := receive(ctx, request, input)
			return progress, err
		}, guard.ObserveDeadline) != nil {
		fatalf("source preparation failed; inspect journal before any write retry")
	}
}

// Source/identity phases keep their narrow paths; host actions select an exact
// generated-file prefix without permitting K3s or identity-consumer startup.
// Controller still owns network/storage/cohort/configuration/admission proof.
func verifyBootstrapSource(ctx context.Context, request clusterbootstrap.PreparationSessionRequest, destination bool) error {
	expected, err := request.Expected()
	if err != nil || ctx.Err() != nil {
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
			if action == "inspect_preparation_progress" {
				return verifyBootstrapProgressPaths(root, expected)
			}
			if bootstrapArtifactAction(action) {
				return verifyBootstrapArtifactPaths(root, expected, action, destination)
			}
			if bootstrapHostAction(action) {
				return verifyBootstrapHostPaths(root, expected, action, destination)
			}
			return verifyBootstrapSourcePaths(root, action, destination)
		},
		unit: func(ctx context.Context, unit string) error {
			if unit == "k3s.service" && (bootstrapK3sStartupAction(request.Action) || request.Action == "inspect_preparation_progress") {
				if request.Action == "start_prepared_k3s" && destination {
					return observeBootstrapInertK3s(ctx, true)
				}
				return observeBootstrapStartingK3s(ctx, request.Action == "inspect_preparation_progress")
			}
			if unit == "k3s.service" && bootstrapK3sConfigurationAction(request.Action) {
				return observeBootstrapInertK3s(ctx, true)
			}
			if unit == "k3s.service" && bootstrapK3sAction(request.Action) {
				return observeBootstrapInertK3s(ctx, false)
			}
			return observeBootstrapAbsentUnit(ctx, unit)
		},
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
	switch action {
	case "stage_source", "reconcile_source", "reconcile_source_absent", "prepare_runtime_identity", "complete_runtime_identity", "reconcile_runtime_identity",
		"prepare_prejoin_identity_root", "reconcile_prejoin_identity_root", "install_prejoin_identity", "reconcile_prejoin_identity":
	default:
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
		if err != nil || absent != (action == "stage_source" || action == "reconcile_source_absent") {
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
