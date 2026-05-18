package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

type githubRefHTTPError struct {
	Ref        string
	StatusCode int
	Body       string
}

func (e *githubRefHTTPError) Error() string {
	body := strings.TrimSpace(e.Body)
	if body != "" {
		return fmt.Sprintf("GitHub commit API HTTP %d for repo_ref %q: %s", e.StatusCode, e.Ref, body)
	}
	return fmt.Sprintf("GitHub commit API HTTP %d for repo_ref %q", e.StatusCode, e.Ref)
}

func resolveRepoRefUpdateTarget(ref string, resolve func(string) (string, error)) (string, string, bool, error) {
	requestedRef := agentconfig.NormalizeBranch(ref)
	target, err := resolve(requestedRef)
	if err == nil {
		return requestedRef, target, false, nil
	}
	if !isGithubRefMissing(err) || strings.EqualFold(requestedRef, agentconfig.DefaultBranch) {
		return requestedRef, "", false, err
	}
	fallbackRef := agentconfig.DefaultBranch
	target, fallbackErr := resolve(fallbackRef)
	if fallbackErr != nil {
		return requestedRef, "", false, fmt.Errorf("repo_ref %q missing; fallback repo_ref %q failed: %w", requestedRef, fallbackRef, fallbackErr)
	}
	return fallbackRef, target, true, nil
}

func isGithubRefMissing(err error) bool {
	var refErr *githubRefHTTPError
	if !errors.As(err, &refErr) {
		return false
	}
	if refErr.StatusCode == http.StatusNotFound {
		return true
	}
	return refErr.StatusCode == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(refErr.Body), "no commit found")
}
