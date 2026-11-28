package clusterbootstrap

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
)

// After stage_images the target journal owns the authenticated publication.
// Later actions carry inventories only. The native journal compares the derived
// publication digest with the applied record before inspecting any installed file.
func (a *PreparationArtifacts) writePublishedInventories(ctx context.Context, out io.Writer, check func(context.Context) error) error {
	if a == nil || out == nil {
		return ErrImageArchive
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.published || a.verifyBindings(ctx, check) != nil {
		return ErrImageArchive
	}
	pg, err := json.Marshal(a.postgres.proof)
	if err != nil {
		return ErrImageArchive
	}
	for _, raw := range [][]byte{a.application.inventory.raw, a.k3s.inventory.raw, a.external.inventory.raw, pg} {
		if len(raw) == 0 || len(raw) > MaxImageInventoryBytes || binary.Write(out, binary.BigEndian, uint32(len(raw))) != nil {
			return ErrImageArchive
		}
		if n, err := out.Write(raw); err != nil || n != len(raw) {
			return ErrImageArchive
		}
	}
	return imageBoundary(ctx, check)
}

func readPublishedInventories(ctx context.Context, expected Expected, version, reference string, in io.Reader, check func(context.Context) error, pins K3sInputPins, externalPins []ExternalImagePin) (*PreparationArtifacts, error) {
	var values [4][]byte
	for i, limit := range []uint32{MaxImageInventoryBytes, MaxK3sInventoryBytes, MaxImageInventoryBytes, MaxImageInventoryBytes} {
		raw, err := readPreparationTransferField(contextReader{ctx, in}, limit)
		if err != nil {
			return nil, ErrImageArchive
		}
		values[i] = raw
	}
	app, err := ParseImageInventory(values[0], expected)
	if err != nil {
		return nil, err
	}
	k3s, err := parseK3sInventory(values[1], expected, version, pins)
	if err != nil {
		return nil, err
	}
	external, err := parseExternalImageInventory(values[2], expected, externalPins)
	if err != nil {
		return nil, err
	}
	var pg ExternalImageProof
	if imageJSON(values[3], &pg) != nil || !ValidPostgresImageReference(reference) || pg.Reference != reference || !digestPattern.MatchString(pg.ArchiveSHA256) || pg.ArchiveBytes < 1 || pg.ArchiveBytes > MaxImageArchiveBytes {
		return nil, ErrImageArchive
	}
	root := pg.IndexDigest
	if root == "" {
		root = pg.ManifestDigest
	}
	if reference != PostgresImageRepository+"@"+root {
		return nil, ErrImageArchive
	}
	pgInventory := &ExternalImageInventory{wire: externalInventoryWire{Images: []ExternalImageProof{pg}}, raw: values[3]}
	a := &PreparationArtifacts{published: true, expected: expected, k3sVersion: version, postgresReference: reference,
		application: &ImageSet{inventory: app}, k3s: &K3sSet{inventory: k3s, assets: map[string]K3sAssetPin{K3sBinaryName: pins.Binary, K3sInstallerName: pins.Installer, K3sArchiveName: pins.Archive}},
		external: &ExternalImageSet{inventory: external}, postgres: &PostgresImage{set: &ExternalImageSet{inventory: pgInventory}, proof: pg}}
	return a, a.verifyBindings(ctx, check)
}
