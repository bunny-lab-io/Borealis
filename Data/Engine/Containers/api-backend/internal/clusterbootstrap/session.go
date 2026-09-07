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
)

const (
	MaxSessionFrameBytes  = 32 << 10
	SessionLifetime       = 5 * time.Minute
	SessionLeaseWindow    = 20 * time.Second
	SessionChallengeEvery = 5 * time.Second
)

var (
	ErrSessionProtocol  = errors.New("node bootstrap session protocol invalid; remote data withheld")
	ErrSessionAuthority = errors.New("node bootstrap session authority expired or rejected")
	sessionUUID         = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	sessionHostname     = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)
	sessionMachineID    = regexp.MustCompile(`^[0-9a-f]{32}$`)
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

// SessionRequest initially permits only verification. Preparation, identity
// writes and join are deliberately absent until their journals/fences exist.
type SessionRequest struct {
	Protocol           int            `json:"protocol"`
	Action             string         `json:"action"`
	Nonce              string         `json:"nonce"`
	Binding            SessionBinding `json:"binding"`
	Repository         string         `json:"repository"`
	Release            string         `json:"release"`
	SourceSHA          string         `json:"source_sha"`
	ManagerSHA256      string         `json:"manager_sha256"`
	AllowQualification bool           `json:"allow_qualification"`
}

func (r SessionRequest) Validate() error {
	if r.Protocol != 1 || r.Action != "verify" || !digestPattern.MatchString(r.Nonce) || !digestPattern.MatchString(r.ManagerSHA256) || r.Binding.Validate() != nil {
		return ErrSessionProtocol
	}
	return (Expected{r.Repository, r.Release, r.SourceSHA, r.AllowQualification}).Validate()
}

type SessionMessage struct {
	Protocol      int             `json:"protocol"`
	State         string          `json:"state"`
	Nonce         string          `json:"nonce"`
	Binding       *SessionBinding `json:"binding,omitempty"`
	SourceSHA     string          `json:"source_sha,omitempty"`
	ManagerSHA256 string          `json:"manager_sha256,omitempty"`
}

// WriteSessionFrame uses length framing so sudo's optional unread password line
// cannot become executable input or confuse a structured request boundary.
func WriteSessionFrame(w io.Writer, value any) error {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) == 0 || len(raw) > MaxSessionFrameBytes {
		return ErrSessionProtocol
	}
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

func serveSession(ctx context.Context, input io.ReadCloser, output io.WriteCloser, nonce string, verify func(context.Context, SessionRequest) error, timing sessionTiming) error {
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
	initial := time.AfterFunc(timing.window, cancel)
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
	pending := false
	issue := func() error {
		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			return ErrSessionProtocol
		}
		challenge, issued, pending = hex.EncodeToString(random[:]), time.Now(), true
		return WriteSessionFrame(output, SessionMessage{Protocol: 1, State: "challenge", Nonce: challenge})
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
			if !pending || message.Protocol != 1 || message.State != "heartbeat" || message.Nonce != challenge || message.Binding != nil || message.SourceSHA != "" || message.ManagerSHA256 != "" || !time.Now().Before(deadline) {
				return ErrSessionAuthority
			}
			pending = false
			deadline = issued.Add(timing.window)
			expiry.Reset(time.Until(deadline))
			if !started {
				started = true
				go func() { finished <- verify(workCtx, request) }()
			}
		case err := <-finished:
			started = false
			if err != nil || ctx.Err() != nil || !time.Now().Before(deadline) {
				return ErrSessionAuthority
			}
			return WriteSessionFrame(output, SessionMessage{Protocol: 1, State: "verified", Nonce: nonce, Binding: &request.Binding, SourceSHA: request.SourceSHA, ManagerSHA256: request.ManagerSHA256})
		case <-ticker.C:
			if !pending && issue() != nil {
				return ErrSessionProtocol
			}
		}
	}
}
