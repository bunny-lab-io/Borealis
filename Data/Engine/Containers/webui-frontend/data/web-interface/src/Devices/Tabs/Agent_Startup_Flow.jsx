import React, { useEffect, useMemo, useRef, useState } from "react";
import { Box, IconButton, Tooltip, Typography } from "@mui/material";
import CheckCircleRoundedIcon from "@mui/icons-material/CheckCircleRounded";
import ContentCopyRoundedIcon from "@mui/icons-material/ContentCopyRounded";
import DoneRoundedIcon from "@mui/icons-material/DoneRounded";
import RadioButtonUncheckedRoundedIcon from "@mui/icons-material/RadioButtonUncheckedRounded";
import ErrorOutlineRoundedIcon from "@mui/icons-material/ErrorOutlineRounded";
import AutorenewRoundedIcon from "@mui/icons-material/AutorenewRounded";
import RemoveCircleOutlineRoundedIcon from "@mui/icons-material/RemoveCircleOutlineRounded";
import { MAGIC_UI } from "./Shared.jsx";

const TIMELINE_WIDTH = 620;
const ROLE_BRANCH_WIDTH = 430;

const MAIN_TIMELINE_DEFINITIONS = Object.freeze([
  { id: "process_start", label: "Agent process started", milestoneKeys: ["process_start"] },
  { id: "server_config_loaded", label: "Server configuration loaded", milestoneKeys: ["server_config_loaded"] },
  { id: "identity_loaded", label: "Device identity loaded", milestoneKeys: ["identity_loaded"] },
  { id: "engine_authentication", label: "Engine authentication", milestoneKeys: ["authenticating", "authenticated"] },
  { id: "agent_role_loading", label: "Agent role loading", milestoneKeys: ["roles_loading", "roles_ready"], branch: "roles" },
  { id: "status_channel_online", label: "Status channel online", milestoneKeys: ["status_channel_online"] },
  { id: "engine_socket", label: "Engine socket", milestoneKeys: ["socket_connecting", "socket_connected"] },
  { id: "steady_state_online", label: "Agent steady state online", milestoneKeys: ["steady_state_online"] },
]);

const STARTUP_STATE_META = Object.freeze({
  complete: { label: "Complete", color: MAGIC_UI.accentC, bg: "rgba(52, 211, 153, 0.12)", Icon: CheckCircleRoundedIcon },
  active: { label: "Active", color: MAGIC_UI.accentA, bg: "rgba(125, 211, 252, 0.13)", Icon: AutorenewRoundedIcon },
  failed: { label: "Failed", color: "#ff7b89", bg: "rgba(255, 123, 137, 0.13)", Icon: ErrorOutlineRoundedIcon },
  pending: { label: "Pending", color: "rgba(148, 163, 184, 0.72)", bg: "rgba(148, 163, 184, 0.08)", Icon: RadioButtonUncheckedRoundedIcon },
  skipped: { label: "Skipped", color: "rgba(176, 184, 200, 0.72)", bg: "rgba(176, 184, 200, 0.08)", Icon: RemoveCircleOutlineRoundedIcon },
});

const RUNTIME_STATUS_COLOR_BY_CODE = Object.freeze({
  healthy: MAGIC_UI.accentC,
  loaded: MAGIC_UI.accentC,
  recovering: "#ffb347",
  stale: "#ffb347",
  pending: MAGIC_UI.accentA,
  unhealthy: "#ff7b89",
  failed: "#ff7b89",
  unsupported: "rgba(176, 184, 200, 0.74)",
  not_applicable: "rgba(176, 184, 200, 0.74)",
  unknown: "rgba(176, 184, 200, 0.74)",
});

export function normalizeMilestoneState(value) {
  const normalized = String(value || "").trim().toLowerCase();
  if (["complete", "active", "failed", "pending", "skipped"].includes(normalized)) return normalized;
  if (normalized === "healthy") return "complete";
  if (normalized === "recovering") return "active";
  if (normalized === "unhealthy" || normalized === "failed") return "failed";
  return "pending";
}

function buildMilestoneLookup(milestones) {
  return (Array.isArray(milestones) ? milestones : []).reduce((acc, milestone) => {
    const key = String(milestone?.key || "").trim();
    if (key) acc[key] = milestone;
    return acc;
  }, {});
}

function normalizeRuntimeHealthState(statusCode) {
  const normalized = String(statusCode || "").trim().toLowerCase();
  if (normalized === "healthy" || normalized === "loaded") return "complete";
  if (normalized === "recovering" || normalized === "pending" || normalized === "stale") return "active";
  if (normalized === "unhealthy") return "failed";
  if (normalized === "unsupported" || normalized === "not_applicable" || normalized === "no_desktop_environment_active") return "skipped";
  return "pending";
}

