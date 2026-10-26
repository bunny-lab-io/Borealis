package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

var errBootstrapHostVerification = errors.New("bootstrap executable or observed host identity differs from authorized target")
var errBootstrapIdentityRecovery = errors.New("identity recovery qualification evidence unavailable or changed")

const bootstrapMutationRoot = "/var/lib/borealis/cluster-bootstrap"

type bootstrapIdentityMutation func(context.Context, string, clusterbootstrap.SessionBinding, clusterbootstrap.Expected, string,
	func(context.Context) error, engineidentity.Installation, *engineidentity.Material) (string, error)

func bootstrapSession(args []string) {
	bootstrapExecutor(args, false)
}

func bootstrapExecutor(args []string, preparation bool) {
	nonce := bootstrapSessionNonce(args)
	executable, err := os.Executable()
	if err != nil {
		fatalf("bootstrap executable unavailable")
	}
	file, err := openBootstrapPublicFile(executable, clusterbootstrap.MaxBundleBytes)
	if err != nil {
		fatalf("bootstrap executable ownership invalid")
	}
	_ = file.Close()
	arguments, err := clusterbootstrap.ExecutorArguments(executable, nonce)
	if preparation {
		arguments, err = clusterbootstrap.PreparationExecutorArguments(executable, nonce)
	}
	if err != nil {
		fatalf("bootstrap executor arguments invalid")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 6*time.Minute+5*time.Second)
	defer timeout()
	command := exec.CommandContext(ctx, "/usr/bin/systemd-run", arguments...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	command.WaitDelay = 2 * time.Second
	if command.Run() != nil {
		fatalf("bootstrap executor stopped or failed; outcome requires verification")
	}
}

func bootstrapSessionNonce(args []string) string {
	if len(args) != 2 || args[0] != "--nonce" || os.Geteuid() != 0 || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		fatalf("bootstrap-session requires root LinuxAMD64 and exact session nonce argument")
	}
	if _, err := clusterbootstrap.SessionPreamble(args[1]); err != nil {
		fatalf("bootstrap-session nonce invalid")
	}
	return args[1]
}

func bootstrapSessionContained(args []string) {
	nonce := bootstrapSessionNonce(args)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	guard, _, err := clusterbootstrap.NewExecutorGuard(ctx, nonce)
	if err != nil {
		fatalf("bootstrap executor context invalid")
	}
	defer guard.Close()
	receive := func(ctx context.Context, request clusterbootstrap.SessionRequest, material *engineidentity.Material) error {
		switch request.Action {
		case "deliver_identity_recovery":
			return receiveBootstrapIdentityMaterialWith(ctx, nonce, guard.CheckAuthority, request, material, verifyBootstrapSessionRequest, applyBootstrapIdentityMutation)
		case "release_identity_recovery":
			return releaseBootstrapIdentityMaterialWith(ctx, nonce, guard.CheckAuthority, request, material, verifyBootstrapSessionRequest, applyBootstrapIdentityRelease)
		default:
			return errBootstrapIdentityRecovery
		}
	}
	if err := clusterbootstrap.ServeSupervisedIdentitySession(ctx, os.Stdin, os.Stdout, nonce, verifyBootstrapSessionRequest, receive, guard.ObserveDeadline); err != nil {
		fatalf("bootstrap-session verification failed; session data withheld")
	}
}

type bootstrapSessionObservers struct {
	host      func(context.Context, clusterbootstrap.SessionRequest) error
	consumers func(context.Context, identityConsumerTarget) error
	runtime   func(context.Context, clusterbootstrap.SessionRequest) error
}

func verifyBootstrapSessionRequest(ctx context.Context, request clusterbootstrap.SessionRequest) error {
	return verifyBootstrapSessionRequestWith(ctx, request, bootstrapSessionObservers{
		host: verifyBootstrapSessionHost,
		consumers: func(ctx context.Context, target identityConsumerTarget) error {
			_, err := observeIdentityConsumers(ctx, target, identityConsumerCommand)
			return err
		},
		runtime: func(ctx context.Context, request clusterbootstrap.SessionRequest) error {
			return observeIdentityHostRuntime(ctx, defaultRepoRoot, request, actionRuntimeID, actionRuntimeID)
		},
	})
}

