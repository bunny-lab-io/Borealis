package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
)

func (client *Client) StartPreparedK3s(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, identity *engineidentity.Material, artifacts *clusterbootstrap.PreparationArtifacts, postgres string, join *clusterbootstrap.K3sJoinMaterial, sudo []byte, check func(context.Context) error) error {
	return client.preparationWithK3sJoinResult(ctx, inputs, expected, "start_prepared_k3s", &source, identity, artifacts, postgres, join, sudo, check, nil)
}

func (client *Client) ReconcilePreparedK3sStart(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, identity *engineidentity.Material, artifacts *clusterbootstrap.PreparationArtifacts, postgres string, join *clusterbootstrap.K3sJoinMaterial, sudo []byte, check func(context.Context) error) error {
	return client.preparationWithK3sJoinResult(ctx, inputs, expected, "reconcile_prepared_k3s_start", &source, identity, artifacts, postgres, join, sudo, check, nil)
}

func (client *Client) VerifyPreparedImages(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, identity *engineidentity.Material, artifacts *clusterbootstrap.PreparationArtifacts, postgres string, join *clusterbootstrap.K3sJoinMaterial, sudo []byte, check func(context.Context) error) error {
	return client.preparationWithK3sJoinResult(ctx, inputs, expected, "verify_prepared_images", &source, identity, artifacts, postgres, join, sudo, check, nil)
}

func (client *Client) ReconcilePreparedImages(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, identity *engineidentity.Material, artifacts *clusterbootstrap.PreparationArtifacts, postgres string, join *clusterbootstrap.K3sJoinMaterial, sudo []byte, check func(context.Context) error) error {
	return client.preparationWithK3sJoinResult(ctx, inputs, expected, "reconcile_prepared_images", &source, identity, artifacts, postgres, join, sudo, check, nil)
}

func (client *Client) ActivatePrejoinIdentity(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, identity *engineidentity.Material, artifacts *clusterbootstrap.PreparationArtifacts, postgres string, join *clusterbootstrap.K3sJoinMaterial, sudo []byte, check func(context.Context) error) error {
	return client.preparationWithK3sJoinResult(ctx, inputs, expected, "activate_prejoin_identity", &source, identity, artifacts, postgres, join, sudo, check, nil)
}

func (client *Client) ReconcilePrejoinIdentityActivation(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, identity *engineidentity.Material, artifacts *clusterbootstrap.PreparationArtifacts, postgres string, join *clusterbootstrap.K3sJoinMaterial, sudo []byte, check func(context.Context) error) error {
	return client.preparationWithK3sJoinResult(ctx, inputs, expected, "reconcile_prejoin_identity_activation", &source, identity, artifacts, postgres, join, sudo, check, nil)
}

func (client *Client) ConfigurePrejoinK3s(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, identity *engineidentity.Material, artifacts *clusterbootstrap.PreparationArtifacts, postgres string, join *clusterbootstrap.K3sJoinMaterial, sudo []byte, check func(context.Context) error) error {
	return client.preparationWithK3sJoinResult(ctx, inputs, expected, "configure_prejoin_k3s", &source, identity, artifacts, postgres, join, sudo, check, nil)
}

func (client *Client) ReconcilePrejoinK3sConfiguration(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, identity *engineidentity.Material, artifacts *clusterbootstrap.PreparationArtifacts, postgres string, join *clusterbootstrap.K3sJoinMaterial, sudo []byte, check func(context.Context) error) error {
	return client.preparationWithK3sJoinResult(ctx, inputs, expected, "reconcile_prejoin_k3s_configuration", &source, identity, artifacts, postgres, join, sudo, check, nil)
}

// Completion remains private and is returned only after remote exit and current
// authority. Controller must independently verify the reported Node identity.
func (client *Client) ReconcilePrejoinPreparation(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, identity *engineidentity.Material, artifacts *clusterbootstrap.PreparationArtifacts, postgres string, join *clusterbootstrap.K3sJoinMaterial, sudo []byte, check func(context.Context) error) (clusterbootstrap.PrejoinPreparationReceipt, error) {
	var receipt clusterbootstrap.PrejoinPreparationReceipt
	if err := client.preparationWithCompletion(ctx, inputs, expected, "reconcile_prejoin_identity_activation", &source, identity, artifacts, postgres, join, sudo, check, nil, &receipt); err != nil {
		return clusterbootstrap.PrejoinPreparationReceipt{}, err
	}
	return receipt, nil
}
