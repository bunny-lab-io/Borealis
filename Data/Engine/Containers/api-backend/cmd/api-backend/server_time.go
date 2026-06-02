package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func registerServerTimeRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("/api/server/time", serverTimeHandler(auth))
	mux.HandleFunc("/api/server/timezones", serverTimezonesHandler(auth))
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

func serverTimezonesHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"current_timezone": currentTimezoneID(),
			"change_supported": timezoneChangeSupported(),
			"timezones":        listAvailableTimezones(),
		})
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

func timezoneChangeSupported() bool {
	_, err := exec.LookPath("timedatectl")
	return err == nil
}

func listAvailableTimezones() []string {
	if timedatectl, err := exec.LookPath("timedatectl"); err == nil {
		ctx, cancel := contextWithTimeout(5 * time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, timedatectl, "list-timezones").Output()
		if err == nil {
			zones := splitTimezoneLines(string(output))
			if len(zones) > 0 {
				return zones
			}
		}
	}

	zones := make([]string, 0, 512)
	root := "/usr/share/zoneinfo"
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		zone := filepath.ToSlash(rel)
		if includeZoneinfoName(zone) {
			zones = append(zones, zone)
		}
		return nil
	})
	sort.Strings(zones)
	return zones
}

func splitTimezoneLines(output string) []string {
	seen := map[string]struct{}{}
	zones := make([]string, 0, 512)
	for _, line := range strings.Split(output, "\n") {
		zone := strings.TrimSpace(line)
		if !includeZoneinfoName(zone) {
			continue
		}
		if _, ok := seen[zone]; ok {
			continue
		}
		seen[zone] = struct{}{}
		zones = append(zones, zone)
	}
	sort.Strings(zones)
	return zones
}

func includeZoneinfoName(zone string) bool {
	if zone == "" {
		return false
	}
	if strings.HasPrefix(zone, ".") || strings.Contains(zone, "/.") {
		return false
	}
	if strings.HasPrefix(zone, "posix/") || strings.HasPrefix(zone, "right/") {
		return false
	}
	switch zone {
	case "localtime", "posixrules", "zone.tab", "zone1970.tab", "iso3166.tab", "tzdata.zi", "leapseconds":
		return false
	}
	return true
}

func contextWithTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}
