import React, { useEffect, useMemo, useRef, useState } from "react";
import { Box, Button, Dialog, DialogActions, DialogContent, Typography } from "@mui/material";
import DeveloperBoardRoundedIcon from "@mui/icons-material/DeveloperBoardRounded";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DialogHeaderBlock,
} from "../../DialogStyles.jsx";
import { MAGIC_UI } from "./Shared.jsx";
import AgentStartupFlow, { normalizeMilestoneState } from "./Agent_Startup_Flow.jsx";

const AGENT_HEALTH_KIND = Object.freeze({
  role: "role",
  service: "service",
});

const AGENT_HEALTH_PRESENTATION_BY_KEY = Object.freeze({
  deviceaudit: { label: "Device Auditor", kind: AGENT_HEALTH_KIND.role },
  deviceauditor: { label: "Device Auditor", kind: AGENT_HEALTH_KIND.role },
  contextsystem: { label: "System Context", kind: AGENT_HEALTH_KIND.role },
  contextcurrentuser: { label: "Current User Context", kind: AGENT_HEALTH_KIND.role },
  remoteshell: { label: "Remote Shell", kind: AGENT_HEALTH_KIND.role },
  remoteshellservice: { label: "Remote Shell", kind: AGENT_HEALTH_KIND.role },
  servicecontrol: { label: "Service Control", kind: AGENT_HEALTH_KIND.role },
  servicemanagement: { label: "Service Management", kind: AGENT_HEALTH_KIND.role },
  softwaremanagement: { label: "Software Management", kind: AGENT_HEALTH_KIND.role },
  scriptexeccurrentuser: { label: "Script Execution - CURRENTUSER", kind: AGENT_HEALTH_KIND.role },
  scriptexecsystem: { label: "Script Execution - SYSTEM", kind: AGENT_HEALTH_KIND.role },
  processmanagement: { label: "Process Management", kind: AGENT_HEALTH_KIND.role },
  filemanagement: { label: "File Management", kind: AGENT_HEALTH_KIND.role },
  vnc: { label: "UltraVNC", kind: AGENT_HEALTH_KIND.service },
  ultravnc: { label: "UltraVNC", kind: AGENT_HEALTH_KIND.service },
  ultravncservice: { label: "UltraVNC", kind: AGENT_HEALTH_KIND.service },
  wireguard: { label: "WireGuard VPN", kind: AGENT_HEALTH_KIND.service },
  wireguardtunnel: { label: "WireGuard VPN", kind: AGENT_HEALTH_KIND.service },
  wireguardservice: { label: "WireGuard VPN", kind: AGENT_HEALTH_KIND.service },
  wireguardvpn: { label: "WireGuard VPN", kind: AGENT_HEALTH_KIND.service },
});

const HIDDEN_AGENT_HEALTH_KEYS = Object.freeze(
  new Set(["macro", "macroautomation", "macros", "screenshot", "screenshotcapture", "nodescreenshot", "systemheartbeat", "startuptimeline"])
);

function compactAgentHealthKey(value) {
  return String(value || "")
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "");
}

function formatRoleHealthContext(context) {
  const normalized = String(context || "").trim().toLowerCase();
  if (!normalized) return "";
  if (normalized === "system") return "System";
  if (normalized === "currentuser" || normalized === "current_user" || normalized === "interactive") return "Current User";
  return normalized.replace(/[_-]+/g, " ").replace(/\b\w/g, (ch) => ch.toUpperCase());
}

function normalizeRoleHealthStatusText(value) {
  const normalized = String(value || "").trim().toLowerCase();
  if (!normalized) return "Unknown";
  return normalized.replace(/[_-]+/g, " ").replace(/\b\w/g, (ch) => ch.toUpperCase());
}