function getRuntimeStatusColor(statusCode) {
  const normalized = String(statusCode || "").trim().toLowerCase();
  return RUNTIME_STATUS_COLOR_BY_CODE[normalized] || RUNTIME_STATUS_COLOR_BY_CODE.unknown;
}

function colorWithAlpha(color, alpha) {
  const text = String(color || "").trim();
  const boundedAlpha = Math.min(Math.max(Number(alpha), 0), 1);
  if (/^#[0-9a-f]{6}$/i.test(text)) {
    const suffix = Math.round(boundedAlpha * 255).toString(16).padStart(2, "0");
    return `${text}${suffix}`;
  }
  const rgbaMatch = text.match(/^rgba?\(([^)]+)\)$/i);
  if (rgbaMatch) {
    const channels = rgbaMatch[1].split(",").slice(0, 3).map((part) => part.trim()).join(", ");
    return `rgba(${channels}, ${boundedAlpha})`;
  }
  return text;
}

function resolveGroupedMilestones(definition, milestoneByKey) {
  return (definition.milestoneKeys || [definition.id]).map((key) => {
    const milestone = milestoneByKey[key] || {};
    return {
      key,
      label: milestone?.label || key,
      detail: String(milestone?.detail || "").trim(),
      state: normalizeMilestoneState(milestone?.state),
      timestamp: milestone?.completed_at || milestone?.updated_at || milestone?.started_at || null,
    };
  });
}

function resolveGroupedState(groupedMilestones) {
  if (groupedMilestones.some((milestone) => milestone.state === "failed")) return "failed";
  const terminalState = groupedMilestones[groupedMilestones.length - 1]?.state || "pending";
  if (terminalState === "complete" || terminalState === "skipped") return "complete";
  if (groupedMilestones.some((milestone) => milestone.state === "active" || milestone.state === "complete")) return "active";
  return terminalState;
}

function selectGroupedMilestone(groupedMilestones, state) {
  if (state === "failed") return groupedMilestones.find((milestone) => milestone.state === "failed") || groupedMilestones[0];
  if (state === "active") {
    return (
      [...groupedMilestones].reverse().find((milestone) => milestone.state === "active" || milestone.state === "complete") ||
      groupedMilestones[0]
    );
  }
  if (state === "complete") {
    return [...groupedMilestones].reverse().find((milestone) => milestone.state === "complete" || milestone.state === "skipped") || groupedMilestones[0];
  }
  return groupedMilestones[0];
}

function resolveRuntimeGroupState(runtimeRows, milestoneState) {
  const rows = Array.isArray(runtimeRows) ? runtimeRows : [];
  if (!rows.length && milestoneState === "complete") return "active";
  const runtimeStates = rows.map((entry) => normalizeRuntimeHealthState(entry?.statusCode));
  if (milestoneState === "failed" || runtimeStates.some((state) => state === "failed")) return "failed";
  if (runtimeStates.some((state) => state === "active" || state === "pending")) return "active";
  if (rows.length && runtimeStates.every((state) => state === "complete" || state === "skipped")) return "complete";
  if (milestoneState === "active") return "active";
  return milestoneState || "pending";
}

function buildRuntimeGroupSummary(runtimeRows) {
  const rows = Array.isArray(runtimeRows) ? runtimeRows : [];
  const counts = rows.reduce(
    (acc, entry) => {
      const kind = String(entry?.healthKind || "role").toLowerCase() === "service" ? "service" : "role";
      const state = normalizeRuntimeHealthState(entry?.statusCode);
      if (state === "skipped") {
        acc[kind].skipped += 1;
      } else {
        acc[kind].total += 1;
        if (state === "complete") acc[kind].healthy += 1;
      }
      return acc;
    },
    { role: { total: 0, healthy: 0, skipped: 0 }, service: { total: 0, healthy: 0, skipped: 0 } }
  );
  const pieces = [];
  if (counts.role.total) pieces.push(`${counts.role.healthy}/${counts.role.total} roles healthy`);
  if (counts.service.total) pieces.push(`${counts.service.healthy}/${counts.service.total} services healthy`);
  const skipped = counts.role.skipped + counts.service.skipped;
  if (skipped) pieces.push(`${skipped} not applicable`);
  return pieces.length ? pieces.join(" · ") : "Awaiting role telemetry";
}

