package main

import (
	"net"
	"os"
	"strings"
)

func overviewEngineIPFallback() string {
	return normalizeEngineIPFallback(os.Getenv("BOREALIS_ENGINE_IP_FALLBACK"))
}

func normalizeEngineIPFallback(value string) string {
	text := strings.TrimSpace(value)
	if text == "" || strings.Contains(text, "://") || strings.Contains(text, "/") {
		return ""
	}
	ip := net.ParseIP(text)
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
		return ""
	}
	return ip.String()
}
