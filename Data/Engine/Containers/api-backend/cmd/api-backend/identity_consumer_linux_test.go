package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIdentityConsumerEntrypointsFailBeforeKeyInitialization(t *testing.T) {
	for _, role := range []string{"api-backend", "job-scheduler", "api-db-healthcheck"} {
		t.Run(role, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, ".borealis-identity-recovery.json"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			secret := filepath.Join(root, "engine_secret.txt")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIdentityConsumerEntrypointHelper$")
			cmd.Env = append(os.Environ(), "BOREALIS_IDENTITY_ENTRYPOINT_TEST=1", "BOREALIS_PROCESS_ROLE="+role,
				"BOREALIS_ENGINE_SECRET_PATH="+secret, "BOREALIS_DATABASE_URL=")
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "identity consumer startup blocked:") {
				t.Fatalf("guard not reached before runtime: %v %s", err, output)
			}
			if _, err := os.Stat(secret); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("identity generated through startup fence")
			}
		})
	}
}

func TestIdentityConsumerEntrypointHelper(t *testing.T) {
	if os.Getenv("BOREALIS_IDENTITY_ENTRYPOINT_TEST") != "1" {
		return
	}
	main()
	t.Fatal("startup unexpectedly returned")
}