function formatHealthBoolean(value, trueLabel = "Yes", falseLabel = "No") {
  if (value === true || String(value).trim().toLowerCase() === "true") return trueLabel;
  if (value === false || String(value).trim().toLowerCase() === "false") return falseLabel;
  return String(value || "").trim();
}

function formatHealthTimestamp(value) {
  const numericValue = Number(value || 0);
  if (!Number.isFinite(numericValue) || numericValue <= 0) return "";
  const timestampMs = numericValue > 1e12 ? numericValue : numericValue * 1000;
  const date = new Date(timestampMs);
  if (Number.isNaN(date.getTime())) return "";
  const dateText = date.toLocaleDateString("en-US", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  });
  const timeText = date.toLocaleTimeString("en-US", {
    hour: "numeric",
    minute: "2-digit",
  });
  return `${dateText} @ ${timeText}`;
}

function readHealthObject(value) {
  if (value && typeof value === "object" && !Array.isArray(value)) return value;
  const text = String(value || "").trim();
  if (!text) return {};
  try {
    const parsed = JSON.parse(text);
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : {};
  } catch {
    return {};
  }
}

export function buildRuntimeHealthTooltipRows(entry) {
  if (!entry) return [];
  const details = entry.detailsMap && typeof entry.detailsMap === "object" ? entry.detailsMap : {};
  const rows = [];
  const addRow = (label, value) => {
    if (value === null || value === undefined || String(value).trim() === "") return;
    rows.push({ label, value: String(value) });
  };
  const addTimestamp = (label, value) => {
    const formatted = formatHealthTimestamp(value);
    if (formatted) addRow(label, formatted);
  };
  const key = String(entry.presentationKey || "").trim().toLowerCase();

  if (entry.lastCheckedText) addRow("Checked", entry.lastCheckedText);
  const statusCode = String(entry.statusCode || entry.status || "").trim().toLowerCase();
  const healthy = ["healthy", "loaded", "ok", "online", "running", "ready", "complete", "completed"].includes(statusCode);
  if (entry.lastSuccessText && (!healthy || entry.lastSuccessText !== entry.lastCheckedText)) {
    addRow("Last healthy", entry.lastSuccessText);
  }
  if (entry.contextLabel && String(entry.contextLabel).trim().toLowerCase() !== "system") {
    const title = String(entry.name || "").trim().toLowerCase();
    if (!title.includes(String(entry.contextLabel).trim().toLowerCase())) addRow("Context", entry.contextLabel);
  }
  const desired = String(entry.desiredState || "").trim().toLowerCase();
  const observed = String(entry.observedState || "").trim().toLowerCase();
  if (!healthy || (desired && desired !== "running") || (observed && !["ready", "running"].includes(observed))) {
    addRow("Desired / observed", `${entry.desiredState || "Unknown"} / ${entry.observedState || "Unknown"}`);
  }
  if (entry.recoveryAttempts) addRow("Recovery attempts", entry.recoveryAttempts);
  if (entry.lastError) addRow("Last error", entry.lastError);

  if (key === "enginesocket") {
    addRow("Socket", details.socket_state);
    if (Number(details.socket_state_age_seconds) >= 0) addRow("State age", `${details.socket_state_age_seconds} seconds`);
  } else if (key === "wireguardtunnel") {
    addRow("Endpoint", details.endpoint);
  } else if (key === "rdp") {
    addRow("Engine address", details.engine_address);
    addRow("Firewall rule", details.firewall_rule);
    if (details.listener_ip || details.listener_port) {
      addRow("Listener", `${details.listener_ip || "0.0.0.0"}:${details.listener_port || "Unknown"}`);
    }
  } else if (key === "vnc" || key === "ultravnc" || key === "ultravncservice") {
    addRow("Allowed Engine IPs", details.allowed_ips);
    addRow("Displays", details.display_count);
    const bounds = readHealthObject(details.display_virtual_bounds || details.display_virtual_bounds_json);
    if (bounds.width && bounds.height) addRow("Desktop bounds", `${bounds.width} × ${bounds.height}`);
  } else if (key === "contextcurrentuser") {
    addRow("Broker", String(details.broker_mode || "").replace(/[_-]+/g, " "));
    addRow("Ready helpers", details.ready_helpers);
    addRow("Loaded sessions", details.loaded_helper_sessions);
  } else if (key === "deviceauditor" || key === "deviceaudit") {
    addRow("CPU telemetry", formatHealthBoolean(details.cpu_present, "Available", "Unavailable"));
    addRow("Memory items", details.memory_items);
    addRow("Network items", details.network_items);
    addRow("Storage items", details.storage_items);
  } else if (key === "filemanagement") {
    addRow("Listener", details.listener_state);
    addRow("Active transfers", details.active_transfers);
    addTimestamp("Last transfer", details.last_transfer_at);
  } else if (key === "patchmanagement") {
    addRow("Available patches", details.patch_count);
    addRow("Install active", formatHealthBoolean(details.install_running));
    addTimestamp("Inventory refreshed", details.last_refresh_at);
    addTimestamp("Last install", details.last_install_at);
  } else if (key === "processmanagement") {
    addRow("Processes", details.process_count);
    addTimestamp("Inventory refreshed", details.last_refresh_at);
  } else if (key === "registrymanagement") {
    addRow("Listener", details.listener_state);
    addRow("Platform", details.platform);
    addRow("Mode", details.service_mode);
    addTimestamp("Last mutation", details.last_mutation_at);
  } else if (key === "remoteshell") {
    addRow("Active session", formatHealthBoolean(details.active_session));
    if (details.listener_ip || details.listener_port) {
      addRow("Listener", `${details.listener_ip || "0.0.0.0"}:${details.listener_port || "Unknown"}`);
    }
  } else if (key === "servicemanagement") {
    addRow("Services", details.service_count);
    addTimestamp("Inventory refreshed", details.last_refresh_at);
  } else if (key === "softwaremanagement") {
    addRow("Software records", details.software_count);
    addTimestamp("Inventory refreshed", details.last_refresh_at);
  }

  if (![
    "enginesocket",
    "wireguardtunnel",
    "rdp",
    "vnc",
    "ultravnc",
    "ultravncservice",
    "contextsystem",
    "contextcurrentuser",
    "deviceauditor",
    "deviceaudit",
    "filemanagement",
    "patchmanagement",
    "processmanagement",
    "registrymanagement",
    "remoteshell",
    "servicemanagement",
    "softwaremanagement",
  ].includes(key)) {
    const hiddenKeys = new Set([
      "desired_state",
      "observed_state",
      "running_status",
      "runtime",
      "supervisor_revision",
      "refresh_interval_ms",
    ]);
    Object.entries(details)
      .filter(([detailKey, value]) => {
        const normalizedKey = String(detailKey || "").trim().toLowerCase();
        return (
          !hiddenKeys.has(normalizedKey) &&
          !normalizedKey.endsWith("_json") &&
          value !== null &&
          value !== undefined &&
          String(value).trim() !== ""
        );
      })
      .slice(0, 5)
      .forEach(([detailKey, value]) => addRow(String(detailKey).replace(/[_-]+/g, " "), value));
  }

  return rows;
}

