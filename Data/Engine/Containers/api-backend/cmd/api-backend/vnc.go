package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultGuacdHost       = "127.0.0.1"
	defaultGuacdPort int64 = 4822
)

func vncViewersHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, failure := requireUser(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		health := guacdHealth(r.Context(), 350*time.Millisecond)
		available := boolFromAny(health["enabled"]) && boolFromAny(health["available"])
		writeJSON(w, http.StatusOK, map[string]any{
			"default_viewer":   "guacamole",
			"default_protocol": "vnc",
			"protocols": []map[string]any{
				{"id": "vnc", "label": "UltraVNC", "default": true},
				{"id": "rdp", "label": "Windows RDP", "default": false},
			},
			"viewers": []map[string]any{
				{
					"id":        "guacamole",
					"label":     "Apache Guacamole",
					"enabled":   boolFromAny(health["enabled"]),
					"available": available,
					"reason":    cleanText(health["reason"]),
				},
			},
			"guacamole": map[string]any{
				"enabled":   boolFromAny(health["enabled"]),
				"available": available,
				"host":      cleanText(health["host"]),
				"port":      coerceInt64(health["port"]),
				"ws_path":   publicGuacamoleVNCPath(),
				"reason":    cleanText(health["reason"]),
			},
		})
	}
}

func guacdHealth(ctx context.Context, timeout time.Duration) map[string]any {
	enabled := parseBoolEnvDefault("BOREALIS_GUACAMOLE_ENABLED", true)
	host := firstText(strings.TrimSpace(os.Getenv("BOREALIS_GUACD_HOST")), defaultGuacdHost)
	port := parseInt64EnvDefault("BOREALIS_GUACD_PORT", defaultGuacdPort)
	payload := map[string]any{
		"enabled":   enabled,
		"available": false,
		"host":      host,
		"port":      port,
		"reason":    "unavailable",
	}
	if !enabled {
		payload["reason"] = "disabled"
		return payload
	}
	if timeout < 100*time.Millisecond {
		timeout = 100 * time.Millisecond
	}
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.FormatInt(port, 10)))
	if err != nil {
		message := err.Error()
		if len(message) > 160 {
			message = message[:160]
		}
		if strings.TrimSpace(message) == "" {
			message = "unavailable"
		}
		payload["reason"] = message
		return payload
	}
	_ = conn.Close()
	payload["available"] = true
	payload["reason"] = "ready"
	return payload
}

func publicGuacamoleVNCPath() string {
	path := strings.TrimSpace(os.Getenv("BOREALIS_GUACAMOLE_VNC_WS_PATH"))
	if path == "" {
		base := strings.TrimSpace(os.Getenv("BOREALIS_PUBLIC_VNC_PATH"))
		if base == "" {
			base = "/remote-desktop/vnc"
		}
		path = strings.TrimRight(base, "/") + "/guacamole"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		path = strings.TrimRight(path, "/")
	}
	return path
}

func parseBoolEnvDefault(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "y", "on", "enabled":
		return true
	case "0", "false", "no", "n", "off", "disabled":
		return false
	default:
		return fallback
	}
}

func parseInt64EnvDefault(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 || parsed > 65535 {
		return fallback
	}
	return parsed
}
