package clusterbootstrap

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPreparationRuntimeSourcePrivateOperatorURL(t *testing.T) {
	base, _ := json.Marshal(sourceRuntimeFixture())
	baseline, err := ParsePreparationRuntimeSecret(base)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(PreparationRuntimeKeys(), "BOREALIS_OPERATOR_BASE_URL") {
		t.Fatal("private URL became bootstrap setting")
	}
	for _, mode := range []string{"valid", "absent", "null", "number", "bad base64", "newline", "non UTF8", "empty", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			object := sourceRuntimeFixture()
			data := object["data"].(map[string]any)
			url := " http://borealis-operator.borealis.svc:8050/ "
			data["BOREALIS_OPERATOR_BASE_URL"] = base64.StdEncoding.EncodeToString([]byte(url))
			switch mode {
			case "absent":
				delete(data, "BOREALIS_OPERATOR_BASE_URL")
			case "null":
				data["BOREALIS_OPERATOR_BASE_URL"] = nil
			case "number":
				data["BOREALIS_OPERATOR_BASE_URL"] = 7
			case "bad base64":
				data["BOREALIS_OPERATOR_BASE_URL"] = "!"
			case "newline":
				data["BOREALIS_OPERATOR_BASE_URL"] = "YQ==\n"
			case "non UTF8":
				data["BOREALIS_OPERATOR_BASE_URL"] = "/w=="
			case "empty":
				data["BOREALIS_OPERATOR_BASE_URL"] = ""
			case "oversize":
				data["BOREALIS_OPERATOR_BASE_URL"] = strings.Repeat("YQ==", MaxPreparationConfigBytes)
			}
			raw, _ := json.Marshal(object)
			secret, err := ParsePreparationRuntimeSecret(raw)
			if err != nil || !reflect.DeepEqual(secret.Settings(), baseline.Settings()) {
				t.Fatal("optional URL changed original parse/settings contract", err)
			}
			got, err := secret.OperatorBaseURL()
			if (err == nil) != (mode == "valid") || (err == nil && got != url) {
				t.Fatal("wrong private extraction outcome")
			}
			if baseline.SameObservation(secret) != (mode == "absent") {
				t.Fatal("full Secret receipt ignored optional data")
			}
			if _, err := json.Marshal(secret); err == nil {
				t.Fatal("private URL serialized")
			}
			// Extraction owns bytes; neither caller buffer nor returned settings alias it.
			for i := range raw {
				raw[i] = 0
			}
			if mode == "valid" {
				if again, err := secret.OperatorBaseURL(); err != nil || again != url {
					t.Fatal("URL aliases caller bytes")
				}
			}
		})
	}
	if _, err := (PreparationRuntimeSecret{}).OperatorBaseURL(); err == nil {
		t.Fatal("zero source accepted")
	}
}

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