export function getRuntimeHealthTooltipDetail(entry, rows = buildRuntimeHealthTooltipRows(entry)) {
  const detail = String(entry?.detail || "").trim();
  if (!detail) return "";
  const statusCode = String(entry?.statusCode || entry?.status || "").trim().toLowerCase();
  const healthy = ["healthy", "loaded", "ok", "online", "running", "ready", "complete", "completed"].includes(statusCode);
  const key = String(entry?.presentationKey || "").trim().toLowerCase();
  const hasRoleSpecificEvidence = (Array.isArray(rows) ? rows : []).some(
    (row) => !["Checked", "Last healthy", "Context", "Desired / observed", "Recovery attempts", "Last error"].includes(row?.label)
  );
  if (!healthy || key === "contextsystem" || !hasRoleSpecificEvidence) return detail;
  return "";
}

async function copyHealthText(text) {
  if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  if (typeof document === "undefined") return;
  const input = document.createElement("textarea");
  input.value = text;
  input.style.position = "fixed";
  input.style.opacity = "0";
  document.body.appendChild(input);
  input.focus();
  input.select();
  document.execCommand("copy");
  input.remove();
}

export function buildHealthCopyText({ title, status = "", rows = [], detail = "" }) {
  const cleanRows = Array.isArray(rows)
    ? rows.filter((row) => row?.label && row?.value !== null && row?.value !== undefined && String(row.value).trim() !== "")
    : [];
  return [
    title,
    status ? `Status: ${status}` : "",
    ...cleanRows.map((row) => `${row.label}: ${row.value}`),
    detail ? `\n${detail}` : "",
  ]
    .filter(Boolean)
    .join("\n");
}

