package main

import (
	"borealis/api-backend/internal/engineidentity"
	"os"
	"path/filepath"
)

// Process lifetime, intentionally not deferred Close: HTTP shutdown can finish
// before background key users exit. Kernel teardown releases the shared flock.
var processIdentityConsumer *engineidentity.Consumer

func holdProcessIdentityConsumer(cfg gatewayConfig) error {
	root := filepath.Dir(cfg.EngineSecretPath)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	guard, err := engineidentity.AcquireConsumer(root)
	if err != nil {
		return err
	}
	processIdentityConsumer = guard
	return nil
}
