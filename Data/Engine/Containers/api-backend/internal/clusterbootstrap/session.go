package clusterbootstrap

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"borealis/api-backend/internal/engineidentity"
)

const (
	MaxSessionFrameBytes  = 32 << 10
	SessionLifetime       = 5 * time.Minute
	SessionLeaseWindow    = 20 * time.Second
	SessionChallengeEvery = 5 * time.Second
)

var (
	ErrSessionProtocol   = errors.New("node bootstrap session protocol invalid; remote data withheld")
	ErrSessionAuthority  = errors.New("node bootstrap session authority expired or rejected")
	sessionUUID          = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	sessionKubernetesUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	sessionHostname      = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)
	sessionMachineID     = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// SessionBinding identifies the exact worker claim and observed SSH host.
// Controller/target authority must be refreshed by sender for each challenge;
// an SSH login or a serialized binding alone does not authorize continued work.
type SessionBinding struct {
	ClusterID          string `json:"cluster_id"`
	OperationID        string `json:"operation_id"`
	TargetID           string `json:"target_id"`
	HolderID           string `json:"holder_id"`
	Generation         int64  `json:"generation"`
	OperationAttempt   int64  `json:"operation_attempt"`
	Address            string `json:"address"`
	Port               int    `json:"port"`
	Hostname           string `json:"hostname"`
	MachineID          string `json:"machine_id"`
	HostKeyAlgorithm   string `json:"host_key_algorithm"`
	HostKeyFingerprint string `json:"host_key_fingerprint"`
}

func (b SessionBinding) Validate() error {
	for _, id := range []string{b.ClusterID, b.OperationID, b.TargetID, b.HolderID} {
		if !sessionUUID.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
			return ErrSessionProtocol
		}
	}
	address, err := netip.ParseAddr(b.Address)
	encoded, prefix := strings.CutPrefix(b.HostKeyFingerprint, "SHA256:")
	digest, digestErr := base64.RawStdEncoding.Strict().DecodeString(encoded)
	if err != nil || !address.Is4() || !address.IsPrivate() || address.String() != b.Address || b.Port < 1 || b.Port > 65535 ||
		b.Generation < 1 || b.Generation > 1<<53-1 || b.OperationAttempt < 1 || b.OperationAttempt > 1<<53-1 ||
		!sessionHostname.MatchString(b.Hostname) || !sessionMachineID.MatchString(b.MachineID) || b.MachineID == strings.Repeat("0", 32) ||
		!prefix || digestErr != nil || len(digest) != 32 || base64.RawStdEncoding.EncodeToString(digest) != encoded {
		return ErrSessionProtocol
	}
	switch b.HostKeyAlgorithm {
	case "ssh-ed25519", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "ssh-rsa":
		return nil
	}
	return ErrSessionProtocol
}

// IdentityRecoveryRequest binds read-only recovery qualification to the
// original source member and exact existing target Node. No identity digest or
// private material crosses this public request.
type IdentityRecoveryRequest struct {
	SourceID      string `json:"source_node_id"`
	SourceNodeUID string `json:"source_node_uid"`
	TargetNodeUID string `json:"target_node_uid"`
}

func (r IdentityRecoveryRequest) Validate(binding SessionBinding) error {
	if !sessionUUID.MatchString(r.SourceID) || r.SourceID == "00000000-0000-0000-0000-000000000000" ||
		!sessionKubernetesUID.MatchString(r.SourceNodeUID) || !sessionKubernetesUID.MatchString(r.TargetNodeUID) ||
		r.SourceID == binding.TargetID || r.SourceNodeUID == r.TargetNodeUID || binding.Validate() != nil {
		return ErrSessionProtocol
	}
	return nil
}

// SessionRequest permits host verification, fixed read-only identity recovery
// qualification and private in-memory identity delivery. Preparation, identity
// writes, marker release and join remain absent until their complete journaled
// dispatch contracts exist.
type SessionRequest struct {
	Protocol           int                      `json:"protocol"`
	Action             string                   `json:"action"`
	Nonce              string                   `json:"nonce"`
	Binding            SessionBinding           `json:"binding"`
	Repository         string                   `json:"repository"`
	Release            string                   `json:"release"`
	SourceSHA          string                   `json:"source_sha"`
	ManagerSHA256      string                   `json:"manager_sha256"`
	AllowQualification bool                     `json:"allow_qualification"`
	IdentityRecovery   *IdentityRecoveryRequest `json:"identity_recovery,omitempty"`
}

