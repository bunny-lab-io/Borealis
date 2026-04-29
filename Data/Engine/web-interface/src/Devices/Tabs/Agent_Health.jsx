import React, { useEffect, useMemo, useRef, useState } from "react";
import { Box, Button, Dialog, DialogActions, DialogContent, Stack, Typography } from "@mui/material";
import CheckCircleRoundedIcon from "@mui/icons-material/CheckCircleRounded";
import RadioButtonUncheckedRoundedIcon from "@mui/icons-material/RadioButtonUncheckedRounded";
import ErrorOutlineRoundedIcon from "@mui/icons-material/ErrorOutlineRounded";
import AutorenewRoundedIcon from "@mui/icons-material/AutorenewRounded";
import DeveloperBoardRoundedIcon from "@mui/icons-material/DeveloperBoardRounded";
import HubRoundedIcon from "@mui/icons-material/HubRounded";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DialogHeaderBlock,
} from "../../DialogStyles.jsx";
import { MAGIC_UI } from "./Shared.jsx";
import AgentStartupFlow, { normalizeMilestoneState } from "./Agent_Startup_Flow.jsx";

const SUMMARY_DEFAULT_TEXT_COLOR = "#f4f7ff";

const AGENT_HEALTH_KIND = Object.freeze({
  role: "role",
  service: "service",
});

