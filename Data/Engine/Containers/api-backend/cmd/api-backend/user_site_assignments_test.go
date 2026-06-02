package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeUserSiteAssignmentStore struct {
	profile            operatorProfile
	selectionUsernames []string
	assignUsernames    []string
	assignSiteIDs      []int64
}

func (s *fakeUserSiteAssignmentStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeUserSiteAssignmentStore) loadUserSiteAssignmentSelection(_ context.Context, usernames []string) (map[string]any, int, error) {
	s.selectionUsernames = usernames
	return map[string]any{
		"users":                 []map[string]any{{"username": usernames[0], "role": "User"}},
		"sites":                 []map[string]any{{"id": int64(1), "name": "Bunny Lab"}},
		"existing_assignments":  map[string]any{usernames[0]: []map[string]any{}},
		"selected_site_ids":     []int64{},
		"has_mixed_assignments": false,
		"warning":               "",
	}, http.StatusOK, nil
}

func (s *fakeUserSiteAssignmentStore) assignUserSites(_ context.Context, usernames []string, siteIDs []int64) (map[string]any, int, error) {
	s.assignUsernames = usernames
	s.assignSiteIDs = siteIDs
	return map[string]any{
		"status":              "ok",
		"assigned_user_count": len(usernames),
		"assigned_site_ids":   siteIDs,
	}, http.StatusOK, nil
}

func userSiteAssignmentTestAuth(store *fakeUserSiteAssignmentStore) *authService {
	return &authService{
		verifier: &tokenVerifier{
			secret: []byte("test-secret"),
			maxAge: time.Hour,
			now:    func() time.Time { return time.Unix(1700000010, 0) },
		},
		store:   store,
		timeout: time.Second,
	}
}

func TestUserSiteAssignmentSelectionRoute(t *testing.T) {
	store := &fakeUserSiteAssignmentStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerUserSiteAssignmentRoutes(mux, userSiteAssignmentTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/user_site_assignments/selection", strings.NewReader(`{"usernames":["example_user","example_user",""]}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(store.selectionUsernames) != 1 || store.selectionUsernames[0] != "example_user" {
		t.Fatalf("unexpected usernames %+v", store.selectionUsernames)
	}
}

func TestUserSiteAssignmentAssignRoute(t *testing.T) {
	store := &fakeUserSiteAssignmentStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerUserSiteAssignmentRoutes(mux, userSiteAssignmentTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/user_site_assignments/assign", strings.NewReader(`{"usernames":["example_user"],"site_ids":[2,"2",3,"bad"]}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(store.assignUsernames) != 1 || store.assignUsernames[0] != "example_user" {
		t.Fatalf("unexpected usernames %+v", store.assignUsernames)
	}
	if len(store.assignSiteIDs) != 2 || store.assignSiteIDs[0] != 2 || store.assignSiteIDs[1] != 3 {
		t.Fatalf("unexpected site ids %+v", store.assignSiteIDs)
	}
}

func TestUserSiteAssignmentRequiresAdmin(t *testing.T) {
	store := &fakeUserSiteAssignmentStore{profile: operatorProfile{Username: "operator", Role: "User"}}
	mux := http.NewServeMux()
	registerUserSiteAssignmentRoutes(mux, userSiteAssignmentTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/user_site_assignments/selection", strings.NewReader(`{"usernames":["example_user"]}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}
