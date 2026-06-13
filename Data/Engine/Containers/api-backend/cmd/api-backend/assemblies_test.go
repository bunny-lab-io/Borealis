package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeAssemblyStore struct {
	profile operatorProfile

	listFilter assemblyListFilter
	items      []map[string]any
	queue      []map[string]any

	getGUID        string
	getIncludeBody bool
	getItem        map[string]any
	getFound       bool

	importPayload map[string]any
	importItem    map[string]any
}

func (s *fakeAssemblyStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	if !strings.EqualFold(username, s.profile.Username) {
		return operatorProfile{}, errOperatorNotFound
	}
	profile := s.profile
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeAssemblyStore) listAssemblies(_ context.Context, filter assemblyListFilter) ([]map[string]any, []map[string]any, error) {
	s.listFilter = filter
	return s.items, s.queue, nil
}

func (s *fakeAssemblyStore) getAssembly(_ context.Context, assemblyGUID string, includePayload bool) (map[string]any, bool, error) {
	s.getGUID = assemblyGUID
	s.getIncludeBody = includePayload
	return s.getItem, s.getFound, nil
}

func (s *fakeAssemblyStore) createAssembly(_ context.Context, payload map[string]any) (map[string]any, int, error) {
	return payload, http.StatusCreated, nil
}

func (s *fakeAssemblyStore) updateAssembly(_ context.Context, _ string, payload map[string]any) (map[string]any, int, error) {
	return payload, http.StatusOK, nil
}

func (s *fakeAssemblyStore) deleteAssembly(_ context.Context, _ string) (map[string]any, int, error) {
	return map[string]any{"status": "queued"}, http.StatusAccepted, nil
}

func (s *fakeAssemblyStore) cloneAssembly(_ context.Context, _ string, payload map[string]any) (map[string]any, int, error) {
	return payload, http.StatusCreated, nil
}

func (s *fakeAssemblyStore) importAssembly(_ context.Context, payload map[string]any) (map[string]any, int, error) {
	s.importPayload = copyMap(payload)
	if s.importItem != nil {
		return s.importItem, http.StatusCreated, nil
	}
	return map[string]any{"assembly_guid": "created"}, http.StatusCreated, nil
}

func testAssemblyAuth(store *fakeAssemblyStore) *authService {
	if store.profile.Username == "" {
		store.profile.Username = "operator"
	}
	if store.profile.Role == "" {
		store.profile.Role = "Admin"
	}
	return &authService{
		verifier: &tokenVerifier{
			secret: []byte("test-secret"),
			maxAge: time.Hour,
			now:    func() time.Time { return time.Unix(1700000010, 0) },
		},
		store:   store,
		devMode: newGoDevModeManager(),
		timeout: time.Second,
	}
}

func assemblyTestMux(store *fakeAssemblyStore, fallback http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	registerAssemblyRoutes(mux, testAssemblyAuth(store), fallback, nil)
	return mux
}

func assemblyRequest(method string, path string, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestAssemblyListUsesGoStore(t *testing.T) {
	store := &fakeAssemblyStore{
		items: []map[string]any{{"assembly_guid": "abc", "name": "Patch"}},
		queue: []map[string]any{{"assembly_guid": "abc", "is_dirty": "false"}},
	}
	recorder := httptest.NewRecorder()

	assemblyTestMux(store, http.NotFoundHandler()).ServeHTTP(recorder, assemblyRequest(http.MethodGet, "/api/assemblies?domain=user&type=script", ""))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.listFilter.Domain != "user" || store.listFilter.AssemblyType != "script" {
		t.Fatalf("unexpected filter %#v", store.listFilter)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload["items"].([]any)) != 1 {
		t.Fatalf("expected one item: %#v", payload)
	}
}

func TestAssemblyCatalogRefreshFallsBack(t *testing.T) {
	store := &fakeAssemblyStore{}
	fallback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusTeapot, map[string]any{"fallback": true})
	})
	recorder := httptest.NewRecorder()

	assemblyTestMux(store, fallback).ServeHTTP(recorder, assemblyRequest(http.MethodGet, "/api/assemblies?refresh_catalog=1", ""))

	if recorder.Code != http.StatusTeapot {
		t.Fatalf("expected fallback status, got %d", recorder.Code)
	}
}

func TestAssemblyCommunityImportRequiresDevMode(t *testing.T) {
	store := &fakeAssemblyStore{}
	mux := assemblyTestMux(store, http.NotFoundHandler())
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, assemblyRequest(http.MethodPost, "/api/assemblies/import", `{"domain":"community","document":{"name":"Tool","script":"Write-Host ok"}}`))

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAssemblyCommunityImportAllowedAfterDevMode(t *testing.T) {
	store := &fakeAssemblyStore{}
	auth := testAssemblyAuth(store)
	mux := http.NewServeMux()
	registerAssemblyRoutes(mux, auth, http.NotFoundHandler(), nil)

	enable := httptest.NewRecorder()
	mux.ServeHTTP(enable, assemblyRequest(http.MethodPost, "/api/assemblies/dev-mode/switch", `{"enabled":true}`))
	if enable.Code != http.StatusOK {
		t.Fatalf("enable dev mode failed: %d %s", enable.Code, enable.Body.String())
	}

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, assemblyRequest(http.MethodPost, "/api/assemblies/import", `{"domain":"community","document":{"name":"Tool","script":"Write-Host ok"}}`))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.importPayload["domain"] != "community" {
		t.Fatalf("unexpected import payload %#v", store.importPayload)
	}
}
