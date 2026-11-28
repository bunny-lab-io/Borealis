package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func policyEnforcementIntentFixture() patchPolicyEnforcementIntent {
	v := patchPolicyEnforcementIntent{Version: 1, PolicyRunID: 7, PolicyID: 3, ScheduledTS: 1770000000, DeviceGUID: "3C7A3EAC-0B2B-4D1B-AC8C-92EB8C145A41", Hostname: "policy-host", AgentID: "original-system-agent", SiteID: 2, Mode: "managed", ScopeSHA256: strings.Repeat("a", 64)}
	v.RequestID = fmt.Sprintf("patch-policy-run-%d-device-%s-site-%d", v.PolicyRunID, v.DeviceGUID, v.SiteID)
	return v
}

func TestPatchPolicyEnforcementReceiptPythonParity(t *testing.T) {
	controls := ""
	for i := 0; i < 32; i++ {
		controls += string(rune(i))
	}
	for _, test := range []struct{ name, status, out, err, digest string }{
		{"ascii", "Success", "", "", "035e7109f54fba4993a3b3b463f7f2aa9967d8987fb0b16e54e9815c228a6913"},
		{"unicode", "Failed", "café 雪 😀 <>&\u2028\u2029", "line\n\t\x00\\\"", "2f372cd428193f6118474ce69cecd42fd296cbc581f7ffc3354df99927401158"},
		{"all controls", "Skipped", controls, "/", "095a5ed00fe3921a4a949df90bde867416c67c443243a87cc800e422c2807ac2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := patchPolicyReceiptDigest(17, test.status, test.out, test.err, strings.Repeat("a", 32))
			if err != nil || got != test.digest {
				t.Fatalf("Python receipt mismatch digest=%s err=%v", got, err)
			}
		})
	}
	for _, mode := range []string{"pending", "acknowledged", "wire status", "bad token", "invalid UTF8", "oversize", "canonical envelope oversize", "fractional bound"} {
		t.Run(mode, func(t *testing.T) {
			status, out, token, id := "Success", "", strings.Repeat("a", 32), int64(17)
			switch mode {
			case "pending":
				status = "Running"
			case "acknowledged":
				status = "acknowledged"
			case "wire status":
				status = "Completed"
			case "bad token":
				token = "x"
			case "invalid UTF8":
				out = string([]byte{255})
			case "oversize":
				out = strings.Repeat("x", 256*1024+1)
			case "canonical envelope oversize":
				out = strings.Repeat("\x00", 256*1024)
			case "fractional bound":
				id = 1 << 53
			}
			if _, err := patchPolicyReceiptDigest(id, status, out, "", token); err == nil {
				t.Fatal("unproven terminal accepted")
			}
		})
	}
}

func policyEnforcementReceiptFixture(t *testing.T) (patchPolicyEnforcementIntent, patchPolicyEnforcementDispatch, map[string]any) {
	t.Helper()
	intent := policyEnforcementIntentFixture()
	operation := schedulerWorkerActivityFixture()
	operation["job_id"] = int64(17)
	operation["site_id"] = intent.SiteID
	operation["agent_guid"] = intent.DeviceGUID
	operation["kind"] = "patch_policy_enforcement_request"
	operation["grant_nonce"] = strings.Repeat("b", 32)
	digest, err := patchPolicyReceiptDigest(17, "Success", "", "", cleanText(operation["token"]))
	if err != nil {
		t.Fatal(err)
	}
	operation["receipt_sha256"] = digest
	dispatch := patchPolicyEnforcementDispatch{WorkerGUID: cleanText(operation["worker_guid"]), RouteGeneration: coerceInt64(operation["route_generation"]), PodUID: cleanText(operation["pod_uid"]), NodeName: cleanText(operation["node_name"])}
	return intent, dispatch, operation
}

