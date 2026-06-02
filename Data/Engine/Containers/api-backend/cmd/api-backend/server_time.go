package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func registerServerTimeRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("/api/server/time", serverTimeHandler(auth))
}

func serverTimeHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if _, failure := requireUser(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}

		nowUTC := time.Now().UTC()
		nowLocal := nowUTC.Local()
		writeJSON(w, http.StatusOK, serializeServerTime(nowLocal, nowUTC, currentTimezoneID()))
	}
}

func serializeServerTime(nowLocal, nowUTC time.Time, timezoneID string) map[string]any {
	return map[string]any{
		"epoch":       nowLocal.Unix(),
		"iso":         pythonISO(nowLocal),
		"utc":         pythonISO(nowUTC),
		"timezone":    nowLocal.Format("MST"),
		"timezone_id": strings.TrimSpace(timezoneID),
		"display":     nowLocal.Format("2006-01-02 15:04:05 MST"),
	}
}

func pythonISO(t time.Time) string {
	microseconds := t.Nanosecond() / 1000
	base := t.Format("2006-01-02T15:04:05")
	if microseconds > 0 {
		base += fmt.Sprintf(".%06d", microseconds)
	}
	return base + t.Format("-07:00")
}

func currentTimezoneID() string {
	if value := strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_HOST_TIMEZONE")); value != "" {
		return value
	}

	if value := strings.TrimSpace(os.Getenv("TZ")); value != "" {
		return value
	}

	if timedatectl, err := exec.LookPath("timedatectl"); err == nil {
		ctx, cancel := contextWithTimeout(5 * time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, timedatectl, "show", "--property=Timezone", "--value").Output()
		if err == nil {
			if value := strings.TrimSpace(string(output)); value != "" {
				return value
			}
		}
	}

	if value, err := os.ReadFile("/etc/timezone"); err == nil {
		if text := strings.TrimSpace(string(value)); text != "" {
			return text
		}
	}

	if resolved, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if timezoneID := timezoneFromZoneinfoPath(resolved); timezoneID != "" {
			return timezoneID
		}
	}

	return time.Now().Local().Format("MST")
}

func timezoneFromZoneinfoPath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for idx, part := range parts {
		if part != "zoneinfo" {
			continue
		}
		timezoneID := strings.Join(parts[idx+1:], "/")
		return strings.TrimSpace(timezoneID)
	}
	return ""
}

func contextWithTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}
