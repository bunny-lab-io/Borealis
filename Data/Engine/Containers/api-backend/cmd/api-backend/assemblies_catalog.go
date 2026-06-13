package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	officialCatalogDefaultRepoURL      = "https://github.com/bunny-lab-io/Aurora"
	officialCatalogDefaultRepoGitURL   = "https://github.com/bunny-lab-io/Aurora.git"
	officialCatalogDefaultRepoRef      = "main"
	officialCatalogManifestFilename    = "manifest.json"
	officialCatalogMaxPayloadSize      = 500 * 1024 * 1024
	officialCatalogStateQualifiedTable = "assemblies.official_catalog_state"
	officialCatalogDefaultCheckoutRoot = "/opt/Borealis/Engine/Services/api-backend/cache"
	officialCatalogDefaultBundledRoot  = "/opt/Borealis/Engine/Official_Assemblies"
	officialCatalogSourceAurora        = "aurora"
	officialCatalogSourceBundled       = "bundled"
)

var officialCatalogRunGit = runOfficialCatalogGit

type officialCatalogState struct {
	AssemblyGUID      string
	BundledHash       string
	RemoteHash        string
	CatalogHash       string
	AppliedHash       string
	LastAppliedSource string
	RepoURL           string
	SourceURL         string
	SourceRepo        string
	SourcePath        string
	SourceVersion     string
	LastCatalogSyncAt string
	UpdatedAt         string
}

type officialCatalogEntry struct {
	AssemblyGUID     string
	DisplayName      string
	Summary          string
	AssemblyType     string
	AssemblySubtype  string
	ContentHash      string
	FilePath         string
	SourceRepo       string
	SourcePath       string
	SourceVersion    string
	SourceURL        string
	PayloadSizeBytes int64
}

type officialCatalogManifest struct {
	Source         string
	RepoURL        string
	RepoGitURL     string
	RepoRef        string
	CatalogVersion string
	GeneratedAt    string
	Entries        map[string]officialCatalogEntry
	BasePath       string
	Error          string
	ScannedFiles   int
	SkippedFiles   int
	FailedFiles    int
}

func (m officialCatalogManifest) available() bool {
	return len(m.Entries) > 0
}

type officialCatalogConfig struct {
	BundledRoot  string
	CheckoutRoot string
	RepoURL      string
	RepoGitURL   string
	RepoRef      string
	ManifestURL  string
}

type officialCatalogService struct {
	auth  *authService
	store assemblyCatalogStore
	cfg   officialCatalogConfig
	now   func() time.Time
}

func assemblyCatalogRefresh(w http.ResponseWriter, r *http.Request, auth *authService, legacyURL *url.URL) {
	_, store, ok := assemblyCatalogRequestContext(w, r, auth)
	if !ok {
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	service := newOfficialCatalogService(auth, store)
	manifest := service.activeManifest(ctx, true, true, true)
	cleanup := service.cleanupDeleted(ctx, manifest)
	if catalogIntFromAny(cleanup["deleted_count"]) > 0 || catalogIntFromAny(cleanup["state_pruned_count"]) > 0 {
		_ = notifyLegacyAssemblyCache(ctx, auth, legacyURL, "reload")
	}
	filter := assemblyListFilter{
		Domain:       assemblyNormalizeDomain(r.URL.Query().Get("domain")),
		AssemblyType: strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type"))),
	}
	items, queue, err := store.listAssemblies(ctx, filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	annotated := service.annotateCollection(ctx, items, manifest)
	status := service.catalogStatus(ctx, manifest)
	for key, value := range cleanup {
		status[key] = value
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":            annotated,
		"queue":            queue,
		"official_catalog": status,
	})
}

func assemblyCatalogUpdateOne(w http.ResponseWriter, r *http.Request, auth *authService, legacyURL *url.URL, assemblyGUID string) {
	if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
		failure.write(w)
		return
	}
	store, ok := auth.store.(assemblyCatalogStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "assemblies_unavailable"})
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	service := newOfficialCatalogService(auth, store)
	item, status, err := service.updateOfficialAssembly(ctx, assemblyGUID)
	if err != nil {
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	_ = notifyLegacyAssemblyCache(ctx, auth, legacyURL, "reload")
	writeJSON(w, http.StatusOK, item)
}

func assemblyCatalogUpdateAll(w http.ResponseWriter, r *http.Request, auth *authService, legacyURL *url.URL) {
	if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
		failure.write(w)
		return
	}
	store, ok := auth.store.(assemblyCatalogStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "assemblies_unavailable"})
		return
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	service := newOfficialCatalogService(auth, store)
	result, status := service.updateAllOfficialAssemblies(ctx)
	if status == http.StatusOK {
		if catalogIntFromAny(result["installed_count"]) > 0 || len(anyStringSlice(result["updated"])) > 0 || catalogIntFromAny(result["deleted_count"]) > 0 {
			_ = notifyLegacyAssemblyCache(ctx, auth, legacyURL, "reload")
		}
	}
	writeJSON(w, status, result)
}

func assemblyCatalogRequestContext(w http.ResponseWriter, r *http.Request, auth *authService) (operatorProfile, assemblyCatalogStore, bool) {
	profile, err := auth.currentProfile(r.Context(), r)
	if err != nil {
		if isUnauthorizedAuthError(err) {
			unauthorizedAuthFailure().write(w)
			return operatorProfile{}, nil, false
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
		return operatorProfile{}, nil, false
	}
	store, ok := auth.store.(assemblyCatalogStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "assemblies_unavailable"})
		return operatorProfile{}, nil, false
	}
	return profile, store, true
}

func newOfficialCatalogService(auth *authService, store assemblyCatalogStore) *officialCatalogService {
	return &officialCatalogService{
		auth:  auth,
		store: store,
		cfg:   officialCatalogConfigFromEnv(),
		now:   time.Now,
	}
}

