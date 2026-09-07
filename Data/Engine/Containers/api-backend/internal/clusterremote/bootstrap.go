package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

var ErrBootstrapVerification = errors.New("SSH bootstrap verification failed; inspect remote outcome before any write retry")

// Root copy is private and rehashed AFTER copying, preventing a login-user
// writable executable from being substituted between hash check and root exec.
// Both shells receive compile-time -c text; stdin is binary/password/protocol,
// never shell source. Scratch cleanup touches only this invocation's mktemp.
const bootstrapRootScript = `set -eu
umask 077
root_stage=$(mktemp -d /var/tmp/borealis-bootstrap-root.XXXXXXXXXX)
trap 'rm -rf -- "$root_stage"' EXIT HUP INT TERM
cp -P -- "$1" "$root_stage/manager"
test -f "$root_stage/manager" && test ! -L "$root_stage/manager"
test "$(wc -c < "$root_stage/manager")" = "$4"
actual=$(sha256sum -- "$root_stage/manager")
test "${actual%% *}" = "$2"
chmod 0500 "$root_stage/manager"
"$root_stage/manager" bootstrap-session --nonce "$3"
`

func bootstrapCommand(size int64, digest, nonce string) string {
	// Values originate in a verified bundle and crypto/rand, never caller shell
	// text. Keep every runtime path in a quoted positional argument.
	script := fmt.Sprintf(`set -eu
export PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C
umask 077
user_stage=$(mktemp -d /var/tmp/borealis-ssh-bootstrap.XXXXXXXXXX)
trap 'rm -rf -- "$user_stage"' EXIT HUP INT TERM
head -c %d > "$user_stage/manager"
test "$(wc -c < "$user_stage/manager")" = '%d'
actual=$(sha256sum -- "$user_stage/manager")
test "${actual%%%% *}" = '%s'
chmod 0500 "$user_stage/manager"
if [ "$(id -u)" = 0 ]; then
  /usr/bin/timeout --signal=TERM --kill-after=2s 6m /bin/sh -c %s bootstrap-root "$user_stage/manager" '%s' '%s' '%d'
else
  sudo -k -S -p '' -- /usr/bin/timeout --signal=TERM --kill-after=2s 6m /bin/sh -c %s bootstrap-root "$user_stage/manager" '%s' '%s' '%d'
fi`, size, size, digest, shellConstant(bootstrapRootScript), digest, nonce, size, shellConstant(bootstrapRootScript), digest, nonce, size)
	return "/bin/sh -c " + shellConstant(script)
}

// VerifyBootstrap delivers only the manifest-bound executable from verified
// immutable source, runs its fixed root verification session and removes its
// temporary executables. It installs no source/services and cannot prepare/join.
// check must revalidate exact controller/operation/target claim (including its
// generation and credential lifetime), releasing DB connections before return.
func (client *Client) VerifyBootstrap(ctx context.Context, bundle *clusterbootstrap.Bundle, binding clusterbootstrap.SessionBinding, sudoPassword []byte, check func(context.Context) error) (clusterbootstrap.SessionMessage, error) {
	invalid := clusterbootstrap.SessionMessage{}
	expected, digest, size, err := bundle.ManagerProof()
	if err != nil || binding.Validate() != nil || check == nil || ValidateSudoPassword(sudoPassword) != nil ||
		binding.Address != client.target.Address || binding.Port != client.target.Port || binding.HostKeyAlgorithm != client.approved.Algorithm || binding.HostKeyFingerprint != client.approved.Fingerprint {
		return invalid, ErrBootstrapVerification
	}
	return client.verifyBootstrapExecutable(ctx, bundle.ManagerPath(), expected, digest, size, binding, sudoPassword, check)
}

