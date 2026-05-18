package patchmanagement

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyPolicyClassTogglesAndHolds(t *testing.T) {
	policy := defaultPolicy()
	policy.ClassToggles["driver"] = false
	policy.Holds = []Hold{{KB: "KB5030001", Reason: "pilot hold"}}
	updates := []Update{
		{UpdateID: "u1", Revision: 1, Title: "Security Update", KBArticleIDs: []string{"5030001"}, Classifications: []string{"Security Updates"}},
		{UpdateID: "u2", Revision: 1, Title: "Contoso Driver", Categories: []string{"Drivers"}},
		{UpdateID: "u3", Revision: 1, Title: "Cumulative Update", Categories: []string{"Cumulative Updates"}},
	}

	applied := applyPolicy(policy, updates)

	if !applied[0].Held || applied[0].Approved || applied[0].HoldReason != "pilot hold" {
		t.Fatalf("security hold not applied: %+v", applied[0])
	}
	if applied[1].Approved || applied[1].PolicyClass != "driver" {
		t.Fatalf("driver class toggle not applied: %+v", applied[1])
	}
	if !applied[2].Approved || applied[2].PolicyClass != "cumulative" {
		t.Fatalf("cumulative update should be approved: %+v", applied[2])
	}
}

func TestSelectInstallTargetsSkipsHeldHiddenInstalled(t *testing.T) {
	updates := []Update{
		{UpdateID: "u1", Revision: 1, Approved: true},
		{UpdateID: "u2", Revision: 1, Approved: false},
		{UpdateID: "u3", Revision: 1, Approved: true, Held: true},
		{UpdateID: "u4", Revision: 1, Approved: true, IsInstalled: true},
		{UpdateID: "u5", Revision: 1, Approved: true, IsHidden: true},
	}

	selected := selectInstallTargets(updates, nil, nil)

	if len(selected) != 1 || selected[0].UpdateID != "u1" {
		t.Fatalf("selected = %+v", selected)
	}
}

func TestNonWindowsUnsupportedAdapter(t *testing.T) {
	if detect, _ := detectSupport(); detect && defaultWUAAdapter() == nil {
		t.Fatalf("supported platform returned nil adapter")
	}
}

func TestPatchManagementLogPathAndWrite(t *testing.T) {
	root := t.TempDir()
	manager := New(nil, "host1", "system", filepath.Join(root, "config.json"))

	expected := filepath.Join(root, "Logs", "Agent", "patch_management.log")
	if manager.logPath != expected {
		t.Fatalf("logPath = %q, want %q", manager.logPath, expected)
	}

	manager.logf("scan test update_count=%d", 3)
	content, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(content), "[patch-management] scan test update_count=3") {
		t.Fatalf("unexpected log content: %s", string(content))
	}
}