func officialCatalogConfigFromEnv() officialCatalogConfig {
	repoURL := firstText(osEnv("BOREALIS_OFFICIAL_ASSEMBLIES_REPO_URL"), osEnv("OFFICIAL_ASSEMBLIES_REPO_URL"), officialCatalogDefaultRepoURL)
	repoGitURL := firstText(osEnv("BOREALIS_OFFICIAL_ASSEMBLIES_REPO_GIT_URL"), osEnv("OFFICIAL_ASSEMBLIES_REPO_GIT_URL"))
	if repoGitURL == "" {
		repoGitURL = repoURL
		if !strings.HasSuffix(repoGitURL, ".git") {
			repoGitURL += ".git"
		}
	}
	engineRoot := firstText(osEnv("BOREALIS_ENGINE_ROOT"), "/opt/Borealis/Engine")
	return officialCatalogConfig{
		BundledRoot:  filepath.Clean(firstText(osEnv("BOREALIS_OFFICIAL_ASSEMBLIES_ROOT"), osEnv("OFFICIAL_ASSEMBLIES_ROOT"), filepath.Join(engineRoot, "Official_Assemblies"), officialCatalogDefaultBundledRoot)),
		CheckoutRoot: filepath.Clean(firstText(osEnv("BOREALIS_OFFICIAL_ASSEMBLIES_CHECKOUT_ROOT"), osEnv("OFFICIAL_ASSEMBLIES_CHECKOUT_ROOT"), officialCatalogDefaultCheckoutRoot)),
		RepoURL:      repoURL,
		RepoGitURL:   repoGitURL,
		RepoRef:      firstText(osEnv("BOREALIS_OFFICIAL_ASSEMBLIES_REPO_REF"), osEnv("OFFICIAL_ASSEMBLIES_REPO_REF"), officialCatalogDefaultRepoRef),
		ManifestURL:  firstText(osEnv("BOREALIS_OFFICIAL_ASSEMBLIES_MANIFEST_URL"), osEnv("OFFICIAL_ASSEMBLIES_MANIFEST_URL")),
	}
}

func (s *officialCatalogService) activeManifest(ctx context.Context, forceRemote bool, allowExistingCheckoutFallback bool, allowBundledFallback bool) officialCatalogManifest {
	repoManifest := s.loadRepoManifest(ctx, forceRemote, allowExistingCheckoutFallback)
	if repoManifest.available() {
		return repoManifest
	}
	if !allowBundledFallback {
		if repoManifest.Error != "" {
			return repoManifest
		}
		return officialCatalogManifest{
			Source:     officialCatalogSourceAurora,
			RepoURL:    s.cfg.RepoURL,
			RepoGitURL: s.cfg.RepoGitURL,
			RepoRef:    s.cfg.RepoRef,
			Entries:    map[string]officialCatalogEntry{},
			Error:      "Aurora official catalog is unavailable.",
		}
	}
	bundled := s.loadBundledManifest()
	if bundled.available() {
		if repoManifest.Error != "" && bundled.Error == "" {
			bundled.Error = repoManifest.Error
		}
		return bundled
	}
	if repoManifest.Error != "" {
		return repoManifest
	}
	return bundled
}

func (s *officialCatalogService) loadBundledManifest() officialCatalogManifest {
	if strings.TrimSpace(s.cfg.BundledRoot) == "" {
		return officialCatalogManifest{
			Source:     officialCatalogSourceBundled,
			RepoURL:    s.cfg.RepoURL,
			RepoGitURL: s.cfg.RepoGitURL,
			RepoRef:    s.cfg.RepoRef,
			Entries:    map[string]officialCatalogEntry{},
			Error:      "Bundled official catalog root not configured.",
		}
	}
	return s.crawlCatalogRoot(s.cfg.BundledRoot, officialCatalogSourceBundled, "", "")
}

func (s *officialCatalogService) loadRepoManifest(ctx context.Context, force bool, allowExistingCheckoutFallback bool) officialCatalogManifest {
	if strings.TrimSpace(s.cfg.CheckoutRoot) == "" {
		return officialCatalogManifest{
			Source:     officialCatalogSourceAurora,
			RepoURL:    s.cfg.RepoURL,
			RepoGitURL: s.cfg.RepoGitURL,
			RepoRef:    s.cfg.RepoRef,
			Entries:    map[string]officialCatalogEntry{},
			Error:      "Official assembly checkout root is not configured.",
		}
	}
	if force {
		checkoutPath, commit, err := s.refreshRepoCheckout(ctx)
		if err == nil {
			return s.crawlCatalogRoot(checkoutPath, officialCatalogSourceAurora, "git:"+commit, "")
		}
		if allowExistingCheckoutFallback {
			if fallback, ok := s.loadExistingCheckoutManifest(ctx, err.Error()); ok {
				return fallback
			}
		}
		return officialCatalogManifest{
			Source:     officialCatalogSourceAurora,
			RepoURL:    s.cfg.RepoURL,
			RepoGitURL: s.cfg.RepoGitURL,
			RepoRef:    s.cfg.RepoRef,
			Entries:    map[string]officialCatalogEntry{},
			Error:      "Failed to sync Aurora repository: " + err.Error(),
		}
	}
	if fallback, ok := s.loadExistingCheckoutManifest(ctx, ""); ok {
		fallback.Error = ""
		return fallback
	}
	return officialCatalogManifest{
		Source:     officialCatalogSourceAurora,
		RepoURL:    s.cfg.RepoURL,
		RepoGitURL: s.cfg.RepoGitURL,
		RepoRef:    s.cfg.RepoRef,
		Entries:    map[string]officialCatalogEntry{},
	}
}

func (s *officialCatalogService) loadExistingCheckoutManifest(ctx context.Context, errorText string) (officialCatalogManifest, bool) {
	active := s.activeCheckoutDir()
	info, err := os.Stat(active)
	if err != nil || !info.IsDir() {
		return officialCatalogManifest{}, false
	}
	commit := ""
	if output, err := officialCatalogRunGit(ctx, active, []string{"rev-parse", "HEAD"}, ""); err == nil {
		commit = strings.TrimSpace(output)
	}
	version := ""
	if commit != "" {
		version = "git:" + commit
	}
	manifest := s.crawlCatalogRoot(active, officialCatalogSourceAurora, version, "")
	if errorText != "" {
		manifest.Error = "Failed to sync Aurora repository: " + errorText
	}
	return manifest, true
}

