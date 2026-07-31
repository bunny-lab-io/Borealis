package main

import (
	"fmt"
	"testing"
)

func TestShouldFallbackRepoRefToStableRequiresMissingRefSignal(t *testing.T) {
	if !shouldFallbackRepoRefToStable(
		fmt.Errorf("Engine repo hash API HTTP 503: GitHub REST API repo head lookup failed: HTTP 404"),
		fmt.Errorf("GitHub commit API HTTP 404"),
	) {
		t.Fatalf("expected missing branch errors to trigger stable fallback")
	}
	if shouldFallbackRepoRefToStable(
		fmt.Errorf("Engine repo hash API HTTP 503: timeout"),
		fmt.Errorf("GitHub commit API HTTP 500"),
	) {
		t.Fatalf("expected transient lookup failures to preserve branch")
	}
}
