package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSaveLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	cfg := Default()
	cfg.ServerURL = "https://borealis.example.com/"
	cfg.ServerIPFallback = "192.168.3.251"
	cfg.EnrollmentCode = "CODE"
	cfg.Agent.GUID = "guid"
	cfg.Agent.ReleaseChannel = "Unstable"
	cfg.Agent.Branch = "feature/test"
	cfg.Agent.InstalledBuildID = "ABCDEF"
	cfg.Trust.EngineCAPEM = "-----BEGIN CERTIFICATE-----\r\nAQID\r\n-----END CERTIFICATE-----"
	cfg.RemoteOps.Available = true
	cfg.RemoteOps.SiteID = 1
	cfg.RemoteOps.WorkerGUID = "worker-config-route"
	cfg.RemoteOps.RouteGeneration = 3
	cfg.RemoteOps.RoutePathPrefix = "/_borealis/site-workers/worker-config-route"
	cfg.RemoteOps.BaseURL = "https://borealis.example.com/_borealis/site-workers/worker-config-route/"
	cfg.RemoteOps.SocketURL = "https://borealis.example.com/_borealis/site-workers/worker-config-route/socket.io/"
	cfg.UpdateDependencyState("WireGuard", func(state *DependencyStateSection) {
		state.Phase = "healthy"
		state.Status = "healthy"
		state.DesiredVersion = " 1.1 "
		state.InstalledVersion = "1.1\r\n"
	})
	cfg.UpdateDependencyState("UltraVNC", func(state *DependencyStateSection) {
		state.Phase = "healthy"
		state.Status = "healthy"
		state.DesiredVersion = "1.8.2.1"
		state.InstalledVersion = "1.8.2.1"
	})
	cfg.Identity.PublicKeySPKIB64 = "pub"

	if err := Save(path, &cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.SchemaVersion != SchemaVersion {
		t.Fatalf("schema mismatch: %d", loaded.SchemaVersion)
	}
	if loaded.ServerURL != "https://borealis.example.com" {
		t.Fatalf("server url not normalized: %q", loaded.ServerURL)
	}
	if loaded.ServerIPFallback != "192.168.3.251" {
		t.Fatalf("server_ip_fallback not normalized: %q", loaded.ServerIPFallback)
	}
	if loaded.Agent.Branch != "feature/test" {
		t.Fatalf("branch mismatch: %q", loaded.Agent.Branch)
	}
	if loaded.Agent.ReleaseChannel != ReleaseChannelUnstable {
		t.Fatalf("release channel mismatch: %q", loaded.Agent.ReleaseChannel)
	}
	if loaded.Agent.InstalledBuildID != "abcdef" {
		t.Fatalf("installed build id mismatch: %q", loaded.Agent.InstalledBuildID)
	}
	if loaded.Trust.EngineCAPEM != "-----BEGIN CERTIFICATE-----\nAQID\n-----END CERTIFICATE-----\n" {
		t.Fatalf("engine CA PEM not normalized: %q", loaded.Trust.EngineCAPEM)
	}
	if loaded.Agent.LogRetentionDays != DefaultLogRetentionDays {
		t.Fatalf("log retention default = %d, want %d", loaded.Agent.LogRetentionDays, DefaultLogRetentionDays)
	}
	if loaded.RemoteOps.BaseURL != "https://borealis.example.com/_borealis/site-workers/worker-config-route" {
		t.Fatalf("remote ops base url not normalized: %q", loaded.RemoteOps.BaseURL)
	}
	if loaded.RemoteOps.SocketURL != "https://borealis.example.com/_borealis/site-workers/worker-config-route/socket.io" {
		t.Fatalf("remote ops socket url not normalized: %q", loaded.RemoteOps.SocketURL)
	}
	if loaded.Agent.State.Revision <= 0 || loaded.Agent.State.Writer == "" || loaded.Agent.State.LastWriteAt <= 0 {
		t.Fatalf("state metadata missing: %#v", loaded.Agent.State)
	}
	wireGuardState, ok := loaded.Agent.DependencyState["wireguard"]
	if !ok {
		t.Fatalf("wireguard dependency_state missing: %#v", loaded.Agent.DependencyState)
	}
	if wireGuardState.DesiredVersion != "1.1" || wireGuardState.InstalledVersion != "1.1" {
		t.Fatalf("wireguard dependency state mismatch: %#v", wireGuardState)
	}
	ultraVNCState, ok := loaded.Agent.DependencyState["ultravnc"]
	if !ok {
		t.Fatalf("ultravnc dependency_state missing: %#v", loaded.Agent.DependencyState)
	}
	if ultraVNCState.DesiredVersion != "1.8.2.1" || ultraVNCState.InstalledVersion != "1.8.2.1" {
		t.Fatalf("ultravnc dependency state mismatch: %#v", ultraVNCState)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, unexpected := range []string{"\"runtime\"", "\"feature_flags\"", "\"last_saved_at\"", "\"extra\"", "\"dependency_versions\""} {
		if strings.Contains(string(raw), unexpected) {
			t.Fatalf("config contains unexpected field %s: %s", unexpected, string(raw))
		}
	}
}

func TestValidateServerURLForEnrollmentRejectsRawIP(t *testing.T) {
	if err := ValidateServerURLForEnrollment("https://192.0.2.10"); err == nil {
		t.Fatal("raw IP server URL accepted")
	}
	if err := ValidateServerURLForEnrollment("https://borealis.internal.example"); err != nil {
		t.Fatalf("FQDN server URL rejected: %v", err)
	}
}

func TestValidateServerIPFallbackRejectsUnsafeValues(t *testing.T) {
	for _, value := range []string{
		"https://192.0.2.10",
		"192.0.2.10/32",
		"borealis.example.com",
		"127.0.0.1",
		"0.0.0.0",
		"224.0.0.1",
	} {
		if err := ValidateServerIPFallback(value); err == nil {
			t.Fatalf("server_ip_fallback %q accepted", value)
		}
	}
	if err := ValidateServerIPFallback("192.168.3.251"); err != nil {
		t.Fatalf("private server_ip_fallback rejected: %v", err)
	}
}

func TestDecodeEngineCAB64NormalizesPEM(t *testing.T) {
	encoded := "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tDQpBUUlEDQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0t"
	decoded, err := DecodeEngineCAB64(encoded)
	if err != nil {
		t.Fatalf("DecodeEngineCAB64 failed: %v", err)
	}
	if decoded != "-----BEGIN CERTIFICATE-----\nAQID\n-----END CERTIFICATE-----\n" {
		t.Fatalf("decoded PEM = %q", decoded)
	}
}

func TestFileNameIsAgentJSONOnly(t *testing.T) {
	if FileName != "agent.json" {
		t.Fatalf("FileName = %q, want agent.json", FileName)
	}
}

func TestLoadDoesNotFallbackToConfigJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"schema_version":1,"server_url":"https://old.example","agent":{},"identity":{},"tokens":{},"trust":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.ServerURL != "" {
		t.Fatalf("loaded server_url from config.json fallback: %q", loaded.ServerURL)
	}
}

func TestLivenessUpdatePersistsAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	cfg := Default()
	if err := Save(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := Update(path, func(cfg *AgentConfig) {
		cfg.Agent.Liveness.PID = 123
		cfg.Agent.Liveness.BootID = "boot-1"
		cfg.Agent.Liveness.LastLocalTickAt = 456
	}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.Liveness.PID != 123 || loaded.Agent.Liveness.BootID != "boot-1" || loaded.Agent.Liveness.LastLocalTickAt != 456 {
		t.Fatalf("liveness not persisted: %#v", loaded.Agent.Liveness)
	}
}

func TestResetAuthForEnrollmentClearsTokensAndRemoteOps(t *testing.T) {
	cfg := Default()
	cfg.Agent.GUID = "device-guid"
	cfg.Identity.PrivateKeyPKCS8B64 = "private-key"
	cfg.Tokens.AccessToken = "access-token"
	cfg.Tokens.RefreshToken = "refresh-token"
	cfg.Tokens.AccessExpiresAt = 123
	cfg.RemoteOps = RemoteOpsSection{
		Available:       true,
		SiteID:          1,
		WorkerGUID:      "worker-guid",
		RouteGeneration: 2,
		RoutePathPrefix: "/_borealis/site-workers/worker-guid",
		BaseURL:         "https://borealis.example.com/_borealis/site-workers/worker-guid",
		SocketURL:       "https://borealis.example.com/_borealis/site-workers/worker-guid/socket.io",
	}

	cfg.ResetAuthForEnrollment()

	if cfg.Agent.GUID != "device-guid" || cfg.Identity.PrivateKeyPKCS8B64 != "private-key" {
		t.Fatalf("identity state changed: %#v %#v", cfg.Agent, cfg.Identity)
	}
	if cfg.Tokens.AccessToken != "" || cfg.Tokens.RefreshToken != "" || cfg.Tokens.AccessExpiresAt != 0 {
		t.Fatalf("tokens not cleared: %#v", cfg.Tokens)
	}
	if cfg.RemoteOps.Available || cfg.RemoteOps.BaseURL != "" || cfg.RemoteOps.WorkerGUID != "" {
		t.Fatalf("remote ops not cleared: %#v", cfg.RemoteOps)
	}
}

func TestUpdateWithWriterRecordsStateMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	cfg := Default()
	if err := SaveWithWriter(path, "test:initial", &cfg); err != nil {
		t.Fatal(err)
	}
	first, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.Agent.State.Writer != "test:initial" {
		t.Fatalf("writer = %q", first.Agent.State.Writer)
	}
	if err := UpdateWithWriter(path, "test:update", func(cfg *AgentConfig) {
		cfg.Agent.Liveness.PID = 12
	}); err != nil {
		t.Fatal(err)
	}
	second, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if second.Agent.State.Writer != "test:update" {
		t.Fatalf("writer = %q", second.Agent.State.Writer)
	}
	if second.Agent.State.Revision <= first.Agent.State.Revision {
		t.Fatalf("revision did not advance: first=%d second=%d", first.Agent.State.Revision, second.Agent.State.Revision)
	}
}

