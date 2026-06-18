package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeOperatorAuthGate struct {
	allowed bool
	err     error
}

func (g fakeOperatorAuthGate) operatorAuthAllowed(_ context.Context) (bool, error) {
	return g.allowed, g.err
}

func TestRequireUserRejectsWhenBootstrapGateBlocksAuth(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	auth.bootstrapGate = fakeOperatorAuthGate{allowed: false}
	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)

	_, failure := requireUser(context.Background(), auth, request)
	if failure == nil || failure.status != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized failure, got %+v", failure)
	}
}
