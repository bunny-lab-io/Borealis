//go:build windows

package currentuser

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/localui"
)

func startHelperUI(ctx context.Context, stateDir string, sessionID int, buildID string) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return
	}
	uiURL := "http://" + listener.Addr().String()
	mux := http.NewServeMux()
	httpClient := &http.Client{Timeout: 12 * time.Second}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(statusPageHTML))
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeHelperJSON(w, http.StatusMethodNotAllowed, localui.CommandResponse{Status: "error", Detail: "method not allowed"})
			return
		}
		response, err := localui.DoCommand(r.Context(), httpClient, stateDir, localui.CommandRequest{Command: localui.CommandStatusGet})
		if err != nil {
			writeHelperJSON(w, http.StatusServiceUnavailable, localui.CommandResponse{Status: "error", Detail: err.Error()})
			return
		}
		writeHelperJSON(w, http.StatusOK, response)
	})
	mux.HandleFunc("/api/action", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeHelperJSON(w, http.StatusMethodNotAllowed, localui.CommandResponse{Status: "error", Detail: "method not allowed"})
			return
		}
		var request localui.CommandRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeHelperJSON(w, http.StatusBadRequest, localui.CommandResponse{Status: "error", Detail: "invalid JSON request"})
			return
		}
		switch strings.TrimSpace(request.Command) {
		case localui.CommandAgentRestart, localui.CommandAgentUpdate, localui.CommandDiagnosticsCopy, localui.CommandStatusGet:
		default:
			writeHelperJSON(w, http.StatusBadRequest, localui.CommandResponse{Status: "error", Detail: "unsupported UI command"})
			return
		}
		response, err := localui.DoCommand(r.Context(), httpClient, stateDir, request)
		if err != nil {
			writeHelperJSON(w, http.StatusServiceUnavailable, response)
			return
		}
		writeHelperJSON(w, http.StatusOK, response)
	})
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = server.Serve(listener)
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go runTray(ctx, trayOptions{
		UIURL:     uiURL,
		StateDir:  stateDir,
		SessionID: sessionID,
		BuildID:   buildID,
	})
}

