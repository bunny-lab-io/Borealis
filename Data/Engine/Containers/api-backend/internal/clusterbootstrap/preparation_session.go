package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"
)

const PreparationChunkBytes = 12 << 10
const MaxPreparationTransferBytes = MaxBundleBytes + MaxPreparationConfigBytes + MaxManifestBytes + engineidentity.MaxEnvelopeBytes + 256
const MaxPreparationSessionOutputBytes = 16 << 20

// PreparationSessionRequest is separate from joined identity recovery. The
// existing session remains verify-only here; this outer fixed action selects
// source/account preparation, retained-fence pre-join identity installation or
// proof-only reconciliation, never arbitrary commands or consumer startup.
type PreparationSessionRequest struct {
	Session           SessionRequest          `json:"session"`
	Action            string                  `json:"action"`
	ExpectedBase64    string                  `json:"expected_base64"`
	IdentitySource    *engineidentity.Binding `json:"identity_source,omitempty"`
	PostgresReference string                  `json:"postgres_reference,omitempty"`
}

func NewPreparationSessionRequest(expected PreparationExpected, manager, nonce, action string) (PreparationSessionRequest, error) {
	return newPreparationSessionRequest(expected, manager, nonce, action, nil)
}

// NewPrejoinPreparationSessionRequest carries public original-source identity,
// never private keys or an invented target Node UID. The source binding must be
// independently proved by the controller for every authority renewal.
func NewPrejoinPreparationSessionRequest(expected PreparationExpected, manager, nonce, action string, source engineidentity.Binding) (PreparationSessionRequest, error) {
	return newPreparationSessionRequest(expected, manager, nonce, action, &source)
}

func newPreparationSessionRequest(expected PreparationExpected, manager, nonce, action string, source *engineidentity.Binding) (PreparationSessionRequest, error) {
	return preparationSessionRequestWithArtifacts(expected, manager, nonce, action, source, "")
}

func NewArtifactPreparationSessionRequest(expected PreparationExpected, manager, nonce, action string, source engineidentity.Binding, postgresReference string) (PreparationSessionRequest, error) {
	if !artifactPreparationAction(action) {
		return PreparationSessionRequest{}, ErrSessionProtocol
	}
	return preparationSessionRequestWithArtifacts(expected, manager, nonce, action, &source, postgresReference)
}

func preparationSessionRequestWithArtifacts(expected PreparationExpected, manager, nonce, action string, source *engineidentity.Binding, postgresReference string) (PreparationSessionRequest, error) {
	raw, err := json.Marshal(expected)
	if err != nil {
		return PreparationSessionRequest{}, ErrSessionProtocol
	}
	r := PreparationSessionRequest{Session: SessionRequest{Protocol: 1, Action: "verify", Nonce: nonce, Binding: expected.Target,
		Repository: expected.Source.Repository, Release: expected.Source.Release, SourceSHA: expected.Source.SourceSHA,
		AllowQualification: expected.Source.AllowQualification, ManagerSHA256: manager}, Action: action, ExpectedBase64: base64.StdEncoding.EncodeToString(raw), IdentitySource: source, PostgresReference: postgresReference}
	_, err = r.Expected()
	return r, err
}

