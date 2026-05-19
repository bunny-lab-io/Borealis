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

func ultraVNCInnoInstallArgs(logPath string) []string {
	return []string{
		"/VERYSILENT",
		"/SUPPRESSMSGBOXES",
		"/NOCLOSEAPPLICATIONS",
		"/NOCANCEL",
		"/NORESTART",
		"/NORESTARTAPPLICATIONS",
		"/LOG=" + logPath,
	}
}