func (s *officialCatalogService) crawlCatalogRoot(root string, source string, sourceVersion string, inheritedError string) officialCatalogManifest {
	root = filepath.Clean(root)
	manifest := officialCatalogManifest{
		Source:         source,
		RepoURL:        s.cfg.RepoURL,
		RepoGitURL:     s.cfg.RepoGitURL,
		RepoRef:        s.cfg.RepoRef,
		CatalogVersion: sourceVersion,
		GeneratedAt:    s.now().UTC().Format(time.RFC3339),
		Entries:        map[string]officialCatalogEntry{},
		BasePath:       root,
		Error:          inheritedError,
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		manifest.Error = fmt.Sprintf("Official catalog root not found at %s", root)
		return manifest
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			manifest.FailedFiles++
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() && path != root {
				manifest.SkippedFiles++
				return filepath.SkipDir
			}
			if !d.IsDir() {
				manifest.SkippedFiles++
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(name), ".json") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			manifest.FailedFiles++
			return nil
		}
		rel = filepath.ToSlash(rel)
		if name == officialCatalogManifestFilename {
			manifest.SkippedFiles++
			return nil
		}
		manifest.ScannedFiles++
		raw, err := os.ReadFile(path)
		if err != nil {
			manifest.FailedFiles++
			return nil
		}
		var document map[string]any
		if err := json.Unmarshal(raw, &document); err != nil || document == nil {
			manifest.FailedFiles++
			return nil
		}
		normalized, err := s.normalizeCatalogDocument(document, s.cfg.RepoURL, rel, sourceVersion)
		if err != nil {
			manifest.FailedFiles++
			return nil
		}
		guid := assemblyCoerceGUID(normalized["assembly_guid"])
		if _, exists := manifest.Entries[guid]; exists {
			manifest.FailedFiles++
			return nil
		}
		payloadSize := officialCatalogJSONSize(normalized["payload"])
		entry := officialCatalogEntry{
			AssemblyGUID:     guid,
			DisplayName:      firstText(cleanText(normalized["display_name"]), guid),
			Summary:          cleanText(normalized["summary"]),
			AssemblyType:     strings.ToLower(firstText(cleanText(normalized["assembly_type"]), "script")),
			AssemblySubtype:  firstText(cleanText(normalized["assembly_subtype"]), "powershell"),
			ContentHash:      cleanText(normalized["content_hash"]),
			FilePath:         rel,
			SourceRepo:       firstText(cleanText(normalized["source_repo"]), s.cfg.RepoURL),
			SourcePath:       firstText(officialCatalogNormalizeRelativePath(normalized["source_path"]), rel),
			SourceVersion:    cleanText(normalized["source_version"]),
			SourceURL:        firstText(cleanText(normalized["source_url"]), s.cfg.RepoURL),
			PayloadSizeBytes: payloadSize,
		}
		if entry.PayloadSizeBytes > officialCatalogMaxPayloadSize {
			// Keep catalog usable; payload size warning is reflected by metadata only.
		}
		manifest.Entries[guid] = entry
		return nil
	})
	if err != nil && manifest.Error == "" {
		manifest.Error = err.Error()
	}
	if len(manifest.Entries) == 0 && manifest.Error == "" {
		manifest.Error = fmt.Sprintf("No official assembly JSON documents were found in %s", root)
	}
	return manifest
}

func (s *officialCatalogService) normalizeCatalogDocument(document map[string]any, sourceRepo string, sourcePath string, sourceVersion string) (map[string]any, error) {
	payload, err := officialCatalogPayloadFromDocument(document)
	if err != nil {
		return nil, err
	}
	switch payload.(type) {
	case map[string]any, []any:
	default:
		return nil, fmt.Errorf("official assembly payload must be a JSON object or array")
	}
	payloadMap, _ := payload.(map[string]any)
	guid := assemblyCoerceGUID(firstNonEmptyAny(document["assembly_guid"], payloadMap["assembly_guid"]))
	if guid == "" {
		return nil, fmt.Errorf("official assembly document is missing assembly_guid")
	}
	displayName := cleanText(firstNonEmptyAny(
		document["display_name"],
		document["name"],
		document["tab_name"],
		payloadMap["display_name"],
		payloadMap["name"],
		payloadMap["tab_name"],
		guid,
	))
	if displayName == "" {
		return nil, fmt.Errorf("official assembly document is missing display_name/name")
	}
	if payloadMap != nil {
		payloadMap = copyMap(payloadMap)
		payloadMap["assembly_guid"] = guid
		payload = payloadMap
	}
	assemblyType := officialCatalogInferType(document, payloadMap)
	assemblySubtype := officialCatalogInferSubtype(document, payloadMap, assemblyType)
	normalizedPath := firstText(officialCatalogNormalizeRelativePath(sourcePath), sourcePath)
	resolvedVersion := firstText(sourceVersion, cleanText(document["source_version"]), cleanText(payloadMap["source_version"]))
	normalized := map[string]any{
		"assembly_guid":    guid,
		"display_name":     displayName,
		"summary":          officialCatalogSummary(document, payload),
		"assembly_type":    assemblyType,
		"assembly_subtype": assemblySubtype,
		"source_repo":      sourceRepo,
		"source_path":      normalizedPath,
		"source_version":   resolvedVersion,
		"payload":          payload,
	}
	normalized["source_url"] = firstText(cleanText(document["source_url"]), officialCatalogGitHubBlobURL(sourceRepo, resolvedVersion, normalizedPath), sourceRepo)
	normalized["content_hash"] = officialCatalogContentHash(normalized)
	return normalized, nil
}

func officialCatalogPayloadFromDocument(document map[string]any) (any, error) {
	if payload, ok := document["payload"]; ok {
		switch typed := payload.(type) {
		case map[string]any:
			return copyMap(typed), nil
		case []any:
			return append([]any(nil), typed...), nil
		}
	}
	if workflow, ok := document["workflow"]; ok && workflow != nil {
		return assemblyWorkflowPayload(document, workflow)
	}
	return copyMap(document), nil
}