func TestPatchPolicyEnforcementCommittedReceipt(t *testing.T) {
	for _, mode := range []string{"exact", "wrong kind", "wrong GUID", "wrong site", "wrong worker", "wrong route", "wrong pod", "wrong node", "missing nonce", "missing digest", "changed stdout", "changed stderr", "changed status", "other activity", "duplicate metadata"} {
		t.Run(mode, func(t *testing.T) {
			intent, dispatch, operation := policyEnforcementReceiptFixture(t)
			status, out, errText, id := "Success", "", "", int64(17)
			switch mode {
			case "wrong kind":
				operation["kind"] = "rdp_start"
			case "wrong GUID":
				operation["agent_guid"] = "E51E33F3-E217-4423-9158-530C3D74BD77"
			case "wrong site":
				operation["site_id"] = int64(99)
			case "wrong worker":
				operation["worker_guid"] = "e51e33f3-e217-4423-9158-530c3d74bd77"
			case "wrong route":
				operation["route_generation"] = int64(99)
			case "wrong pod":
				operation["pod_uid"] = "e51e33f3-e217-4423-9158-530c3d74bd77"
			case "wrong node":
				operation["node_name"] = "other-node"
			case "missing nonce":
				delete(operation, "grant_nonce")
			case "missing digest":
				delete(operation, "receipt_sha256")
			case "changed stdout":
				out = "changed"
			case "changed stderr":
				errText = "changed"
			case "changed status":
				status = "Failed"
			case "other activity":
				id = 18
			}
			raw := mustJSON(map[string]any{schedulerWorkerOperationKey: operation})
			if mode == "duplicate metadata" {
				raw = `{"worker_operation":{},"worker_operation":` + mustJSON(operation) + `}`
			}
			_, err := patchPolicyCommittedReceipt(intent, dispatch, id, status, out, errText, raw)
			if (err == nil) != (mode == "exact") {
				t.Fatalf("receipt guard %s err=%v", mode, err)
			}
		})
	}
}

func TestPatchPolicyEnforcementOriginalActivity(t *testing.T) {
	for _, mode := range []string{"exact", "request", "mode", "GUID", "execution", "work", "holder", "generation", "missing intent"} {
		t.Run(mode, func(t *testing.T) {
			intent := policyEnforcementIntentFixture()
			retained := intent
			record := schedulerExecutionRecord{ID: "work:31:work", Holder: "original-owner", Generation: 4}
			metadata := map[string]any{"scheduler_execution_id": record.ID, "scheduler_work_id": int64(31), "scheduler_holder": record.Holder, "scheduler_generation": record.Generation}
			switch mode {
			case "request":
				retained.RequestID = "other"
			case "mode":
				retained.Mode = "frozen"
			case "GUID":
				retained.DeviceGUID = "E51E33F3-E217-4423-9158-530C3D74BD77"
			case "execution":
				metadata["scheduler_execution_id"] = "work:32:work"
			case "work":
				metadata["scheduler_work_id"] = int64(32)
			case "holder":
				metadata["scheduler_holder"] = "later-owner"
			case "generation":
				metadata["scheduler_generation"] = int64(5)
			}
			if mode != "missing intent" {
				metadata[patchPolicyEnforcementKey] = retained
			}
			if got := patchPolicyActivityBound(mustJSON(metadata), intent, 31, record); got != (mode == "exact") {
				t.Fatal("original activity association bypassed")
			}
		})
	}
}

