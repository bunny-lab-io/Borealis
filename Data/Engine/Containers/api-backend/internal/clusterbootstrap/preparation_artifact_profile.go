package clusterbootstrap

import "time"

const ArtifactPreparationLifetime = 30 * time.Minute

func artifactPreparationAction(action string) bool {
	return action == "publish_prejoin_artifacts" || action == "reconcile_prejoin_artifacts" || action == "install_prejoin_k3s" || action == "reconcile_prejoin_k3s" || action == "configure_prejoin_k3s" || action == "reconcile_prejoin_k3s_configuration" || action == "start_prepared_k3s" || action == "reconcile_prepared_k3s_start" || action == "verify_prepared_images" || action == "reconcile_prepared_images" || action == "activate_prejoin_identity" || action == "reconcile_prejoin_identity_activation"
}

func bulkPreparationAction(action string) bool {
	return artifactPreparationAction(action) || HostPackagePreparationAction(action)
}

type preparationTransportProfile struct {
	artifacts     bool
	challenges    int
	bytes, output int64
	lifetime      time.Duration
}

// Artifact-specific bounds cover 34 archives, binary and installer plus bounded
// inventories. SSH supplies flow control for the data channel. The
// existing heartbeat deadline and independent watchdog remain unchanged.
func preparationProfile(artifacts bool) preparationTransportProfile {
	if artifacts {
		return preparationTransportProfile{true, 512,
			MaxPreparationTransferBytes + 36*MaxImageArchiveBytes + 8*MaxImageInventoryBytes, 128 << 20, ArtifactPreparationLifetime}
	}
	return preparationTransportProfile{false, 64,
		MaxPreparationTransferBytes, MaxPreparationSessionOutputBytes, SessionLifetime}
}