const ROLE_HEALTH_STATUS_COLOR_BY_CODE = Object.freeze({
  healthy: "#00d18c",
  recovering: "#ffb347",
  unhealthy: "#ff7b89",
  pending: "#7dd3fc",
  loaded: "#7dd3fc",
  unsupported: "#b0b8c8",
  unknown: "#b0b8c8",
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

function getRoleHealthStatusColor(statusCode) {
  const normalized = String(statusCode || "").trim().toLowerCase();
  return ROLE_HEALTH_STATUS_COLOR_BY_CODE[normalized] || SUMMARY_DEFAULT_TEXT_COLOR;
}

function getRoleHealthStatusIcon(statusCode) {
  const normalized = String(statusCode || "").trim().toLowerCase();
  if (normalized === "healthy" || normalized === "loaded") return CheckCircleRoundedIcon;
  if (normalized === "recovering" || normalized === "pending") return AutorenewRoundedIcon;
  if (normalized === "unhealthy") return ErrorOutlineRoundedIcon;
  return RadioButtonUncheckedRoundedIcon;
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

function buildAgentHealthMeta(rows, emptyText, fallbackText) {
  if (!rows.length) return emptyText;
  const counts = rows.reduce((acc, row) => {
    const key = String(row.statusCode || "unknown").trim().toLowerCase();
    acc[key] = (acc[key] || 0) + 1;
    return acc;
  }, {});
  const parts = [];
  if (counts.healthy) parts.push(`${counts.healthy} healthy`);
  if (counts.recovering) parts.push(`${counts.recovering} recovering`);
  if (counts.unhealthy) parts.push(`${counts.unhealthy} unhealthy`);
  if (counts.pending) parts.push(`${counts.pending} pending`);
  if (!parts.length) parts.push(fallbackText);
  return parts.join(" • ");
}

function RuntimeHealthEmptyState({ children }) {
  return (
    <Box
      sx={{
        borderRadius: 2,
        border: `1px dashed ${MAGIC_UI.panelBorder}`,
        px: 1.25,
        py: 1,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        color: MAGIC_UI.textMuted,
        minHeight: 86,
      }}
    >
      <Typography variant="caption" sx={{ color: "inherit", letterSpacing: 0.2 }}>
        {children}
      </Typography>
    </Box>
  );
}

function RuntimeHealthNode({ entry, onOpen }) {
  const color = getRoleHealthStatusColor(entry?.statusCode);
  const Icon = getRoleHealthStatusIcon(entry?.statusCode);
  const statusText = String(entry?.status || "Unknown");
  const lastChecked = String(entry?.lastCheckedText || "").trim();
  return (
    <Button
      type="button"
      onClick={() => onOpen(entry)}
      sx={{
        width: "100%",
        minHeight: 74,
        px: 1,
        py: 0.9,
        borderRadius: 2,
        border: `1px solid ${color}`,
        background: `linear-gradient(145deg, rgba(7,11,24,0.96), ${color}18)`,
        boxShadow: "0 14px 32px rgba(2,6,23,0.44)",
        color: MAGIC_UI.textBright,
        textTransform: "none",
        justifyContent: "flex-start",
        textAlign: "left",
        overflow: "hidden",
        "&:hover": {
          background: `linear-gradient(145deg, rgba(10,17,33,0.98), ${color}24)`,
          boxShadow: `0 0 0 1px ${color}44, 0 18px 38px rgba(2,6,23,0.55)`,
        },
      }}
    >
      <Box
        sx={{
          width: 24,
          height: 24,
          borderRadius: "50%",
          mr: 1,
          flexShrink: 0,
          color,
          background: `${color}18`,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
        }}
      >
        <Icon
          sx={{
            fontSize: 18,
            animation:
              entry?.statusCode === "recovering" || entry?.statusCode === "pending"
                ? "agentRuntimeNodeSpin 1.15s linear infinite"
                : "none",
          }}
        />
      </Box>
      <Box sx={{ minWidth: 0, flex: 1 }}>
        <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.78rem", fontWeight: 720, lineHeight: 1.2 }} noWrap>
          {entry?.name || "Unknown"}
        </Typography>
        <Typography sx={{ mt: 0.25, color, fontSize: "0.7rem", fontWeight: 650, lineHeight: 1.2 }} noWrap>
          {statusText}
        </Typography>
        {lastChecked ? (
          <Typography sx={{ mt: 0.15, color: MAGIC_UI.textMuted, fontSize: "0.66rem", lineHeight: 1.2 }} noWrap>
            {lastChecked}
          </Typography>
        ) : null}
      </Box>
    </Button>
  );
}

function RuntimeHealthNodeGroup({ title, rows, emptyText, onOpen }) {
  const meta = buildAgentHealthMeta(rows, emptyText, `${rows.length} reporting`);
  return (
    <Box sx={{ minHeight: 0 }}>
      <Box sx={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 1, mb: 0.9 }}>
        <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.82rem", fontWeight: 760 }}>{title}</Typography>
        <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.72rem", textAlign: "right" }}>{meta}</Typography>
      </Box>
      {rows.length ? (
        <Box
          sx={{
            display: "grid",
            gridTemplateColumns: { xs: "1fr", md: "repeat(auto-fit, minmax(190px, 1fr))" },
            gap: 1,
            minWidth: 0,
          }}
        >
          {rows.map((entry) => (
            <RuntimeHealthNode key={entry.id} entry={entry} onOpen={onOpen} />
          ))}
        </Box>
      ) : (
        <RuntimeHealthEmptyState>{emptyText}</RuntimeHealthEmptyState>
      )}
    </Box>
  );
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
  const roleRows = useMemo(() => agentHealthRows.filter((row) => row.healthKind === AGENT_HEALTH_KIND.role), [agentHealthRows]);
  const serviceRows = useMemo(() => agentHealthRows.filter((row) => row.healthKind === AGENT_HEALTH_KIND.service), [agentHealthRows]);
  const runtimeMeta = useMemo(() => buildAgentHealthMeta(agentHealthRows, "No runtime telemetry reported yet", `${agentHealthRows.length} checks reporting`), [agentHealthRows]);
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
          gridTemplateColumns: { xs: "1fr", xl: "minmax(0, 1fr) minmax(360px, 0.9fr)" },
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
            <AgentStartupFlow milestones={milestones} formatTimestamp={formatTimestamp} />
          </Box>
        </Island>
        <Island
          title="Runtime Health"
          icon={<HubRoundedIcon sx={{ fontSize: 18 }} />}
          meta={runtimeMeta}
          sx={{
            height: { xs: "auto", xl: "100%" },
            display: "flex",
            flexDirection: "column",
            "@keyframes agentRuntimeNodeSpin": {
              "100%": { transform: "rotate(360deg)" },
            },
          }}
        >
          <Stack spacing={1.4} sx={{ flex: 1, minHeight: 0, overflowY: { xs: "visible", xl: "auto" }, pr: { xs: 0, xl: 0.4 } }}>
            {summaryDataReady ? (
              <>
                <RuntimeHealthNodeGroup title="Roles" rows={roleRows} emptyText="No role telemetry reported yet" onOpen={setDialogEntry} />
                <RuntimeHealthNodeGroup title="Services" rows={serviceRows} emptyText="No service telemetry reported yet" onOpen={setDialogEntry} />
              </>
            ) : (
              <RuntimeHealthEmptyState>Loading telemetry...</RuntimeHealthEmptyState>
            )}
          </Stack>
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