func TestPatchPolicyEnforcementScopeAndIntent(t *testing.T) {
	for _, mode := range []string{"exact", "resolver GUID normalization", "managed to frozen", "new hostname", "new agent", "new site", "changed policy binding", "conflict", "missing target", "unknown field", "request substitution"} {
		t.Run(mode, func(t *testing.T) {
			intent := policyEnforcementIntentFixture()
			row := patchPolicyRow{ID: sql.NullInt64{Int64: 3, Valid: true}, ManagedUpdateMode: sql.NullInt64{Int64: 1, Valid: true}}
			target := patchPolicyDevice{DeviceGUID: intent.DeviceGUID}
			proof := patchPolicyScopeProof{Bindings: map[string]string{patchPolicyScopeDeviceKey(intent.PolicyID, intent.DeviceGUID): intent.ScopeSHA256}, Devices: map[string]map[string]any{intent.DeviceGUID: {"hostname": intent.Hostname, "agent_id": intent.AgentID, "site_id": intent.SiteID}}, Scopes: map[int64]*patchPolicyEffectiveScope{3: {Policy: row, Devices: []patchPolicyDevice{target}}}}
			switch mode {
			case "resolver GUID normalization":
				proof.Scopes[3].Devices[0].DeviceGUID = strings.ToLower(intent.DeviceGUID)
			case "managed to frozen":
				intent.Mode = "frozen"
			case "new hostname":
				intent.Hostname = "other"
			case "new agent":
				intent.AgentID = "other"
			case "new site":
				intent.SiteID = 99
			case "changed policy binding":
				intent.ScopeSHA256 = strings.Repeat("b", 64)
			case "conflict":
				proof.Scopes[3].Devices[0].Conflict = true
			case "missing target":
				proof.Scopes[3].Devices = nil
			case "request substitution":
				intent.RequestID = "other"
			}
			if mode == "unknown field" {
				var value map[string]any
				_ = json.Unmarshal([]byte(stringIntent(intent)), &value)
				value["retry"] = true
				if _, err := patchPolicyIntentFromPayload(map[string]any{patchPolicyEnforcementKey: value}); err == nil {
					t.Fatal("unknown intent accepted")
				}
				return
			}
			if got := proof.matches(intent); got != (mode == "exact" || mode == "resolver GUID normalization") {
				t.Fatal("scope substitution accepted")
			}
		})
	}
}

