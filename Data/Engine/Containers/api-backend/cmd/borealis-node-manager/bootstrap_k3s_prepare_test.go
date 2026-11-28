package main

import (
	"strings"
	"testing"
)

const bootstrapInertK3sFixture = "Id=k3s.service\nNames=k3s.service\nLoadState=loaded\nActiveState=inactive\nSubState=dead\nMainPID=0\nControlPID=0\nNeedDaemonReload=no\nJob=\nUnitFileState=disabled\nFragmentPath=/etc/systemd/system/k3s.service\nSourcePath=\nDropInPaths=\nTransient=no\nType=notify\nResult=success\nExecMainStatus=0\n"

func TestBootstrapK3sInertLoadedProof(t *testing.T) {
	if validateBootstrapInertK3s(bootstrapInertK3sFixture, true) != nil {
		t.Fatal("fixed loaded inactive service rejected")
	}
	for _, mutation := range []struct{ key, value string }{
		{"Id", "k3s-agent.service"}, {"Names", "k3s.service foreign.service"}, {"LoadState", "masked"},
		{"ActiveState", "activating"}, {"SubState", "running"}, {"MainPID", "9"}, {"ControlPID", "9"},
		{"NeedDaemonReload", "yes"}, {"Job", "10"}, {"UnitFileState", "enabled"}, {"FragmentPath", "/tmp/k3s.service"},
		{"SourcePath", "/tmp/input"}, {"DropInPaths", "/etc/systemd/system/service.d/foreign.conf"}, {"Transient", "yes"},
		{"Type", "simple"}, {"Result", "exit-code"}, {"ExecMainStatus", "1"},
	} {
		t.Run(mutation.key, func(t *testing.T) {
			lines := strings.Split(bootstrapInertK3sFixture, "\n")
			for i, line := range lines {
				if strings.HasPrefix(line, mutation.key+"=") {
					lines[i] = mutation.key + "=" + mutation.value
				}
			}
			if validateBootstrapInertK3s(strings.Join(lines, "\n"), true) == nil {
				t.Fatal("unsafe loaded service admitted")
			}
		})
	}
	for _, raw := range []string{bootstrapInertK3sFixture + "MainPID=0\n", strings.Replace(bootstrapInertK3sFixture, "Job=\n", "", 1), strings.TrimSuffix(bootstrapInertK3sFixture, "\n"), bootstrapInertK3sFixture + "Unknown=\n"} {
		if validateBootstrapInertK3s(raw, true) == nil {
			t.Fatal("ambiguous observation admitted")
		}
	}
}

func TestBootstrapK3sPartialLoadedProofCannotSettle(t *testing.T) {
	absent := strings.NewReplacer("LoadState=loaded", "LoadState=not-found", "UnitFileState=disabled", "UnitFileState=", "FragmentPath=/etc/systemd/system/k3s.service", "FragmentPath=", "Type=notify", "Type=simple").Replace(bootstrapInertK3sFixture)
	for _, raw := range []string{absent, strings.Replace(absent, "NeedDaemonReload=no", "NeedDaemonReload=yes", 1), strings.Replace(bootstrapInertK3sFixture, "NeedDaemonReload=no", "NeedDaemonReload=yes", 1)} {
		if validateBootstrapInertK3s(raw, false) != nil || validateBootstrapInertK3s(raw, true) == nil {
			t.Fatal("partial observation mistaken for completed installation")
		}
	}
}