func (r SessionRequest) Validate() error {
	if r.Protocol != 1 || !digestPattern.MatchString(r.Nonce) || !digestPattern.MatchString(r.ManagerSHA256) || r.Binding.Validate() != nil {
		return ErrSessionProtocol
	}
	switch r.Action {
	case "verify":
		if r.IdentityRecovery != nil {
			return ErrSessionProtocol
		}
	case "verify_identity_recovery", "deliver_identity_recovery":
		if r.IdentityRecovery == nil || r.IdentityRecovery.Validate(r.Binding) != nil {
			return ErrSessionProtocol
		}
	default:
		return ErrSessionProtocol
	}
	return (Expected{r.Repository, r.Release, r.SourceSHA, r.AllowQualification}).Validate()
}

type SessionMessage struct {
	Protocol         int                      `json:"protocol"`
	State            string                   `json:"state"`
	Nonce            string                   `json:"nonce"`
	Binding          *SessionBinding          `json:"binding,omitempty"`
	SourceSHA        string                   `json:"source_sha,omitempty"`
	ManagerSHA256    string                   `json:"manager_sha256,omitempty"`
	IdentityRecovery *IdentityRecoveryRequest `json:"identity_recovery,omitempty"`
	// IdentityEnvelope is allowed only in the private delivery response after
	// target qualification. Never include this message in diagnostics or events.
	IdentityEnvelope *json.RawMessage `json:"identity_envelope,omitempty"`
}

type IdentityReceiver func(context.Context, SessionRequest, *engineidentity.Material) error

// WriteSessionFrame uses length framing so sudo's optional unread password line
// cannot become executable input or confuse a structured request boundary.
func WriteSessionFrame(w io.Writer, value any) error {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) == 0 || len(raw) > MaxSessionFrameBytes {
		return ErrSessionProtocol
	}
	defer clear(raw)
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(raw)))
	if _, err := io.Copy(w, bytes.NewReader(header[:])); err != nil {
		return ErrSessionProtocol
	}
	if _, err := io.Copy(w, bytes.NewReader(raw)); err != nil {
		return ErrSessionProtocol
	}
	return nil
}

func ReadSessionFrame(r io.Reader, value any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return ErrSessionProtocol
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > MaxSessionFrameBytes {
		return ErrSessionProtocol
	}
	raw := make([]byte, size)
	defer clear(raw)
	if _, err := io.ReadFull(r, raw); err != nil || !validSessionJSON(raw) {
		return ErrSessionProtocol
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(&struct{}{}) != io.EOF {
		return ErrSessionProtocol
	}
	return nil
}

func validSessionJSON(raw []byte) bool {
	if len(raw) == 0 || raw[0] != '{' || !utf8.Valid(raw) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var object func(int) bool
	object = func(depth int) bool {
		if depth > 4 {
			return false
		}
		seen := map[string]bool{}
		for d.More() {
			token, err := d.Token()
			name, ok := token.(string)
			if err != nil || !ok || seen[name] || name != strings.ToLower(name) {
				return false
			}
			seen[name] = true
			value, err := d.Token()
			if err != nil || value == nil {
				return false
			}
			if delimiter, ok := value.(json.Delim); ok && (delimiter != '{' || !object(depth+1)) {
				return false
			}
		}
		token, err := d.Token()
		return err == nil && token == json.Delim('}')
	}
	start, err := d.Token()
	return err == nil && start == json.Delim('{') && object(0) && d.Decode(&struct{}{}) == io.EOF
}

func SessionPreamble(nonce string) (string, error) {
	if !digestPattern.MatchString(nonce) {
		return "", ErrSessionProtocol
	}
	return "BOREALIS-BOOTSTRAP-V1:" + nonce + "\n", nil
}

func readSessionPreamble(r *bufio.Reader, nonce string) error {
	expected, err := SessionPreamble(nonce)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		line, err := r.ReadSlice('\n')
		if err != nil || len(line) > 4097 {
			clear(line)
			return ErrSessionProtocol
		}
		matched := bytes.Equal(line, []byte(expected))
		validPasswordLine := utf8.Valid(line) && !bytes.ContainsAny(line[:len(line)-1], "\x00\r\n")
		clear(line) // May be unused sudo password; never decode/log it.
		if matched {
			return nil
		}
		if attempt != 0 || !validPasswordLine {
			return ErrSessionProtocol
		}
	}
	return ErrSessionProtocol
}

type sessionTiming struct {
	challenge, window, stopGrace time.Duration
}

