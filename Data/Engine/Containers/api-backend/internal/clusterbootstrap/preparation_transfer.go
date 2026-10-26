package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
)

const preparationTransferMagic = "BOREALIS-NODE-PREPARATION-1\n"

// ConsumePreparation requires the complete verified transfer and protocol EOF
// before invoking a mutation consumer. It binds the received helper to the
// independently verified running executable and owns scratch cleanup.
func ConsumePreparation(ctx context.Context, parent string, expected PreparationExpected, manager string, input io.Reader,
	check func(context.Context) error, consume func(context.Context, *PreparationInputs) error) (result error) {
	if !digestPattern.MatchString(manager) || consume == nil {
		return ErrPreparationConfig
	}
	inputs, err := ReceivePreparation(ctx, parent, expected, input, check)
	if err != nil {
		return ErrPreparationConfig
	}
	defer func() {
		if inputs.Close() != nil {
			result = ErrPreparationConfig
		}
	}()
	var trailing [1]byte
	if n, err := input.Read(trailing[:]); n != 0 || err != io.EOF {
		return ErrPreparationConfig
	}
	_, actual, _, err := inputs.bundle.ManagerProof()
	if err != nil || actual != manager || imageBoundary(ctx, check) != nil {
		return ErrPreparationConfig
	}
	if consume(ctx, inputs) != nil || imageBoundary(ctx, check) != nil {
		return ErrPreparationConfig
	}
	return nil
}

// WritePreparation sends private canonical configuration, authenticated release
// manifest and exact archive bytes. No installed path or command is accepted.
// Transport owns cancellation of blocked IO and continuous lease supervision.
// This transfer is inert; PublishSource separately consumes mutation authority.
func (p *PreparationInputs) WritePreparation(ctx context.Context, expected PreparationExpected, output io.Writer, check func(context.Context) error) error {
	if output == nil || p.Verify(ctx, expected, check) != nil {
		return ErrPreparationConfig
	}
	configuration, err := p.configuration.Export()
	if err != nil {
		return ErrPreparationConfig
	}
	defer clear(configuration)
	manifest, err := json.Marshal(struct {
		identity
		Asset assetIdentity `json:"asset"`
	}{p.bundle.identity, p.bundle.asset})
	if err != nil {
		return ErrPreparationConfig
	}
	parsed, err := ParseManifest(manifest, expected.Source)
	if err != nil || parsed.identity != p.bundle.identity || parsed.asset != p.bundle.asset {
		return ErrPreparationConfig
	}
	archive, err := os.Open(p.bundle.ArchivePath())
	if err != nil {
		return ErrPreparationConfig
	}
	defer archive.Close()
	// Digest and transfer use one opened archive, not separately resolved paths.
	if verifyPreparationTransferArchive(ctx, archive, p.bundle.asset) != nil {
		return ErrPreparationConfig
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return ErrPreparationConfig
	}
	var header bytes.Buffer
	header.WriteString(preparationTransferMagic)
	_ = binary.Write(&header, binary.BigEndian, uint32(len(configuration)))
	header.Write(configuration)
	_ = binary.Write(&header, binary.BigEndian, uint32(len(manifest)))
	header.Write(manifest)
	defer clear(header.Bytes())
	digest := sha256.New()
	writer := io.MultiWriter(output, digest)
	if imageBoundary(ctx, check) != nil {
		return ErrSessionAuthority
	}
	if _, err := io.Copy(writer, &header); err != nil {
		return ErrPreparationConfig
	}
	count, err := io.Copy(writer, io.LimitReader(contextReader{ctx, archive}, p.bundle.asset.Size+1))
	if err != nil || count != p.bundle.asset.Size || verifyPreparationTransferArchive(ctx, archive, p.bundle.asset) != nil || imageBoundary(ctx, check) != nil {
		return ErrPreparationConfig
	}
	if _, err := io.Copy(output, bytes.NewReader(digest.Sum(nil))); err != nil {
		return ErrPreparationConfig
	}
	return imageBoundary(ctx, check)
}

// ReceivePreparation reads one bounded transfer into new private scratch. Its
// expected target/source/configuration binding comes independently from the
// supervised request. Only a full digest-checked and source-verified transfer
// yields usable inputs; errors remove owned scratch without touching /opt.
func ReceivePreparation(ctx context.Context, parent string, expected PreparationExpected, input io.Reader, check func(context.Context) error) (*PreparationInputs, error) {
	if input == nil || expected.Validate() != nil || imageBoundary(ctx, check) != nil {
		return nil, ErrPreparationConfig
	}
	digest := sha256.New()
	reader := io.TeeReader(contextReader{ctx, input}, digest)
	prefix := make([]byte, len(preparationTransferMagic))
	if _, err := io.ReadFull(reader, prefix); err != nil || string(prefix) != preparationTransferMagic {
		return nil, ErrPreparationConfig
	}
	configuration, err := readPreparationTransferField(reader, MaxPreparationConfigBytes)
	if err != nil {
		return nil, ErrPreparationConfig
	}
	defer clear(configuration)
	config, err := ImportPreparationConfiguration(configuration, expected)
	if err != nil {
		return nil, ErrPreparationConfig
	}
	raw, err := readPreparationTransferField(reader, MaxManifestBytes)
	if err != nil {
		return nil, ErrPreparationConfig
	}
	manifest, err := ParseManifest(raw, expected.Source)
	if err != nil || imageBoundary(ctx, check) != nil {
		return nil, ErrPreparationConfig
	}
	bundle, err := Stage(ctx, parent, manifest, io.LimitReader(reader, manifest.BundleSize()), "")
	if err != nil {
		return nil, ErrPreparationConfig
	}
	keep := false
	defer func() {
		if !keep {
			_ = bundle.Close()
		}
	}()
	var trailer [sha256.Size]byte
	if _, err := io.ReadFull(contextReader{ctx, input}, trailer[:]); err != nil || !bytes.Equal(trailer[:], digest.Sum(nil)) || imageBoundary(ctx, check) != nil {
		return nil, ErrPreparationConfig
	}
	prepared, err := BindPreparationInputs(ctx, bundle, config, check)
	if err != nil {
		return nil, ErrPreparationConfig
	}
	keep = true
	return prepared, nil
}

func readPreparationTransferField(input io.Reader, maximum uint32) ([]byte, error) {
	var length uint32
	if binary.Read(input, binary.BigEndian, &length) != nil || length == 0 || length > maximum {
		return nil, ErrPreparationConfig
	}
	raw := make([]byte, int(length))
	if _, err := io.ReadFull(input, raw); err != nil {
		clear(raw)
		return nil, ErrPreparationConfig
	}
	return raw, nil
}

// Kept portable: the Linux publisher additionally checks ownership/link count.
func verifyPreparationTransferArchive(ctx context.Context, file *os.File, asset assetIdentity) error {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != asset.Size || asset.Size < 1 || asset.Size > MaxBundleBytes {
		return ErrPreparationConfig
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ErrPreparationConfig
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(contextReader{ctx, file}, asset.Size+1))
	if err != nil || n != asset.Size || hex.EncodeToString(h.Sum(nil)) != asset.SHA256 {
		return ErrPreparationConfig
	}
	return nil
}
