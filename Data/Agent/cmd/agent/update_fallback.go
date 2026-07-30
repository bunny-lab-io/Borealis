package main

import (
	"errors"
	"strings"
)

func shouldFallbackRepoRefToStable(engineErr error, githubErr error) bool {
	return isRepoRefMissingError(engineErr) || isRepoRefMissingError(githubErr)
}

func isRepoRefMissingError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	if text == "" {
		return false
	}
	for _, marker := range []string{
		"http 404",
		"http status 404",
		"statuscode=404",
		"404 not found",
		"not found",
		"http 422",
		"statuscode=422",
		"no commit found",
		"reference does not exist",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	if wrapped := errors.Unwrap(err); wrapped != nil {
		return isRepoRefMissingError(wrapped)
	}
	return false
}