// ServeSession requires a fresh unpredictable challenge response before work,
// then throughout work. A buffered old heartbeat cannot extend authority: only
// the current challenge is valid and expiry is measured from issue time.
// Handler owns cancellation/joining of any children before returning. Current
// protocol allows read-only verification only; it has no mutation journal.
func ServeSession(ctx context.Context, input io.ReadCloser, output io.WriteCloser, nonce string, verify func(context.Context, SessionRequest) error) error {
	ctx, cancel := context.WithTimeout(ctx, SessionLifetime)
	defer cancel()
	return serveSession(ctx, input, output, nonce, verify, sessionTiming{SessionChallengeEvery, SessionLeaseWindow, 2 * time.Second})
}

// ServeSupervisedSession shares the exact challenge protocol while publishing
// only locally derived authority deadlines to an independent executor guard.
// An observer cannot grant work; rejection aborts the session before dispatch.
func ServeSupervisedSession(ctx context.Context, input io.ReadCloser, output io.WriteCloser, nonce string, verify func(context.Context, SessionRequest) error, observe func(time.Time) error) error {
	if observe == nil {
		return ErrSessionAuthority
	}
	ctx, cancel := context.WithTimeout(ctx, SessionLifetime)
	defer cancel()
	return serveSessionObserved(ctx, input, output, nonce, verify, nil, sessionTiming{SessionChallengeEvery, SessionLeaseWindow, 2 * time.Second}, observe)
}

// ServeSupervisedIdentitySession adds one private, binding-checked envelope
// after read-only target qualification. Receiver may inspect material only in
// memory; this protocol provides no journal, file, marker or restart dispatch.
func ServeSupervisedIdentitySession(ctx context.Context, input io.ReadCloser, output io.WriteCloser, nonce string, verify func(context.Context, SessionRequest) error, receive IdentityReceiver, observe func(time.Time) error) error {
	if receive == nil || observe == nil {
		return ErrSessionAuthority
	}
	ctx, cancel := context.WithTimeout(ctx, SessionLifetime)
	defer cancel()
	return serveSessionObserved(ctx, input, output, nonce, verify, receive, sessionTiming{SessionChallengeEvery, SessionLeaseWindow, 2 * time.Second}, observe)
}

func serveSession(ctx context.Context, input io.ReadCloser, output io.WriteCloser, nonce string, verify func(context.Context, SessionRequest) error, timing sessionTiming) error {
	return serveSessionObserved(ctx, input, output, nonce, verify, nil, timing, nil)
}

