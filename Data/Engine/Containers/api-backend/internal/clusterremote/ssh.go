// Package clusterremote provides the central Engine's pinned SSH transport.
// It does not own membership, persist credentials, or expose arbitrary commands.
package clusterremote

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"
)

const (
	MaxPasswordBytes   = 4096
	MaxPrivateKeyBytes = 64 * 1024
	MaxOutputBytes     = 16 * 1024
)

var (
	ErrInvalidTarget  = errors.New("invalid private IPv4 SSH target or port")
	ErrInvalidAuth    = errors.New("invalid SSH authentication material")
	ErrHostKeyChanged = errors.New("SSH host key differs from approved key")
	ErrTransport      = errors.New("SSH connection or authentication failed")
	ErrCancelled      = errors.New("SSH operation cancelled or timed out")
	ErrOutputLimit    = errors.New("SSH inspection output exceeds limit")
	ErrInspection     = errors.New("SSH host inspection response invalid")
	errKeyObserved    = errors.New("SSH host key observed without authentication")
	usernamePattern   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)
	factPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,252}$`)
)

type Target struct {
	Address string
	Port    int
}

func (target Target) Validate() error {
	_, err := target.endpoint()
	return err
}

func (target Target) endpoint() (string, error) {
	ip, err := netip.ParseAddr(target.Address)
	if err != nil || !ip.Is4() || !ip.IsPrivate() || ip.String() != target.Address || target.Port < 1 || target.Port > 65535 {
		return "", ErrInvalidTarget
	}
	return net.JoinHostPort(target.Address, strconv.Itoa(target.Port)), nil
}

// HostKey contains public material only. Discovery is not approval: callers
// must obtain explicit operator approval before passing this key to Connect.
// Provisioning callers must persist that approval with the operation target.
type HostKey struct {
	Algorithm   string
	Fingerprint string
	PublicKey   []byte
}

func (key HostKey) Validate() error {
	if len(key.PublicKey) > 4096 || len(key.Algorithm) > 64 || len(key.Fingerprint) > 64 {
		return ErrHostKeyChanged
	}
	parsed, err := ssh.ParsePublicKey(key.PublicKey)
	if err != nil || ssh.FingerprintSHA256(parsed) != key.Fingerprint || parsed.Type() != key.Algorithm {
		return ErrHostKeyChanged
	}
	return nil
}

// Credential has one owning goroutine. Destroy only after its Connect call
// returns; callers must not share credentials between concurrent targets.
type Credential struct {
	username   string
	password   []byte
	privateKey []byte
	passphrase []byte
}

func (Credential) String() string   { return "SSH credential [redacted]" }
func (Credential) GoString() string { return "SSH credential [redacted]" }

func PasswordCredential(username string, password []byte) (*Credential, error) {
	if !usernamePattern.MatchString(username) || !validSecret(password, MaxPasswordBytes) {
		return nil, ErrInvalidAuth
	}
	return &Credential{username: username, password: bytes.Clone(password)}, nil
}

func KeyCredential(username string, key, passphrase []byte) (*Credential, error) {
	if !usernamePattern.MatchString(username) || len(key) == 0 || len(key) > MaxPrivateKeyBytes ||
		len(passphrase) > MaxPasswordBytes || (len(passphrase) > 0 && !validSecret(passphrase, MaxPasswordBytes)) {
		return nil, ErrInvalidAuth
	}
	credential := &Credential{username: username, privateKey: bytes.Clone(key), passphrase: bytes.Clone(passphrase)}
	if _, err := credential.authMethod(); err != nil {
		credential.Destroy()
		return nil, err
	}
	return credential, nil
}

func validSecret(value []byte, maximum int) bool {
	return len(value) > 0 && len(value) <= maximum && utf8.Valid(value) && !bytes.ContainsRune(value, 0)
}

// Destroy removes mutable credential copies when the scoped operation ends.
// Callers must also expire persisted encrypted credentials; this is not a
// guarantee that Go or SSH library internal immutable copies are zeroed.
func (credential *Credential) Destroy() {
	if credential == nil {
		return
	}
	clear(credential.password)
	clear(credential.privateKey)
	clear(credential.passphrase)
	credential.password, credential.privateKey, credential.passphrase = nil, nil, nil
	credential.username = ""
}

func (credential *Credential) authMethod() (ssh.AuthMethod, error) {
	if credential == nil || !usernamePattern.MatchString(credential.username) {
		return nil, ErrInvalidAuth
	}
	if len(credential.password) > 0 && len(credential.privateKey) == 0 {
		if !validSecret(credential.password, MaxPasswordBytes) {
			return nil, ErrInvalidAuth
		}
		return ssh.Password(string(credential.password)), nil
	}
	var signer ssh.Signer
	var err error
	if len(credential.privateKey) == 0 || len(credential.password) != 0 {
		return nil, ErrInvalidAuth
	}
	if len(credential.passphrase) > 0 {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(credential.privateKey, credential.passphrase)
	} else {
		signer, err = ssh.ParsePrivateKey(credential.privateKey)
	}
	if err != nil {
		return nil, ErrInvalidAuth
	}
	switch signer.PublicKey().Type() {
	case ssh.KeyAlgoED25519, ssh.KeyAlgoRSA, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521:
		return ssh.PublicKeys(signer), nil
	default:
		return nil, ErrInvalidAuth
	}
}

type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type Transport struct {
	Dialer Dialer
}

func (transport Transport) handshake(ctx context.Context, target Target, config *ssh.ClientConfig) (*ssh.Client, error) {
	endpoint, err := target.endpoint()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	dialer := transport.Dialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 10 * time.Second}
	}
	connection, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ErrCancelled
		}
		return nil, ErrTransport
	}
	stop := context.AfterFunc(ctx, func() { connection.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := connection.SetDeadline(deadline); err != nil {
		connection.Close()
		return nil, ErrTransport
	}
	client, channels, requests, err := ssh.NewClientConn(connection, endpoint, config)
	if err != nil {
		connection.Close()
		if ctx.Err() != nil {
			return nil, ErrCancelled
		}
		return nil, err
	}
	if !stop() || ctx.Err() != nil {
		client.Close()
		return nil, ErrCancelled
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		client.Close()
		return nil, ErrTransport
	}
	return ssh.NewClient(client, channels, requests), nil
}

// ProbeHostKey aborts key exchange at host-key verification, before SSH user
// authentication. It cannot accept or send an operator credential.
func (transport Transport) ProbeHostKey(ctx context.Context, target Target) (HostKey, error) {
	var observed HostKey
	config := &ssh.ClientConfig{User: "borealis-host-key-probe", HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		observed = HostKey{Algorithm: key.Type(), Fingerprint: ssh.FingerprintSHA256(key), PublicKey: bytes.Clone(key.Marshal())}
		return errKeyObserved
	}}
	client, err := transport.handshake(ctx, target, config)
	if client != nil {
		client.Close()
	}
	if errors.Is(err, errKeyObserved) && len(observed.PublicKey) > 0 {
		return observed, nil
	}
	if errors.Is(err, ErrInvalidTarget) || errors.Is(err, ErrCancelled) {
		return HostKey{}, err
	}
	return HostKey{}, ErrTransport
}

type Client struct{ ssh *ssh.Client }

func (client *Client) Close() error { return client.ssh.Close() }

func (transport Transport) Connect(ctx context.Context, target Target, approved HostKey, credential *Credential) (*Client, error) {
	if err := approved.Validate(); err != nil {
		return nil, err
	}
	key, _ := ssh.ParsePublicKey(approved.PublicKey)
	method, err := credential.authMethod()
	if err != nil {
		return nil, err
	}
	config := &ssh.ClientConfig{User: credential.username, Auth: []ssh.AuthMethod{method},
		HostKeyCallback: func(_ string, _ net.Addr, presented ssh.PublicKey) error {
			if !bytes.Equal(presented.Marshal(), key.Marshal()) {
				return ErrHostKeyChanged
			}
			return nil
		}}
	client, err := transport.handshake(ctx, target, config)
	if err != nil {
		for _, safe := range []error{ErrHostKeyChanged, ErrInvalidTarget, ErrCancelled} {
			if errors.Is(err, safe) {
				return nil, safe
			}
		}
		return nil, ErrTransport
	}
	return &Client{ssh: client}, nil
}

type boundedOutput struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	overflow bool
	close    func()
}

func (output *boundedOutput) Write(raw []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	if len(raw) > MaxOutputBytes-output.buffer.Len() {
		output.overflow = true
		if output.close != nil {
			output.close()
		}
		return 0, ErrOutputLimit
	}
	return output.buffer.Write(raw)
}

type HostFacts struct {
	Kernel       string
	Architecture string
	UID          uint32
	Hostname     string
	OSID         string
	OSVersion    string
	CPUCount     uint32
	MemoryKiB    uint64
	DiskTotalKiB uint64
	DiskFreeKiB  uint64
	DiskScope    string
	BorealisPath string
	K3sUnit      string
}

// SupportedPlatform covers OS/architecture only. It does not prove sizing,
// storage, network, privilege, clean-host state or membership eligibility.
func (facts HostFacts) SupportedPlatform() bool {
	if facts.Kernel != "Linux" || facts.Architecture != "x86_64" || facts.OSID != "ubuntu" {
		return false
	}
	majorText, minorText, ok := strings.Cut(facts.OSVersion, ".")
	major, majorErr := strconv.ParseUint(majorText, 10, 16)
	minor, minorErr := strconv.ParseUint(minorText, 10, 8)
	return ok && majorErr == nil && minorErr == nil && len(majorText) == 2 && len(minorText) == 2 &&
		(major > 24 || (major == 24 && minor >= 4))
}

// Fixed script contains no target, credentials or caller-provided shell text.
// Missing inventory is reported as unknown or fails parsing; it never establishes
// that an inaccessible path or unavailable service manager is a clean target.
const inspectionCommand = `LC_ALL=C /bin/sh -s <<'BOREALIS_SSH_INSPECT'
set -eu
printf 'kernel='; uname -s
printf 'architecture='; uname -m
printf 'uid='; id -u
printf 'hostname='; hostname -s
if [ -r /etc/os-release ]; then
  awk -F= '$1=="ID" || $1=="VERSION_ID" { value=substr($0,index($0,"=")+1); sub(/^"/,"",value); sub(/"$/,"",value); if ($1=="ID") print "os_id=" value; else print "os_version=" value }' /etc/os-release
