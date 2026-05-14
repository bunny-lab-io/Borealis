package auth

import (
	"testing"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func TestLoadOrCreateIdentityRoundTrip(t *testing.T) {
	cfg := agentconfig.Default()
	created, changed, err := LoadOrCreateIdentity(&cfg)
	if err != nil {
		t.Fatalf("LoadOrCreateIdentity failed: %v", err)
	}
	if !changed {
		t.Fatalf("expected new identity")
	}
	loaded, changed, err := LoadOrCreateIdentity(&cfg)
	if err != nil {
		t.Fatalf("Load existing identity failed: %v", err)
	}
	if changed {
		t.Fatalf("existing identity should not be changed")
	}
	if created.Fingerprint != loaded.Fingerprint {
		t.Fatalf("fingerprint mismatch")
	}
}
