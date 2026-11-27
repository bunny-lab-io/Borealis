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
	SSHStream         bool                    `json:"ssh_stream,omitempty"`
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
		Development: expected.Source.Development, AllowQualification: expected.Source.AllowQualification, ManagerSHA256: manager}, SSHStream: true, Action: action, ExpectedBase64: base64.StdEncoding.EncodeToString(raw), IdentitySource: source, PostgresReference: postgresReference}
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
		"prepare_prejoin_host", "complete_prejoin_host", "complete_prejoin_firewall", "resume_fresh_prejoin_host", "resume_repository_prejoin_host", "reconcile_prejoin_host", "publish_prejoin_artifacts", "reconcile_prejoin_artifacts", "install_prejoin_k3s", "reconcile_prejoin_k3s", "configure_prejoin_k3s", "reconcile_prejoin_k3s_configuration", "start_prepared_k3s", "reconcile_prepared_k3s_start", "verify_prepared_images", "reconcile_prepared_images", "activate_prejoin_identity", "reconcile_prejoin_identity_activation":
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
	if err != nil || !bytes.Equal(raw, canonical) || expected.Target != r.Session.Binding || expected.Source != (Expected{r.Session.Repository, r.Session.Release, r.Session.SourceSHA, r.Session.AllowQualification, r.Session.Development}) {
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
// progress. Native completion evidence remains on this private channel only.
type PreparationSessionFrame struct {
	Bytes            int64  `json:"bytes,omitempty"`
	StreamSHA256     string `json:"stream_sha256,omitempty"`
	Protocol         int    `json:"protocol"`
	State            string `json:"state"`
	Nonce            string `json:"nonce"`
	RequestSHA256    string `json:"request_sha256,omitempty"`
	GrantMillis      int64  `json:"grant_ms,omitempty"`
	Sequence         int64  `json:"sequence,omitempty"`
	Data             string `json:"data,omitempty"`
	JournalStep      string `json:"journal_step,omitempty"`
	CompletionBase64 string `json:"completion_base64,omitempty"`
}

func (PreparationSessionFrame) String() string   { return "preparation frame [private data withheld]" }
func (PreparationSessionFrame) GoString() string { return "preparation frame [private data withheld]" }

type PreparationSessionReceiver func(context.Context, PreparationSessionRequest, io.Reader) error

type PreparationSessionProgressReceiver func(context.Context, PreparationSessionRequest, io.Reader) (PreparationProgress, error)

// PreparationSessionResult separates the unproved inspection cursor from native
// completion evidence. Only activation reconciliation may return a receipt.
type PreparationSessionResult struct {
	Progress   PreparationProgress
	Completion *PrejoinPreparationReceipt
}
type PreparationSessionResultReceiver func(context.Context, PreparationSessionRequest, io.Reader) (PreparationSessionResult, error)

func servePreparationSessionResult(parent context.Context, input io.ReadCloser, output io.WriteCloser, nonce string,
	verify func(context.Context, PreparationSessionRequest) error, receive PreparationSessionResultReceiver, observe func(time.Time) error, timing sessionTiming, profile preparationTransportProfile, open PreparationStreamOpen) error {
	if open == nil || input == nil || output == nil || verify == nil || receive == nil || observe == nil || !digestPattern.MatchString(nonce) {
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
	if ReadSessionFrame(reader, &request) != nil || request.Session.Nonce != nonce || !request.SSHStream {
		return ErrSessionProtocol
	}
	digest, err := request.Digest()
	if err != nil || ctx.Err() != nil || bulkPreparationAction(request.Action) != profile.artifacts {
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
	endStream := make(chan PreparationStreamReceipt, 1)

	type receiverResult struct {
		value PreparationSessionResult
		err   error
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
		select {
		case <-ctx.Done():
			return ErrSessionAuthority
		case <-readDone:
			return ErrSessionProtocol
		case frame := <-frames:
			if frame.Protocol != 1 || frame.RequestSHA256 != digest || frame.JournalStep != "" || frame.CompletionBase64 != "" || !time.Now().Before(deadline) {
				return ErrSessionProtocol
			}
			if frame.State == "heartbeat" {
				if !pendingChallenge || frame.Nonce != challenge || frame.Sequence != 0 || frame.Data != "" {
					return ErrSessionAuthority
				}
				if frame.Bytes != 0 || frame.StreamSHA256 != "" {
					return ErrSessionProtocol
				}
				pendingChallenge = false
				window := timing.window
				if request.SSHStream && frame.GrantMillis == 0 {
					return ErrSessionAuthority
				}
				if frame.GrantMillis != 0 {
					if frame.GrantMillis < 1 || frame.GrantMillis > timing.window.Milliseconds() {
						return ErrSessionAuthority
					}
					window = time.Duration(frame.GrantMillis) * time.Millisecond
				}
				deadline = issued.Add(window)
				if !time.Now().Before(deadline) {
					return ErrSessionAuthority
				}
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
			if frame.State != "end" || frame.GrantMillis != 0 || phase != "receiving" || frame.Nonce != nonce || frame.Sequence != 0 || frame.Data != "" || frame.Bytes < 1 || frame.Bytes > profile.bytes || !digestPattern.MatchString(frame.StreamSHA256) {
				return ErrSessionProtocol
			}
			endStream <- PreparationStreamReceipt{frame.Bytes, frame.StreamSHA256}
			phase = "processing"
		case result := <-finished:
			working = false
			if result.err != nil || ctx.Err() != nil || !time.Now().Before(deadline) {
				return ErrSessionAuthority
			}
			if phase == "verify" {
				phase, working = "receiving", true
				go func() {
					data, err := open(ctx, nonce)
					if err != nil {
						finished <- receiverResult{err: ErrSessionProtocol}
						return
					}
					stopData := context.AfterFunc(ctx, func() { data.Close() })
					raw := &preparationStreamReader{ctx: ctx, input: data, end: endStream, hash: sha256.New(), maximum: profile.bytes}
					value, err := receive(ctx, request, raw)
					stopData()
					data.Close()
					if !raw.verified {
						err = ErrSessionProtocol
					}
					finished <- receiverResult{value, err}
				}()
				if send("source_ready", 0) != nil {
					return ErrSessionProtocol
				}
				continue
			}
			if phase != "processing" {
				return ErrSessionProtocol
			}
			if request.Action == "inspect_preparation_progress" {
				if result.value.Progress.Validate() != nil {
					return ErrSessionProtocol
				}
			} else if result.value.Progress != (PreparationProgress{}) {
				return ErrSessionProtocol
			}
			completionBase64 := ""
			expected, expectedErr := request.Expected()
			if request.Action == "reconcile_prejoin_identity_activation" {
				if expectedErr != nil || request.IdentitySource == nil || result.value.Completion == nil || result.value.Completion.Validate(expected, *request.IdentitySource) != nil {
					return ErrSessionProtocol
				}
				var encodeErr error
				completionBase64, encodeErr = encodePreparationReceipt(*result.value.Completion)
				if encodeErr != nil {
					return ErrSessionProtocol
				}
			} else if result.value.Completion != nil {
				return ErrSessionProtocol
			}
			return WriteSessionFrame(output, PreparationSessionFrame{Protocol: 1, State: "verified", Nonce: nonce, RequestSHA256: digest, Sequence: 0, JournalStep: result.value.Progress.Step, CompletionBase64: completionBase64})
		case <-ticker.C:
			if !pendingChallenge && issue() != nil {
				return ErrSessionProtocol
			}
		}
	}
}

func ServeSSHPreparation(parent context.Context, input io.ReadCloser, output io.WriteCloser, nonce string, artifacts bool, open PreparationStreamOpen, verify func(context.Context, PreparationSessionRequest) error, receive PreparationSessionResultReceiver, observe func(time.Time) error) error {
	profile := preparationProfile(artifacts)
	ctx, cancel := context.WithTimeout(parent, profile.lifetime)
	defer cancel()
	return servePreparationSessionResult(ctx, input, output, nonce, verify, receive, observe, sessionTiming{SessionChallengeEvery, SessionLeaseWindow, 2 * time.Second}, profile, open)
}