function resolveAgentHealthPresentation(item, index = 0) {
  const rawRoleName = String(item?.role_name || item?.role || "").trim();
  const rawRoleLabel = String(item?.role_label || "").trim();
  const presentation =
    AGENT_HEALTH_PRESENTATION_BY_KEY[compactAgentHealthKey(rawRoleName)] ||
    AGENT_HEALTH_PRESENTATION_BY_KEY[compactAgentHealthKey(rawRoleLabel)] ||
    null;
  return {
    label: presentation?.label || rawRoleLabel || rawRoleName || `Role ${index + 1}`,
    kind: presentation?.kind || AGENT_HEALTH_KIND.role,
  };
}

function parseJsonArray(value) {
  if (Array.isArray(value)) return value;
  const text = String(value || "").trim();
  if (!text) return [];
  try {
    const parsed = JSON.parse(text);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function parseAgentHealthHostPort(detailText) {
  const text = String(detailText || "").trim();
  const match = text.match(/\b(?:Listening on\s+)?([^\s:]+):(\d{1,5})\b/i);
  if (!match) return { host: "", port: "" };
  return { host: String(match[1] || "").trim(), port: String(match[2] || "").trim() };
}

function parseAgentHealthTunnelId(detailText) {
  const text = String(detailText || "").trim();
  const match = text.match(/\btunnel_id=([^\s]+)/i);
  return match ? String(match[1] || "").trim() : "";
}

function formatAgentHealthDialogValue(value, fallback = "Unavailable") {
  const text = String(value || "").trim();
  return text || fallback;
}

function buildAgentHealthDialogContent(entry, tunnelInfo) {
  if (!entry) return "";
  const details = entry.detailsMap && typeof entry.detailsMap === "object" ? entry.detailsMap : {};
  const lines = [];
  const appendLine = (label, value, fallback = "Unavailable") => {
    lines.push(`${label}: ${formatAgentHealthDialogValue(value, fallback)}`);
  };
  const presentationKey = String(entry.presentationKey || "").trim().toLowerCase();
  const parsedHostPort = parseAgentHealthHostPort(entry.detail);
  const fallbackWireGuardPeerIp = tunnelInfo?.virtual_ip ? String(tunnelInfo.virtual_ip).split("/")[0] : "";
  const fallbackTunnelId = String(tunnelInfo?.tunnel_id || "").trim();

  appendLine("Running Status", details.running_status || entry.status, "Unknown");
  switch (presentationKey) {
    case "remoteshell":
    case "remoteshellservice":
      appendLine("IP", details.listener_ip || parsedHostPort.host, "Unavailable");
      appendLine("Port", details.listener_port || parsedHostPort.port, "Unavailable");
      appendLine("Shell Binary", details.shell_binary, "Unavailable");
      break;
    case "wireguard":
    case "wireguardtunnel":
    case "wireguardservice":
    case "wireguardvpn":
      appendLine("Peer IP", details.wireguard_peer_ip || fallbackWireGuardPeerIp, "Inactive");
      appendLine("Tunnel ID", details.tunnel_id || parseAgentHealthTunnelId(entry.detail) || fallbackTunnelId, "Unavailable");
      appendLine("Endpoint", details.endpoint, "Unavailable");
      break;
    case "vnc":
    case "ultravnc":
    case "ultravncservice":
      appendLine("Service State", details.service_state || details.running_status, "Unknown");
      appendLine("Listener State", details.listener_state, "Unknown");
      appendLine("Ready", details.ready, "Unknown");
      break;
    default:
      appendLine("Context", entry.contextLabel, "Unknown");
      break;
  }
  if (entry.detail) {
    lines.push("");
    lines.push("Detail:");
    lines.push(entry.detail);
  }
  return lines.join("\n");
}

function Island({ title, icon = null, meta = "", children, sx = {} }) {
  return (
    <Box
      sx={{
        borderRadius: 3,
        border: `1px solid ${MAGIC_UI.panelBorder}`,
        background: "linear-gradient(180deg, rgba(15,23,42,0.78), rgba(7,11,24,0.92))",
        boxShadow: "0 24px 70px rgba(2,6,23,0.45)",
        p: 1.6,
        minWidth: 0,
        minHeight: 0,
        ...sx,
      }}
    >
      <Box sx={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 1.5, mb: 1.3 }}>
        <Box sx={{ display: "flex", alignItems: "center", gap: 0.8, minWidth: 0 }}>
          {icon ? <Box sx={{ color: MAGIC_UI.accentA, display: "inline-flex", alignItems: "center" }}>{icon}</Box> : null}
          <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.95rem", fontWeight: 760 }}>{title}</Typography>
        </Box>
        {meta ? (
          <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.78rem", textAlign: "right" }}>{meta}</Typography>
        ) : null}
      </Box>
      {children}
    </Box>
  );
}

export default function AgentHealthTab({
  agentRoleHealth,
  summaryDataReady = true,
  formatTimestamp = (value) => String(value || ""),
  tunnelInfo = null,
  hostname = "",
  onRequestRefresh = null,
}) {
  const [dialogEntry, setDialogEntry] = useState(null);
  const refreshTimerRef = useRef(null);
  const payload = agentRoleHealth && typeof agentRoleHealth === "object" ? agentRoleHealth : {};
  const items = Array.isArray(payload?.roles) ? payload.roles : [];

  const agentHealthRows = useMemo(() => {
    const normalizedItems = items.map((item, index) => {
      const rawRoleName = String(item?.role_name || item?.role || "").trim();
      const rawRoleLabel = String(item?.role_label || item?.label || "").trim();
      const presentation = resolveAgentHealthPresentation(item, index);
      return {
        id: item?.role_id || `${presentation.kind}-${presentation.label}-${index}`,
        baseLabel: presentation.label,
        presentationKey: compactAgentHealthKey(rawRoleName || rawRoleLabel || presentation.label),
        healthKind: presentation.kind,
        sourceRoleName: rawRoleName,
        sourceRoleLabel: rawRoleLabel,
        contextLabel: formatRoleHealthContext(item?.context),
        status: normalizeRoleHealthStatusText(item?.status || item?.status_code || "Unknown"),
        statusCode: String(item?.status_code || item?.status || "unknown").trim().toLowerCase(),
        lastCheckedAt: item?.last_checked_at ?? null,
        lastCheckedText: formatTimestamp(item?.last_checked_at),
        detail: String(item?.detail || "").trim(),
        detailsMap: item?.details && typeof item.details === "object" ? item.details : {},
      };
    });
    const visibleItems = normalizedItems.filter(
      (item) => !HIDDEN_AGENT_HEALTH_KEYS.has(String(item.presentationKey || "").trim().toLowerCase())
    );
    const labelCounts = visibleItems.reduce((acc, item) => {
      const key = `${item.healthKind}:${String(item.baseLabel || "").trim().toLowerCase()}`;
      if (key !== `${item.healthKind}:`) {
        acc[key] = (acc[key] || 0) + 1;
      }
      return acc;
    }, {});
    return visibleItems
      .map((item) => {
        const labelKey = `${item.healthKind}:${String(item.baseLabel || "").trim().toLowerCase()}`;
        const name =
          item.contextLabel && labelCounts[labelKey] > 1 ? `${item.baseLabel} (${item.contextLabel})` : item.baseLabel;
        return { ...item, name };
      })
      .sort((left, right) => String(left.name || "").localeCompare(String(right.name || "")));
  }, [formatTimestamp, items]);

  const startupRole = useMemo(() => {
    for (const item of items) {
      const roleName = compactAgentHealthKey(item?.role_name || item?.role || "");
      const roleLabel = compactAgentHealthKey(item?.role_label || item?.label || "");
      if (roleName === "systemheartbeat" || roleLabel === "startuptimeline") {
        return {
          detail: String(item?.detail || "").trim(),
          statusCode: String(item?.status_code || item?.status || "unknown").trim().toLowerCase(),
          detailsMap: item?.details && typeof item.details === "object" ? item.details : {},
        };
      }
    }
    return null;
  }, [items]);

  const milestones = useMemo(() => parseJsonArray(startupRole?.detailsMap?.milestones_json), [startupRole]);
  const timelineMeta = useMemo(() => {
    if (!milestones.length) return "Awaiting startup telemetry";
    const active = milestones.find((item) => normalizeMilestoneState(item?.state) === "active");
    if (active) return `Active: ${active.label || active.key}`;
    const failed = milestones.find((item) => normalizeMilestoneState(item?.state) === "failed");
    if (failed) return `Needs attention: ${failed.label || failed.key}`;
    return `${milestones.filter((item) => normalizeMilestoneState(item?.state) === "complete").length}/${milestones.length} complete`;
  }, [milestones]);

  const dialogTitle = dialogEntry?.name ? `Agent Health Details - ${dialogEntry.name}` : "Agent Health Details";
  const dialogContent = useMemo(() => buildAgentHealthDialogContent(dialogEntry, tunnelInfo), [dialogEntry, tunnelInfo]);

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    const expectedHost = String(hostname || "").trim().toLowerCase();
    if (!socket || !expectedHost || typeof onRequestRefresh !== "function") return undefined;
    const handler = (eventPayload = {}) => {
      const payloadHost = String(eventPayload?.hostname || "").trim().toLowerCase();
      if (payloadHost && payloadHost !== expectedHost) return;
      if (refreshTimerRef.current) window.clearTimeout(refreshTimerRef.current);
      refreshTimerRef.current = window.setTimeout(() => onRequestRefresh({ silent: true, includeAgents: false }), 250);
    };
    socket.on("agent_status_changed", handler);
    return () => {
      if (refreshTimerRef.current) window.clearTimeout(refreshTimerRef.current);
      socket.off("agent_status_changed", handler);
    };
  }, [hostname, onRequestRefresh]);

  return (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        gap: 1.6,
        minWidth: 0,
        minHeight: { xs: 0, xl: "calc(100vh - 272px)" },
        height: { xs: "auto", xl: "calc(100vh - 272px)" },
        flex: "1 1 auto",
        width: "100%",
      }}
    >
      <Box
        sx={{
          display: "grid",
          gridTemplateColumns: "1fr",
          alignItems: "stretch",
          gap: 1.6,
          flex: { xs: "0 0 auto", xl: 1 },
          minHeight: 0,
          minWidth: 0,
        }}
      >
        <Island
          title="Startup Timeline"
          icon={<DeveloperBoardRoundedIcon sx={{ fontSize: 18 }} />}
          meta={timelineMeta}
          sx={{ height: { xs: "auto", xl: "100%" }, display: "flex", flexDirection: "column" }}
        >
          <Box sx={{ flex: 1, minHeight: 0 }}>
            <AgentStartupFlow
              milestones={milestones}
              runtimeRows={summaryDataReady ? agentHealthRows : []}
              formatTimestamp={formatTimestamp}
              onRuntimeNodeOpen={setDialogEntry}
            />
          </Box>
        </Island>
      </Box>
      <Dialog
        open={Boolean(dialogEntry)}
        onClose={() => setDialogEntry(null)}
        fullWidth
        maxWidth="sm"
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogHeaderBlock title={dialogTitle} subtitle="Current health telemetry and runtime details." />
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Box
            component="pre"
            sx={{
              m: 0,
              p: 1.4,
              borderRadius: 2,
              border: `1px solid ${MAGIC_UI.panelBorder}`,
              background: "rgba(3,7,18,0.64)",
              color: MAGIC_UI.textBright,
              fontFamily: '"IBM Plex Mono", Consolas, monospace',
              fontSize: "0.82rem",
              lineHeight: 1.55,
              whiteSpace: "pre-wrap",
              wordBreak: "break-word",
            }}
          >
            {dialogContent || "No details reported."}
          </Box>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={() => setDialogEntry(null)} sx={DIALOG_BUTTON_SX}>
            Close
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  );
}