export function CopyHealthButton({ copyText = "", label = "Copy health details", sx = {} }) {
  const [copied, setCopied] = useState(false);
  const resetTimerRef = useRef(null);

  useEffect(
    () => () => {
      if (resetTimerRef.current) window.clearTimeout(resetTimerRef.current);
    },
    []
  );

  const handleCopy = async (event) => {
    event.preventDefault();
    event.stopPropagation();
    if (!copyText) return;
    try {
      await copyHealthText(copyText);
      setCopied(true);
      if (resetTimerRef.current) window.clearTimeout(resetTimerRef.current);
      resetTimerRef.current = window.setTimeout(() => setCopied(false), 1600);
    } catch {
      setCopied(false);
    }
  };

  return (
    <IconButton
      size="small"
      aria-label={copied ? `${label} copied` : label}
      title={copied ? "Copied" : label}
      onMouseDown={(event) => event.stopPropagation()}
      onClick={handleCopy}
      sx={{ p: 0.35, flexShrink: 0, color: copied ? MAGIC_UI.accentC : MAGIC_UI.accentA, ...sx }}
    >
      {copied ? <DoneRoundedIcon sx={{ fontSize: 16 }} /> : <ContentCopyRoundedIcon sx={{ fontSize: 15 }} />}
    </IconButton>
  );
}

export function CopyableHealthTooltip({ title, status = "", rows = [], detail = "" }) {
  const cleanRows = Array.isArray(rows)
    ? rows.filter((row) => row?.label && row?.value !== null && row?.value !== undefined && String(row.value).trim() !== "")
    : [];
  const copyText = buildHealthCopyText({ title, status, rows: cleanRows, detail });

  return (
    <Box sx={{ width: 280, maxWidth: "calc(100vw - 64px)", py: 0.25 }}>
      <Box sx={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: 1 }}>
        <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.8rem", fontWeight: 760, lineHeight: 1.25 }}>
          {title || "Health details"}
        </Typography>
        <CopyHealthButton copyText={copyText} />
      </Box>
      {status ? (
        <Typography sx={{ mt: 0.45, color: MAGIC_UI.textMuted, fontSize: "0.7rem", lineHeight: 1.35 }}>
          <Box component="span" sx={{ color: MAGIC_UI.textBright, fontWeight: 760 }}>
            Status:
          </Box>{" "}
          {status}
        </Typography>
      ) : null}
      {cleanRows.map((row, index) => (
        <Typography key={`${row.label}-${index}`} sx={{ color: MAGIC_UI.textMuted, fontSize: "0.68rem", lineHeight: 1.35 }}>
          <Box component="span" sx={{ color: MAGIC_UI.textBright, fontWeight: 760 }}>
            {row.label}:
          </Box>{" "}
          {row.value}
        </Typography>
      ))}
      {detail ? (
        <Typography sx={{ mt: 0.65, color: MAGIC_UI.textBright, fontSize: "0.69rem", lineHeight: 1.35, whiteSpace: "pre-wrap" }}>
          {detail}
        </Typography>
      ) : null}
    </Box>
  );
}

function buildRuntimeHealthTooltip(entry) {
  if (!entry) return "";
  const rows = buildRuntimeHealthTooltipRows(entry);
  return (
    <CopyableHealthTooltip
      title={entry.name || "Runtime health"}
      status={entry.status || "Unknown"}
      rows={rows}
      detail={getRuntimeHealthTooltipDetail(entry, rows)}
    />
  );
}

function getConnectorColor(state) {
  if (state === "complete") return "linear-gradient(180deg, rgba(52,211,153,0.9), rgba(52,211,153,0.16))";
  if (state === "active") return "linear-gradient(180deg, rgba(125,211,252,0.95), rgba(125,211,252,0.18))";
  if (state === "failed") return "linear-gradient(180deg, rgba(255,123,137,0.9), rgba(255,123,137,0.18))";
  return "rgba(148,163,184,0.18)";
}

function TimelineIcon({ state }) {
  const meta = STARTUP_STATE_META[state] || STARTUP_STATE_META.pending;
  const Icon = meta.Icon;
  return (
    <Box
      sx={{
        width: 24,
        height: 24,
        borderRadius: "50%",
        color: meta.color,
        background: state === "active" ? meta.bg : "transparent",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        ...(state === "active"
          ? {
              animation: "agentTimelinePulse 1.8s ease-out infinite",
            }
          : null),
      }}
    >
      <Icon
        sx={{
          fontSize: 20,
          animation: state === "active" ? "agentTimelineSpin 1.15s linear infinite" : "none",
        }}
      />
    </Box>
  );
}