func serveSessionObserved(ctx context.Context, input io.ReadCloser, output io.WriteCloser, nonce string, verify func(context.Context, SessionRequest) error, receive IdentityReceiver, timing sessionTiming, observe func(time.Time) error) error {
	if input == nil || output == nil {
		return ErrSessionProtocol
	}
	ctx, cancel := context.WithCancel(ctx)
	stopIO := context.AfterFunc(ctx, func() { _ = input.Close(); _ = output.Close() })
	defer func() {
		cancel()
		_ = input.Close()
		_ = output.Close()
		stopIO()
	}()
	if _, err := SessionPreamble(nonce); err != nil || verify == nil {
		return ErrSessionProtocol
	}
	reader := bufio.NewReaderSize(input, 4098)
	// Initial framing cannot wait forever before the authority guard starts.
	initialDeadline := time.Now().Add(timing.window)
	if observe != nil && observe(initialDeadline) != nil {
		return ErrSessionAuthority
	}
	initial := time.AfterFunc(time.Until(initialDeadline), cancel)
	defer initial.Stop()
	if err := readSessionPreamble(reader, nonce); err != nil {
		return err
	}
	if WriteSessionFrame(output, SessionMessage{Protocol: 1, State: "ready", Nonce: nonce}) != nil {
		return ErrSessionProtocol
	}
	var request SessionRequest
	if ReadSessionFrame(reader, &request) != nil || request.Validate() != nil || request.Nonce != nonce || ctx.Err() != nil {
		return ErrSessionProtocol
	}
	initial.Stop()
	frames := make(chan SessionMessage, 1)
	readFailed := make(chan struct{})
	go func() {
		defer close(readFailed)
		for {
			var message SessionMessage
			if ReadSessionFrame(reader, &message) != nil {
				return
			}
			select {
			case frames <- message:
			case <-ctx.Done():
				return
			}
		}
	}()
	workCtx, stopWork := context.WithCancel(ctx)
	defer stopWork()
	finished := make(chan error, 1)
	started := false
	phase := "verify"
	deliveryNonce := ""
	defer func() {
		stopWork()
		if started {
			select {
			case <-finished:
			case <-time.After(timing.stopGrace):
			}
		}
	}()
	challenge := ""
	issued, deadline := time.Time{}, time.Now().Add(timing.window)
	if observe != nil && observe(deadline) != nil {
		return ErrSessionAuthority
	}
	pending := false
	issue := func() error {
		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			return ErrSessionProtocol
		}
		challenge, issued, pending = hex.EncodeToString(random[:]), time.Now(), true
		return WriteSessionFrame(output, SessionMessage{Protocol: 1, State: "challenge", Nonce: challenge})
	}
	boundMessage := func(message SessionMessage, state, expectedNonce string) bool {
		return message.Protocol == 1 && message.State == state && message.Nonce == expectedNonce && message.Binding != nil && *message.Binding == request.Binding &&
			message.SourceSHA == request.SourceSHA && message.ManagerSHA256 == request.ManagerSHA256 && sameIdentityRecovery(message.IdentityRecovery, request.IdentityRecovery)
	}
	// Cancellation is independent of output writes or a slow verifier.
	expiry := time.AfterFunc(time.Until(deadline), cancel)
	defer expiry.Stop()
	if issue() != nil {
		return ErrSessionProtocol
	}
	ticker := time.NewTicker(timing.challenge)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ErrSessionAuthority
		case <-readFailed:
			return ErrSessionProtocol
		case message := <-frames:
			if pending && message.Protocol == 1 && message.State == "heartbeat" && message.Nonce == challenge && message.Binding == nil && message.SourceSHA == "" && message.ManagerSHA256 == "" && message.IdentityRecovery == nil && message.IdentityEnvelope == nil && time.Now().Before(deadline) {
				pending = false
				deadline = issued.Add(timing.window)
				expiry.Reset(time.Until(deadline))
				if observe != nil && observe(deadline) != nil {
					return ErrSessionAuthority
				}
				if !started && phase == "verify" {
					started = true
					go func() { finished <- verify(workCtx, request) }()
				}
				continue
			}
			if phase != "await_identity" || message.IdentityEnvelope == nil || len(*message.IdentityEnvelope) == 0 || !boundMessage(message, "identity", deliveryNonce) || !time.Now().Before(deadline) {
				if message.IdentityEnvelope != nil {
					clear(*message.IdentityEnvelope)
				}
				return ErrSessionAuthority
			}
			expected := engineidentity.Binding{ClusterID: request.Binding.ClusterID, SourceID: request.IdentityRecovery.SourceID, SourceUID: request.IdentityRecovery.SourceNodeUID}
			material, err := engineidentity.Import(*message.IdentityEnvelope, expected)
			clear(*message.IdentityEnvelope)
			if err != nil || receive == nil || workCtx.Err() != nil {
				return ErrSessionAuthority
			}
			phase, started = "consume_identity", true
			go func() { finished <- receive(workCtx, request, material) }()
		case err := <-finished:
			started = false
			if err != nil || ctx.Err() != nil || !time.Now().Before(deadline) {
				return ErrSessionAuthority
			}
			if phase == "verify" && request.Action == "deliver_identity_recovery" {
				if receive == nil {
					return ErrSessionAuthority
				}
				var random [32]byte
				if _, err := rand.Read(random[:]); err != nil {
					return ErrSessionProtocol
				}
				deliveryNonce = hex.EncodeToString(random[:])
				phase = "await_identity"
				if WriteSessionFrame(output, SessionMessage{Protocol: 1, State: "identity_ready", Nonce: deliveryNonce, Binding: &request.Binding, SourceSHA: request.SourceSHA, ManagerSHA256: request.ManagerSHA256, IdentityRecovery: request.IdentityRecovery}) != nil {
					return ErrSessionProtocol
				}
				continue
			}
			return WriteSessionFrame(output, SessionMessage{Protocol: 1, State: "verified", Nonce: nonce, Binding: &request.Binding, SourceSHA: request.SourceSHA, ManagerSHA256: request.ManagerSHA256, IdentityRecovery: request.IdentityRecovery})
		case <-ticker.C:
			if !pending && issue() != nil {
				return ErrSessionProtocol
			}
		}
	}
}

func sameIdentityRecovery(observed, expected *IdentityRecoveryRequest) bool {
	if observed == nil || expected == nil {
		return observed == nil && expected == nil
	}
	return *observed == *expected
}
