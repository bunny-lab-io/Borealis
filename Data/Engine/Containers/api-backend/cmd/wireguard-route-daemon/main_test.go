package main

import "testing"

func TestRouteConfigRejectsInvalidInputs(t *testing.T) {
	t.Setenv("BOREALIS_WIREGUARD_INTERFACE", "wg0;id")
	t.Setenv("BOREALIS_WIREGUARD_ROUTE_CIDRS", "10.0.0.0/24")
	if _, err := configFromEnv(); err == nil {
		t.Fatal("expected interface injection rejection")
	}
	t.Setenv("BOREALIS_WIREGUARD_INTERFACE", "wg0")
	t.Setenv("BOREALIS_WIREGUARD_ROUTE_CIDRS", "not-a-network")
	if _, err := configFromEnv(); err == nil {
		t.Fatal("expected invalid CIDR rejection")
	}
}

func TestRouteConfigDeduplicatesCIDRs(t *testing.T) {
	t.Setenv("BOREALIS_WIREGUARD_INTERFACE", "wg0")
	t.Setenv("BOREALIS_WIREGUARD_ROUTE_CIDRS", "10.0.0.0/24, 10.0.0.0/24,fd00::/64")
	config, err := configFromEnv()
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if len(config.CIDRs) != 2 {
		t.Fatalf("expected two unique CIDRs, got %#v", config.CIDRs)
	}
}