export function RuntimeRoleHealthBreakdown({ runtimeRows, sx = {} }) {
  const rows = Array.isArray(runtimeRows) ? runtimeRows : [];
  const groups = [
    { key: "role", label: "Roles", rows: rows.filter((entry) => String(entry?.healthKind || "role").toLowerCase() !== "service") },
    { key: "service", label: "Services", rows: rows.filter((entry) => String(entry?.healthKind || "").toLowerCase() === "service") },
  ].filter((group) => group.rows.length);
  const state = resolveRuntimeGroupState(rows, rows.length ? "complete" : "active");
  const meta = STARTUP_STATE_META[state] || STARTUP_STATE_META.pending;

  return (
    <Box
      sx={{
        borderRadius: 2,
        border: `1px solid ${state === "pending" ? "rgba(148, 163, 184, 0.26)" : meta.color}`,
        background: `linear-gradient(145deg, rgba(7,11,24,0.96), ${meta.bg})`,
        boxShadow:
          state === "active"
            ? `0 0 0 1px ${meta.color}44, 0 18px 44px rgba(2,6,23,0.58), 0 0 28px ${meta.color}28`
            : "0 16px 36px rgba(2,6,23,0.46)",
        p: 1.1,
        "@keyframes agentTimelineSpin": {
          from: { transform: "rotate(0deg)" },
          to: { transform: "rotate(360deg)" },
        },
        ...sx,
      }}
    >
      <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.78rem", fontWeight: 760, lineHeight: 1.2 }}>
        Runtime role health
      </Typography>
      <Typography sx={{ mt: 0.2, color: state === "active" ? MAGIC_UI.accentA : MAGIC_UI.textMuted, fontSize: "0.67rem", lineHeight: 1.25 }}>
        {buildRuntimeGroupSummary(rows)}
      </Typography>
      <Box sx={{ mt: 0.9, display: "flex", flexDirection: "column", gap: 0.85 }}>
        {groups.length ? (
          groups.map((group, groupIndex) => (
            <Box key={group.key} sx={{ minWidth: 0, mt: groupIndex > 0 ? 0.7 : 0 }}>
              <Typography
                sx={{
                  mb: 0.35,
                  color: MAGIC_UI.textMuted,
                  fontSize: "0.58rem",
                  fontWeight: 800,
                  letterSpacing: "0.08em",
                  textTransform: "uppercase",
                }}
              >
                {group.label}
              </Typography>
              <Box sx={{ display: "flex", flexDirection: "column", gap: 0.35 }}>
                {group.rows.map((entry, index) => {
                  const rowState = normalizeRuntimeHealthState(entry?.statusCode);
                  const rowColor = getRuntimeStatusColor(entry?.statusCode);
                  const rowMeta = STARTUP_STATE_META[rowState] || STARTUP_STATE_META.pending;
                  const RowIcon = rowMeta.Icon;
                  return (
                    <Tooltip key={entry?.id || `${group.key}-${index}`} title={buildRuntimeHealthTooltip(entry)} arrow placement="right">
                      <Box
                        sx={{
                          width: "100%",
                          minHeight: 31,
                          px: 0.65,
                          py: 0.4,
                          borderRadius: 1.3,
                          border: `1px solid ${colorWithAlpha(rowColor, 0.28)}`,
                          background: colorWithAlpha(rowColor, 0.06),
                          color: MAGIC_UI.textBright,
                          display: "flex",
                          alignItems: "center",
                          justifyContent: "flex-start",
                          gap: 0.65,
                          textAlign: "left",
                          overflow: "hidden",
                          cursor: "help",
                          "&:hover": {
                            borderColor: colorWithAlpha(rowColor, 0.68),
                            background: colorWithAlpha(rowColor, 0.11),
                          },
                        }}
                      >
                        <RowIcon
                          sx={{
                            color: rowColor,
                            fontSize: 15,
                            flexShrink: 0,
                            animation: rowState === "active" ? "agentTimelineSpin 1.15s linear infinite" : "none",
                          }}
                        />
                        <Box sx={{ minWidth: 0, flex: 1 }}>
                          <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.66rem", fontWeight: 740, lineHeight: 1.12 }} noWrap>
                            {entry?.name || `Runtime ${index + 1}`}
                          </Typography>
                          <Typography sx={{ mt: 0.1, color: rowColor, fontSize: "0.59rem", fontWeight: 700, lineHeight: 1.1 }} noWrap>
                            {entry?.status || "Unknown"}
                          </Typography>
                        </Box>
                      </Box>
                    </Tooltip>
                  );
                })}
              </Box>
            </Box>
          ))
        ) : (
          <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.68rem", lineHeight: 1.35 }}>
            Role telemetry has not reported yet.
          </Typography>
        )}
      </Box>
    </Box>
  );
}

