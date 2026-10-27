package clusterbootstrap

import "time"

const ArtifactPreparationLifetime = 30 * time.Minute

func artifactPreparationAction(action string) bool {
	return action == "publish_prejoin_artifacts" || action == "reconcile_prejoin_artifacts" || action == "install_prejoin_k3s" || action == "reconcile_prejoin_k3s"
}

type preparationTransportProfile struct {
	artifacts                bool
	chunk, frame, challenges int
	bytes, output            int64
	lifetime                 time.Duration
}

// Artifact-specific bounds cover 34 archives, binary and installer plus bounded
// inventories/framing. One outstanding chunk still enforces backpressure; the
// existing heartbeat deadline and independent watchdog remain unchanged.
func preparationProfile(artifacts bool) preparationTransportProfile {
	if artifacts {
		return preparationTransportProfile{true, 256 << 10, 512 << 10, 512,
			MaxPreparationTransferBytes + 36*MaxImageArchiveBytes + 8*MaxImageInventoryBytes, 128 << 20, ArtifactPreparationLifetime}
	}
	return preparationTransportProfile{false, PreparationChunkBytes, MaxSessionFrameBytes, 64,
		MaxPreparationTransferBytes, MaxPreparationSessionOutputBytes, SessionLifetime}
}
