package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash"
	"io"
	"os"
)

const artifactTransferMagic = "BOREALIS-NODE-ARTIFACTS-1\n"
const maxArtifactTransferIdentity = 2048

type artifactTransferIdentity struct {
	Version           int    `json:"version"`
	Repository        string `json:"repository"`
	Release           string `json:"release"`
	SourceSHA         string `json:"source_sha"`
	K3sVersion        string `json:"k3s_version"`
	PostgresReference string `json:"postgres_reference"`
}

func artifactIdentity(expected Expected, k3sVersion, postgresReference string) (artifactTransferIdentity, error) {
	if expected.Validate() != nil || k3sVersion != K3sPins().Version || !ValidPostgresImageReference(postgresReference) {
		return artifactTransferIdentity{}, ErrImageArchive
	}
	return artifactTransferIdentity{1, expected.Repository, expected.Release, expected.SourceSHA, k3sVersion, postgresReference}, nil
}

// WriteArtifacts binds all four complete families to one independently chosen
// source identity. Per-family writers authenticate bytes, and final framing is
// emitted only after every family and current authority still agree.
func (a *PreparationArtifacts) WriteArtifacts(ctx context.Context, output io.Writer, check func(context.Context) error) error {
	if a == nil || output == nil {
		return ErrImageArchive
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.verifyBindings(ctx, check); err != nil {
		return err
	}
	identity, err := artifactIdentity(a.expected, a.k3sVersion, a.postgresReference)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(identity)
	if err != nil || len(raw) > maxArtifactTransferIdentity {
		return ErrImageArchive
	}
	var header bytes.Buffer
	header.WriteString(artifactTransferMagic)
	_ = binary.Write(&header, binary.BigEndian, uint32(len(raw)))
	header.Write(raw)
	if _, err := io.Copy(output, &header); err != nil {
		return ErrImageArchive
	}
	for _, write := range []func(context.Context, io.Writer, func(context.Context) error) error{
		a.application.WriteImages, a.k3s.WriteInputs, a.external.WriteImages, a.postgres.WriteImage,
	} {
		if imageBoundary(ctx, check) != nil {
			return ErrSessionAuthority
		}
		if err := write(ctx, output, check); err != nil {
			return err
		}
	}
	if err := a.verifyBindings(ctx, check); err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	if _, err := io.Copy(output, bytes.NewReader(digest[:])); err != nil {
		return ErrImageArchive
	}
	return imageBoundary(ctx, check)
}

// Only ApprovalDigest constructs this destination. No caller can receive a
// partial stream while archive bytes are being authenticated.
type preparationApprovalHash struct{ hash.Hash }

// ApprovalDigest authenticates exactly the same framed bytes as WriteArtifacts.
// It returns the digest only after every archive, binding and authority check passes.
func (a *PreparationArtifacts) ApprovalDigest(ctx context.Context, check func(context.Context) error) (string, error) {
	output := &preparationApprovalHash{sha256.New()}
	if err := a.WriteArtifacts(ctx, output, check); err != nil {
		return "", err
	}
	return hex.EncodeToString(output.Sum(nil)), nil
}

// Transfers authenticate the private file before exposing bytes to a receiver.
// In a private approval hash the single authenticated read also feeds the overall digest.
func writeVerifiedPreparationFile(ctx context.Context, root *os.Root, name string, size int64, digest string, output io.Writer, check func(context.Context) error) error {
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0o600 || before.Size() != size {
		return ErrImageArchive
	}
	file, err := root.Open(name)
	if err != nil {
		return ErrImageArchive
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || opened.Mode() != before.Mode() || opened.Size() != size {
		return ErrImageArchive
	}
	h := sha256.New()
	authenticatedCopy := func(dst io.Writer) bool {
		n, err := io.Copy(dst, io.LimitReader(contextReader{ctx, file}, size+1))
		return err == nil && n == size && hex.EncodeToString(h.Sum(nil)) == digest
	}
	if _, private := output.(*preparationApprovalHash); !private {
		if !authenticatedCopy(h) {
			return ErrImageArchive
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return ErrImageArchive
		}
		h.Reset()
	}
	if imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	if !authenticatedCopy(io.MultiWriter(output, h)) {
		return ErrImageArchive
	}
	after, err := root.Lstat(name)
	if err != nil || !os.SameFile(opened, after) || after.Mode() != opened.Mode() || after.Size() != opened.Size() || after.ModTime() != opened.ModTime() {
		return ErrImageArchive
	}
	return imageBoundary(ctx, check)
}

// ReceivePreparationArtifacts returns only a complete owned set. All expectations
// come from current source/claim/capacity scope, never the incoming header. Each
// family reserves its scratch before consuming its archives; caller must retain
// the existing complete-cohort budget throughout the whole bounded transport.
func ReceivePreparationArtifacts(ctx context.Context, parent string, expected Expected, k3sVersion, postgresReference string,
	input io.Reader, check func(context.Context) error) (*PreparationArtifacts, error) {
	return receivePreparationArtifacts(ctx, parent, expected, k3sVersion, postgresReference, input, check, K3sPins(), ExternalImagePins())
}

func receivePreparationArtifacts(ctx context.Context, parent string, expected Expected, k3sVersion, postgresReference string,
	input io.Reader, check func(context.Context) error, k3sPins K3sInputPins, externalPins []ExternalImagePin) (_ *PreparationArtifacts, result error) {
	want, err := artifactIdentity(expected, k3sVersion, postgresReference)
	if err != nil || input == nil || imageBoundary(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	reader := contextReader{ctx, input}
	prefix := make([]byte, len(artifactTransferMagic)+4)
	if _, err := io.ReadFull(reader, prefix); err != nil || string(prefix[:len(artifactTransferMagic)]) != artifactTransferMagic {
		return nil, ErrImageArchive
	}
	size := binary.BigEndian.Uint32(prefix[len(artifactTransferMagic):])
	if size == 0 || size > maxArtifactTransferIdentity {
		return nil, ErrImageArchive
	}
	raw := make([]byte, int(size))
	if _, err := io.ReadFull(reader, raw); err != nil {
		return nil, ErrImageArchive
	}
	var identity artifactTransferIdentity
	if imageFields(raw, []string{"version", "repository", "release", "source_sha", "k3s_version", "postgres_reference"}, nil) != nil || imageJSON(raw, &identity) != nil || identity != want {
		return nil, ErrImageArchive
	}
	a := &PreparationArtifacts{expected: expected, k3sVersion: k3sVersion, postgresReference: postgresReference}
	defer func() {
		if result != nil {
			_ = a.Close()
		}
	}()
	a.application, err = ReceiveImages(ctx, parent, expected, reader, check)
	if err != nil {
		return nil, err
	}
	a.k3s, err = receiveK3sInputs(ctx, parent, expected, k3sVersion, reader, check, k3sPins)
	if err != nil {
		return nil, err
	}
	a.external, err = receiveExternalImages(ctx, parent, expected, reader, check, externalPins)
	if err != nil {
		return nil, err
	}
	a.postgres, err = ReceivePostgresImage(ctx, parent, postgresReference, reader, check)
	if err != nil {
		return nil, err
	}
	var trailer [sha256.Size]byte
	digest := sha256.Sum256(raw)
	if _, err := io.ReadFull(reader, trailer[:]); err != nil || trailer != digest || a.verifyBindings(ctx, check) != nil {
		return nil, ErrImageArchive
	}
	return a, nil
}
