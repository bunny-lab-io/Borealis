package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// PreparationArtifactProof rechecks owned bootstrap bytes without manufacturing
// a target worker binding. It proves only immutable source/helper compatibility;
// the caller owns current proposal authority, lifetime and eventual Close.
func (b *Bundle) PreparationArtifactProof(ctx context.Context, expected Expected, version string, manifest *Manifest, check func(context.Context) error) (string, error) {
	if expected.Validate() != nil || version != K3sPins().Version || imageBoundary(ctx, check) != nil {
		return "", ErrPreparationConfig
	}
	actual, _, _, err := b.ManagerProof()
	if err != nil || actual != expected || manifest == nil || b.identity != manifest.identity || b.asset != manifest.asset || b.verifyPreparationSource(ctx, version) != nil || imageBoundary(ctx, check) != nil {
		return "", ErrPreparationConfig
	}
	raw, err := json.Marshal(struct {
		Version    int
		Source     identity
		Asset      assetIdentity
		K3sVersion string
	}{1, b.identity, b.asset, version})
	if err != nil {
		return "", ErrPreparationConfig
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
