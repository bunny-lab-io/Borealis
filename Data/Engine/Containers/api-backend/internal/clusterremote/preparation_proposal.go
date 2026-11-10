package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrPreparationProposal = errors.New("SSH preparation proposal observation unavailable; host inventory withheld")

var proposalDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var proposalHostnamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// Version 2 adds read-only supervision prerequisites and absence of retained
// preparation journals/producers. Version 1 clean-host receipts cannot satisfy
// supervised preparation readiness.
const PreparationProposalVersion = 2

// PreparationProposal binds an observation to the original waiting proposal.
// QualificationGeneration is its reserved generation, never a worker claim.
// Source identifies immutable source code; it supplies no mutation authority.
type PreparationProposal struct {
	Version                 int                       `json:"version"`
	Source                  clusterbootstrap.Expected `json:"source"`
	ClusterID               string                    `json:"cluster_id"`
	OperationID             string                    `json:"operation_id"`
	OperationAttempt        int64                     `json:"operation_attempt"`
	ControllerHolder        string                    `json:"controller_holder"`
	PlanSHA256              string                    `json:"plan_sha256"`
	TargetID                string                    `json:"target_id"`
	QualificationGeneration int64                     `json:"qualification_generation"`
	Address                 string                    `json:"address"`
	Port                    int                       `json:"port"`
	HostKeyFingerprint      string                    `json:"host_key_fingerprint"`
	Hostname                string                    `json:"hostname"`
	MachineID               string                    `json:"machine_id"`
	BootID                  string                    `json:"boot_id"`
	Nonce                   string                    `json:"nonce"`
}

func proposalDigest(value string) bool {
	return proposalDigestPattern.MatchString(value) && value != strings.Repeat("0", 64)
}

func (p PreparationProposal) Validate() error {
	if p.Version != PreparationProposalVersion || p.Source.Validate() != nil || !validPublicUUID(p.ClusterID) || !validPublicUUID(p.OperationID) ||
		!validPublicUUID(p.TargetID) || p.OperationAttempt <= 0 || p.QualificationGeneration <= 0 ||
		len(p.ControllerHolder) == 0 || len(p.ControllerHolder) > 1024 || !utf8.ValidString(p.ControllerHolder) ||
		strings.IndexFunc(p.ControllerHolder, unicode.IsControl) >= 0 || !proposalDigest(p.PlanSHA256) || !proposalDigest(p.Nonce) ||
		(Target{Address: p.Address, Port: p.Port}).Validate() != nil || !proposalHostnamePattern.MatchString(p.Hostname) ||
		!machineIDPattern.MatchString(p.MachineID) || p.MachineID == strings.Repeat("0", 32) || !validPublicUUID(p.BootID) {
		return ErrPreparationProposal
	}
	if !strings.HasPrefix(p.HostKeyFingerprint, "SHA256:") {
		return ErrPreparationProposal
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(p.HostKeyFingerprint, "SHA256:"))
	if err != nil || len(raw) != sha256.Size || "SHA256:"+base64.RawStdEncoding.EncodeToString(raw) != p.HostKeyFingerprint {
		return ErrPreparationProposal
	}
	// Match the fixed remote public-argument bound before connecting a session.
	// JSON escapes (not only field byte lengths) contribute to the wire size.
	wire, err := json.Marshal(p)
	if err != nil || base64.RawStdEncoding.EncodedLen(len(wire)) > 8192 {
		return ErrPreparationProposal
	}
	return nil
}

// RequestSHA256 binds every public proposal field, including the fresh nonce.
func (p PreparationProposal) RequestSHA256() (string, error) {
	if p.Validate() != nil {
		return "", ErrPreparationProposal
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", ErrPreparationProposal
	}
	digest := sha256.Sum256(append([]byte("borealis-preparation-proposal-v2\n"), raw...))
	return hex.EncodeToString(digest[:]), nil
}

// PreparationProposalProof contains no account rows, package inventory or host
// configuration. Stable ObservationSHA256 excludes the request/nonce; callers
// must retain it with the exact proposal and recheck their own durable authority.
// This observation is not a preparation claim, effect receipt or readiness proof.
type PreparationProposalProof struct {
	Version           int    `json:"version"`
	RequestSHA256     string `json:"request_sha256"`
	Nonce             string `json:"nonce"`
	ObservationSHA256 string `json:"observation_sha256"`
}

func (proof PreparationProposalProof) Validate(p PreparationProposal) error {
	digest, err := p.RequestSHA256()
	if err != nil || proof.Version != PreparationProposalVersion || proof.RequestSHA256 != digest || proof.Nonce != p.Nonce || !proposalDigest(proof.ObservationSHA256) {
		return ErrPreparationProposal
	}
	return nil
}

func parsePreparationProposalProof(raw []byte, p PreparationProposal) (PreparationProposalProof, error) {
	var proof PreparationProposalProof
	if len(raw) == 0 || len(raw) > MaxOutputBytes || json.Unmarshal(raw, &proof) != nil || proof.Validate(p) != nil {
		return PreparationProposalProof{}, ErrPreparationProposal
	}
	canonical, err := json.Marshal(proof)
	if err != nil || !bytes.Equal(canonical, bytes.TrimSpace(raw)) {
		return PreparationProposalProof{}, ErrPreparationProposal
	}
	return proof, nil
}

func preparationProposalCommand(p PreparationProposal) string {
	raw, _ := json.Marshal(p) // Caller validates before command construction.
	return buildPrivilegedInspectionCommand("/usr/sbin:/usr/bin:/sbin:/bin",
		"exec /usr/bin/python3 -I -B -c "+shellConstant(preparationProposalScript)+" "+shellConstant(base64.RawStdEncoding.EncodeToString(raw))+" 2>/dev/null")
}

// InspectPreparationProposal executes a fixed read-only inventory twice on the
// pinned target. Password remains stdin-only. No helper or scratch file is
// uploaded. Unknown/changed evidence, cancellation or lost authority yields no
// proof. Cancellation joins the local SSH session; lost transport cannot prove
// remote exit, so the fixed remote 15s timeout (+2s kill) remains the backstop.
func (client *Client) InspectPreparationProposal(ctx context.Context, sudoPassword []byte, p PreparationProposal, check func(context.Context) error) (PreparationProposalProof, error) {
	invalid := PreparationProposalProof{}
	if client == nil || client.ssh == nil || p.Validate() != nil || check == nil || client.target != (Target{Address: p.Address, Port: p.Port}) ||
		client.approved.Validate() != nil || client.approved.Fingerprint != p.HostKeyFingerprint {
		return invalid, ErrPreparationProposal
	}
	if ctx.Err() != nil {
		return invalid, ErrCancelled
	}
	if check(ctx) != nil || ctx.Err() != nil {
		return invalid, ErrPreparationProposal
	}
	raw, err := client.inspectPrivilegedOutput(ctx, sudoPassword, preparationProposalCommand(p))
	if err != nil {
		return invalid, err
	}
	defer clear(raw)
	proof, err := parsePreparationProposalProof(raw, p)
	if err != nil || ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
		return invalid, ErrPreparationProposal
	}
	return proof, nil
}