func verifyBootstrapSessionRequestWith(ctx context.Context, request clusterbootstrap.SessionRequest, observers bootstrapSessionObservers) error {
	if request.Validate() != nil || observers.host == nil || ctx.Err() != nil || observers.host(ctx, request) != nil || ctx.Err() != nil {
		return errBootstrapHostVerification
	}
	if request.Action == "verify" {
		return nil
	}
	if (request.Action != "verify_identity_recovery" && request.Action != "deliver_identity_recovery" && request.Action != "release_identity_recovery") || request.IdentityRecovery == nil || observers.consumers == nil || observers.runtime == nil {
		return errBootstrapIdentityRecovery
	}
	target := identityConsumerTarget{NodeName: request.Binding.Hostname, NodeUID: request.IdentityRecovery.TargetNodeUID}
	if observers.consumers(ctx, target) != nil || ctx.Err() != nil || observers.runtime(ctx, request) != nil || ctx.Err() != nil ||
		observers.consumers(ctx, target) != nil || ctx.Err() != nil {
		return errBootstrapIdentityRecovery
	}
	return nil
}

// receiveBootstrapIdentityMaterialWith is the only delivery-to-write bridge.
// It binds exact request identity to both journals while the supervised
// challenge lease and repeated Pod/runtime/Pod proof remain current. Success
// leaves the restart marker in place; this receiver cannot release or restart.
func receiveBootstrapIdentityMaterialWith(ctx context.Context, nonce string, authority func(context.Context) error,
	request clusterbootstrap.SessionRequest, material *engineidentity.Material, quiesced func(context.Context, clusterbootstrap.SessionRequest) error,
	mutate bootstrapIdentityMutation) error {
	source, install, err := bootstrapIdentityInstallation(ctx, nonce, "deliver_identity_recovery", authority, request, material, quiesced)
	if err != nil || mutate == nil {
		return errBootstrapIdentityRecovery
	}
	state, err := mutate(ctx, bootstrapMutationRoot, request.Binding, source, nonce, authority, install, material)
	if err != nil || (state != "committed" && state != "already_committed") {
		return errBootstrapIdentityRecovery
	}
	return nil
}

// releaseBootstrapIdentityMaterialWith is reachable only through a later
// supervised session. It reuses exact install binding and accepts only marker
// release outcomes; consumer restart remains absent.
func releaseBootstrapIdentityMaterialWith(ctx context.Context, nonce string, authority func(context.Context) error,
	request clusterbootstrap.SessionRequest, material *engineidentity.Material, quiesced func(context.Context, clusterbootstrap.SessionRequest) error,
	mutate bootstrapIdentityMutation) error {
	source, install, err := bootstrapIdentityInstallation(ctx, nonce, "release_identity_recovery", authority, request, material, quiesced)
	if err != nil || mutate == nil {
		return errBootstrapIdentityRecovery
	}
	state, err := mutate(ctx, bootstrapMutationRoot, request.Binding, source, nonce, authority, install, material)
	if err != nil || (state != "released" && state != "already_released") {
		return errBootstrapIdentityRecovery
	}
	return nil
}

func bootstrapIdentityInstallation(ctx context.Context, nonce, action string, authority func(context.Context) error,
	request clusterbootstrap.SessionRequest, material *engineidentity.Material,
	quiesced func(context.Context, clusterbootstrap.SessionRequest) error) (clusterbootstrap.Expected, engineidentity.Installation, error) {
	invalidSource, invalidInstall := clusterbootstrap.Expected{}, engineidentity.Installation{}
	if ctx.Err() != nil || request.Validate() != nil || request.Action != action || request.Nonce != nonce ||
		request.IdentityRecovery == nil || material == nil || material.Digest() == "" || authority == nil || quiesced == nil {
		return invalidSource, invalidInstall, errBootstrapIdentityRecovery
	}
	check := func() error { return authority(ctx) }
	stopped := func() error { return quiesced(ctx, request) }
	recovery := request.IdentityRecovery
	install := engineidentity.Installation{
		Root: identityRuntimeRoot, Journal: filepath.Join(bootstrapMutationRoot, "identity-attempt"),
		Binding: engineidentity.Binding{ClusterID: request.Binding.ClusterID, SourceID: recovery.SourceID, SourceUID: recovery.SourceNodeUID},
		Target:  engineidentity.TargetBinding{ProvisioningID: request.Binding.TargetID, SSHFingerprint: request.Binding.HostKeyFingerprint, NodeUID: recovery.TargetNodeUID},
		UID:     actionRuntimeID, GID: actionRuntimeID, Check: check, Quiesced: stopped,
	}
	source := clusterbootstrap.Expected{
		Repository: request.Repository, Release: request.Release, SourceSHA: request.SourceSHA, AllowQualification: request.AllowQualification,
	}
	return source, install, nil
}