func writeHelperJSON(w http.ResponseWriter, status int, response localui.CommandResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

const statusPageHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Borealis Agent</title>
  <style>
    :root {
      color-scheme: dark;
      --bg: #070b16;
      --panel: #0d1526;
      --panel-2: #101b2f;
      --text: #eef6ff;
      --muted: #9fb2c9;
      --line: #22324a;
      --good: #36f0a4;
      --warn: #f3b34c;
      --bad: #ff6f91;
      --accent: #62c8ff;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      font-family: Inter, "Segoe UI", Arial, sans-serif;
      background: radial-gradient(circle at top left, rgba(48, 115, 190, 0.28), transparent 34rem), linear-gradient(145deg, #07101f, #090713 75%);
      color: var(--text);
    }
    main { max-width: 1080px; margin: 0 auto; padding: 28px; }
    header { display: flex; justify-content: space-between; gap: 18px; align-items: flex-start; margin-bottom: 22px; }
    h1 { font-size: 25px; margin: 0 0 6px; letter-spacing: 0; }
    .sub { color: var(--muted); font-size: 13px; line-height: 1.45; }
    .pill { display: inline-flex; align-items: center; gap: 8px; border: 1px solid var(--line); background: rgba(13, 21, 38, 0.82); border-radius: 999px; padding: 9px 12px; font-weight: 700; font-size: 13px; }
    .dot { width: 9px; height: 9px; border-radius: 50%; background: var(--warn); box-shadow: 0 0 18px currentColor; }
    .dot.good { background: var(--good); }
    .dot.bad { background: var(--bad); }
    .grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; margin-bottom: 14px; }
    .card { background: rgba(13, 21, 38, 0.86); border: 1px solid var(--line); border-radius: 8px; padding: 14px; min-height: 86px; }
    .label { color: var(--muted); font-size: 11px; text-transform: uppercase; font-weight: 800; letter-spacing: .08em; margin-bottom: 7px; }
    .value { color: var(--text); font-size: 14px; line-height: 1.35; overflow-wrap: anywhere; }
    .actions { display: flex; gap: 10px; flex-wrap: wrap; margin: 16px 0 18px; }
    button, a.action {
      border: 1px solid #355070;
      background: #121f35;
      color: var(--text);
      border-radius: 7px;
      padding: 10px 13px;
      font-weight: 800;
      cursor: pointer;
      text-decoration: none;
    }
    button.primary, a.primary { border-color: #2a98d8; background: #0f3551; }
    button:hover, a.action:hover { border-color: var(--accent); }
    .roles { background: rgba(13, 21, 38, 0.72); border: 1px solid var(--line); border-radius: 8px; overflow: hidden; }
    .role { display: grid; grid-template-columns: 1fr auto; gap: 12px; padding: 13px 15px; border-top: 1px solid rgba(34, 50, 74, .78); }
    .role:first-child { border-top: 0; }
    .role-name { font-weight: 850; }
    .role-detail { color: var(--muted); font-size: 12px; margin-top: 4px; line-height: 1.35; }
    .badge { border: 1px solid var(--line); border-radius: 999px; padding: 6px 9px; font-size: 12px; font-weight: 850; height: max-content; }
    .badge.healthy { color: var(--good); border-color: rgba(54, 240, 164, .55); background: rgba(54, 240, 164, .08); }
    .badge.recovering, .badge.degraded { color: var(--warn); border-color: rgba(243, 179, 76, .55); background: rgba(243, 179, 76, .08); }
    .badge.unhealthy, .badge.failed { color: var(--bad); border-color: rgba(255, 111, 145, .55); background: rgba(255, 111, 145, .08); }
    .logs { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; margin-top: 14px; color: var(--muted); font-size: 12px; }
    .log { border: 1px solid rgba(34, 50, 74, .8); background: rgba(7, 11, 22, .36); border-radius: 7px; padding: 9px; overflow-wrap: anywhere; }
    #toast { min-height: 22px; color: var(--muted); font-size: 13px; margin-top: 8px; }
    @media (max-width: 760px) {
      main { padding: 18px; }
      header { flex-direction: column; }
      .grid, .logs { grid-template-columns: 1fr; }
      .role { grid-template-columns: 1fr; }
    }
  </style>
</head>
<body>
  <main>
    <header>
      <div>
        <h1>Borealis Agent</h1>
        <div class="sub" id="summary">Loading local agent status...</div>
      </div>
      <div class="pill"><span id="state-dot" class="dot"></span><span id="engine-state">Starting</span></div>
    </header>
    <section class="grid">
      <div class="card"><div class="label">Device</div><div class="value" id="hostname">-</div></div>
      <div class="card"><div class="label">Engine</div><div class="value" id="server">-</div></div>
      <div class="card"><div class="label">Build</div><div class="value" id="build">-</div></div>
      <div class="card"><div class="label">Release</div><div class="value" id="release">-</div></div>
    </section>
    <div class="actions">
      <a class="action primary" id="engine-link" href="#" target="_blank" rel="noreferrer">Open Engine Web UI</a>
      <button type="button" onclick="sendAction('agent.update_check')">Check For Updates</button>
      <button type="button" onclick="sendAction('agent.restart')">Restart Agent</button>
      <button type="button" onclick="copyDiagnostics()">Copy Diagnostics</button>
    </div>
    <div id="toast"></div>
    <section class="roles" id="roles"></section>
    <section class="logs" id="logs"></section>
  </main>
  <script>
    const statusUrl = "/api/status";
    const actionUrl = "/api/action";
    let latest = null;
    const text = (value, fallback = "-") => (value === undefined || value === null || String(value).trim() === "") ? fallback : String(value);
    const fmtTime = (epoch) => epoch ? new Date(epoch * 1000).toLocaleString() : "Pending";
    async function loadStatus() {
      try {
        const response = await fetch(statusUrl, { cache: "no-store" });
        const body = await response.json();
        if (body.status !== "ok") throw new Error(body.detail || "Status unavailable");
        latest = body.data || {};
        render(latest);
      } catch (error) {
        document.getElementById("summary").textContent = error.message;
        document.getElementById("engine-state").textContent = "Unavailable";
        document.getElementById("state-dot").className = "dot bad";
      }
    }
    function render(data) {
      document.getElementById("hostname").textContent = text(data.hostname, "Unknown");
      document.getElementById("server").textContent = text(data.server_url, "Not configured");
      document.getElementById("build").textContent = text(data.installed_build_id || data.build_id, "Unknown");
      document.getElementById("release").textContent = text(data.release_channel, "stable") + " / " + text(data.branch, "main");
      document.getElementById("engine-state").textContent = text(data.engine_state, "Starting");
      document.getElementById("summary").textContent = "Last heartbeat: " + fmtTime(data.last_heartbeat_at) + ". Last status: " + text(data.last_status_phase, "Pending") + ".";
      document.getElementById("engine-link").href = data.server_url || "#";
      const state = String(data.engine_state || "").toLowerCase();
      document.getElementById("state-dot").className = state.includes("online") ? "dot good" : (state.includes("unhealthy") ? "dot bad" : "dot");
      const roles = Array.isArray(data.roles) ? data.roles : [];
      document.getElementById("roles").innerHTML = roles.map((role) => {
        const status = text(role.status_code || role.status, "unknown").toLowerCase();
        return "<div class=\"role\"><div><div class=\"role-name\">" + escapeHtml(text(role.role_label || role.role_name, "Role")) + "</div><div class=\"role-detail\">" + escapeHtml(text(role.detail, "")) + "</div></div><div class=\"badge " + escapeHtml(status) + "\">" + escapeHtml(status) + "</div></div>";
      }).join("") || "<div class=\"role\"><div><div class=\"role-name\">Role Health</div><div class=\"role-detail\">Waiting for first heartbeat.</div></div><div class=\"badge recovering\">pending</div></div>";
      const logs = Array.isArray(data.logs) ? data.logs : [];
      document.getElementById("logs").innerHTML = logs.map((item) => "<div class=\"log\"><strong>" + escapeHtml(text(item.label, "Log")) + "</strong><br>" + escapeHtml(text(item.path, "")) + "</div>").join("");
    }
    async function sendAction(command) {
      const response = await fetch(actionUrl, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ command }) });
      const body = await response.json();
      document.getElementById("toast").textContent = body.detail || (body.status === "ok" ? "Command sent." : "Command failed.");
      if (command !== "agent.restart") setTimeout(loadStatus, 1200);
    }
    async function copyDiagnostics() {
      const response = await fetch(actionUrl, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ command: "diagnostics.copy_summary" }) });
      const body = await response.json();
      const text = body?.data?.diagnostics_text || "";
      await navigator.clipboard.writeText(text);
      document.getElementById("toast").textContent = "Diagnostics copied to clipboard.";
    }
    function escapeHtml(value) {
      return String(value).replace(/[&<>"']/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[char]));
    }
    loadStatus();
    setInterval(loadStatus, 15000);
  </script>
</body>
</html>`