func TestPatchPolicyEnforcementStrictFilterCache(t *testing.T) {
	cache := newPatchPolicyResolutionCache(&postgresOperatorStore{}, operatorProfile{Role: "Admin"})
	cache.strictInputs = true
	cache.devices = []map[string]any{}
	cache.filters = map[int64]map[string]any{7: {"name": "retained"}}
	cache.loadedFilterIDs[7] = true
	if _, err := cache.loadFilters(context.Background(), []int64{7}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.loadFilters(context.Background(), []int64{8}); err == nil {
		t.Fatal("missing filter reached permissive reader")
	}
}

func TestPatchPolicyEnforcementClockFence(t *testing.T) {
	now := time.Unix(1300, 0)
	for _, field := range []string{"status", "last_seen_age_minutes"} {
		t.Run(field, func(t *testing.T) {
			condition := map[string]any{"field": field, "operator": "equals", "value": "Online"}
			if field == "last_seen_age_minutes" {
				condition["operator"] = "less_than_or_equal"
				condition["value"] = float64(5)
			}
			proof := patchPolicyScopeProof{ClockChecks: []patchPolicyClockCheck{{LastSeen: 1000, Condition: condition, Matched: true}}}
			if !proof.clockMatches(now) {
				t.Fatal("original clock predicate rejected")
			}
			if proof.clockMatches(now.Add(time.Second)) {
				t.Fatal("elapsed clock boundary accepted without scope revalidation")
			}
		})
	}
	if !(&patchPolicyScopeProof{}).clockMatches(now.Add(24 * time.Hour)) {
		t.Fatal("unconsumed clock invalidated static policy")
	}
}

func TestPatchPolicyEnforcementConsumedJSON(t *testing.T) {
	for _, field := range []string{"total_ram_gb", "storage_free_percent", "installed_software", "cpu_model"} {
		for _, raw := range []string{`[]`, `{}`, `"alias"`, `{bad`, `null`} {
			t.Run(field+"/"+raw, func(t *testing.T) {
				err := patchPolicyConsumedJSON(field, sql.NullString{String: raw, Valid: true})
				valid := raw == "null" || (field == "cpu_model" && raw == "{}") || (field != "cpu_model" && raw == "[]")
				if (err == nil) != valid {
					t.Fatalf("consumed JSON shape err=%v", err)
				}
			})
		}
	}
}

func TestPatchPolicyEnforcementRawFilterShape(t *testing.T) {
	condition := `{"field":"hostname","operator":"contains","value":"policy"}`
	for _, test := range []struct {
		name, basic, advanced, site string
		valid                       bool
	}{
		{"empty objects", "{}", "{}", "global", true},
		{"empty arrays", `{"criteria":[]}`, `{"groups":[]}`, "global", true},
		{"empty explicit group", "{}", `{"groups":[{"conditions":[]}]}`, "global", true},
		{"basic condition", `{"criteria":[` + condition + `]}`, "{}", "global", true},
		{"advanced condition", "{}", `{"groups":[{"join_with":null,"conditions":[` + condition + `]}]}`, "global", true},
		{"advanced array", "{}", "[]", "global", false},
		{"advanced null", "{}", "null", "global", false},
		{"advanced scalar", "{}", `"alias"`, "global", false},
		{"groups string", "{}", `{"groups":"bad"}`, "global", false},
		{"groups null", "{}", `{"groups":null}`, "global", false},
		{"group scalar", "{}", `{"groups":["bad"]}`, "global", false},
		{"missing conditions", "{}", `{"groups":[{}]}`, "global", false},
		{"conditions string", "{}", `{"groups":[{"conditions":"bad"}]}`, "global", false},
		{"condition scalar", "{}", `{"groups":[{"conditions":["bad"]}]}`, "global", false},
		{"condition object value", "{}", `{"groups":[{"conditions":[{"field":"hostname","operator":"does_not_contain","value":{}}]}]}`, "global", false},
		{"condition option alias", "{}", `{"groups":[{"conditions":[{"field":"hostname","operator":"contains","value":"x","use_regex":"false"}]}]}`, "global", false},
		{"unknown group join", "{}", `{"groups":[{"conditions":[]},{"join_with":"BAD","conditions":[]}]}`, "global", false},
		{"unknown condition join", "{}", `{"groups":[{"conditions":[` + condition + `,{"field":"hostname","operator":"contains","value":"x","join_with":"BAD"}]}]}`, "global", false},
		{"duplicate key", "{}", `{"groups":[],"groups":[]}`, "global", false},
		{"groups alias", "{}", `{"group":[]}`, "global", false},
		{"basic array", "[]", "{}", "global", false},
		{"basic wrong list", `{"criteria":{}}`, "{}", "global", false},
		{"basic wrong condition", `{"criteria":[null]}`, "{}", "global", false},
		{"unknown site mode", "{}", "{}", "unknown", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := deviceFilterRow{ID: sql.NullInt64{Int64: 7, Valid: true}, Name: sql.NullString{String: "shape", Valid: true}, SiteMode: sql.NullString{String: test.site, Valid: true}, BasicCriteriaJSON: sql.NullString{String: test.basic, Valid: true}, AdvancedCriteriaJSON: sql.NullString{String: test.advanced, Valid: true}}
			_, _, err := patchPolicyScopeFilters([]deviceFilterRow{row}, nil)
			if (err == nil) != test.valid {
				t.Fatalf("raw selected filter changed meaning err=%v", err)
			}
		})
	}
}

