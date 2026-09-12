package clusterbootstrap

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func sourceRuntimeFixture() map[string]any {
	_, settings := preparationFixture()
	data := map[string]any{}
	for k, v := range settings {
		data[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	data["BOREALIS_REPO_ROOT"] = base64.StdEncoding.EncodeToString([]byte("/untrusted/path"))
	data["BOREALIS_JWT_SECRET"] = base64.StdEncoding.EncodeToString([]byte("synthetic-private-identity"))
	return map[string]any{"apiVersion": "v1", "kind": "Secret", "type": "Opaque", "metadata": map[string]any{"name": "borealis-api-backend-runtime-env", "namespace": "borealis", "uid": "44444444-4444-4444-8444-444444444444", "resourceVersion": "100"}, "data": data}
}

func TestPreparationRuntimeSourceRetainsPrivateReceiptAndLiteralSettings(t *testing.T) {
	object := sourceRuntimeFixture()
	raw, _ := json.Marshal(object)
	source, err := ParsePreparationRuntimeSecret(raw)
	if err != nil {
		t.Fatal(err)
	}
	expected, settings := preparationFixture()
	selected := source.Settings()
	if len(selected) != len(settings) || selected["POSTGRES_PASSWORD"] != settings["POSTGRES_PASSWORD"] {
		t.Fatal("literal setting lost or unknown setting inherited")
	}
	if _, err := NewPreparationConfiguration(expected, selected); err != nil {
		t.Fatal(err)
	}
	selected["POSTGRES_PASSWORD"] = "changed"
	if source.Settings()["POSTGRES_PASSWORD"] != settings["POSTGRES_PASSWORD"] {
		t.Fatal("source settings alias")
	}
	if _, err := json.Marshal(source); err == nil || strings.Contains(fmt.Sprintf("%v %#v", source, source), "private-test") {
		t.Fatal("private source serializes")
	}
	copy, err := ParsePreparationRuntimeSecret(raw)
	if err != nil || !source.SameObservation(copy) {
		t.Fatal("unchanged source rejected")
	}
	for _, mode := range []string{"uid", "resourceVersion", "excluded setting", "selected setting"} {
		object := sourceRuntimeFixture()
		if mode == "uid" || mode == "resourceVersion" {
			object["metadata"].(map[string]any)[mode] = "55555555-5555-4555-8555-555555555555"
		} else {
			key := "BOREALIS_REPO_ROOT"
			if mode == "selected setting" {
				key = "POSTGRES_PASSWORD"
			}
			object["data"].(map[string]any)[key] = base64.StdEncoding.EncodeToString([]byte("changed"))
		}
		changed, _ := json.Marshal(object)
		other, err := ParsePreparationRuntimeSecret(changed)
		if err != nil || source.SameObservation(other) {
			t.Fatalf("receipt failed to detect %s", mode)
		}
	}
}

func TestPreparationRuntimeSourceRejectsWrongObjectAndMalformedData(t *testing.T) {
	for _, mode := range []string{"wrong name", "wrong namespace", "zero uid", "missing revision", "deleted", "wrong type", "stringData", "null data", "null secret", "bad base64", "newline base64", "non UTF8"} {
		t.Run(mode, func(t *testing.T) {
			object := sourceRuntimeFixture()
			meta := object["metadata"].(map[string]any)
			data := object["data"].(map[string]any)
			switch mode {
			case "wrong name":
				meta["name"] = "another-secret"
			case "wrong namespace":
				meta["namespace"] = "default"
			case "zero uid":
				meta["uid"] = "00000000-0000-0000-0000-000000000000"
			case "missing revision":
				delete(meta, "resourceVersion")
			case "deleted":
				meta["deletionTimestamp"] = "2026-09-10T00:00:00Z"
			case "wrong type":
				object["type"] = "kubernetes.io/tls"
			case "stringData":
				object["stringData"] = map[string]string{}
			case "null data":
				object["data"] = nil
			case "null secret":
				data["POSTGRES_PASSWORD"] = nil
			case "bad base64":
				data["POSTGRES_PASSWORD"] = "abc!"
			case "newline base64":
				data["POSTGRES_PASSWORD"] = "YQ==\n"
			case "non UTF8":
				data["POSTGRES_PASSWORD"] = "/w=="
			}
			raw, _ := json.Marshal(object)
			if _, err := ParsePreparationRuntimeSecret(raw); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
	raw, _ := json.Marshal(sourceRuntimeFixture())
	bad := bytes.Replace(raw, []byte(`"POSTGRES_PASSWORD":`), []byte(`"postgres_password":"Yg==","POSTGRES_PASSWORD":`), 1)
	if _, err := ParsePreparationRuntimeSecret(bad); err == nil {
		t.Fatal("case alias accepted")
	}
}
