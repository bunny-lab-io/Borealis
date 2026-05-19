package main

import (
	"slices"
	"testing"
)

func TestMSIInstallArgsSuppressUIRestartManagerAndReboot(t *testing.T) {
	args := msiInstallArgs(`C:\Temp\dependency.msi`, `C:\Logs\dependency.log`, "DO_NOT_LAUNCH=1")
	for _, expected := range []string{
		"/i",
		`C:\Temp\dependency.msi`,
		"/qn",
		"/norestart",
		"REBOOT=ReallySuppress",
		"REBOOTPROMPT=S",
		"MSIRESTARTMANAGERCONTROL=Disable",
		"MSIDISABLERMRESTART=1",
		"DO_NOT_LAUNCH=1",
		"/l*v",
		`C:\Logs\dependency.log`,
	} {
		if !slices.Contains(args, expected) {
			t.Fatalf("MSI args missing %q: %#v", expected, args)
		}
	}
}

func TestUltraVNCInnoInstallArgsSuppressFileInUsePrompts(t *testing.T) {
	args := ultraVNCInnoInstallArgs(`C:\Logs\UltraVNC\install.log`)
	for _, expected := range []string{
		"/VERYSILENT",
		"/SUPPRESSMSGBOXES",
		"/NOCLOSEAPPLICATIONS",
		"/NOCANCEL",
		"/NORESTART",
		"/NORESTARTAPPLICATIONS",
		`/LOG=C:\Logs\UltraVNC\install.log`,
	} {
		if !slices.Contains(args, expected) {
			t.Fatalf("UltraVNC Inno args missing %q: %#v", expected, args)
		}
	}
}
