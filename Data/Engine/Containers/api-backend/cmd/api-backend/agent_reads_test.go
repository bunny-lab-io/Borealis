package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type agentReadTestStore struct {
	deviceAuthRecord deviceBearerAuthRecord
	deviceAuthFound  bool
	requiredVersion  *int
	metadataGUID     string
	metadataField    int
	metadataPayload  map[string]any
	metadataStatus   int
	metadataErr      error
}

func (s *agentReadTestStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	return operatorProfile{Username: username, Role: fallbackRole}, nil
}

func (s *agentReadTestStore) requiredDeviceTokenVersion(_ context.Context, _ string) (*int, error) {
	return s.requiredVersion, nil
}

func (s *agentReadTestStore) deviceBearerAuthRecord(_ context.Context, _ string) (deviceBearerAuthRecord, bool, error) {
	if !s.deviceAuthFound {
		return deviceBearerAuthRecord{}, false, nil
	}
	return s.deviceAuthRecord, true, nil
}

func (s *agentReadTestStore) agentMetadataField(_ context.Context, guid string, fieldNumber int) (map[string]any, int, error) {
	s.metadataGUID = guid
	s.metadataField = fieldNumber
	if s.metadataErr != nil {
		status := s.metadataStatus
		if status == 0 {
			status = http.StatusInternalServerError
		}
		return nil, status, s.metadataErr
	}
	status := s.metadataStatus
	if status == 0 {
		status = http.StatusOK
	}
	return copyMap(s.metadataPayload), status, nil
}

func TestAgentMetadataFieldHandlerReturnsAuthenticatedField(t *testing.T) {
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	signer := testAgentJWTSigner(t)
	signer.now = func() time.Time { return time.Unix(1700000000, 0) }
	token, err := signer.issueAccessToken(guid, "fingerprint", 4, agentAccessTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	store := &agentReadTestStore{
		deviceAuthFound: true,
		deviceAuthRecord: deviceBearerAuthRecord{
			GUID:         guid,
			Fingerprint:  "fingerprint",
			TokenVersion: 4,
			Status:       "active",
		},
		metadataPayload: map[string]any{
			"field": map[string]any{
				"field_number": 9,
				"field_key":    "field_009",
				"label":        "Field 009",
				"value":        "rack-a-42",
				"modified_at":  1234,
				"source":       "engine",
				"actor":        "admin",
				"has_value":    true,
			},
		},
	}
	auth := &authService{store: store, timeout: time.Second}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/agent/metadata/9", nil)
	request.SetPathValue("field_number", "9")
	request.Header.Set("Authorization", "Bearer "+token)

	agentMetadataFieldHandler(auth, signer, &dpopVerifier{seenJTI: map[string]time.Time{}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.metadataGUID != guid || store.metadataField != 9 {
		t.Fatalf("unexpected metadata lookup guid=%s field=%d", store.metadataGUID, store.metadataField)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	field := payload["field"].(map[string]any)
	if field["field_key"] != "field_009" || field["value"] != "rack-a-42" {
		t.Fatalf("unexpected field payload %+v", field)
	}
}

func TestAgentSoftwareOverridesHandlerReturnsIconOverrides(t *testing.T) {
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	signer := testAgentJWTSigner(t)
	signer.now = func() time.Time { return time.Unix(1700000000, 0) }
	token, err := signer.issueAccessToken(guid, "fingerprint", 4, agentAccessTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	store := &agentReadTestStore{
		deviceAuthFound: true,
		deviceAuthRecord: deviceBearerAuthRecord{
			GUID:         guid,
			Fingerprint:  "fingerprint",
			TokenVersion: 4,
			Status:       "active",
		},
	}
	tempPath := filepath.Join(t.TempDir(), "software_icons_overrides.json")
	payload := `{"windows_icon_overrides":[{"rule_id":"icon_override_contoso","name":"Contoso Agent","display_icon":"C:\\Program Files\\Contoso\\agent.ico"}]}`
	if err := os.WriteFile(tempPath, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOREALIS_SOFTWARE_ICON_OVERRIDES_PATH", tempPath)
	auth := &authService{store: store, timeout: time.Second}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/agent/software-management/overrides", nil)
	request.Header.Set("Authorization", "Bearer "+token)

	agentSoftwareManagementOverridesHandler(auth, signer, &dpopVerifier{seenJTI: map[string]time.Time{}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	rows := response["windows_icon_overrides"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected one override, got %+v", response)
	}
	row := rows[0].(map[string]any)
	if row["name"] != "Contoso Agent" || !strings.Contains(row["display_icon"].(string), "agent.ico") {
		t.Fatalf("unexpected override row %+v", row)
	}
}

func TestDecodeMetadataValueFallsBackToPlainText(t *testing.T) {
	if decodeMetadataValue(base64.StdEncoding.EncodeToString([]byte("rack-a-42"))) != "rack-a-42" {
		t.Fatal("expected base64 metadata value decoded")
	}
	if decodeMetadataValue("plain-text") != "plain-text" {
		t.Fatal("expected non-base64 metadata value preserved")
	}
}
