package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnrollmentBase64InputPreservesEncodingAndRejectsMalformedValues(t *testing.T) {
	const valid = "MCowBQYDK2VwAyEAZ7y25wZ806ry8XZHh0bRAdSR2tOnfsxQGgQOmMHEQpI="
	for _, field := range []string{"agent_pubkey", "client_nonce"} {
		for _, tc := range []struct {
			name  string
			value string
			valid bool
		}{
			{"attribute_like_suffix", valid, true},
			{"legacy_whitespace", " \n" + valid + "\t", true},
			{"legacy_url_alphabet", "-___", true},
			{"malformed", "%%%", false},
			{"markup", "<script>alert(1)</script>", false},
			{"oversized", strings.Repeat("A", maxInputPlainTextLength+4), false},
			{"nul", valid + "\x00", false},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				raw, err := json.Marshal(map[string]string{field: tc.value})
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, "/api/agent/enroll/request", strings.NewReader(string(raw)))
				body, err := readJSONMap(req)
				if (err == nil) != tc.valid {
					t.Fatalf("valid=%t err=%v", tc.valid, err)
				}
				if tc.valid && body[field] != tc.value {
					t.Fatal("encoded binary input changed")
				}
			})
		}
	}
	if errs := sanitizeJSONInputMap(map[string]any{"name": "<img onload=alert(1)>"}); len(errs) == 0 {
		t.Fatal("plain text executable markup accepted")
	}
}

func TestPublicInputValidationRejectsUnsafeQuery(t *testing.T) {
	handler := withPublicInputValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/devices/search?hostname=%3Cscript%3Ealert(1)%3C%2Fscript%3E", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "validation_failed") {
		t.Fatalf("expected validation payload, got %s", rec.Body.String())
	}
}

func TestPublicInputValidationSkipsInternalRoutes(t *testing.T) {
	handler := withPublicInputValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/internal/job-scheduler/test?hostname=%3Cscript%3E", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected internal route bypass, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPublicInputValidationRejectsUnsafePath(t *testing.T) {
	handler := withPublicInputValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/devices/%3Cscript%3E", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected unsafe path to fail, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestReadJSONMapWithLimitSanitizesPlainText(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader("{\"name\":\"  Alpha\\nBeta  \",\"content\":\"if (1 < 2) {\\n  echo ok\\n}\"}"))
	body, err := readJSONMapWithLimit(req, 1<<20)
	if err != nil {
		t.Fatalf("expected payload, got %v", err)
	}
	if got := cleanText(body["name"]); got != "Alpha Beta" {
		t.Fatalf("expected single-line name sanitation, got %q", got)
	}
	if got := body["content"].(string); !strings.Contains(got, "1 < 2") || !strings.Contains(got, "\n") {
		t.Fatalf("expected code content preserved, got %q", got)
	}
}

func TestReadJSONMapWithLimitRejectsInvalidRegex(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(`{"regex":"["}`))
	_, err := readJSONMapWithLimit(req, 1<<20)
	if err == nil {
		t.Fatal("expected validation error")
	}
	errs, ok := asPublicValidationErrors(err)
	if !ok || len(errs) == 0 || errs[0].Field != "regex" {
		t.Fatalf("expected regex validation errors, got %#v ok=%v", errs, ok)
	}
}

func TestPasskeyVerifyPayloadUsesTransportValidation(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/passkeys/authenticate/verify", strings.NewReader(`{
		"request_id":"pending.token.value",
		"credential":{
			"id":"abc+/=",
			"rawId":"abc+/=",
			"type":"public-key",
			"response":{
				"clientDataJSON":"abc+/=",
				"authenticatorData":"abc+/=",
				"signature":"abc+/="
			}
		}
	}`))
	rec := httptest.NewRecorder()
	_, body, ok := readPasskeyJSONRaw(rec, req)
	if !ok {
		t.Fatalf("expected passkey payload to pass transport validation, status=%d body=%s", rec.Code, rec.Body.String())
	}
	credential, ok := body["credential"].(map[string]any)
	if !ok || credential["id"] != "abc+/=" {
		t.Fatalf("expected opaque credential id preserved, got %#v", body["credential"])
	}
}

func TestHostValidationRejectsSlashHostname(t *testing.T) {
	if err := validateHostInput("hostname", "borealis/evil"); err == nil {
		t.Fatal("expected slash hostname to fail")
	}
	if err := validateHostInput("hostname", "fd00::1/64"); err != nil {
		t.Fatalf("expected IPv6 CIDR to pass, got %v", err)
	}
}

func TestSanitizeNotificationTextPreservesOnlyBoldMarkup(t *testing.T) {
	got := sanitizeNotificationText("Hello <b>World</b> <script>alert(1)</script>")
	if !strings.Contains(got, "<b>World</b>") {
		t.Fatalf("expected bold markup preserved, got %q", got)
	}
	if strings.Contains(strings.ToLower(got), "<script") {
		t.Fatalf("expected script tag removed, got %q", got)
	}
}