func officialCatalogInferType(document map[string]any, payload map[string]any) string {
	typeHint := strings.ToLower(cleanText(firstNonEmptyAny(document["assembly_type"], document["kind"], payload["assembly_type"], payload["kind"])))
	if typeHint == "workflow" || typeHint == "ansible" || typeHint == "script" {
		return typeHint
	}
	subtypeHint := strings.ToLower(cleanText(firstNonEmptyAny(document["assembly_subtype"], document["type"], payload["assembly_subtype"], payload["type"], payload["script_type"])))
	if subtypeHint == "workflow" {
		return "workflow"
	}
	if subtypeHint == "ansible" || strings.Contains(subtypeHint, "playbook") {
		return "ansible"
	}
	if payload != nil {
		if _, hasNodes := payload["nodes"]; hasNodes {
			if _, hasEdges := payload["edges"]; hasEdges {
				return "workflow"
			}
		}
		if _, ok := payload["playbook"]; ok {
			return "ansible"
		}
		if _, ok := payload["tasks"]; ok {
			return "ansible"
		}
		if _, ok := payload["roles"]; ok {
			return "ansible"
		}
	}
	return "script"
}

func officialCatalogInferSubtype(document map[string]any, payload map[string]any, assemblyType string) string {
	subtype := strings.ToLower(cleanText(firstNonEmptyAny(document["assembly_subtype"], document["type"], payload["assembly_subtype"], payload["type"], payload["script_type"])))
	if subtype != "" {
		return subtype
	}
	return assemblyDefaultSubtype(assemblyType)
}

func officialCatalogSummary(document map[string]any, payload any) string {
	if payloadMap, ok := payload.(map[string]any); ok {
		if summary := cleanText(firstNonEmptyAny(payloadMap["description"], payloadMap["summary"])); summary != "" {
			return summary
		}
	}
	return cleanText(firstNonEmptyAny(document["description"], document["summary"]))
}

func officialCatalogContentHash(document map[string]any) string {
	payload := document["payload"]
	if payloadMap, ok := payload.(map[string]any); ok {
		payloadMap = copyMap(payloadMap)
		payloadMap["assembly_guid"] = assemblyCoerceGUID(document["assembly_guid"])
		payload = payloadMap
	}
	fields := map[string]any{
		"assembly_guid":    assemblyCoerceGUID(document["assembly_guid"]),
		"display_name":     cleanText(document["display_name"]),
		"summary":          cleanText(document["summary"]),
		"assembly_type":    firstText(strings.ToLower(cleanText(document["assembly_type"])), "script"),
		"assembly_subtype": firstText(strings.ToLower(cleanText(document["assembly_subtype"])), "powershell"),
		"payload":          payload,
	}
	encoded, _ := json.Marshal(fields)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (s *officialCatalogService) loadEntryDocument(manifest officialCatalogManifest, entry officialCatalogEntry) (map[string]any, error) {
	if strings.TrimSpace(manifest.BasePath) == "" {
		return nil, fmt.Errorf("Official catalog root is unavailable.")
	}
	path := filepath.Join(manifest.BasePath, filepath.FromSlash(entry.FilePath))
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil || document == nil {
		return nil, fmt.Errorf("Official catalog entry %s did not decode to an object.", entry.FilePath)
	}
	return s.normalizeCatalogDocument(document, entry.SourceRepo, entry.FilePath, firstText(entry.SourceVersion, manifest.CatalogVersion))
}

func (s *officialCatalogService) updateOfficialAssembly(ctx context.Context, assemblyGUID string) (map[string]any, int, error) {
	guid := assemblyCoerceGUID(assemblyGUID)
	if guid == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("assembly_guid is required")
	}
	manifest := s.activeManifest(ctx, true, false, false)
	if !manifest.available() {
		return nil, http.StatusBadGateway, fmt.Errorf(firstText(manifest.Error, "Official Aurora catalog is unavailable."))
	}
	entry, ok := manifest.Entries[guid]
	if !ok {
		return nil, http.StatusNotFound, fmt.Errorf("Assembly '%s' not found in the official catalog.", guid)
	}
	document, err := s.loadEntryDocument(manifest, entry)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	_, status, err := s.store.importAssembly(ctx, map[string]any{
		"domain":        assemblyDomainOfficial,
		"assembly_guid": entry.AssemblyGUID,
		"document":      document,
	})
	if err != nil {
		return nil, status, err
	}
	if err := s.upsertEntryState(ctx, manifest, entry); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	item, found, err := s.store.getAssembly(ctx, entry.AssemblyGUID, true)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if !found {
		return nil, http.StatusInternalServerError, fmt.Errorf("updated assembly was not found after import")
	}
	annotated := s.annotateCollection(ctx, []map[string]any{item}, manifest)
	if len(annotated) == 1 {
		return annotated[0], http.StatusOK, nil
	}
	return item, http.StatusOK, nil
}