export function RuntimeRoleHealthSidebarRows({ runtimeRows }) {
  const rows = Array.isArray(runtimeRows) ? runtimeRows : [];
  if (!rows.length) {
    return (
      <Typography sx={{ px: 1, py: 0.8, color: MAGIC_UI.textMuted, fontSize: "0.66rem", lineHeight: 1.35 }}>
        Role telemetry has not reported yet.
      </Typography>
    );
  }

  return (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        gap: 0.2,
        p: 0.35,
        "@keyframes agentTimelineSpin": {
          from: { transform: "rotate(0deg)" },
          to: { transform: "rotate(360deg)" },
        },
      }}
    >
      {rows.map((entry, index) => {
        const rowState = normalizeRuntimeHealthState(entry?.statusCode);
        const rowColor = getRuntimeStatusColor(entry?.statusCode);
        const rowMeta = STARTUP_STATE_META[rowState] || STARTUP_STATE_META.pending;
        const RowIcon = rowMeta.Icon;
        return (
          <Tooltip key={entry?.id || `runtime-sidebar-${index}`} title={buildRuntimeHealthTooltip(entry)} arrow placement="right">
            <Box
              aria-label={`${entry?.name || `Runtime ${index + 1}`}: ${entry?.status || "Unknown"}`}
              sx={{
                minHeight: 34,
                px: 0.8,
                py: 0.45,
                borderRadius: 1,
                color: MAGIC_UI.textBright,
                display: "flex",
                alignItems: "center",
                gap: 0.7,
                minWidth: 0,
                cursor: "help",
                transition: "background 140ms ease",
                "&:hover": {
                  background: "rgba(125,183,255,0.08)",
                },
              }}
            >
              <RowIcon
                sx={{
                  color: rowColor,
                  fontSize: 15,
                  flexShrink: 0,
                  animation: rowState === "active" ? "agentTimelineSpin 1.15s linear infinite" : "none",
                }}
              />
              <Box sx={{ minWidth: 0, flex: 1 }}>
                <Typography sx={{ color: "#cbd5e1", fontSize: "0.67rem", fontWeight: 500, lineHeight: 1.15 }} noWrap>
                  {entry?.name || `Runtime ${index + 1}`}
                </Typography>
              </Box>
            </Box>
          </Tooltip>
        );
      })}
    </Box>
  );
}

function RuntimeBranch({ runtimeRows }) {
  const rows = Array.isArray(runtimeRows) ? runtimeRows : [];
  const state = resolveRuntimeGroupState(rows, rows.length ? "complete" : "active");
  const meta = STARTUP_STATE_META[state] || STARTUP_STATE_META.pending;

  return (
    <Box
      sx={{
        position: "relative",
        width: { xs: "100%", lg: ROLE_BRANCH_WIDTH },
        ml: { xs: 0, lg: 2.2 },
        mt: { xs: 1.15, lg: 0 },
        flexShrink: 0,
        "&::before": {
          content: '""',
          position: "absolute",
          left: { xs: 14, lg: -28 },
          top: { xs: -12, lg: 26 },
          width: { xs: 2, lg: 28 },
          height: { xs: 12, lg: 2 },
          borderRadius: 999,
          background: meta.color,
          opacity: state === "pending" ? 0.35 : 0.9,
        },
      }}
    >
      <RuntimeRoleHealthBreakdown runtimeRows={rows} />
    </Box>
  );
}