func applyBootstrapIdentityMutation(ctx context.Context, root string, owner clusterbootstrap.SessionBinding, source clusterbootstrap.Expected,
	nonce string, authority func(context.Context) error, install engineidentity.Installation, material *engineidentity.Material) (string, error) {
	journal, err := clusterbootstrap.OpenMutationJournal(ctx, root, owner, source, nonce, authority)
	if err != nil {
		return "", err
	}
	state, installErr := journal.InstallJoinedIdentity(ctx, install, material)
	return state, errors.Join(installErr, journal.Close())
}

func applyBootstrapIdentityRelease(ctx context.Context, root string, owner clusterbootstrap.SessionBinding, source clusterbootstrap.Expected,
	nonce string, authority func(context.Context) error, install engineidentity.Installation, material *engineidentity.Material) (string, error) {
	journal, err := clusterbootstrap.OpenMutationJournal(ctx, root, owner, source, nonce, authority)
	if err != nil {
		return "", err
	}
	state, releaseErr := journal.ReconcileAndReleaseJoinedIdentity(ctx, install, material)
	return state, errors.Join(releaseErr, journal.Close())
}

// This host verifier reads only public host-key/machine metadata and its own
// executable. It never reads installed manager credentials or Engine secrets.
func verifyBootstrapSessionHost(ctx context.Context, request clusterbootstrap.SessionRequest) error {
	if request.Validate() != nil || ctx.Err() != nil {
		return errBootstrapHostVerification
	}
	hostname, err := os.Hostname()
	if err != nil {
		return errBootstrapHostVerification
	}
	machine, err := readBootstrapPublicFile("/etc/machine-id", 128)
	if err != nil {
		return err
	}
	keyName := ""
	switch request.Binding.HostKeyAlgorithm {
	case "ssh-ed25519":
		keyName = "ssh_host_ed25519_key.pub"
	case "ssh-rsa":
		keyName = "ssh_host_rsa_key.pub"
	case "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521":
		keyName = "ssh_host_ecdsa_key.pub"
	default:
		return errBootstrapHostVerification
	}
	key, err := readBootstrapPublicFile(filepath.Join("/etc/ssh", keyName), 4096)
	if err != nil {
		return err
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return errBootstrapHostVerification
	}
	var addresses []netip.Addr
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		values, err := iface.Addrs()
		if err != nil {
			return errBootstrapHostVerification
		}
		for _, value := range values {
			if prefix, err := netip.ParsePrefix(value.String()); err == nil && prefix.Addr().Is4() {
				addresses = append(addresses, prefix.Addr())
			}
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return errBootstrapHostVerification
	}
	file, err := openBootstrapPublicFile(executable, clusterbootstrap.MaxBundleBytes)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	for {
		if ctx.Err() != nil {
			return errBootstrapHostVerification
		}
		n, err := file.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return errBootstrapHostVerification
		}
	}
	return compareBootstrapHost(request, hostname, machine, key, addresses, hex.EncodeToString(hash.Sum(nil)))
}

func compareBootstrapHost(request clusterbootstrap.SessionRequest, hostname string, machine, publicKey []byte, addresses []netip.Addr, executableSHA string) error {
	if request.Validate() != nil || strings.SplitN(hostname, ".", 2)[0] != request.Binding.Hostname || strings.TrimSpace(string(machine)) != request.Binding.MachineID || executableSHA != request.ManagerSHA256 {
		return errBootstrapHostVerification
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey(publicKey)
	if err != nil || len(options) != 0 || len(strings.TrimSpace(string(rest))) != 0 || key.Type() != request.Binding.HostKeyAlgorithm || ssh.FingerprintSHA256(key) != request.Binding.HostKeyFingerprint {
		return errBootstrapHostVerification
	}
	matching := 0
	for _, address := range addresses {
		if address.String() == request.Binding.Address {
			matching++
		}
	}
	if matching != 1 {
		return errBootstrapHostVerification
	}
	return nil
}

func openBootstrapPublicFile(name string, maximum int64) (*os.File, error) {
	real, err := filepath.EvalSymlinks(name)
	if err != nil || !filepath.IsAbs(name) || real != name {
		return nil, errBootstrapHostVerification
	}
	file, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errBootstrapHostVerification
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maximum || info.Mode().Perm()&0o022 != 0 {
		_ = file.Close()
		return nil, errBootstrapHostVerification
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) {
		_ = file.Close()
		return nil, errBootstrapHostVerification
	}
	return file, nil
}

func readBootstrapPublicFile(name string, maximum int64) ([]byte, error) {
	file, err := openBootstrapPublicFile(name, maximum)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) > maximum {
		return nil, errBootstrapHostVerification
	}
	return raw, nil
}