func (s *officialCatalogService) updateAllOfficialAssemblies(ctx context.Context) (map[string]any, int) {
	manifest := s.activeManifest(ctx, true, false, false)
	if !manifest.available() {
		return map[string]any{
			"updated":       []string{},
			"updated_items": []map[string]any{},
			"skipped":       0,
			"failed":        []map[string]string{},
			"source":        manifest.Source,
			"repo_url":      manifest.RepoURL,
			"warning":       "",
			"error":         manifest.Error,
		}, http.StatusBadGateway
	}
	current, _, err := s.store.listAssemblies(ctx, assemblyListFilter{Domain: assemblyDomainOfficial})
	if err != nil {
		return map[string]any{"error": err.Error()}, http.StatusInternalServerError
	}
	currentByGUID := map[string]map[string]any{}
	currentHashes := map[string]string{}
	for _, item := range current {
		guid := assemblyCoerceGUID(firstNonEmptyAny(item["assembly_guid"], item["assembly_id"]))
		if guid == "" {
			continue
		}
		currentByGUID[guid] = item
		currentHashes[guid] = firstText(cleanText(item["content_hash"]), officialCatalogAPIItemContentHash(item))
	}
	updated := []string{}
	updatedItems := []map[string]any{}
	installed := []string{}
	installedItems := []map[string]any{}
	failed := []map[string]string{}
	skipped := 0
	entries := make([]officialCatalogEntry, 0, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].AssemblyGUID < entries[j].AssemblyGUID })
	for _, entry := range entries {
		currentItem := currentByGUID[entry.AssemblyGUID]
		isNewInstall := currentItem == nil
		if currentHash := currentHashes[entry.AssemblyGUID]; currentHash != "" && currentHash == entry.ContentHash && !officialCatalogEntryRequiresMetadataRefresh(currentItem, entry) {
			skipped++
			continue
		}
		document, err := s.loadEntryDocument(manifest, entry)
		if err != nil {
			failed = append(failed, map[string]string{"assembly_guid": entry.AssemblyGUID, "error": err.Error()})
			continue
		}
		if _, status, err := s.store.importAssembly(ctx, map[string]any{"domain": assemblyDomainOfficial, "assembly_guid": entry.AssemblyGUID, "document": document}); err != nil {
			failed = append(failed, map[string]string{"assembly_guid": entry.AssemblyGUID, "error": err.Error(), "status": fmt.Sprintf("%d", status)})
			continue
		}
		if err := s.upsertEntryState(ctx, manifest, entry); err != nil {
			failed = append(failed, map[string]string{"assembly_guid": entry.AssemblyGUID, "error": err.Error()})
			continue
		}
		updated = append(updated, entry.AssemblyGUID)
		refreshed, found, _ := s.store.getAssembly(ctx, entry.AssemblyGUID, false)
		refreshedItem := map[string]any{
			"assembly_guid":  entry.AssemblyGUID,
			"display_name":   entry.DisplayName,
			"source_path":    entry.SourcePath,
			"source_version": firstText(entry.SourceVersion, manifest.CatalogVersion),
		}
		if found {
			refreshedItem["display_name"] = firstText(cleanText(refreshed["display_name"]), entry.DisplayName)
			refreshedItem["source_path"] = firstText(officialCatalogNormalizeRelativePath(firstNonEmptyAny(refreshed["source_path"], refreshed["path"])), entry.SourcePath)
		}
		updatedItems = append(updatedItems, refreshedItem)
		if isNewInstall {
			installed = append(installed, entry.AssemblyGUID)
			installedItems = append(installedItems, refreshedItem)
		}
	}
	cleanup := s.cleanupDeleted(ctx, manifest)
	for _, item := range cleanupFailureSlice(cleanup["failed"]) {
		failed = append(failed, item)
	}
	return map[string]any{
		"updated":                updated,
		"updated_items":          updatedItems,
		"installed":              installed,
		"installed_items":        installedItems,
		"installed_count":        len(installed),
		"updated_existing_count": catalogMaxInt(len(updated)-len(installed), 0),
		"deleted":                cleanup["deleted"],
		"deleted_items":          cleanup["deleted_items"],
		"deleted_count":          cleanup["deleted_count"],
		"skipped":                skipped,
		"failed":                 failed,
		"source":                 manifest.Source,
		"repo_url":               manifest.RepoURL,
		"source_version":         manifest.CatalogVersion,
		"warning":                map[bool]string{true: manifest.Error, false: ""}[manifest.available()],
		"error":                  map[bool]string{true: "", false: manifest.Error}[manifest.available()],
	}, http.StatusOK
}

func (s *officialCatalogService) upsertEntryState(ctx context.Context, manifest officialCatalogManifest, entry officialCatalogEntry) error {
	state := officialCatalogState{
		AssemblyGUID:      entry.AssemblyGUID,
		CatalogHash:       entry.ContentHash,
		AppliedHash:       entry.ContentHash,
		LastAppliedSource: manifest.Source,
		RepoURL:           manifest.RepoURL,
		SourceURL:         firstText(entry.SourceURL, manifest.RepoURL),
		SourceRepo:        entry.SourceRepo,
		SourcePath:        entry.SourcePath,
		SourceVersion:     firstText(entry.SourceVersion, manifest.CatalogVersion),
		LastCatalogSyncAt: s.now().UTC().Format(time.RFC3339),
	}
	if manifest.Source == officialCatalogSourceBundled {
		state.BundledHash = entry.ContentHash
	} else {
		state.RemoteHash = entry.ContentHash
	}
	return s.store.upsertOfficialCatalogState(ctx, state)
}

func (s *officialCatalogService) annotateCollection(ctx context.Context, items []map[string]any, manifest officialCatalogManifest) []map[string]any {
	stateMap, _ := s.store.listOfficialCatalogState(ctx)
	annotated := make([]map[string]any, 0, len(items))
	for _, raw := range items {
		item := copyMap(raw)
		if !strings.EqualFold(cleanText(item["source"]), assemblyDomainOfficial) {
			annotated = append(annotated, item)
			continue
		}
		guid := assemblyCoerceGUID(firstNonEmptyAny(item["assembly_guid"], item["assembly_id"]))
		currentHash := firstText(cleanText(item["content_hash"]), officialCatalogAPIItemContentHash(item))
		entry, hasEntry := manifest.Entries[guid]
		state := stateMap[guid]
		repoURL := firstText(manifest.RepoURL, state.RepoURL, s.cfg.RepoURL)
		sourceURL := firstText(entry.SourceURL, state.SourceURL, repoURL)
		sourceRepo := firstText(entry.SourceRepo, state.SourceRepo, repoURL)
		sourcePath := firstText(entry.SourcePath, state.SourcePath, cleanText(item["source_path"]))
		sourceVersion := firstText(entry.SourceVersion, manifest.CatalogVersion, state.SourceVersion, cleanText(item["source_version"]))
		item["source_repo"] = firstNonEmptyAny(item["source_repo"], sourceRepo)
		item["source_path"] = firstNonEmptyAny(item["source_path"], sourcePath)
		item["source_version"] = firstNonEmptyAny(item["source_version"], sourceVersion)
		item["official_managed"] = hasEntry || state.AssemblyGUID != "" || sourcePath != ""
		item["official_repo_url"] = repoURL
		item["official_source_url"] = sourceURL
		if manifest.available() {
			item["official_catalog_source"] = manifest.Source
		} else {
			item["official_catalog_source"] = officialCatalogSourceBundled
		}
		item["official_source_version"] = sourceVersion
		item["official_source_path"] = sourcePath
		item["official_update_available"] = hasEntry && entry.ContentHash != "" && currentHash != entry.ContentHash
		item["official_last_applied_source"] = nilStringValue(state.LastAppliedSource)
		item["official_last_synced_at"] = nilStringValue(firstText(state.LastCatalogSyncAt, state.UpdatedAt))
		annotated = append(annotated, item)
	}
	return annotated
}

