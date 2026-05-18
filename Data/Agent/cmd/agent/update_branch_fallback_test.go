package main

import (
	"errors"
	"net/http"
	"testing"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func TestResolveRepoRefUpdateTargetFallsBackWhenBranchMissing(t *testing.T) {
	calls := []string{}
	effectiveRef, target, fellBack, err := resolveRepoRefUpdateTarget("feature/deleted", func(ref string) (string, error) {
		calls = append(calls, ref)
		if ref == "feature/deleted" {
			return "", &githubRefHTTPError{Ref: ref, StatusCode: http.StatusUnprocessableEntity, Body: "No commit found for SHA: feature/deleted"}
		}
		if ref == agentconfig.DefaultBranch {
			return "main-sha", nil
		}
		return "", errors.New("unexpected ref")
	})
	if err != nil {
		t.Fatal(err)
	}
	if !fellBack {
		t.Fatalf("expected fallback")
	}
	if effectiveRef != agentconfig.DefaultBranch {
		t.Fatalf("effective ref = %q", effectiveRef)
	}
	if target != "main-sha" {
		t.Fatalf("target = %q", target)
	}
	if len(calls) != 2 || calls[0] != "feature/deleted" || calls[1] != agentconfig.DefaultBranch {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestResolveRepoRefUpdateTargetDoesNotFallbackWhenMainMissing(t *testing.T) {
	_, _, fellBack, err := resolveRepoRefUpdateTarget(agentconfig.DefaultBranch, func(ref string) (string, error) {
		return "", &githubRefHTTPError{Ref: ref, StatusCode: http.StatusNotFound}
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if fellBack {
		t.Fatalf("unexpected fallback")
	}
}

func TestResolveRepoRefUpdateTargetDoesNotFallbackOnServerError(t *testing.T) {
	calls := 0
	_, _, fellBack, err := resolveRepoRefUpdateTarget("feature/broken", func(ref string) (string, error) {
		calls++
		return "", &githubRefHTTPError{Ref: ref, StatusCode: http.StatusInternalServerError}
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if fellBack {
		t.Fatalf("unexpected fallback")
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}
