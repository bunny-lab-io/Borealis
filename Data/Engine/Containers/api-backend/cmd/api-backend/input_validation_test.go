package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
