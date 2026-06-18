package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	states        map[string]officialCatalogState
	deletedStates []string
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

func (s *fakeAssemblyStore) listOfficialCatalogState(_ context.Context) (map[string]officialCatalogState, error) {
	if s.states == nil {
		return map[string]officialCatalogState{}, nil
	}
	out := make(map[string]officialCatalogState, len(s.states))
	for key, value := range s.states {
		out[key] = value
	}
	return out, nil
}

func (s *fakeAssemblyStore) upsertOfficialCatalogState(_ context.Context, state officialCatalogState) error {
	if s.states == nil {
		s.states = map[string]officialCatalogState{}
	}
	s.states[state.AssemblyGUID] = state
	return nil
}

func (s *fakeAssemblyStore) deleteOfficialCatalogState(_ context.Context, assemblyGUID string) error {
	s.deletedStates = append(s.deletedStates, assemblyGUID)
	if s.states != nil {
		delete(s.states, assemblyGUID)
	}
	return nil
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
	registerAssemblyRoutes(mux, testAssemblyAuth(store), fallback)
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

func TestAssemblyCatalogRefreshUsesGoCatalog(t *testing.T) {
	root := t.TempDir()
	catalogFile := filepath.Join(root, "scripts", "powershell", "tool.json")
	if err := os.MkdirAll(filepath.Dir(catalogFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogFile, []byte(`{"assembly_guid":"cat-1","name":"Catalog Tool","type":"powershell","payload":{"assembly_guid":"cat-1","script":"Write-Host ok"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOREALIS_OFFICIAL_ASSEMBLIES_ROOT", root)
	t.Setenv("BOREALIS_OFFICIAL_ASSEMBLIES_CHECKOUT_ROOT", "")
	oldRunner := officialCatalogRunGit
	officialCatalogRunGit = func(context.Context, string, []string, string) (string, error) {
		return "", os.ErrNotExist
	}
	t.Cleanup(func() { officialCatalogRunGit = oldRunner })
	store := &fakeAssemblyStore{
		items: []map[string]any{{
			"assembly_guid":    "cat-1",
			"display_name":     "Catalog Tool",
			"assembly_type":    "script",
			"assembly_subtype": "powershell",
			"source":           "official",
			"payload_json":     map[string]any{"assembly_guid": "cat-1", "script": "Write-Host old"},
		}},
	}
	recorder := httptest.NewRecorder()

	assemblyTestMux(store, http.NotFoundHandler()).ServeHTTP(recorder, assemblyRequest(http.MethodGet, "/api/assemblies?refresh_catalog=1", ""))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	status := payload["official_catalog"].(map[string]any)
	if status["available"] != true || status["source"] != "bundled" {
		t.Fatalf("unexpected catalog status %#v", status)
	}
	items := payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one item: %#v", payload)
	}
	item := items[0].(map[string]any)
	if item["official_managed"] != true {
		t.Fatalf("expected managed official item: %#v", item)
	}
}

func TestAssemblyCatalogUpdateAllImportsAuroraEntries(t *testing.T) {
	checkoutRoot := t.TempDir()
	oldRunner := officialCatalogRunGit
	officialCatalogRunGit = func(_ context.Context, cwd string, args []string, _ string) (string, error) {
		if len(args) > 0 && args[0] == "checkout" {
			path := filepath.Join(cwd, "scripts", "powershell", "remote-tool.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(path, []byte(`{"assembly_guid":"remote-1","name":"Remote Tool","type":"powershell","payload":{"assembly_guid":"remote-1","script":"Write-Host remote"}}`), 0o644); err != nil {
				return "", err
			}
		}
		if len(args) > 0 && args[0] == "rev-parse" {
			return "abc123", nil
		}
		return "", nil
	}
	t.Cleanup(func() { officialCatalogRunGit = oldRunner })
	t.Setenv("BOREALIS_OFFICIAL_ASSEMBLIES_CHECKOUT_ROOT", checkoutRoot)
	store := &fakeAssemblyStore{
		importItem: map[string]any{"assembly_guid": "remote-1"},
		getItem: map[string]any{
			"assembly_guid":    "remote-1",
			"display_name":     "Remote Tool",
			"assembly_type":    "script",
			"assembly_subtype": "powershell",
			"source":           "official",
			"source_path":      "scripts/powershell/remote-tool.json",
			"source_version":   "git:abc123",
			"payload_json":     map[string]any{"assembly_guid": "remote-1", "script": "Write-Host remote"},
		},
		getFound: true,
	}
	recorder := httptest.NewRecorder()

	assemblyTestMux(store, http.NotFoundHandler()).ServeHTTP(recorder, assemblyRequest(http.MethodPost, "/api/assemblies/official/update-all", ""))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.importPayload["domain"] != "official" {
		t.Fatalf("expected official import, got %#v", store.importPayload)
	}
	if store.states["remote-1"].RemoteHash == "" || store.states["remote-1"].SourcePath != "scripts/powershell/remote-tool.json" {
		t.Fatalf("expected catalog state update, got %#v", store.states)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if int(payload["installed_count"].(float64)) != 1 {
		t.Fatalf("expected installed count 1: %#v", payload)
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
	registerAssemblyRoutes(mux, auth, http.NotFoundHandler())

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
