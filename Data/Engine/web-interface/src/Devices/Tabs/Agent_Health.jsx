import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Box, Button, Dialog, DialogActions, DialogContent, Stack, Tooltip, Typography } from "@mui/material";
import CheckCircleRoundedIcon from "@mui/icons-material/CheckCircleRounded";
import RadioButtonUncheckedRoundedIcon from "@mui/icons-material/RadioButtonUncheckedRounded";
import ErrorOutlineRoundedIcon from "@mui/icons-material/ErrorOutlineRounded";
import AutorenewRoundedIcon from "@mui/icons-material/AutorenewRounded";
import DeveloperBoardRoundedIcon from "@mui/icons-material/DeveloperBoardRounded";
import HubRoundedIcon from "@mui/icons-material/HubRounded";
import { AgGridReact } from "ag-grid-react";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DialogHeaderBlock,
} from "../../DialogStyles.jsx";
import { DEVICE_DETAILS_GRID_THEME, GridShell, MAGIC_UI, gridFontFamily } from "./Shared.jsx";

const SUMMARY_FIELD_TEXT_COLOR = "#58a6ff";
const SUMMARY_DEFAULT_TEXT_COLOR = "#f4f7ff";
const ROLE_HEALTH_LAST_CHECKED_COLOR = "rgba(226,232,240,0.72)";
const SUMMARY_GRID_STYLE = {
  width: "100%",
  height: "100%",
  fontFamily: gridFontFamily,
};

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

const AgentHealthLinkCell = React.memo(function AgentHealthLinkCell(props) {
  const row = props?.data || null;
  const onOpen = props?.onOpen;
  const value = String(props?.value || row?.name || "").trim();
  if (!value) return null;
  return (
    <Button
      type="button"
      onClick={(event) => {
        event.preventDefault();
        event.stopPropagation();
        if (row && typeof onOpen === "function") onOpen(row);
      }}
      sx={{
        minWidth: 0,
        px: 0,
        py: 0,
        justifyContent: "flex-start",
        textTransform: "none",
        color: SUMMARY_FIELD_TEXT_COLOR,
        fontWeight: 400,
        textDecoration: "none",
        "&:hover": { background: "transparent", color: "#7dd3fc" },
      }}
    >
      {value}
    </Button>
  );
});

function createAgentHealthColumnDefs(nameHeader, onOpen) {
  return [
    {
      field: "name",
      headerName: nameHeader,
      width: 220,
      flex: 1.1,
      sortable: false,
      filter: false,
      cellRenderer: AgentHealthLinkCell,
      cellRendererParams: { onOpen },
      cellStyle: { color: SUMMARY_FIELD_TEXT_COLOR },
      tooltipValueGetter: (params) => params?.data?.detail || params?.value || "",
    },
    {
      field: "status",
      headerName: "Status",
      width: 170,
      flex: 0.7,
      sortable: false,
      filter: false,
      cellStyle: (params) => ({
        color: getRoleHealthStatusColor(params?.data?.statusCode),
        fontWeight: 600,
      }),
      tooltipValueGetter: (params) => params?.data?.detail || params?.value || "",
    },
    {
      field: "lastCheckedText",
      headerName: "Last Checked",
      width: 220,
      flex: 0.85,
      sortable: false,
      filter: false,
      cellStyle: { color: ROLE_HEALTH_LAST_CHECKED_COLOR },
    },
  ];
}

function SummarySectionGrid({ sectionKey, rowData, columnDefs, height }) {
  const defaultColDef = useMemo(
    () => ({
      sortable: false,
      resizable: true,
      filter: false,
      flex: 1,
      minWidth: 140,
      cellStyle: { color: SUMMARY_DEFAULT_TEXT_COLOR },
    }),
    []
  );
  return (
    <GridShell
      sx={{
        height,
        "& .ag-cell, & .ag-cell-wrapper, & .ag-cell-value": {
          display: "flex",
          alignItems: "center",
          minHeight: "100%",
        },
        "& .ag-cell-value": { width: "100%" },
      }}
    >
      <AgGridReact
        rowData={rowData}
        columnDefs={columnDefs}
        defaultColDef={defaultColDef}
        pagination={false}
        animateRows={false}
        suppressCellFocus
        getRowId={(params) => params.data?.id ?? `${sectionKey}-${params.rowIndex}`}
        theme={DEVICE_DETAILS_GRID_THEME}
        style={SUMMARY_GRID_STYLE}
      />
    </GridShell>
  );
}

function SummaryGridPlaceholder({ height }) {
  return (
    <GridShell sx={{ height, display: "flex", alignItems: "center", justifyContent: "center" }}>
      <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted, letterSpacing: 0.2 }}>
        Loading telemetry...
      </Typography>
    </GridShell>
  );
}

