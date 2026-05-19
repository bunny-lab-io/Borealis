package main

func msiInstallArgs(msiPath string, logPath string, properties ...string) []string {
	return msiArgs("/i", msiPath, logPath, properties...)
}

func msiUninstallArgs(msiPath string, logPath string, properties ...string) []string {
	return msiArgs("/x", msiPath, logPath, properties...)
}

func msiArgs(action string, msiPath string, logPath string, properties ...string) []string {
	args := []string{
		action,
		msiPath,
		"/qn",
		"/norestart",
		"REBOOT=ReallySuppress",
		"REBOOTPROMPT=S",
		"MSIRESTARTMANAGERCONTROL=Disable",
		"MSIDISABLERMRESTART=1",
	}
	args = append(args, properties...)
	args = append(args, "/l*v", logPath)
	return args
}

func ultraVNCMSIProperties() []string {
	args := "/VERYSILENT /SUPPRESSMSGBOXES /CLOSEAPPLICATIONS /FORCECLOSEAPPLICATIONS /NOCANCEL /NORESTART /NORESTARTAPPLICATIONS"
	return []string{
		"DO_NOT_LAUNCH=1",
		"WRAPPED_ARGUMENTS=" + args,
		"BZ.UINONE_INSTALL_ARGUMENTS=" + args,
		"BZ.UIBASIC_INSTALL_ARGUMENTS=" + args,
		"BZ.UIREDUCED_INSTALL_ARGUMENTS=" + args,
		"BZ.UIFULL_INSTALL_ARGUMENTS=" + args,
	}
}