// Private transport seam permits isolated SSH fixtures without exposing an
// unverified executable constructor. Production entry requires Bundle proof.
func (client *Client) verifyBootstrapExecutable(ctx context.Context, managerPath string, expected clusterbootstrap.Expected, digest string, size int64, binding clusterbootstrap.SessionBinding, sudoPassword []byte, check func(context.Context) error) (clusterbootstrap.SessionMessage, error) {
	invalid := clusterbootstrap.SessionMessage{}
	ctx, cancel := context.WithTimeout(ctx, clusterbootstrap.SessionLifetime)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { client.Close() })
	defer stop()
	if refreshBootstrapAuthority(ctx, check) != nil {
		return invalid, ErrBootstrapVerification
	}
	manager, err := os.Open(managerPath)
	if err != nil {
		return invalid, ErrBootstrapVerification
	}
	defer manager.Close()
	info, err := manager.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return invalid, ErrBootstrapVerification
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return invalid, ErrBootstrapVerification
	}
	nonce := hex.EncodeToString(random[:])
	request := clusterbootstrap.SessionRequest{Protocol: 1, Action: "verify", Nonce: nonce, Binding: binding, Repository: expected.Repository, Release: expected.Release,
		SourceSHA: expected.SourceSHA, ManagerSHA256: digest, AllowQualification: expected.AllowQualification}
	if request.Validate() != nil {
		return invalid, ErrBootstrapVerification
	}
	session, err := client.ssh.NewSession()
	if err != nil {
		return invalid, ErrBootstrapVerification
	}
	defer session.Close()
	diagnostics := boundedOutput{close: func() { client.Close() }}
	session.Stderr = &diagnostics
	stdin, err := session.StdinPipe()
	if err != nil {
		return invalid, ErrBootstrapVerification
	}
	defer stdin.Close()
	stdout, err := session.StdoutPipe()
	if err != nil || session.Start(bootstrapCommand(size, digest, nonce)) != nil {
		return invalid, ErrBootstrapVerification
	}
	// Reconfirm ownership during a slow upload, before sudo or helper execution.
	buffer := make([]byte, 128<<10)
	remaining, checked := size, time.Now()
	for remaining > 0 {
		if time.Since(checked) >= clusterbootstrap.SessionChallengeEvery {
			if refreshBootstrapAuthority(ctx, check) != nil {
				return invalid, ErrBootstrapVerification
			}
			checked = time.Now()
		}
		n, readErr := manager.Read(buffer[:min(int64(len(buffer)), remaining)])
		if n == 0 || readErr != nil || ctx.Err() != nil {
			return invalid, ErrBootstrapVerification
		}
		if _, err := io.Copy(stdin, bytes.NewReader(buffer[:n])); err != nil {
			return invalid, ErrBootstrapVerification
		}
		remaining -= int64(n)
	}
	preamble, _ := clusterbootstrap.SessionPreamble(nonce)
	secret := append(bytes.Clone(sudoPassword), '\n')
	defer clear(secret)
	if _, err := io.Copy(stdin, bytes.NewReader(secret)); err != nil {
		return invalid, ErrBootstrapVerification
	}
	if _, err := io.WriteString(stdin, preamble); err != nil {
		return invalid, ErrBootstrapVerification
	}
	// Bound all stdout across the session, including repeated challenge frames.
	output := &io.LimitedReader{R: stdout, N: MaxOutputBytes + 1}
	var message clusterbootstrap.SessionMessage
	if clusterbootstrap.ReadSessionFrame(output, &message) != nil || message.Protocol != 1 || message.State != "ready" || message.Nonce != nonce || message.Binding != nil || message.SourceSHA != "" || message.ManagerSHA256 != "" ||
		refreshBootstrapAuthority(ctx, check) != nil || clusterbootstrap.WriteSessionFrame(stdin, request) != nil {
		return invalid, ErrBootstrapVerification
	}
	challenges := map[string]bool{}
	for {
		message = clusterbootstrap.SessionMessage{}
		if clusterbootstrap.ReadSessionFrame(output, &message) != nil || message.Protocol != 1 || ctx.Err() != nil {
			return invalid, ErrBootstrapVerification
		}
		switch message.State {
		case "challenge":
			if _, err := clusterbootstrap.SessionPreamble(message.Nonce); err != nil || challenges[message.Nonce] || len(challenges) >= 64 || message.Binding != nil || message.SourceSHA != "" || message.ManagerSHA256 != "" {
				return invalid, ErrBootstrapVerification
			}
			challenges[message.Nonce] = true
			if refreshBootstrapAuthority(ctx, check) != nil || clusterbootstrap.WriteSessionFrame(stdin, clusterbootstrap.SessionMessage{Protocol: 1, State: "heartbeat", Nonce: message.Nonce}) != nil {
				return invalid, ErrBootstrapVerification
			}
		case "verified":
			if len(challenges) == 0 || message.Nonce != nonce || message.Binding == nil || *message.Binding != binding || message.SourceSHA != expected.SourceSHA || message.ManagerSHA256 != digest || refreshBootstrapAuthority(ctx, check) != nil {
				return invalid, ErrBootstrapVerification
			}
			_ = stdin.Close()
			// Reject extra protocol output and require successful remote cleanup.
			var extra [1]byte
			if n, readErr := output.Read(extra[:]); n != 0 || readErr != io.EOF || output.N < 1 || session.Wait() != nil || diagnostics.overflow {
				return invalid, ErrBootstrapVerification
			}
			return message, nil
		default:
			return invalid, ErrBootstrapVerification
		}
	}
}

func refreshBootstrapAuthority(ctx context.Context, check func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- check(ctx) }()
	select {
	case err := <-result:
		if err != nil || ctx.Err() != nil {
			return ErrBootstrapVerification
		}
		return nil
	case <-ctx.Done():
		return ErrBootstrapVerification
	}
}