function TimelineStep({ step, isLast, runtimeRows }) {
  const meta = STARTUP_STATE_META[step.state] || STARTUP_STATE_META.pending;
  const showDetail = step.state !== "pending" || Boolean(step.detail);
  const detailColor = step.state === "failed" ? "#ff7b89" : step.state === "active" ? MAGIC_UI.accentA : MAGIC_UI.textMuted;
  return (
    <Box sx={{ display: "flex", alignItems: "stretch", width: "100%" }}>
      <Box
        sx={{
          width: 28,
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          flexShrink: 0,
        }}
      >
        <TimelineIcon state={step.state} />
        {!isLast ? (
          <Box
            sx={{
              mt: 0.45,
              mb: 0.2,
              width: 2,
              flexGrow: 1,
              minHeight: step.branch ? 135 : showDetail ? 34 : 22,
              borderRadius: 999,
              background: getConnectorColor(step.state),
            }}
          />
        ) : null}
      </Box>
      <Box sx={{ flex: 1, minWidth: 0, pb: isLast ? 0 : 0.9 }}>
        <Box sx={{ display: { xs: "block", lg: step.branch ? "flex" : "block" }, alignItems: "flex-start", minWidth: 0 }}>
          <Tooltip title={step.timestampText || ""} arrow placement="top">
            <Box
              sx={{
                width: "100%",
                maxWidth: TIMELINE_WIDTH,
                borderRadius: 2,
                border: `1px solid ${step.state === "pending" ? "rgba(148, 163, 184, 0.2)" : colorWithAlpha(meta.color, 0.64)}`,
                background: `linear-gradient(145deg, rgba(7,11,24,0.78), ${meta.bg})`,
                px: 1.15,
                py: 0.9,
                boxShadow: step.state === "active" ? `0 0 0 1px ${meta.color}33, 0 0 24px ${meta.color}22` : "none",
              }}
            >
              <Typography
                sx={{
                  color: step.state === "pending" || step.state === "skipped" ? MAGIC_UI.textMuted : MAGIC_UI.textBright,
                  fontSize: "0.84rem",
                  fontWeight: step.state === "active" || step.state === "complete" ? 700 : 600,
                  lineHeight: 1.22,
                }}
              >
                {step.label}
              </Typography>
              {showDetail ? (
                <Typography sx={{ mt: 0.3, color: detailColor, fontSize: "0.72rem", lineHeight: 1.35 }}>
                  {step.detail || meta.label}
                </Typography>
              ) : null}
            </Box>
          </Tooltip>
          {step.branch ? <RuntimeBranch runtimeRows={runtimeRows} /> : null}
        </Box>
      </Box>
    </Box>
  );
}

function useTimelineSteps(milestones, runtimeRows, formatTimestamp) {
  return useMemo(() => {
    const milestoneByKey = buildMilestoneLookup(milestones);
    return MAIN_TIMELINE_DEFINITIONS.map((definition) => {
      const groupedMilestones = resolveGroupedMilestones(definition, milestoneByKey);
      const milestoneState = resolveGroupedState(groupedMilestones);
      const state = definition.id === "agent_role_loading" ? resolveRuntimeGroupState(runtimeRows, milestoneState) : milestoneState;
      const displayMilestone = selectGroupedMilestone(groupedMilestones, state) || {};
      return {
        id: definition.id,
        label: definition.label,
        branch: definition.branch,
        state,
        detail: displayMilestone.detail || STARTUP_STATE_META[state]?.label || "",
        timestampText: displayMilestone.timestamp ? formatTimestamp(displayMilestone.timestamp) : "",
      };
    });
  }, [formatTimestamp, milestones, runtimeRows]);
}

export default function AgentStartupFlow({
  milestones,
  runtimeRows = [],
  formatTimestamp = (value) => String(value || ""),
}) {
  const rows = Array.isArray(milestones) ? milestones : [];
  const runtimeHealthRows = Array.isArray(runtimeRows) ? runtimeRows : [];
  const timelineSteps = useTimelineSteps(rows, runtimeRows, formatTimestamp);

  if (!rows.length && !runtimeHealthRows.length) {
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
        height: "100%",
        minHeight: { xs: 520, md: 620 },
        width: "100%",
        overflow: "auto",
        borderRadius: 2,
        background:
          "linear-gradient(180deg, rgba(3,7,18,0.22), rgba(3,7,18,0.42)), radial-gradient(circle at 25% 25%, rgba(125,211,252,0.08), transparent 34%)",
        p: { xs: 1.1, md: 1.5 },
        "@keyframes agentTimelineSpin": {
          from: { transform: "rotate(0deg)" },
          to: { transform: "rotate(360deg)" },
        },
        "@keyframes agentTimelinePulse": {
          "0%": { boxShadow: "0 0 0 0 rgba(125, 211, 252, 0.26)" },
          "70%": { boxShadow: "0 0 0 10px rgba(125, 211, 252, 0)" },
          "100%": { boxShadow: "0 0 0 0 rgba(125, 211, 252, 0)" },
        },
      }}
    >
      <Box sx={{ width: "100%", maxWidth: 1120, mx: "auto", display: "flex", flexDirection: "column", alignItems: "stretch" }}>
        {timelineSteps.map((step, index) => (
          <TimelineStep
            key={step.id}
            step={step}
            isLast={index === timelineSteps.length - 1}
            runtimeRows={runtimeRows}
          />
        ))}
      </Box>
    </Box>
  );
}