func (r PreparationSessionRequest) Expected() (PreparationExpected, error) {
	var expected PreparationExpected
	if r.Session.Validate() != nil || r.Session.Action != "verify" || len(r.ExpectedBase64) > 24<<10 {
		return expected, ErrSessionProtocol
	}
	if artifactPreparationAction(r.Action) {
		if !ValidPostgresImageReference(r.PostgresReference) {
			return expected, ErrSessionProtocol
		}
	} else if r.PostgresReference != "" {
		return expected, ErrSessionProtocol
	}
	switch r.Action {
	case "stage_source", "reconcile_source", "reconcile_source_absent", "prepare_runtime_identity", "complete_runtime_identity", "reconcile_runtime_identity", "inspect_preparation_progress":
		if r.IdentitySource != nil {
			return expected, ErrSessionProtocol
		}
	case "prepare_prejoin_identity_root", "reconcile_prejoin_identity_root", "install_prejoin_identity", "reconcile_prejoin_identity",
		"prepare_prejoin_host", "complete_prejoin_host", "complete_prejoin_firewall", "resume_fresh_prejoin_host", "resume_repository_prejoin_host", "reconcile_prejoin_host", "publish_prejoin_artifacts", "reconcile_prejoin_artifacts":
		if r.IdentitySource == nil || validatePrejoinSource(r.Session.Binding, *r.IdentitySource) != nil {
			return expected, ErrSessionProtocol
		}
	default:
		return expected, ErrSessionProtocol
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(r.ExpectedBase64)
	if err != nil || len(raw) == 0 || len(raw) > MaxManifestBytes || base64.StdEncoding.EncodeToString(raw) != r.ExpectedBase64 || json.Unmarshal(raw, &expected) != nil || expected.Validate() != nil {
		return PreparationExpected{}, ErrSessionProtocol
	}
	canonical, err := json.Marshal(expected)
	if err != nil || !bytes.Equal(raw, canonical) || expected.Target != r.Session.Binding || expected.Source != (Expected{r.Session.Repository, r.Session.Release, r.Session.SourceSHA, r.Session.AllowQualification}) {
		return PreparationExpected{}, ErrSessionProtocol
	}
	return expected, nil
}

func validatePrejoinSource(target SessionBinding, source engineidentity.Binding) error {
	if target.Validate() != nil || source.ClusterID != target.ClusterID || !sessionUUID.MatchString(source.SourceID) ||
		source.SourceID == "00000000-0000-0000-0000-000000000000" || source.SourceID == target.TargetID || !sessionKubernetesUID.MatchString(source.SourceUID) {
		return ErrSessionProtocol
	}
	return nil
}

func (r PreparationSessionRequest) Digest() (string, error) {
	if _, err := r.Expected(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return "", ErrSessionProtocol
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Data is private base64 payload. Never log frames or return them through API
// progress. Completion contains only session/request identity, not config hash.
type PreparationSessionFrame struct {
	Protocol      int    `json:"protocol"`
	State         string `json:"state"`
	Nonce         string `json:"nonce"`
	RequestSHA256 string `json:"request_sha256,omitempty"`
	Sequence      int64  `json:"sequence,omitempty"`
	Data          string `json:"data,omitempty"`
	JournalStep   string `json:"journal_step,omitempty"`
}

func (PreparationSessionFrame) String() string   { return "preparation frame [private data withheld]" }
func (PreparationSessionFrame) GoString() string { return "preparation frame [private data withheld]" }

type PreparationSessionReceiver func(context.Context, PreparationSessionRequest, io.Reader) error

type PreparationSessionProgressReceiver func(context.Context, PreparationSessionRequest, io.Reader) (PreparationProgress, error)

// ServePreparationSessionWithProgress adds only a bounded journal cursor to the
// inspection action. Other actions must return the zero cursor. The cursor is
// emitted only after complete private transfer, EOF and successful processing.
func ServePreparationSessionWithProgress(parent context.Context, input io.ReadCloser, output io.WriteCloser, nonce string,
	verify func(context.Context, PreparationSessionRequest) error, receive PreparationSessionProgressReceiver, observe func(time.Time) error) error {
	ctx, cancel := context.WithTimeout(parent, SessionLifetime)
	defer cancel()
	return servePreparationSessionWithProgress(ctx, input, output, nonce, verify, receive, observe, sessionTiming{SessionChallengeEvery, SessionLeaseWindow, 2 * time.Second})
}

// ServePreparationSession maintains challenge authority while the receiver
// consumes a bounded, backpressured stream. Only one unacknowledged chunk may
// exist. Handler must validate complete transfer and EOF before any mutation.
// The independent executor watchdog bounds stalled IO/processes as in recovery.
func ServePreparationSession(parent context.Context, input io.ReadCloser, output io.WriteCloser, nonce string,
	verify func(context.Context, PreparationSessionRequest) error, receive PreparationSessionReceiver, observe func(time.Time) error) error {
	ctx, cancel := context.WithTimeout(parent, SessionLifetime)
	defer cancel()
	return servePreparationSession(ctx, input, output, nonce, verify, receive, observe, sessionTiming{SessionChallengeEvery, SessionLeaseWindow, 2 * time.Second})
}

func servePreparationSession(parent context.Context, input io.ReadCloser, output io.WriteCloser, nonce string,
	verify func(context.Context, PreparationSessionRequest) error, receive PreparationSessionReceiver, observe func(time.Time) error, timing sessionTiming) error {
	if receive == nil {
		return ErrSessionProtocol
	}
	return servePreparationSessionWithProgress(parent, input, output, nonce, verify,
		func(ctx context.Context, request PreparationSessionRequest, stream io.Reader) (PreparationProgress, error) {
			return PreparationProgress{}, receive(ctx, request, stream)
		}, observe, timing)
}

func servePreparationSessionWithProgress(parent context.Context, input io.ReadCloser, output io.WriteCloser, nonce string,
	verify func(context.Context, PreparationSessionRequest) error, receive PreparationSessionProgressReceiver, observe func(time.Time) error, timing sessionTiming) error {
	return servePreparationSessionProfile(parent, input, output, nonce, verify, receive, observe, timing, preparationProfile(false))
}

func ServeArtifactPreparationSession(parent context.Context, input io.ReadCloser, output io.WriteCloser, nonce string,
	verify func(context.Context, PreparationSessionRequest) error, receive PreparationSessionProgressReceiver, observe func(time.Time) error) error {
	ctx, cancel := context.WithTimeout(parent, ArtifactPreparationLifetime)
	defer cancel()
	return servePreparationSessionProfile(ctx, input, output, nonce, verify, receive, observe, sessionTiming{SessionChallengeEvery, SessionLeaseWindow, 2 * time.Second}, preparationProfile(true))
}

func servePreparationSessionProfile(parent context.Context, input io.ReadCloser, output io.WriteCloser, nonce string,
	verify func(context.Context, PreparationSessionRequest) error, receive PreparationSessionProgressReceiver, observe func(time.Time) error, timing sessionTiming, profile preparationTransportProfile) error {
	if input == nil || output == nil || verify == nil || receive == nil || observe == nil || !digestPattern.MatchString(nonce) {
		return ErrSessionProtocol
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(ctx, func() { input.Close(); output.Close() })
	defer func() { cancel(); input.Close(); output.Close(); stop() }()
	deadline := time.Now().Add(timing.window)
	if observe(deadline) != nil {
		return ErrSessionAuthority
	}
	expiry := time.AfterFunc(time.Until(deadline), cancel)
	defer expiry.Stop()
	reader := bufio.NewReaderSize(input, 4098)
	if readSessionPreamble(reader, nonce) != nil || WriteSessionFrame(output, PreparationSessionFrame{Protocol: 1, State: "ready", Nonce: nonce}) != nil {
		return ErrSessionProtocol
	}
	var request PreparationSessionRequest
	if ReadSessionFrame(reader, &request) != nil || request.Session.Nonce != nonce {
		return ErrSessionProtocol
	}
	digest, err := request.Digest()
	if err != nil || ctx.Err() != nil || artifactPreparationAction(request.Action) != profile.artifacts {
		return ErrSessionProtocol
	}
	frames := make(chan PreparationSessionFrame)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			var frame PreparationSessionFrame
			if readSessionFrameBounded(reader, &frame, profile.frame) != nil {
				return
			}
			select {
			case frames <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() { cancel(); input.Close(); <-readDone }()
	chunks := make(chan []byte)
	chunkReader := &preparationChunkReader{ctx: ctx, chunks: chunks}
	type receiverResult struct {
		progress PreparationProgress
		err      error
	}
	finished := make(chan receiverResult, 1)
	working := false
	defer func() {
		cancel()
		if working {
			select {
			case <-finished:
			case <-time.After(timing.stopGrace):
			}
		}
	}()
	phase := "verify"
	var pendingChunk []byte
	defer func() { clear(pendingChunk) }()
	var sequence, total int64
	challenge := ""
	issued := time.Time{}
	pendingChallenge := false
	send := func(state string, seq int64) error {
		return WriteSessionFrame(output, PreparationSessionFrame{Protocol: 1, State: state, Nonce: nonce, RequestSHA256: digest, Sequence: seq})
	}
	issue := func() error {
		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			return ErrSessionProtocol
		}
		challenge, issued, pendingChallenge = hex.EncodeToString(random[:]), time.Now(), true
		return WriteSessionFrame(output, PreparationSessionFrame{Protocol: 1, State: "challenge", Nonce: challenge, RequestSHA256: digest})
	}
	if issue() != nil {
		return ErrSessionProtocol
	}
	ticker := time.NewTicker(timing.challenge)
	defer ticker.Stop()
	for {
		var deliver chan []byte
		if pendingChunk != nil {
			deliver = chunks
		}
		select {
		case <-ctx.Done():
			return ErrSessionAuthority
		case <-readDone:
			return ErrSessionProtocol
		case deliver <- pendingChunk:
			pendingChunk = nil
			if send("ack", sequence) != nil {
				return ErrSessionProtocol
			}
			sequence++
		case frame := <-frames:
			if frame.Protocol != 1 || frame.RequestSHA256 != digest || frame.JournalStep != "" || !time.Now().Before(deadline) {
				return ErrSessionProtocol
			}
			if frame.State == "heartbeat" {
				if !pendingChallenge || frame.Nonce != challenge || frame.Sequence != 0 || frame.Data != "" {
					return ErrSessionAuthority
				}
				pendingChallenge = false
				deadline = issued.Add(timing.window)
				expiry.Reset(time.Until(deadline))
				if observe(deadline) != nil {
					return ErrSessionAuthority
				}
				if phase == "verify" && !working {
					working = true
					go func() { finished <- receiverResult{err: verify(ctx, request)} }()
				}
				continue
			}
			if phase != "receiving" || pendingChunk != nil || frame.Nonce != nonce || frame.Sequence != sequence {
				return ErrSessionProtocol
			}
			switch frame.State {
			case "chunk":
				if len(frame.Data) == 0 || len(frame.Data) > base64.StdEncoding.EncodedLen(profile.chunk) {
					return ErrSessionProtocol
				}
				data, err := base64.StdEncoding.Strict().DecodeString(frame.Data)
				if err != nil || len(data) == 0 || base64.StdEncoding.EncodeToString(data) != frame.Data || total+int64(len(data)) > profile.bytes {
					clear(data)
					return ErrSessionProtocol
				}
				total += int64(len(data))
				pendingChunk = data
			case "end":
				if frame.Data != "" || total == 0 {
					return ErrSessionProtocol
				}
				close(chunks)
				phase = "processing"
			default:
				return ErrSessionProtocol
			}
		case result := <-finished:
			working = false
			if result.err != nil || ctx.Err() != nil || !time.Now().Before(deadline) {
				return ErrSessionAuthority
			}
			if phase == "verify" {
				phase, working = "receiving", true
				go func() {
					progress, err := receive(ctx, request, chunkReader)
					clear(chunkReader.current)
					finished <- receiverResult{progress, err}
				}()
				if send("source_ready", 0) != nil {
					return ErrSessionProtocol
				}
				continue
			}
			if phase != "processing" || !chunkReader.eof {
				return ErrSessionProtocol
			}
			if request.Action == "inspect_preparation_progress" {
				if result.progress.Validate() != nil {
					return ErrSessionProtocol
				}
			} else if result.progress != (PreparationProgress{}) {
				return ErrSessionProtocol
			}
			return WriteSessionFrame(output, PreparationSessionFrame{Protocol: 1, State: "verified", Nonce: nonce, RequestSHA256: digest, Sequence: sequence, JournalStep: result.progress.Step})
		case <-ticker.C:
			if !pendingChallenge && issue() != nil {
				return ErrSessionProtocol
			}
		}
	}
}

type preparationChunkReader struct {
	ctx     context.Context
	chunks  <-chan []byte
	current []byte
	eof     bool
}

func (r *preparationChunkReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.ctx.Err() != nil {
		return 0, ErrSessionAuthority
	}
	if len(r.current) == 0 {
		select {
		case data, ok := <-r.chunks:
			if !ok {
				r.eof = true
				return 0, io.EOF
			}
			r.current = data
		case <-r.ctx.Done():
			return 0, ErrSessionAuthority
		}
	}
	n := copy(p, r.current)
	clear(r.current[:n])
	r.current = r.current[n:]
	return n, nil
}