func (s *officialCatalogService) catalogStatus(ctx context.Context, manifest officialCatalogManifest) map[string]any {
	current, _, _ := s.store.listAssemblies(ctx, assemblyListFilter{Domain: assemblyDomainOfficial})
	currentByGUID := map[string]map[string]any{}
	for _, item := range current {
		guid := assemblyCoerceGUID(firstNonEmptyAny(item["assembly_guid"], item["assembly_id"]))
		if guid != "" {
			currentByGUID[guid] = item
		}
	}
	updateCount := 0
	newCount := 0
	metadataRefreshCount := 0
	for _, entry := range manifest.Entries {
		currentItem := currentByGUID[entry.AssemblyGUID]
		if currentItem == nil {
			newCount++
			continue
		}
		currentHash := firstText(cleanText(currentItem["content_hash"]), officialCatalogAPIItemContentHash(currentItem))
		if currentHash != "" && entry.ContentHash != "" && currentHash != entry.ContentHash {
			updateCount++
			continue
		}
		if officialCatalogEntryRequiresMetadataRefresh(currentItem, entry) {
			metadataRefreshCount++
		}
	}
	manifestError := manifest.Error
	status := map[string]any{
		"repo_url":               firstText(manifest.RepoURL, s.cfg.RepoURL),
		"repo_git_url":           firstText(manifest.RepoGitURL, s.cfg.RepoGitURL),
		"repo_ref":               firstText(manifest.RepoRef, s.cfg.RepoRef),
		"source":                 manifest.Source,
		"available":              manifest.available(),
		"manifest_url":           nilStringValue(s.cfg.ManifestURL),
		"source_version":         nilStringValue(manifest.CatalogVersion),
		"generated_at":           nilStringValue(manifest.GeneratedAt),
		"error":                  "",
		"warning":                "",
		"update_count":           updateCount,
		"new_assembly_count":     newCount,
		"metadata_refresh_count": metadataRefreshCount,
		"actionable_count":       updateCount + newCount + metadataRefreshCount,
		"scanned_files":          manifest.ScannedFiles,
		"failed_files":           manifest.FailedFiles,
	}
	if manifest.available() {
		status["warning"] = manifestError
	} else {
		status["error"] = manifestError
	}
	return status
}

func (s *officialCatalogService) cleanupDeleted(ctx context.Context, manifest officialCatalogManifest) map[string]any {
	result := map[string]any{
		"cleanup_performed":  false,
		"deleted":            []string{},
		"deleted_items":      []map[string]any{},
		"deleted_count":      0,
		"state_pruned_count": 0,
		"failed":             []map[string]string{},
	}
	if !manifest.available() || manifest.Source != officialCatalogSourceAurora || manifest.Error != "" || manifest.FailedFiles != 0 {
		return result
	}
	result["cleanup_performed"] = true
	current, _, err := s.store.listAssemblies(ctx, assemblyListFilter{Domain: assemblyDomainOfficial})
	if err != nil {
		result["failed"] = []map[string]string{{"error": err.Error(), "action": "list"}}
		return result
	}
	manifestGUIDs := map[string]bool{}
	for guid := range manifest.Entries {
		manifestGUIDs[guid] = true
	}
	currentGUIDs := map[string]bool{}
	deleted := []string{}
	deletedItems := []map[string]any{}
	failed := []map[string]string{}
	for _, item := range current {
		guid := assemblyCoerceGUID(firstNonEmptyAny(item["assembly_guid"], item["assembly_id"]))
		if guid == "" {
			continue
		}
		currentGUIDs[guid] = true
		if manifestGUIDs[guid] {
			continue
		}
		if _, status, err := s.store.deleteAssembly(ctx, guid); err != nil {
			failed = append(failed, map[string]string{"assembly_guid": guid, "error": err.Error(), "status": fmt.Sprintf("%d", status), "action": "delete"})
			continue
		}
		_ = s.store.deleteOfficialCatalogState(ctx, guid)
		deleted = append(deleted, guid)
		deletedItems = append(deletedItems, map[string]any{
			"assembly_guid": guid,
			"display_name":  firstText(cleanText(firstNonEmptyAny(item["display_name"], item["name"])), guid),
			"source_path":   officialCatalogNormalizeRelativePath(firstNonEmptyAny(item["source_path"], item["path"])),
		})
	}
	stateMap, _ := s.store.listOfficialCatalogState(ctx)
	statePruned := 0
	for guid := range stateMap {
		if !manifestGUIDs[guid] && !currentGUIDs[guid] {
			if err := s.store.deleteOfficialCatalogState(ctx, guid); err == nil {
				statePruned++
			}
		}
	}
	result["deleted"] = deleted
	result["deleted_items"] = deletedItems
	result["deleted_count"] = len(deleted)
	result["state_pruned_count"] = statePruned
	result["failed"] = failed
	return result
}