function Island({ title, icon = null, meta = "", children }) {
  return (
    <Box
      sx={{
        borderRadius: 3,
        border: `1px solid ${MAGIC_UI.panelBorder}`,
        background: "linear-gradient(180deg, rgba(15,23,42,0.78), rgba(7,11,24,0.92))",
        boxShadow: "0 24px 70px rgba(2,6,23,0.45)",
        p: 1.6,
        minWidth: 0,
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

function normalizeMilestoneState(value) {
  const normalized = String(value || "").trim().toLowerCase();
  if (["complete", "active", "failed", "pending", "skipped"].includes(normalized)) return normalized;
  if (normalized === "healthy") return "complete";
  if (normalized === "recovering") return "active";
  if (normalized === "unhealthy") return "failed";
  return "pending";
}

function AgentStartupTimeline({ milestones, startupRole, formatTimestamp }) {
  const rows = Array.isArray(milestones) ? milestones : [];
  if (!rows.length) {
    return (
      <Box
        sx={{
          borderRadius: 2,
          border: `1px dashed ${MAGIC_UI.panelBorder}`,
          px: 1.4,
          py: 1.2,
          color: MAGIC_UI.textMuted,
          fontSize: "0.84rem",
        }}
      >
        Awaiting startup telemetry
      </Box>
    );
  }
  return (
    <Box
      sx={{
        "@keyframes agentHealthFlowPulse": {
          "0%": { boxShadow: "0 0 0 0 rgba(125, 201, 255, 0.38)" },
          "100%": { boxShadow: "0 0 0 10px rgba(125, 201, 255, 0)" },
        },
        "@keyframes agentHealthFlowSpin": {
          "100%": { transform: "rotate(360deg)" },
        },
      }}
    >
      <Box sx={{ display: "grid", gridTemplateColumns: { xs: "1fr", lg: "minmax(0, 1.15fr) minmax(0, 0.85fr)" }, gap: 2 }}>
        <Box sx={{ minWidth: 0 }}>
          {rows.map((step, index) => {
            const isLast = index === rows.length - 1;
            const state = normalizeMilestoneState(step?.state);
            const iconColor =
              state === "complete"
                ? MAGIC_UI.accentC
                : state === "active"
                  ? MAGIC_UI.accentA
                  : state === "failed"
                    ? "#ff7b89"
                    : "rgba(148, 163, 184, 0.58)";
            const labelColor = state === "pending" || state === "skipped" ? MAGIC_UI.textMuted : MAGIC_UI.textBright;
            const detailColor = state === "failed" ? "#ff7b89" : state === "active" ? MAGIC_UI.accentA : MAGIC_UI.textMuted;
            const connectorColor =
              state === "complete"
                ? "linear-gradient(180deg, rgba(52,211,153,0.9), rgba(52,211,153,0.16))"
                : state === "active"
                  ? "linear-gradient(180deg, rgba(125,201,255,0.95), rgba(125,201,255,0.18))"
                  : state === "failed"
                    ? "linear-gradient(180deg, rgba(255,123,137,0.9), rgba(255,123,137,0.18))"
                    : "rgba(148,163,184,0.18)";
            const showStepDetail = state === "active" || state === "failed";
            const timestamp = step?.completed_at || step?.updated_at || step?.started_at || null;
            return (
              <Box key={step?.key || `${index}`} sx={{ display: "flex", alignItems: "stretch", gap: 1.25, width: "100%" }}>
                <Box sx={{ width: 22, display: "flex", flexDirection: "column", alignItems: "center", flexShrink: 0 }}>
                  <Tooltip title={timestamp ? formatTimestamp(timestamp) : ""} arrow placement="left">
                    <Box
                      sx={{
                        width: 20,
                        height: 20,
                        display: "flex",
                        alignItems: "center",
                        justifyContent: "center",
                        color: iconColor,
                        borderRadius: "50%",
                        ...(state === "active"
                          ? { background: "rgba(125, 201, 255, 0.12)", animation: "agentHealthFlowPulse 1.8s ease-out infinite" }
                          : null),
                      }}
                    >
                      {state === "complete" ? (
                        <CheckCircleRoundedIcon sx={{ fontSize: 18 }} />
                      ) : state === "active" ? (
                        <AutorenewRoundedIcon sx={{ fontSize: 18, animation: "agentHealthFlowSpin 1.15s linear infinite" }} />
                      ) : state === "failed" ? (
                        <ErrorOutlineRoundedIcon sx={{ fontSize: 18 }} />
                      ) : (
                        <RadioButtonUncheckedRoundedIcon sx={{ fontSize: 18 }} />
                      )}
                    </Box>
                  </Tooltip>
                  {!isLast ? (
                    <Box sx={{ mt: 0.4, mb: 0.2, width: 2, flexGrow: 1, minHeight: showStepDetail ? 24 : 16, borderRadius: 999, background: connectorColor }} />
                  ) : null}
                </Box>
                <Box sx={{ pt: 0.05, pb: isLast ? 0 : 0.75, minWidth: 0, textAlign: "left" }}>
                  <Typography sx={{ color: labelColor, fontSize: "0.88rem", fontWeight: state === "active" || state === "complete" ? 650 : 500 }}>
                    {String(step?.label || step?.key || `Step ${index + 1}`)}
                  </Typography>
                  {showStepDetail || step?.detail ? (
                    <Typography sx={{ mt: 0.2, color: detailColor, fontSize: "0.76rem", lineHeight: 1.35 }}>
                      {String(step?.detail || "")}
                    </Typography>
                  ) : null}
                </Box>
              </Box>
            );
          })}
        </Box>
        <Box
          sx={{
            minWidth: 0,
            borderRadius: 2,
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            background: "rgba(3,7,18,0.48)",
            p: 1.3,
            alignSelf: "start",
          }}
        >
          <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.82rem", fontWeight: 750 }}>Current Phase</Typography>
          <Typography sx={{ mt: 0.5, color: MAGIC_UI.accentA, fontSize: "0.95rem", fontWeight: 760 }}>
            {startupRole?.detailsMap?.message || startupRole?.detail || "Startup telemetry active"}
          </Typography>
          <Typography sx={{ mt: 0.7, color: MAGIC_UI.textMuted, fontSize: "0.78rem", lineHeight: 1.45 }}>
            Boot ID: {startupRole?.detailsMap?.boot_id || "Unavailable"}
          </Typography>
        </Box>
      </Box>
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
  const roleMeta = useMemo(() => buildAgentHealthMeta(roleRows, "No role telemetry reported yet", `${roleRows.length} roles reporting`), [roleRows]);
  const serviceMeta = useMemo(() => buildAgentHealthMeta(serviceRows, "No service telemetry reported yet", `${serviceRows.length} services reporting`), [serviceRows]);
  const timelineMeta = useMemo(() => {
    if (!milestones.length) return "Awaiting startup telemetry";
    const active = milestones.find((item) => normalizeMilestoneState(item?.state) === "active");
    if (active) return `Active: ${active.label || active.key}`;
    const failed = milestones.find((item) => normalizeMilestoneState(item?.state) === "failed");
    if (failed) return `Needs attention: ${failed.label || failed.key}`;
    return `${milestones.filter((item) => normalizeMilestoneState(item?.state) === "complete").length}/${milestones.length} complete`;
  }, [milestones]);

  const gridHeight = useCallback((count) => Math.max(190, Math.min(360, 58 + Math.max(3, count) * 42)), []);
  const roleColumns = useMemo(() => createAgentHealthColumnDefs("Role", setDialogEntry), []);
  const serviceColumns = useMemo(() => createAgentHealthColumnDefs("Service", setDialogEntry), []);
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
    <Box sx={{ display: "flex", flexDirection: "column", gap: 1.6, minWidth: 0, width: "100%" }}>
      <Island title="Startup Timeline" icon={<DeveloperBoardRoundedIcon sx={{ fontSize: 18 }} />} meta={timelineMeta}>
        <AgentStartupTimeline milestones={milestones} startupRole={startupRole} formatTimestamp={formatTimestamp} />
      </Island>
      <Stack direction={{ xs: "column", xl: "row" }} spacing={1.6} sx={{ minWidth: 0 }}>
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <Island title="Roles" icon={<DeveloperBoardRoundedIcon sx={{ fontSize: 18 }} />} meta={roleMeta}>
            {summaryDataReady ? (
              <SummarySectionGrid sectionKey="agent-health-roles" rowData={roleRows} columnDefs={roleColumns} height={gridHeight(roleRows.length)} />
            ) : (
              <SummaryGridPlaceholder height={gridHeight(roleRows.length)} />
            )}
          </Island>
        </Box>
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <Island title="Services" icon={<HubRoundedIcon sx={{ fontSize: 18 }} />} meta={serviceMeta}>
            {summaryDataReady ? (
              <SummarySectionGrid sectionKey="agent-health-services" rowData={serviceRows} columnDefs={serviceColumns} height={gridHeight(serviceRows.length)} />
            ) : (
              <SummaryGridPlaceholder height={gridHeight(serviceRows.length)} />
            )}
          </Island>
        </Box>
      </Stack>
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