func TestPruneStaleTempFilesRemovesOnlyOldAgentTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	oldConfigTemp := filepath.Join(dir, ".config-140525730.tmp")
	oldMetadataTemp := filepath.Join(dir, ".metadata-queue-140525730.tmp")
	freshConfigTemp := filepath.Join(dir, ".config-215131942.tmp")
	otherTemp := filepath.Join(dir, "config-140525730.tmp")
	lockPath := filepath.Join(dir, FileName+".lock")
	tempDir := filepath.Join(dir, ".config-dir.tmp")

	for _, filePath := range []string{oldConfigTemp, oldMetadataTemp, freshConfigTemp, otherTemp, lockPath} {
		if err := os.WriteFile(filePath, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(tempDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	for _, filePath := range []string{oldConfigTemp, oldMetadataTemp, otherTemp, lockPath, tempDir} {
		if err := os.Chtimes(filePath, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := PruneStaleTempFiles(path, time.Hour)
	if err != nil {
		t.Fatalf("PruneStaleTempFiles failed: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	for _, removedPath := range []string{oldConfigTemp, oldMetadataTemp} {
		if _, err := os.Stat(removedPath); !os.IsNotExist(err) {
			t.Fatalf("%s still exists or stat failed differently: %v", removedPath, err)
		}
	}
	for _, keptPath := range []string{freshConfigTemp, otherTemp, lockPath, tempDir} {
		if _, err := os.Stat(keptPath); err != nil {
			t.Fatalf("%s should remain: %v", keptPath, err)
		}
	}
}

func TestSavePreservesNewerLivenessFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	stale := Default()
	stale.ServerURL = "https://old.example"
	if err := Save(path, &stale); err != nil {
		t.Fatal(err)
	}
	if err := Update(path, func(cfg *AgentConfig) {
		cfg.Agent.Liveness.PID = 456
		cfg.Agent.Liveness.BootID = "boot-live"
		cfg.Agent.Liveness.LastLocalTickAt = 200
		cfg.Agent.Liveness.LastSocketState = "connected"
		cfg.Agent.Liveness.LastSocketStateAt = 300
		cfg.Agent.Liveness.LastHeartbeatSuccessAt = 400
	}); err != nil {
		t.Fatal(err)
	}

	stale.ServerURL = "https://new.example"
	stale.Agent.Liveness.LastSocketState = "disconnected"
	stale.Agent.Liveness.LastSocketStateAt = 100
	if err := Save(path, &stale); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ServerURL != "https://new.example" {
		t.Fatalf("server_url = %q", loaded.ServerURL)
	}
	if loaded.Agent.Liveness.LastSocketState != "connected" || loaded.Agent.Liveness.LastSocketStateAt != 300 {
		t.Fatalf("newer socket liveness was clobbered: %#v", loaded.Agent.Liveness)
	}
	if loaded.Agent.Liveness.PID != 456 || loaded.Agent.Liveness.BootID != "boot-live" || loaded.Agent.Liveness.LastLocalTickAt != 200 {
		t.Fatalf("newer process liveness was clobbered: %#v", loaded.Agent.Liveness)
	}
	if loaded.Agent.Liveness.LastHeartbeatSuccessAt != 400 {
		t.Fatalf("newer heartbeat liveness was clobbered: %#v", loaded.Agent.Liveness)
	}
}

func TestDependencyStateNormalizesAndPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	cfg := Default()
	cfg.UpdateDependencyState("UltraVNC", func(state *DependencyStateSection) {
		state.Phase = "INSTALLING"
		state.Status = "Recovering"
		state.DesiredVersion = " 1.8.2.1 "
		state.Detail = " installing "
	})
	if err := Save(path, &cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	state, ok := loaded.Agent.DependencyState["ultravnc"]
	if !ok {
		t.Fatalf("dependency state missing: %#v", loaded.Agent.DependencyState)
	}
	if state.Phase != "installing" || state.Status != "recovering" || state.DesiredVersion != "1.8.2.1" || state.Detail != "installing" {
		t.Fatalf("dependency state not normalized: %#v", state)
	}
}

func TestSavePreservesNewerDependencyStateFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	stale := Default()
	stale.UpdateDependencyState("wireguard", func(state *DependencyStateSection) {
		state.Phase = "detected"
		state.Status = "recovering"
		state.LastAttemptAt = 10
	})
	if err := Save(path, &stale); err != nil {
		t.Fatal(err)
	}
	if err := Update(path, func(cfg *AgentConfig) {
		cfg.UpdateDependencyState("wireguard", func(state *DependencyStateSection) {
			state.Phase = "healthy"
			state.Status = "healthy"
			state.LastSuccessAt = 20
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, &stale); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	state := loaded.Agent.DependencyState["wireguard"]
	if state.Phase != "healthy" || state.LastSuccessAt != 20 {
		t.Fatalf("newer dependency state was clobbered: %#v", state)
	}
}

func TestMetadataQueueNormalizesPersistsOutsideAgentJSONAndAcks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)

	if err := QueueMetadataField(path, 1, "asset-tag-123", "cli"); err != nil {
		t.Fatalf("QueueMetadataField failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("metadata queue write should not create agent.json, stat err=%v", err)
	}
	fields, err := LoadQueuedMetadataFields(path)
	if err != nil {
		t.Fatal(err)
	}
	field, ok := fields["field_001"]
	if !ok {
		t.Fatalf("metadata field missing: %#v", fields)
	}
	if field.Value != "YXNzZXQtdGFnLTEyMw==" || DecodeMetadataFieldValue(field.Value) != "asset-tag-123" || field.ModifiedAt <= 0 || field.Source != "cli" {
		t.Fatalf("metadata field not normalized: %#v", field)
	}

	if err := QueueMetadataField(path, 1, "", "cli"); err != nil {
		t.Fatalf("clear QueueMetadataField failed: %v", err)
	}
	fields, err = LoadQueuedMetadataFields(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["field_001"]; !ok {
		t.Fatalf("blank clear marker should remain until ack: %#v", fields)
	}
	if fields["field_001"].Value != "" {
		t.Fatalf("clear marker value = %q", fields["field_001"].Value)
	}

	if err := AckQueuedMetadataFields(path, []string{"field_001"}); err != nil {
		t.Fatalf("AckQueuedMetadataFields failed: %v", err)
	}
	fields, err = LoadQueuedMetadataFields(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 0 {
		t.Fatalf("acked metadata field survived: %#v", fields)
	}
	queuePath, err := MetadataQueuePath(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(queuePath); !os.IsNotExist(err) {
		t.Fatalf("empty metadata queue should be removed, stat err=%v", err)
	}
}

func TestSaveDropsLegacyAgentJSONMetadataFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	raw := `{
  "schema_version": 1,
  "server_url": "https://borealis.example.com",
  "agent": {
    "metadata_fields": {
      "field_001": {"value":"b2xk","modified_at":10,"source":"legacy"}
    }
  },
  "identity": {},
  "tokens": {},
  "trust": {}
}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(path, &loaded); err != nil {
		t.Fatal(err)
	}
	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rewritten), "metadata_fields") {
		t.Fatalf("legacy metadata_fields survived rewrite: %s", string(rewritten))
	}
}

func TestDefaultBranch(t *testing.T) {
	cfg := Default()
	cfg.ApplyDefaults()
	if cfg.Agent.Branch != DefaultBranch {
		t.Fatalf("default branch = %q, want %q", cfg.Agent.Branch, DefaultBranch)
	}
	if cfg.Agent.ReleaseChannel != ReleaseChannelStable {
		t.Fatalf("default release channel = %q, want %q", cfg.Agent.ReleaseChannel, ReleaseChannelStable)
	}
}

func TestEmptyUpdateSectionDoesNotDefaultToStableMain(t *testing.T) {
	cfg := Default()
	cfg.ApplyDefaults()
	if cfg.Agent.Update.PreviousChannel != "" || cfg.Agent.Update.PreviousBranch != "" || cfg.Agent.Update.TargetChannel != "" || cfg.Agent.Update.TargetBranch != "" {
		t.Fatalf("empty update section defaulted channel/branch: %#v", cfg.Agent.Update)
	}
}

func TestUpdateSectionNormalizesPresentChannelsAndBranches(t *testing.T) {
	cfg := Default()
	cfg.Agent.Update = AgentUpdateSection{
		OperationID:     " operation-1 ",
		Kind:            " switch_branch_channel ",
		Status:          " SUCCESS ",
		PreviousChannel: "release",
		PreviousBranch:  "feature/old",
		TargetChannel:   "source",
		TargetBranch:    " feature/new ",
		LastError:       " done ",
	}
	cfg.ApplyDefaults()
	if cfg.Agent.Update.OperationID != "operation-1" || cfg.Agent.Update.Kind != "switch_branch_channel" || cfg.Agent.Update.Status != "success" {
		t.Fatalf("update metadata not normalized: %#v", cfg.Agent.Update)
	}
	if cfg.Agent.Update.PreviousChannel != ReleaseChannelStable || cfg.Agent.Update.PreviousBranch != DefaultBranch {
		t.Fatalf("previous stable channel not normalized to main: %#v", cfg.Agent.Update)
	}
	if cfg.Agent.Update.TargetChannel != ReleaseChannelUnstable || cfg.Agent.Update.TargetBranch != "feature/new" {
		t.Fatalf("target unstable branch not normalized: %#v", cfg.Agent.Update)
	}
	if cfg.Agent.Update.LastError != "done" {
		t.Fatalf("last error not trimmed: %q", cfg.Agent.Update.LastError)
	}
}

func TestLoadToleratesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	raw := `{
  "schema_version": 1,
  "server_url": "https://borealis.example.com",
  "agent": {},
  "identity": {},
  "tokens": {},
  "trust": {},
  "runtime": {"feature_flags": {"system_scripts": true}}
}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.ServerURL != "https://borealis.example.com" {
		t.Fatalf("server_url = %q", loaded.ServerURL)
	}
}

func TestUnixPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions test")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	cfg := Default()
	if err := Save(path, &cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %o, want 600", got)
	}
	parent, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := parent.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode = %o, want 700", got)
	}
}