func TestPatchPolicyEnforcementAuthorityRows(t *testing.T) {
	for _, mode := range []string{"exact", "explicit both", "unknown role", "unknown policy", "unknown update mode", "unknown exclusion", "unknown target", "invalid target GUID", "host exclusion missing site", "conflicting filter target", "invalid rule"} {
		t.Run(mode, func(t *testing.T) {
			row := patchPolicyRow{Enabled: sql.NullInt64{Int64: 1, Valid: true}, ManagedUpdateMode: sql.NullInt64{Int64: 1, Valid: true}, PolicyType: sql.NullString{String: "site", Valid: true}, RoleScope: sql.NullString{String: "Workstation", Valid: true}}
			switch mode {
			case "explicit both":
				row.RoleScope.String = "Both"
			case "unknown role":
				row.RoleScope.String = "bad"
			case "unknown policy":
				row.PolicyType.String = "bad"
			case "unknown update mode":
				row.ManagedUpdateMode.Int64 = 2
			case "unknown exclusion":
				row.Exclusions = []map[string]any{{"exclusion_type": "bad", "target_type": "device", "device_guid": policyEnforcementIntentFixture().DeviceGUID}}
			case "unknown target":
				row.Targets = []map[string]any{{"target_type": "bad", "hostname": "policy-host"}}
			case "invalid target GUID":
				row.Targets = []map[string]any{{"target_type": "device", "device_guid": "bad", "hostname": "policy-host"}}
			case "host exclusion missing site":
				row.Exclusions = []map[string]any{{"exclusion_type": "frozen", "target_type": "device", "hostname": "policy-host"}}
			case "conflicting filter target":
				row.Targets = []map[string]any{{"target_type": "device", "filter_id": int64(7)}}
			case "invalid rule":
				row.Rules = []patchPolicyRule{{RuleType: "", MatchType: "severity", MatchValue: "Critical"}}
			}
			err := patchPolicyAuthorityRows([]patchPolicyRow{row})
			if (err == nil) != (mode == "exact" || mode == "explicit both") {
				t.Fatalf("authority fallback accepted %s err=%v", mode, err)
			}
		})
	}
}

func TestPatchPolicyEnforcementHostServiceContract(t *testing.T) {
	intent := policyEnforcementIntentFixture()
	expected := patchPolicyHostServiceBody(intent, 17)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/remote-ops/host-service/call" || r.Method != http.MethodPost || r.Header.Get(internalTokenHeader) != goInternalToken([]byte("fixture")) {
			t.Error("existing host-service boundary changed")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if mustJSON(body) != mustJSON(expected) {
			t.Errorf("original dispatch payload changed: %v", body)
		}
		_, _ = w.Write([]byte(`{"called":true,"response":{"ok":true,"status":"accepted"}}`))
	}))
	defer server.Close()
	m := goSchedulerManager{secret: []byte("fixture"), httpClient: server.Client()}
	response, _, err := m.callPatchPolicyEnforcement(context.Background(), routeForTestWorker(t, server.URL), intent, 17)
	if err != nil || !reflect.DeepEqual(response, map[string]any{"ok": true, "status": "accepted"}) {
		t.Fatalf("legacy ACK changed response=%v err=%v", response, err)
	}
	if expected["allow_pending"] != false {
		t.Fatal("deferred delivery enabled")
	}
}

func TestPatchPolicyEnforcementRejectsRedirectReplay(t *testing.T) {
	for _, code := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var originals, destinations, originalPolicy atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				destinations.Add(1)
				_, _ = w.Write([]byte(`{"called":true,"response":{"status":"accepted"}}`))
			}))
			defer destination.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				originals.Add(1)
				w.Header().Set("Location", destination.URL+r.URL.Path)
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"called":false}`))
			}))
			defer origin.Close()
			client := origin.Client()
			client.CheckRedirect = func(*http.Request, []*http.Request) error { originalPolicy.Add(1); return nil }
			m := goSchedulerManager{httpClient: client}
			_, _, err := m.callPatchPolicyEnforcement(context.Background(), routeForTestWorker(t, origin.URL), policyEnforcementIntentFixture(), 17)
			if err == nil || originals.Load() != 1 || destinations.Load() != 0 || originalPolicy.Load() != 0 {
				t.Fatalf("policy POST replayed original=%d redirected=%d error=%v", originals.Load(), destinations.Load(), err)
			}
			if err = client.CheckRedirect(&http.Request{}, nil); err != nil || originalPolicy.Load() != 1 {
				t.Fatal("shared redirect policy mutated")
			}
		})
	}
}