func (s *officialCatalogService) refreshRepoCheckout(ctx context.Context) (string, string, error) {
	checkoutRoot := filepath.Clean(s.cfg.CheckoutRoot)
	if err := os.MkdirAll(checkoutRoot, 0o755); err != nil {
		return "", "", err
	}
	active := s.activeCheckoutDir()
	tempCheckout, err := os.MkdirTemp(checkoutRoot, "aurora-sync-")
	if err != nil {
		return "", "", err
	}
	backup := active + ".previous"
	token := s.loadGitToken(ctx)
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = os.RemoveAll(tempCheckout)
		}
	}()
	if _, err := officialCatalogRunGit(ctx, tempCheckout, []string{"init"}, token); err != nil {
		return "", "", err
	}
	if _, err := officialCatalogRunGit(ctx, tempCheckout, []string{"remote", "add", "origin", s.cfg.RepoGitURL}, token); err != nil {
		return "", "", err
	}
	if _, err := officialCatalogRunGit(ctx, tempCheckout, []string{"fetch", "--depth", "1", "origin", s.cfg.RepoRef}, token); err != nil {
		return "", "", err
	}
	if _, err := officialCatalogRunGit(ctx, tempCheckout, []string{"checkout", "--detach", "FETCH_HEAD"}, token); err != nil {
		return "", "", err
	}
	commit, err := officialCatalogRunGit(ctx, tempCheckout, []string{"rev-parse", "HEAD"}, "")
	if err != nil {
		return "", "", err
	}
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return "", "", fmt.Errorf("Git checkout completed without a resolved commit SHA.")
	}
	_ = os.RemoveAll(backup)
	if _, err := os.Stat(active); err == nil {
		if err := os.Rename(active, backup); err != nil {
			return "", "", err
		}
	}
	if err := os.Rename(tempCheckout, active); err != nil {
		if _, statErr := os.Stat(backup); statErr == nil {
			_ = os.Rename(backup, active)
		}
		return "", "", err
	}
	cleanupTemp = false
	_ = os.RemoveAll(backup)
	return active, commit, nil
}

func (s *officialCatalogService) activeCheckoutDir() string {
	return filepath.Join(filepath.Clean(s.cfg.CheckoutRoot), officialCatalogCheckoutName(s.cfg.RepoURL, s.cfg.RepoGitURL))
}

func (s *officialCatalogService) loadGitToken(ctx context.Context) string {
	if s.auth == nil || s.auth.aegis == nil {
		return ""
	}
	store, ok := s.auth.store.(githubTokenManagementStore)
	if !ok {
		return ""
	}
	record, err := store.loadGithubToken(ctx, s.auth.aegis)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(record.Token)
}

func runOfficialCatalogGit(ctx context.Context, cwd string, args []string, token string) (string, error) {
	runOnce := func(useToken string) ([]byte, []byte, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = cwd
		cmd.Env = os.Environ()
		cmd.Env = setEnv(cmd.Env, "GIT_TERMINAL_PROMPT", "0")
		cmd.Env = setEnv(cmd.Env, "GIT_ASKPASS", "true")
		if strings.TrimSpace(useToken) != "" {
			cmd.Env = setEnv(cmd.Env, "GIT_CONFIG_COUNT", "1")
			cmd.Env = setEnv(cmd.Env, "GIT_CONFIG_KEY_0", "http.extraHeader")
			cmd.Env = setEnv(cmd.Env, "GIT_CONFIG_VALUE_0", "AUTHORIZATION: bearer "+useToken)
		}
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.Bytes(), stderr.Bytes(), err
	}
	stdout, stderr, err := runOnce(token)
	if err != nil {
		errorText := strings.TrimSpace(string(stderr))
		if errorText == "" {
			errorText = strings.TrimSpace(string(stdout))
		}
		lowered := strings.ToLower(errorText)
		if strings.TrimSpace(token) != "" && (strings.Contains(lowered, "could not read username for 'https://github.com'") || strings.Contains(lowered, "authentication failed")) {
			anonOut, anonErr, anonRunErr := runOnce("")
			if anonRunErr == nil {
				return strings.TrimSpace(string(anonOut)), nil
			}
			if text := strings.TrimSpace(string(anonErr)); text != "" {
				errorText = text
			}
			return "", fmt.Errorf("Failed to fetch Aurora from GitHub using the configured token. Verify the token, repo URL/ref, and outbound network path, or remove the stored token so public Aurora access can proceed anonymously.")
		}
		if strings.Contains(lowered, "could not read username for 'https://github.com'") || strings.Contains(lowered, "authentication failed") {
			return "", fmt.Errorf("Failed to fetch Aurora from GitHub over HTTPS. Verify the repo URL/ref, outbound network or proxy settings, and any cached Git credentials. If Aurora is private, configure /api/github/token.")
		}
		if errorText == "" {
			errorText = "git " + strings.Join(args, " ") + " failed"
		}
		return "", errors.New(errorText)
	}
	return strings.TrimSpace(string(stdout)), nil
}

func officialCatalogCheckoutName(repoURL string, repoGitURL string) string {
	parsed, err := url.Parse(firstText(repoGitURL, repoURL))
	leaf := ""
	if err == nil {
		leaf = filepath.Base(parsed.Path)
	}
	leaf = strings.TrimSuffix(leaf, ".git")
	if leaf == "" || leaf == "." || leaf == "/" {
		leaf = "Aurora"
	}
	var builder strings.Builder
	for _, ch := range leaf {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			builder.WriteRune(ch)
		} else {
			builder.WriteByte('-')
		}
	}
	name := strings.Trim(builder.String(), "-_")
	if name == "" {
		return "Aurora"
	}
	return name
}

func officialCatalogGitHubBlobURL(repoURL string, sourceVersion string, sourcePath string) string {
	if !strings.Contains(strings.ToLower(repoURL), "github.com") {
		return ""
	}
	rel := officialCatalogNormalizeRelativePath(sourcePath)
	if rel == "" {
		return repoURL
	}
	commit := strings.TrimPrefix(strings.TrimSpace(sourceVersion), "git:")
	if commit == "" {
		return repoURL
	}
	escapedPath := strings.ReplaceAll(url.PathEscape(rel), "%2F", "/")
	return strings.TrimRight(repoURL, "/") + "/blob/" + url.PathEscape(commit) + "/" + escapedPath
}

func officialCatalogNormalizeRelativePath(value any) string {
	text := strings.ReplaceAll(cleanText(value), "\\", "/")
	if text == "" {
		return ""
	}
	parts := []string{}
	for _, part := range strings.Split(text, "/") {
		candidate := strings.TrimSpace(part)
		if candidate == "" || candidate == "." {
			continue
		}
		if candidate == ".." {
			return ""
		}
		parts = append(parts, candidate)
	}
	return strings.Join(parts, "/")
}

