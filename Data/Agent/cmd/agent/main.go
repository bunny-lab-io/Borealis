package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	agentruntime "github.com/bunny-lab-io/borealis/go-agent/internal/runtime"
)

var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	if code, handled := maybeRunBootstrap(os.Args[1:]); handled {
		return code
	}

	var options agentruntime.Options
	var installService bool
	var uninstallService bool
	var printVersion bool
	flag.StringVar(&options.ConfigPath, "config-path", "", "Path to config.json. Defaults beside Agent.exe.")
	flag.StringVar(&options.ServerURL, "server-url", "", "Borealis Engine public URL.")
	flag.StringVar(&options.EnrollmentCode, "site-enrollment-code", "", "Site enrollment code.")
	flag.StringVar(&options.EnrollmentCode, "enrollment-code", "", "Enrollment code.")
	flag.BoolVar(&options.Verbose, "verbose", false, "Mirror logs to stdout.")
	flag.BoolVar(&options.Once, "once", false, "Run auth and heartbeat once, then exit.")
	flag.BoolVar(&installService, "install-service", false, "Install and start Borealis Agent service.")
	flag.BoolVar(&uninstallService, "uninstall-service", false, "Uninstall Borealis Agent service.")
	flag.BoolVar(&printVersion, "version", false, "Print version.")
	systemService := flag.Bool("system-service", false, "Run as SYSTEM/root service.")
	helperMode := flag.Bool("helper", false, "Run as current-user helper.")
	flag.Parse()

	if printVersion {
		fmt.Println(version)
		return 0
	}

	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve executable: %v\n", err)
		return 1
	}
	if installService {
		if err := agentruntime.InstallService(exePath); err != nil {
			fmt.Fprintf(os.Stderr, "install service: %v\n", err)
			return 1
		}
		return 0
	}
	if uninstallService {
		if err := agentruntime.UninstallService(); err != nil {
			fmt.Fprintf(os.Stderr, "uninstall service: %v\n", err)
			return 1
		}
		return 0
	}

	if *helperMode {
		options.ServiceMode = "currentuser"
	} else if *systemService {
		options.ServiceMode = "system"
	} else {
		options.ServiceMode = "system"
	}

	configPath := options.ConfigPath
	if configPath == "" {
		configPath, err = agentconfig.PathFromBinary()
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve config path: %v\n", err)
			return 1
		}
	}
	logger, closeLogger := agentruntime.OpenLogger(configPath, options.Verbose)
	defer closeLogger()

	agent, err := agentruntime.New(options, logger)
	if err != nil {
		logger.Printf("agent init failed: %v", err)
		fmt.Fprintf(os.Stderr, "agent init failed: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := agent.Run(ctx); err != nil && ctx.Err() == nil {
		logger.Printf("agent stopped: %v", err)
		fmt.Fprintf(os.Stderr, "agent stopped: %v\n", err)
		return 1
	}
	return 0
}
