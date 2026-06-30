package registrymanagement

import (
	"context"
	"runtime"
	"testing"
)

func TestParseRegistryPathNormalizesHiveAliases(t *testing.T) {
	parsed, err := parseRegistryPath(`HKEY_LOCAL_MACHINE\SOFTWARE\\Microsoft`)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Hive != "HKLM" || parsed.SubPath != `SOFTWARE\Microsoft` || parsed.Path != `HKLM\SOFTWARE\Microsoft` {
		t.Fatalf("unexpected path %#v", parsed)
	}
}

func TestParseRegistryPathRejectsInvalidHive(t *testing.T) {
	_, err := parseRegistryPath(`HKZZ\Software`)
	if err == nil {
		t.Fatal("expected invalid hive error")
	}
	regErr := normalizeError(err)
	if regErr.Code != "invalid_hive" {
		t.Fatalf("error = %#v", regErr)
	}
}

func TestValidateRegistryChildNameRejectsSeparators(t *testing.T) {
	if _, err := validateRegistryChildName(`bad\name`); err == nil {
		t.Fatal("expected separator rejection")
	}
	if _, err := validateRegistryChildName("GoodName"); err != nil {
		t.Fatalf("valid name rejected: %v", err)
	}
}

func TestNormalizeRegistryValueType(t *testing.T) {
	cases := map[string]string{
		"sz":        "REG_SZ",
		"expand-sz": "REG_EXPAND_SZ",
		"multi sz":  "REG_MULTI_SZ",
		"dword":     "REG_DWORD",
		"qword":     "REG_QWORD",
		"binary":    "REG_BINARY",
	}
	for input, want := range cases {
		if got := normalizeRegistryValueType(input); got != want {
			t.Fatalf("type %q = %q want %q", input, got, want)
		}
	}
	if got := normalizeRegistryValueType("resource_list"); got != "" {
		t.Fatalf("unsupported type normalized to %q", got)
	}
}

func TestParseBinaryDataAcceptsHexAndBase64(t *testing.T) {
	fromHex, err := parseBinaryData("DE AD BE EF")
	if err != nil {
		t.Fatal(err)
	}
	if formatBinaryData(fromHex) != "DE AD BE EF" {
		t.Fatalf("hex roundtrip = %q", formatBinaryData(fromHex))
	}
	fromBase64, err := parseBinaryData("base64:3q2+7w==")
	if err != nil {
		t.Fatal(err)
	}
	if formatBinaryData(fromBase64) != "DE AD BE EF" {
		t.Fatalf("base64 roundtrip = %q", formatBinaryData(fromBase64))
	}
}

func TestHandleRequestRejectsOtherHost(t *testing.T) {
	manager := New("HOST-ONE", "system")
	response, err := manager.HandleRequest(context.Background(), map[string]any{
		"action":   "roots",
		"hostname": "HOST-TWO",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := response.(map[string]any)
	if payload["ok"] != false || payload["error"] != "not_for_host" {
		t.Fatalf("response = %#v", payload)
	}
}

func TestHandleRequestReportsUnsupportedOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unsupported platform response is for non-Windows agents")
	}
	manager := New("HOST-ONE", "system")
	response, err := manager.HandleRequest(context.Background(), map[string]any{
		"action":   "roots",
		"hostname": "HOST-ONE",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := response.(map[string]any)
	if payload["ok"] != false || payload["error"] != "unsupported_platform" {
		t.Fatalf("response = %#v", payload)
	}
	health := manager.Health()
	if health.Status != "unsupported" || health.StatusCode != "unsupported" {
		t.Fatalf("health = %#v", health)
	}
}