func officialCatalogEntryRequiresMetadataRefresh(currentItem map[string]any, entry officialCatalogEntry) bool {
	if currentItem == nil {
		return true
	}
	if cleanText(currentItem["source_repo"]) == "" {
		return true
	}
	if cleanText(currentItem["source_repo"]) != entry.SourceRepo {
		return true
	}
	if officialCatalogNormalizeRelativePath(currentItem["source_path"]) != entry.SourcePath {
		return true
	}
	if cleanText(currentItem["source_version"]) == "" {
		return true
	}
	if cleanText(currentItem["content_hash"]) == "" {
		return true
	}
	return false
}

func officialCatalogAPIItemContentHash(item map[string]any) string {
	payload := item["payload_json"]
	if payload == nil || cleanText(payload) == "" {
		payload = item["payload"]
	}
	if payloadText, ok := payload.(string); ok {
		var decoded any
		if err := json.Unmarshal([]byte(payloadText), &decoded); err == nil {
			payload = decoded
		}
	}
	document := map[string]any{
		"assembly_guid":    firstNonEmptyAny(item["assembly_guid"], item["assembly_id"]),
		"display_name":     firstNonEmptyAny(item["display_name"], item["name"], item["assembly_guid"]),
		"summary":          firstNonEmptyAny(item["summary"], item["description"]),
		"assembly_type":    firstNonEmptyAny(item["assembly_type"], "script"),
		"assembly_subtype": firstNonEmptyAny(item["assembly_subtype"], "powershell"),
		"payload":          payload,
	}
	if document["summary"] == "" {
		document["summary"] = officialCatalogSummary(map[string]any{}, payload)
	}
	return officialCatalogContentHash(document)
}

func officialCatalogJSONSize(value any) int64 {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return int64(len(encoded))
}

func nilStringValue(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func catalogIntFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func anyStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	default:
		return nil
	}
}

func cleanupFailureSlice(value any) []map[string]string {
	switch typed := value.(type) {
	case []map[string]string:
		return typed
	default:
		return nil
	}
}

func catalogMaxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *postgresOperatorStore) listOfficialCatalogState(ctx context.Context) (map[string]officialCatalogState, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT assembly_guid, bundled_hash, remote_hash, catalog_hash, applied_hash, last_applied_source,
		       repo_url, source_url, source_repo, source_path, source_version, last_catalog_sync_at, updated_at
		  FROM `+officialCatalogStateQualifiedTable)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]officialCatalogState{}
	for rows.Next() {
		var row officialCatalogState
		var bundled, remote, catalog, applied, source, repoURL, sourceURL, sourceRepo, sourcePath, sourceVersion, synced, updated sql.NullString
		if err := rows.Scan(&row.AssemblyGUID, &bundled, &remote, &catalog, &applied, &source, &repoURL, &sourceURL, &sourceRepo, &sourcePath, &sourceVersion, &synced, &updated); err != nil {
			return nil, err
		}
		row.AssemblyGUID = assemblyCoerceGUID(row.AssemblyGUID)
		row.BundledHash = nullString(bundled)
		row.RemoteHash = nullString(remote)
		row.CatalogHash = nullString(catalog)
		row.AppliedHash = nullString(applied)
		row.LastAppliedSource = nullString(source)
		row.RepoURL = nullString(repoURL)
		row.SourceURL = nullString(sourceURL)
		row.SourceRepo = nullString(sourceRepo)
		row.SourcePath = nullString(sourcePath)
		row.SourceVersion = nullString(sourceVersion)
		row.LastCatalogSyncAt = nullString(synced)
		row.UpdatedAt = nullString(updated)
		if row.AssemblyGUID != "" {
			result[row.AssemblyGUID] = row
		}
	}
	return result, rows.Err()
}

func (s *postgresOperatorStore) upsertOfficialCatalogState(ctx context.Context, state officialCatalogState) error {
	guid := assemblyCoerceGUID(state.AssemblyGUID)
	if guid == "" {
		return fmt.Errorf("assembly_guid required for official catalog state")
	}
	existingMap, _ := s.listOfficialCatalogState(ctx)
	existing := existingMap[guid]
	now := assemblyNowString()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `
		INSERT INTO `+officialCatalogStateQualifiedTable+` (
			assembly_guid, bundled_hash, remote_hash, catalog_hash, applied_hash, last_applied_source,
			repo_url, source_url, source_repo, source_path, source_version, last_catalog_sync_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT(assembly_guid) DO UPDATE SET
			bundled_hash=EXCLUDED.bundled_hash,
			remote_hash=EXCLUDED.remote_hash,
			catalog_hash=EXCLUDED.catalog_hash,
			applied_hash=EXCLUDED.applied_hash,
			last_applied_source=EXCLUDED.last_applied_source,
			repo_url=EXCLUDED.repo_url,
			source_url=EXCLUDED.source_url,
			source_repo=EXCLUDED.source_repo,
			source_path=EXCLUDED.source_path,
			source_version=EXCLUDED.source_version,
			last_catalog_sync_at=EXCLUDED.last_catalog_sync_at,
			updated_at=EXCLUDED.updated_at
	`, guid,
		nullableString(firstText(state.BundledHash, existing.BundledHash)),
		nullableString(firstText(state.RemoteHash, existing.RemoteHash)),
		nullableString(firstText(state.CatalogHash, existing.CatalogHash)),
		nullableString(firstText(state.AppliedHash, existing.AppliedHash)),
		nullableString(firstText(state.LastAppliedSource, existing.LastAppliedSource)),
		nullableString(firstText(state.RepoURL, existing.RepoURL)),
		nullableString(firstText(state.SourceURL, existing.SourceURL)),
		nullableString(firstText(state.SourceRepo, existing.SourceRepo)),
		nullableString(firstText(state.SourcePath, existing.SourcePath)),
		nullableString(firstText(state.SourceVersion, existing.SourceVersion)),
		nullableString(firstText(state.LastCatalogSyncAt, existing.LastCatalogSyncAt)),
		now,
	)
	return err
}

func (s *postgresOperatorStore) deleteOfficialCatalogState(ctx context.Context, assemblyGUID string) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `DELETE FROM `+officialCatalogStateQualifiedTable+` WHERE assembly_guid=$1`, assemblyCoerceGUID(assemblyGUID))
	return err
}
