package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/crypto/ssh"
)

var errBootstrapHostVerification = errors.New("bootstrap executable or observed host identity differs from authorized target")

func bootstrapSession(args []string) {
	if len(args) != 2 || args[0] != "--nonce" || os.Geteuid() != 0 || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		fatalf("bootstrap-session requires root LinuxAMD64 and exact session nonce argument")
	}
	if _, err := clusterbootstrap.SessionPreamble(args[1]); err != nil {
		fatalf("bootstrap-session nonce invalid")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := clusterbootstrap.ServeSession(ctx, os.Stdin, os.Stdout, args[1], verifyBootstrapSessionHost); err != nil {
		fatalf("bootstrap-session verification failed; session data withheld")
	}
}

// Only public host-key/machine metadata and this executable are read. No
// installed manager token, kubeconfig, Engine secrets or local auth setup is
// involved. This command has no prepare/join/identity-write dispatch surface.
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
