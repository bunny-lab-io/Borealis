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
	"slices"
	"sync"
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
	return a.writeArtifacts(ctx, output, check)
}

// Caller holds the artifact set lock through the complete write.
func (a *PreparationArtifacts) writeArtifacts(ctx context.Context, output io.Writer, check func(context.Context) error) error {
	if a.published {
		return ErrImageArchive
	}
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

// The ordered archives and their positions among the framing bytes uniquely
// describe the transport stream. Keep only digests and lengths on the owner.
type preparationArchiveSpan struct {
	framingOffset int64
	size          int64
	digest        string
}

type preparationStreamProof struct {
	transportDigest string
	framingDigest   string
	archives        []preparationArchiveSpan
}

// Only ApprovalDigest constructs this destination. Write receives framing;
// writeVerifiedPreparationFile authenticates each archive and records its span.
type preparationApprovalHash struct {
	transport, framing hash.Hash
	framingBytes       int64
	archives           []preparationArchiveSpan
}

func (p *preparationApprovalHash) Write(data []byte) (int, error) {
	p.framingBytes += int64(len(data))
	p.framing.Write(data)
	if p.transport != nil {
		p.transport.Write(data)
	}
	return len(data), nil
}

// ApprovalDigest returns the SHA-256 of WriteArtifacts. The first proof hashes
// that full stream. Later proofs authenticate the same ordered bytes by checking
// every file anew, every framing byte and every archive boundary.
func (a *PreparationArtifacts) ApprovalDigest(ctx context.Context, check func(context.Context) error) (string, error) {
	if a == nil {
		return "", ErrImageArchive
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	output := &preparationApprovalHash{framing: sha256.New()}
	if a.approval == nil {
		output.transport = sha256.New()
	}
	if err := a.writeArtifacts(ctx, output, check); err != nil {
		return "", err
	}
	framing := hex.EncodeToString(output.framing.Sum(nil))
	if a.approval == nil {
		a.approval = &preparationStreamProof{hex.EncodeToString(output.transport.Sum(nil)), framing, output.archives}
	} else if a.approval.framingDigest != framing || !slices.Equal(a.approval.archives, output.archives) {
		return "", ErrImageArchive
	}
	return a.approval.transportDigest, nil
}

// The family owner holds its lock until all private reads have joined. The
// first proof and real transfers still emit their complete stream in order.
func verifyPreparationArchives(ctx context.Context, output io.Writer, check func(context.Context) error, count int,
	read func(context.Context, int, io.Writer, func(context.Context) error) error) error {
	parent, private := output.(*preparationApprovalHash)
	if !private || parent.transport != nil || count < 2 {
		for i := range count {
			if err := read(ctx, i, output, check); err != nil {
				return err
			}
		}
		return nil
	}
	work, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var checks sync.Mutex
	current := func(ctx context.Context) error {
		checks.Lock()
		defer checks.Unlock()
		return imageBoundary(ctx, check)
	}
	spans := make([]preparationArchiveSpan, count)
	queue := make(chan int, count)
	for i := range count {
		queue <- i
	}
	close(queue)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			for i := range queue {
				if work.Err() != nil {
					return
				}
				part := &preparationApprovalHash{framing: sha256.New()}
				err := read(work, i, part, current)
				if err == nil && (len(part.archives) != 1 || part.framingBytes != 0) {
					err = ErrImageArchive
				}
				if err != nil {
					cancel(err)
					return
				}
				spans[i] = part.archives[0]
			}
		})
	}
	group.Wait()
	if ctx.Err() != nil {
		return ErrSessionAuthority
	}
	if err := context.Cause(work); err != nil {
		return err
	}
	for i := range spans {
		spans[i].framingOffset = parent.framingBytes
	}
	parent.archives = append(parent.archives, spans...)
	return nil
}

// External writers receive bytes only after prior authentication. A private
// proof receives no output outside the owner and authenticates each file once.
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
	approval, private := output.(*preparationApprovalHash)
	if !private {
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
	var destination io.Writer = h
	if private {
		approval.archives = append(approval.archives, preparationArchiveSpan{approval.framingBytes, size, digest})
		if approval.transport != nil {
			destination = io.MultiWriter(h, approval.transport)
		}
	} else {
		destination = io.MultiWriter(h, output)
	}
	if !authenticatedCopy(destination) {
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