else
  printf 'os_id=unknown\nos_version=unknown\n'
fi
printf 'cpu_count='; getconf _NPROCESSORS_ONLN
awk '$1=="MemTotal:" { print "memory_kib=" $2 }' /proc/meminfo
disk_path=/
disk_scope=root
if [ -d /opt ] && [ -x /opt ]; then disk_path=/opt; disk_scope=opt; fi
printf 'disk_scope=%s\n' "$disk_scope"
df -Pk "$disk_path" | awk 'NR==2 { print "disk_total_kib=" $2; print "disk_free_kib=" $4 }'
if [ ! -x /opt ]; then
  printf 'borealis_path=unknown\n'
elif [ -e /opt/Borealis ] || [ -L /opt/Borealis ]; then
  printf 'borealis_path=present\n'
else
  printf 'borealis_path=absent\n'
fi
k3s_unit=$(systemctl show k3s.service --property=LoadState --value 2>/dev/null) || k3s_unit=unknown
case "$k3s_unit" in loaded|not-found|masked|error|bad-setting) ;; *) k3s_unit=unknown ;; esac
printf 'k3s_unit=%s\n' "$k3s_unit"
BOREALIS_SSH_INSPECT`

func parseFacts(raw []byte) (HostFacts, error) {
	if !utf8.Valid(raw) {
		return HostFacts{}, ErrInspection
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found || values[key] != "" || !factPattern.MatchString(value) {
			return HostFacts{}, ErrInspection
		}
		switch key {
		case "kernel", "architecture", "uid", "hostname", "os_id", "os_version", "cpu_count", "memory_kib", "disk_total_kib", "disk_free_kib", "disk_scope", "borealis_path", "k3s_unit":
			values[key] = value
		default:
			return HostFacts{}, ErrInspection
		}
	}
	uid, err := strconv.ParseUint(values["uid"], 10, 32)
	if err != nil || len(values) != 13 {
		return HostFacts{}, ErrInspection
	}
	cpu, cpuErr := strconv.ParseUint(values["cpu_count"], 10, 32)
	memory, memoryErr := strconv.ParseUint(values["memory_kib"], 10, 64)
	disk, diskErr := strconv.ParseUint(values["disk_total_kib"], 10, 64)
	free, freeErr := strconv.ParseUint(values["disk_free_kib"], 10, 64)
	const maxJSONInteger = 1<<53 - 1
	if cpuErr != nil || cpu == 0 || memoryErr != nil || memory == 0 || memory > maxJSONInteger ||
		diskErr != nil || disk == 0 || disk > maxJSONInteger || freeErr != nil || free > disk {
		return HostFacts{}, ErrInspection
	}
	if !oneOf(values["disk_scope"], "root", "opt") || !oneOf(values["borealis_path"], "present", "absent", "unknown") ||
		!oneOf(values["k3s_unit"], "loaded", "not-found", "masked", "error", "bad-setting", "unknown") {
		return HostFacts{}, ErrInspection
	}
	return HostFacts{Kernel: values["kernel"], Architecture: values["architecture"], UID: uint32(uid), Hostname: values["hostname"],
		OSID: values["os_id"], OSVersion: values["os_version"], CPUCount: uint32(cpu), MemoryKiB: memory,
		DiskTotalKiB: disk, DiskFreeKiB: free, DiskScope: values["disk_scope"], BorealisPath: values["borealis_path"], K3sUnit: values["k3s_unit"]}, nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

// Inspect executes one fixed read-only command. No caller-provided shell text,
// environment variables, stdin or credentials enter the command channel.
func (client *Client) Inspect(ctx context.Context) (HostFacts, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { client.Close() })
	defer stop()
	session, err := client.ssh.NewSession()
	if err != nil {
		if ctx.Err() != nil {
			return HostFacts{}, ErrCancelled
		}
		return HostFacts{}, ErrTransport
	}
	defer session.Close()
	output := boundedOutput{close: func() { client.Close() }}
	diagnostics := boundedOutput{close: func() { client.Close() }}
	session.Stdout = &output
	session.Stderr = &diagnostics
	err = session.Run(inspectionCommand)
	if output.overflow || diagnostics.overflow {
		return HostFacts{}, ErrOutputLimit
	}
	if ctx.Err() != nil {
		return HostFacts{}, ErrCancelled
	}
	if err != nil {
		return HostFacts{}, errors.New("SSH host inspection failed; remote diagnostics withheld")
	}
	return parseFacts(output.buffer.Bytes())
}
