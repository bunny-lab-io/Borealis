package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"
)

var interfacePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)

type routeConfig struct {
	Interface string
	CIDRs     []string
}

func main() {
	config, err := configFromEnv()
	if err != nil {
		fatal(err)
	}
	command := "serve"
	if len(os.Args) > 1 {
		command = strings.TrimSpace(os.Args[1])
	}
	switch command {
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := serve(ctx, config); err != nil {
			fatal(err)
		}
	case "health":
		if err := verify(config); err != nil {
			fatal(err)
		}
	case "live":
		if err := syscall.Kill(1, 0); err != nil {
			fatal(err)
		}
	case "withdraw":
		if err := withdraw(config); err != nil {
			fatal(err)
		}
	default:
		fatal(fmt.Errorf("unsupported command %q", command))
	}
}

func configFromEnv() (routeConfig, error) {
	config := routeConfig{Interface: strings.TrimSpace(os.Getenv("BOREALIS_WIREGUARD_INTERFACE"))}
	if !interfacePattern.MatchString(config.Interface) {
		return routeConfig{}, errors.New("BOREALIS_WIREGUARD_INTERFACE is invalid")
	}
	seen := map[string]bool{}
	for _, raw := range strings.Split(os.Getenv("BOREALIS_WIREGUARD_ROUTE_CIDRS"), ",") {
		cidr := strings.TrimSpace(raw)
		if cidr == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return routeConfig{}, fmt.Errorf("invalid route CIDR %q", cidr)
		}
		if !seen[cidr] {
			config.CIDRs = append(config.CIDRs, cidr)
			seen[cidr] = true
		}
	}
	if len(config.CIDRs) == 0 || len(config.CIDRs) > 32 {
		return routeConfig{}, errors.New("BOREALIS_WIREGUARD_ROUTE_CIDRS must contain 1 through 32 CIDRs")
	}
	return config, nil
}

func serve(ctx context.Context, config routeConfig) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := reconcile(config); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return withdrawContext(shutdownCtx, config)
		case <-ticker.C:
		}
	}
}

func reconcile(config routeConfig) error {
	for _, cidr := range config.CIDRs {
		if output, err := exec.Command("ip", "route", "replace", cidr, "dev", config.Interface, "proto", "static").CombinedOutput(); err != nil {
			return fmt.Errorf("replace route %s: %w: %s", cidr, err, strings.TrimSpace(string(output)))
		}
	}
	return verify(config)
}

func verify(config routeConfig) error {
	for _, cidr := range config.CIDRs {
		output, err := exec.Command("ip", "route", "show", cidr, "dev", config.Interface).CombinedOutput()
		if err != nil || strings.TrimSpace(string(output)) == "" {
			return fmt.Errorf("route %s through %s is unavailable", cidr, config.Interface)
		}
	}
	return nil
}

func withdraw(config routeConfig) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return withdrawContext(ctx, config)
}

func withdrawContext(ctx context.Context, config routeConfig) error {
	var result error
	for _, cidr := range config.CIDRs {
		output, err := exec.CommandContext(ctx, "ip", "route", "del", cidr, "dev", config.Interface, "proto", "static").CombinedOutput()
		if err != nil && !strings.Contains(strings.ToLower(string(output)), "no such process") {
			result = errors.Join(result, fmt.Errorf("withdraw route %s: %w", cidr, err))
		}
	}
	return result
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
