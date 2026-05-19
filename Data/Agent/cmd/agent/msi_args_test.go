package main

import (
	"slices"
	"strings"
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

func TestUltraVNCMSIPropertiesOverrideWrappedInnoArgs(t *testing.T) {
	properties := ultraVNCMSIProperties()
	for _, expectedPrefix := range []string{
		"DO_NOT_LAUNCH=1",
		"WRAPPED_ARGUMENTS=",
		"BZ.UINONE_INSTALL_ARGUMENTS=",
		"BZ.UIBASIC_INSTALL_ARGUMENTS=",
		"BZ.UIREDUCED_INSTALL_ARGUMENTS=",
		"BZ.UIFULL_INSTALL_ARGUMENTS=",
	} {
		var found string
		for _, item := range properties {
			if item == expectedPrefix || len(item) > len(expectedPrefix) && item[:len(expectedPrefix)] == expectedPrefix {
				found = item
				break
			}
		}
		if found == "" {
			t.Fatalf("UltraVNC properties missing %q: %#v", expectedPrefix, properties)
		}
		if expectedPrefix != "DO_NOT_LAUNCH=1" {
			for _, flag := range []string{"/SUPPRESSMSGBOXES", "/CLOSEAPPLICATIONS", "/FORCECLOSEAPPLICATIONS", "/NORESTARTAPPLICATIONS"} {
				if !slices.Contains(strings.Fields(found), flag) {
					t.Fatalf("UltraVNC property %q missing flag %s", found, flag)
				}
			}
		}
	}
}
