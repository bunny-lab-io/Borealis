import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Box, GlobalStyles, IconButton, Tooltip, Typography } from "@mui/material";
import { animate, stagger } from "animejs";
import ContentCopyRoundedIcon from "@mui/icons-material/ContentCopyRounded";
import CheckCircleRoundedIcon from "@mui/icons-material/CheckCircleRounded";
import RadioButtonUncheckedRoundedIcon from "@mui/icons-material/RadioButtonUncheckedRounded";
import ErrorOutlineRoundedIcon from "@mui/icons-material/ErrorOutlineRounded";
import AutorenewRoundedIcon from "@mui/icons-material/AutorenewRounded";
import RemoveCircleOutlineRoundedIcon from "@mui/icons-material/RemoveCircleOutlineRounded";
import ReactFlow, { Background, Handle, Position, applyNodeChanges } from "reactflow";
import "reactflow/dist/style.css";
import { MAGIC_UI } from "./Shared.jsx";

const HIDDEN_HANDLE_STYLE = {
  width: 10,
  height: 10,
  border: "none",
  background: "transparent",
  opacity: 0,
  pointerEvents: "none",
};

const STARTUP_FLOW_NODE_WIDTH = 256;
const RUNTIME_FLOW_GROUP_WIDTH = 390;
const STEADY_FLOW_X = 2940;
const STEADY_FLOW_Y = 315;
const SNAP_THRESHOLD_PX = 8;
const DEFAULT_FLOW_VIEWPORT = Object.freeze({ x: 40, y: 120, zoom: 0.5 });
const FLOW_DRAFT_LAYOUT_STORAGE_KEY = "borealis.agentHealth.startupFlowDraftLayout.v1";

const STARTUP_FLOW_DEFINITIONS = Object.freeze([
  { id: "process_start", label: "Agent process started", milestoneKeys: ["process_start"], x: 20, y: 315 },
  { id: "server_config_loaded", label: "Server configuration loaded", milestoneKeys: ["server_config_loaded"], x: 360, y: 190 },
  { id: "identity_loaded", label: "Device identity loaded", milestoneKeys: ["identity_loaded"], x: 360, y: 440 },
  { id: "engine_authentication", label: "Engine authentication", milestoneKeys: ["authenticating", "authenticated"], x: 770, y: 315 },
  { id: "status_channel_online", label: "Status channel online", milestoneKeys: ["status_channel_online"], x: 1180, y: 100 },
  { id: "agent_role_loading", label: "Agent role loading", milestoneKeys: ["roles_loading", "roles_ready"], x: 1180, y: 315 },
  { id: "engine_socket", label: "Engine socket", milestoneKeys: ["socket_connecting", "socket_connected"], x: 1180, y: 530 },
]);

const STARTUP_FLOW_EDGES = Object.freeze([
  ["process_start", "server_config_loaded"],
  ["process_start", "identity_loaded"],
  ["server_config_loaded", "engine_authentication"],
  ["identity_loaded", "engine_authentication"],
  ["engine_authentication", "status_channel_online"],
  ["engine_authentication", "agent_role_loading"],
  ["engine_authentication", "engine_socket"],
]);

const STARTUP_STATE_META = Object.freeze({
  complete: { label: "Complete", color: MAGIC_UI.accentC, bg: "rgba(52, 211, 153, 0.12)", Icon: CheckCircleRoundedIcon },
  active: { label: "Active", color: MAGIC_UI.accentA, bg: "rgba(125, 211, 252, 0.13)", Icon: AutorenewRoundedIcon },
  failed: { label: "Failed", color: "#ff7b89", bg: "rgba(255, 123, 137, 0.13)", Icon: ErrorOutlineRoundedIcon },
  pending: { label: "Pending", color: "rgba(148, 163, 184, 0.72)", bg: "rgba(148, 163, 184, 0.08)", Icon: RadioButtonUncheckedRoundedIcon },
  skipped: { label: "Skipped", color: "rgba(176, 184, 200, 0.72)", bg: "rgba(176, 184, 200, 0.08)", Icon: RemoveCircleOutlineRoundedIcon },
});

const EDGE_STYLE_BY_STATE = Object.freeze({
  complete: { stroke: MAGIC_UI.accentC, strokeWidth: 2.25, strokeDasharray: "6 3" },
  active: { stroke: MAGIC_UI.accentA, strokeWidth: 2.5, strokeDasharray: "6 3" },
  failed: { stroke: "#ff7b89", strokeWidth: 2.5, strokeDasharray: "6 3" },
  pending: { stroke: "rgba(148, 163, 184, 0.34)", strokeWidth: 1.8, strokeDasharray: "6 5" },
  skipped: { stroke: "rgba(176, 184, 200, 0.36)", strokeWidth: 1.8, strokeDasharray: "5 6" },
});

const RUNTIME_STATUS_COLOR_BY_CODE = Object.freeze({
  healthy: MAGIC_UI.accentC,
  loaded: MAGIC_UI.accentC,
  recovering: "#ffb347",
  pending: MAGIC_UI.accentA,
  unhealthy: "#ff7b89",
  unsupported: "rgba(176, 184, 200, 0.74)",
  unknown: "rgba(176, 184, 200, 0.74)",
});

export function normalizeMilestoneState(value) {
  const normalized = String(value || "").trim().toLowerCase();
  if (["complete", "active", "failed", "pending", "skipped"].includes(normalized)) return normalized;
  if (normalized === "healthy") return "complete";
  if (normalized === "recovering") return "active";
  if (normalized === "unhealthy") return "failed";
  return "pending";
}

function getEdgeState(sourceState, targetState) {
  if (targetState === "failed" || sourceState === "failed") return "failed";
  if (targetState === "active") return "active";
  if (targetState === "skipped") return "skipped";
  if (targetState === "complete" && sourceState === "complete") return "complete";
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
  if (normalized === "recovering" || normalized === "pending") return "active";
  if (normalized === "unhealthy") return "failed";
  if (normalized === "unsupported") return "skipped";
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
      acc[kind].total += 1;
      if (state === "complete") acc[kind].healthy += 1;
      if (state === "failed") acc[kind].failed += 1;
      if (state === "active" || state === "pending") acc[kind].working += 1;
      return acc;
    },
    {
      role: { total: 0, healthy: 0, failed: 0, working: 0 },
      service: { total: 0, healthy: 0, failed: 0, working: 0 },
    }
  );
  const pieces = [];
  if (counts.role.total) pieces.push(`${counts.role.healthy}/${counts.role.total} roles healthy`);
  if (counts.service.total) pieces.push(`${counts.service.healthy}/${counts.service.total} services healthy`);
  return {
    counts,
    text: pieces.length ? pieces.join(" · ") : "Awaiting role telemetry",
  };
}

function buildRuntimeHealthTooltip(entry) {
  if (!entry) return "";
  const details = entry.detailsMap && typeof entry.detailsMap === "object" ? entry.detailsMap : {};
  const detailRows = Object.entries(details)
    .filter(([, value]) => value !== null && value !== undefined && String(value).trim() !== "")
    .slice(0, 8);
  return (
    <Box sx={{ maxWidth: 360, py: 0.25 }}>
      <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.8rem", fontWeight: 760, lineHeight: 1.25 }}>
        {entry.name || "Runtime health"}
      </Typography>
      <Typography sx={{ mt: 0.55, color: MAGIC_UI.textMuted, fontSize: "0.7rem", lineHeight: 1.35 }}>
        Status: {entry.status || "Unknown"}
      </Typography>
      {entry.lastCheckedText ? (
        <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.7rem", lineHeight: 1.35 }}>
          Last checked: {entry.lastCheckedText}
        </Typography>
      ) : null}
      {entry.contextLabel ? (
        <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.7rem", lineHeight: 1.35 }}>
          Context: {entry.contextLabel}
        </Typography>
      ) : null}
      {detailRows.length ? (
        <Box sx={{ mt: 0.55 }}>
          {detailRows.map(([key, value]) => (
            <Typography key={key} sx={{ color: MAGIC_UI.textMuted, fontSize: "0.68rem", lineHeight: 1.35 }}>
              {String(key).replace(/[_-]+/g, " ")}: {String(value)}
            </Typography>
          ))}
        </Box>
      ) : null}
      {entry.detail ? (
        <Typography sx={{ mt: 0.65, color: MAGIC_UI.textBright, fontSize: "0.69rem", lineHeight: 1.35, whiteSpace: "pre-wrap" }}>
          {entry.detail}
        </Typography>
      ) : null}
    </Box>
  );
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

function AgentStartupFlowNode({ data }) {
  const state = normalizeMilestoneState(data?.state);
  const meta = STARTUP_STATE_META[state] || STARTUP_STATE_META.pending;
  const Icon = meta.Icon;
  const detail = String(data?.detail || meta.label).trim();
  const timestamp = data?.timestampText || "";
  return (
    <Tooltip title={timestamp} arrow placement="top">
      <Box
        className={`agent-startup-card agent-startup-card-${state}`}
        sx={{
          width: STARTUP_FLOW_NODE_WIDTH,
          minHeight: 66,
          px: 1.25,
          py: 1,
          borderRadius: 2,
          border: `1px solid ${state === "pending" ? "rgba(148, 163, 184, 0.26)" : meta.color}`,
          background: `linear-gradient(145deg, rgba(7,11,24,0.96), ${meta.bg})`,
          boxShadow:
            state === "active"
              ? `0 0 0 1px ${meta.color}44, 0 18px 44px rgba(2,6,23,0.62), 0 0 28px ${meta.color}36`
              : "0 16px 36px rgba(2,6,23,0.54)",
          display: "flex",
          alignItems: "center",
          gap: 1,
          textAlign: "left",
          userSelect: "none",
          position: "relative",
          overflow: "hidden",
        }}
      >
        <Handle type="target" position={Position.Left} style={HIDDEN_HANDLE_STYLE} isConnectable={false} />
        <Handle type="source" position={Position.Right} style={HIDDEN_HANDLE_STYLE} isConnectable={false} />
        <Box
          sx={{
            width: 24,
            height: 24,
            borderRadius: "50%",
            flexShrink: 0,
            color: meta.color,
            background: meta.bg,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
          }}
        >
          <Icon
            sx={{
              fontSize: 19,
              animation: state === "active" ? "agentStartupFlowSpin 1.15s linear infinite" : "none",
            }}
          />
        </Box>
        <Box sx={{ minWidth: 0, flex: 1 }}>
          <Typography sx={{ color: state === "pending" || state === "skipped" ? MAGIC_UI.textMuted : MAGIC_UI.textBright, fontSize: "0.77rem", fontWeight: 720, lineHeight: 1.22 }} noWrap>
            {data?.label || data?.id}
          </Typography>
          <Typography sx={{ mt: 0.25, color: state === "failed" ? "#ff7b89" : state === "active" ? MAGIC_UI.accentA : MAGIC_UI.textMuted, fontSize: "0.68rem", lineHeight: 1.2 }} noWrap>
            {detail}
          </Typography>
        </Box>
      </Box>
    </Tooltip>
  );
}

function AgentRuntimeHealthGroupNode({ data }) {
  const rows = Array.isArray(data?.runtimeRows) ? data.runtimeRows : [];
  const state = normalizeMilestoneState(data?.state);
  const groupMeta = STARTUP_STATE_META[state] || STARTUP_STATE_META.pending;
  const GroupIcon = groupMeta.Icon;
  const summary = buildRuntimeGroupSummary(rows);
  const timestamp = data?.timestampText || "";
  const groupedRows = [
    { key: "role", label: "Roles", rows: rows.filter((entry) => String(entry?.healthKind || "role").toLowerCase() !== "service") },
    { key: "service", label: "Services", rows: rows.filter((entry) => String(entry?.healthKind || "").toLowerCase() === "service") },
  ].filter((group) => group.rows.length);
  return (
    <Box
      className={`agent-startup-card agent-runtime-health-group agent-startup-card-${state}`}
      sx={{
        width: RUNTIME_FLOW_GROUP_WIDTH,
        minHeight: 88,
        px: 1.15,
        py: 1,
        borderRadius: 2,
        border: `1px solid ${state === "pending" ? "rgba(148, 163, 184, 0.26)" : groupMeta.color}`,
        background: `linear-gradient(145deg, rgba(7,11,24,0.97), ${groupMeta.bg})`,
        boxShadow:
          state === "active"
            ? `0 0 0 1px ${groupMeta.color}44, 0 18px 44px rgba(2,6,23,0.62), 0 0 28px ${groupMeta.color}36`
            : "0 16px 36px rgba(2,6,23,0.54)",
        color: MAGIC_UI.textBright,
        textAlign: "left",
        userSelect: "none",
        position: "relative",
        overflow: "hidden",
      }}
    >
      <Handle type="target" position={Position.Left} style={HIDDEN_HANDLE_STYLE} isConnectable={false} />
      <Handle type="source" position={Position.Right} style={HIDDEN_HANDLE_STYLE} isConnectable={false} />
      <Tooltip title={timestamp} arrow placement="top">
        <Box sx={{ display: "flex", alignItems: "center", gap: 1, minWidth: 0 }}>
          <Box
            sx={{
              width: 24,
              height: 24,
              borderRadius: "50%",
              flexShrink: 0,
              color: groupMeta.color,
              background: groupMeta.bg,
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
            }}
          >
            <GroupIcon
              sx={{
                fontSize: 19,
                animation: state === "active" ? "agentStartupFlowSpin 1.15s linear infinite" : "none",
              }}
            />
          </Box>
          <Box sx={{ minWidth: 0, flex: 1 }}>
            <Typography sx={{ color: state === "pending" || state === "skipped" ? MAGIC_UI.textMuted : MAGIC_UI.textBright, fontSize: "0.78rem", fontWeight: 760, lineHeight: 1.2 }} noWrap>
              {data?.label || "Agent runtime health"}
            </Typography>
            <Typography sx={{ mt: 0.25, color: state === "failed" ? "#ff7b89" : state === "active" ? MAGIC_UI.accentA : MAGIC_UI.textMuted, fontSize: "0.68rem", lineHeight: 1.2 }} noWrap>
              {summary.text}
            </Typography>
          </Box>
        </Box>
      </Tooltip>
      <Box sx={{ mt: 0.9, display: "flex", flexDirection: "column", gap: 0.7 }}>
        {groupedRows.length ? (
          groupedRows.map((group, groupIndex) => (
            <Box key={group.key} sx={{ minWidth: 0, mt: groupIndex > 0 ? 0.75 : 0 }}>
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
                    <Tooltip
                      key={entry?.id || `${group.key}-${index}`}
                      title={buildRuntimeHealthTooltip(entry)}
                      arrow
                      placement="right"
                    >
                      <Box
                        className={`nodrag agent-runtime-health-row agent-runtime-health-row-${rowState}`}
                        sx={{
                          width: "100%",
                          minHeight: 32,
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
                            animation: rowState === "active" ? "agentStartupFlowSpin 1.15s linear infinite" : "none",
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

const STARTUP_FLOW_NODE_TYPES = Object.freeze({
  startupMilestone: AgentStartupFlowNode,
  runtimeHealthGroup: AgentRuntimeHealthGroupNode,
});

function useStartupFlowElements(milestones, runtimeRows, formatTimestamp) {
  return useMemo(() => {
    const milestoneByKey = buildMilestoneLookup(milestones);
    const stateByKey = {};
    const milestoneNodes = STARTUP_FLOW_DEFINITIONS.map((definition) => {
      const groupedMilestones = resolveGroupedMilestones(definition, milestoneByKey);
      const milestoneState = resolveGroupedState(groupedMilestones);
      const state =
        definition.id === "agent_role_loading"
          ? resolveRuntimeGroupState(runtimeRows, milestoneState)
          : milestoneState;
      const displayMilestone = selectGroupedMilestone(groupedMilestones, state) || {};
      stateByKey[definition.id] = state;
      return {
        id: definition.id,
        type: definition.id === "agent_role_loading" ? "runtimeHealthGroup" : "startupMilestone",
        position: { x: definition.x, y: definition.y },
        data: {
          id: definition.id,
          label: definition.label,
          detail: displayMilestone.detail || STARTUP_STATE_META[state]?.label || "",
          state,
          timestampText: displayMilestone.timestamp ? formatTimestamp(displayMilestone.timestamp) : "",
          runtimeRows: definition.id === "agent_role_loading" ? runtimeRows : [],
        },
        draggable: true,
        selectable: true,
      };
    });
    const steadyStateY = STEADY_FLOW_Y;
    const steadyStateMilestone = milestoneByKey.steady_state_online || {};
    const steadyStateState = normalizeMilestoneState(steadyStateMilestone?.state);
    stateByKey.steady_state_online = steadyStateState;
    const steadyStateNode = {
      id: "steady_state_online",
      type: "startupMilestone",
      position: { x: STEADY_FLOW_X, y: steadyStateY },
      data: {
        id: "steady_state_online",
        label: "Agent steady state online",
        detail: steadyStateMilestone?.detail || STARTUP_STATE_META[steadyStateState]?.label || "",
        state: steadyStateState,
        timestampText:
          steadyStateMilestone?.completed_at || steadyStateMilestone?.updated_at || steadyStateMilestone?.started_at
            ? formatTimestamp(steadyStateMilestone.completed_at || steadyStateMilestone.updated_at || steadyStateMilestone.started_at)
            : "",
      },
      draggable: true,
      selectable: true,
    };
    const allEdges = [
      ...STARTUP_FLOW_EDGES,
      ["status_channel_online", "steady_state_online"],
      ["engine_socket", "steady_state_online"],
    ];
    const nodes = [...milestoneNodes, steadyStateNode];
    const edges = allEdges.map(([source, target]) => {
      const edgeState = getEdgeState(stateByKey[source], stateByKey[target]);
      return {
        id: `${source}-${target}`,
        source,
        target,
        type: "smoothstep",
        animated: true,
        className: `agent-startup-flow-edge agent-startup-flow-edge-${edgeState}`,
        style: EDGE_STYLE_BY_STATE[edgeState] || EDGE_STYLE_BY_STATE.pending,
      };
    });
    return { nodes, edges };
  }, [formatTimestamp, milestones, runtimeRows]);
}

function copyTextToClipboard(text) {
  const clipboard = typeof navigator !== "undefined" ? navigator.clipboard : null;
  if (clipboard && typeof clipboard.writeText === "function") {
    clipboard.writeText(text).catch(() => {
      fallbackCopyTextToClipboard(text);
    });
    return;
  }
  fallbackCopyTextToClipboard(text);
}

function fallbackCopyTextToClipboard(text) {
  if (typeof document === "undefined") return;
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.left = "-9999px";
  textarea.style.top = "0";
  document.body.appendChild(textarea);
  textarea.select();
  try {
    document.execCommand("copy");
  } catch {
    // Clipboard is best-effort for temporary layout tooling.
  } finally {
    document.body.removeChild(textarea);
  }
}

function buildNodeCopyPayload(node) {
  return JSON.stringify(
    {
      id: node?.id || "",
      type: node?.type || "",
      label: node?.data?.label || node?.data?.entry?.name || "",
      x: Math.round(Number(node?.position?.x || 0)),
      y: Math.round(Number(node?.position?.y || 0)),
    },
    null,
    2
  );
}

function buildLayoutCopyPayload(nodes, viewport) {
  return JSON.stringify(
    {
      viewport: {
        x: Math.round(Number(viewport?.x || 0)),
        y: Math.round(Number(viewport?.y || 0)),
        zoom: Number(Number(viewport?.zoom || 1).toFixed(4)),
      },
      nodes: nodes.map((node) => ({
        id: node.id,
        type: node.type,
        label: node.data?.label || node.data?.entry?.name || "",
        x: Math.round(Number(node.position?.x || 0)),
        y: Math.round(Number(node.position?.y || 0)),
      })),
    },
    null,
    2
  );
}

function readDraftLayout() {
  if (typeof window === "undefined") return { positions: {}, viewport: DEFAULT_FLOW_VIEWPORT };
  try {
    const raw = window.localStorage.getItem(FLOW_DRAFT_LAYOUT_STORAGE_KEY);
    if (!raw) return { positions: {}, viewport: DEFAULT_FLOW_VIEWPORT };
    const parsed = JSON.parse(raw);
    const positions = parsed?.positions && typeof parsed.positions === "object" ? parsed.positions : {};
    const viewport = parsed?.viewport && typeof parsed.viewport === "object" ? parsed.viewport : DEFAULT_FLOW_VIEWPORT;
    return { positions, viewport };
  } catch {
    return { positions: {}, viewport: DEFAULT_FLOW_VIEWPORT };
  }
}

function writeDraftLayout(positions, viewport) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(
      FLOW_DRAFT_LAYOUT_STORAGE_KEY,
      JSON.stringify({
        positions,
        viewport,
      })
    );
  } catch {
    // Temporary layout tooling should never block live health rendering.
  }
}

function mergeNodesPreservingPositions(nextNodes, currentNodes, positionOverrides) {
  if (!currentNodes.length) {
    return nextNodes.map((node) => ({
      ...node,
      position: positionOverrides[node.id] || node.position,
    }));
  }
  const currentById = new Map(currentNodes.map((node) => [node.id, node]));
  return nextNodes.map((node) => {
    const currentNode = currentById.get(node.id);
    const overridePosition = positionOverrides[node.id];
    if (!currentNode) {
      return {
        ...node,
        position: overridePosition || node.position,
      };
    }
    return {
      ...node,
      position: overridePosition || currentNode.position || node.position,
      selected: currentNode.selected,
      dragging: currentNode.dragging,
    };
  });
}

function cleanupAnimeInstances(instances) {
  instances.forEach((instance) => {
    if (!instance) return;
    if (typeof instance.revert === "function") {
      instance.revert();
      return;
    }
    if (typeof instance.cancel === "function") {
      instance.cancel();
    }
  });
}

export default function AgentStartupFlow({
  milestones,
  runtimeRows = [],
  formatTimestamp = (value) => String(value || ""),
}) {
  const rows = Array.isArray(milestones) ? milestones : [];
  const { nodes, edges } = useStartupFlowElements(rows, runtimeRows, formatTimestamp);
  const animationSignature = useMemo(
    () =>
      [
        nodes
          .map((node) => {
            const runtimeState = Array.isArray(node.data?.runtimeRows)
              ? node.data.runtimeRows.map((entry) => `${entry.id}:${entry.statusCode}`).join(",")
              : "";
            return `${node.id}:${node.type}:${node.data?.state}:${runtimeState}`;
          })
          .join("|"),
        edges.map((edge) => `${edge.id}:${edge.className}`).join("|"),
      ].join("::"),
    [edges, nodes]
  );
  const draftLayout = useMemo(() => readDraftLayout(), []);
  const wrapperRef = useRef(null);
  const movingFlowSize = useRef({ width: STARTUP_FLOW_NODE_WIDTH, height: 76 });
  const [nodePositionOverrides, setNodePositionOverrides] = useState(draftLayout.positions);
  const [viewport, setViewport] = useState(draftLayout.viewport);
  const [guides, setGuides] = useState([]);
  const [activeGuides, setActiveGuides] = useState([]);
  const [editableNodes, setEditableNodes] = useState(() =>
    mergeNodesPreservingPositions(nodes, [], draftLayout.positions)
  );
  useEffect(() => {
    setEditableNodes((currentNodes) => mergeNodesPreservingPositions(nodes, currentNodes, nodePositionOverrides));
  }, [nodePositionOverrides, nodes]);
  const screenToFlowPosition = useCallback(
    (point) => {
      const wrapperRect = wrapperRef.current?.getBoundingClientRect();
      const zoom = Math.max(Number(viewport?.zoom || 1), 0.01);
      if (!wrapperRect) return { x: 0, y: 0 };
      return {
        x: (point.x - wrapperRect.left - Number(viewport?.x || 0)) / zoom,
        y: (point.y - wrapperRect.top - Number(viewport?.y || 0)) / zoom,
      };
    },
    [viewport]
  );
  const computeGuides = useCallback(
    (dragNode) => {
      const wrapper = wrapperRef.current;
      if (!wrapper || !dragNode?.id) return;
      const wrapperRect = wrapper.getBoundingClientRect();
      const dragEl = wrapper.querySelector(`.react-flow__node[data-id="${dragNode.id}"]`);
      if (dragEl) {
        const rect = dragEl.getBoundingClientRect();
        const topLeft = screenToFlowPosition({ x: rect.left, y: rect.top });
        const topRight = screenToFlowPosition({ x: rect.right, y: rect.top });
        const bottomLeft = screenToFlowPosition({ x: rect.left, y: rect.bottom });
        movingFlowSize.current = {
          width: Math.max(topRight.x - topLeft.x, STARTUP_FLOW_NODE_WIDTH),
          height: Math.max(bottomLeft.y - topLeft.y, 66),
        };
      }

      const nextGuides = [];
      editableNodes.forEach((node) => {
        if (!node?.id || node.id === dragNode.id) return;
        const el = wrapper.querySelector(`.react-flow__node[data-id="${node.id}"]`);
        if (!el) return;
        const rect = el.getBoundingClientRect();
        const relLeft = rect.left - wrapperRect.left;
        const relTop = rect.top - wrapperRect.top;
        const relRight = relLeft + rect.width;
        const relBottom = relTop + rect.height;
        const relCenterX = relLeft + rect.width / 2;
        const relCenterY = relTop + rect.height / 2;
        const topLeft = screenToFlowPosition({ x: rect.left, y: rect.top });
        const topRight = screenToFlowPosition({ x: rect.right, y: rect.top });
        const bottomLeft = screenToFlowPosition({ x: rect.left, y: rect.bottom });
        const center = screenToFlowPosition({ x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 });
        nextGuides.push({ xFlow: topLeft.x, xPx: relLeft });
        nextGuides.push({ xFlow: center.x, xPx: relCenterX });
        nextGuides.push({ xFlow: topRight.x, xPx: relRight });
        nextGuides.push({ yFlow: topLeft.y, yPx: relTop });
        nextGuides.push({ yFlow: center.y, yPx: relCenterY });
        nextGuides.push({ yFlow: bottomLeft.y, yPx: relBottom });
      });
      setGuides(nextGuides);
    },
    [editableNodes, screenToFlowPosition]
  );
  const persistNodePosition = useCallback(
    (nodeId, position) => {
      if (!nodeId || !position) return;
      setNodePositionOverrides((currentOverrides) => {
        const nextOverrides = {
          ...currentOverrides,
          [nodeId]: {
            x: position.x,
            y: position.y,
          },
        };
        writeDraftLayout(nextOverrides, viewport);
        return nextOverrides;
      });
    },
    [viewport]
  );
  const handleNodesChange = useCallback((changes) => {
    const positionChanges = changes.filter((change) => change.type === "position" && change.id && change.position);
    if (positionChanges.length) {
      setNodePositionOverrides((currentOverrides) => {
        const nextOverrides = { ...currentOverrides };
        positionChanges.forEach((change) => {
          nextOverrides[change.id] = change.position;
        });
        writeDraftLayout(nextOverrides, viewport);
        return nextOverrides;
      });
    }
    setEditableNodes((currentNodes) => applyNodeChanges(changes, currentNodes));
  }, [viewport]);
  const handleNodeDrag = useCallback(
    (_, node) => {
      if (!node?.id) return;
      const threshold = SNAP_THRESHOLD_PX / Math.max(Number(viewport?.zoom || 1), 0.01);
      const { width, height } = movingFlowSize.current;
      let bestX = null;
      let bestY = null;
      guides.forEach((guide) => {
        if (guide.xFlow != null) {
          [
            { value: node.position.x, offset: 0 },
            { value: node.position.x + width / 2, offset: width / 2 },
            { value: node.position.x + width, offset: width },
          ].forEach((candidate) => {
            const distance = Math.abs(candidate.value - guide.xFlow);
            if (distance < threshold && (!bestX || distance < bestX.distance)) {
              bestX = { distance, x: guide.xFlow - candidate.offset, xPx: guide.xPx };
            }
          });
        }
        if (guide.yFlow != null) {
          [
            { value: node.position.y, offset: 0 },
            { value: node.position.y + height / 2, offset: height / 2 },
            { value: node.position.y + height, offset: height },
          ].forEach((candidate) => {
            const distance = Math.abs(candidate.value - guide.yFlow);
            if (distance < threshold && (!bestY || distance < bestY.distance)) {
              bestY = { distance, y: guide.yFlow - candidate.offset, yPx: guide.yPx };
            }
          });
        }
      });
      if (!bestX && !bestY) {
        setActiveGuides([]);
        return;
      }
      const snappedPosition = {
        x: bestX ? bestX.x : node.position.x,
        y: bestY ? bestY.y : node.position.y,
      };
      setEditableNodes((currentNodes) =>
        applyNodeChanges(
          [
            {
              id: node.id,
              type: "position",
              position: snappedPosition,
            },
          ],
          currentNodes
        )
      );
      persistNodePosition(node.id, snappedPosition);
      setActiveGuides([
        ...(bestX ? [{ xPx: bestX.xPx }] : []),
        ...(bestY ? [{ yPx: bestY.yPx }] : []),
      ]);
    },
    [guides, persistNodePosition, viewport]
  );
  const handleNodeDragStop = useCallback(
    (_, node) => {
      setGuides([]);
      setActiveGuides([]);
      if (!node?.id) return;
      setEditableNodes((currentNodes) => {
        const currentNode = currentNodes.find((entry) => entry.id === node.id);
        if (currentNode?.position) persistNodePosition(node.id, currentNode.position);
        return currentNodes;
      });
    },
    [persistNodePosition]
  );
  const handleMove = useCallback(
    (_, nextViewport) => {
      setViewport(nextViewport);
      writeDraftLayout(nodePositionOverrides, nextViewport);
    },
    [nodePositionOverrides]
  );
  const handleNodeContextMenu = useCallback((event, node) => {
    event.preventDefault();
    copyTextToClipboard(buildNodeCopyPayload(node));
  }, []);
  const handlePaneContextMenu = useCallback(
    (event) => {
      event.preventDefault();
      copyTextToClipboard(buildLayoutCopyPayload(editableNodes, viewport));
    },
    [editableNodes, viewport]
  );
  const handleCopyLayout = useCallback(
    () => copyTextToClipboard(buildLayoutCopyPayload(editableNodes, viewport)),
    [editableNodes, viewport]
  );
  useEffect(() => {
    const root = wrapperRef.current;
    if (!root || !rows.length) return undefined;
    const animations = [];
    let frame = window.requestAnimationFrame(() => {
      const cards = root.querySelectorAll(".agent-startup-card");
      if (cards.length) {
        animations.push(
          animate(cards, {
            opacity: [0.82, 1],
            scale: [0.985, 1],
            duration: 420,
            delay: stagger(34),
            ease: "outCubic",
          })
        );
      }

      const paths = root.querySelectorAll(".react-flow__edge-path");
      paths.forEach((path, index) => {
        const length = typeof path.getTotalLength === "function" ? path.getTotalLength() : 140;
        path.style.strokeDasharray = "8 5";
        path.style.strokeDashoffset = `${length}`;
        animations.push(
          animate(path, {
            strokeDashoffset: [length, 0],
            duration: 720,
            delay: 70 + index * 54,
            ease: "outCubic",
          })
        );
      });

      const activePaths = root.querySelectorAll(".agent-startup-flow-edge-active .react-flow__edge-path");
      if (activePaths.length) {
        animations.push(
          animate(activePaths, {
            strokeDashoffset: [0, -52],
            duration: 980,
            loop: true,
            ease: "linear",
          })
        );
      }

      const activeCards = root.querySelectorAll(".agent-startup-card-active, .agent-runtime-health-row-active");
      if (activeCards.length) {
        animations.push(
          animate(activeCards, {
            scale: [1, 1.018],
            filter: ["brightness(1)", "brightness(1.24)"],
            duration: 860,
            loop: true,
            alternate: true,
            ease: "inOutSine",
          })
        );
      }

      const steadyOnline = root.querySelector('.react-flow__node[data-id="steady_state_online"] .agent-startup-card-complete');
      if (steadyOnline) {
        animations.push(
          animate(steadyOnline, {
            filter: ["drop-shadow(0 0 0 rgba(52,211,153,0))", "drop-shadow(0 0 16px rgba(52,211,153,.42))"],
            duration: 1600,
            loop: true,
            alternate: true,
            ease: "inOutSine",
          })
        );
      }
    });
    return () => {
      window.cancelAnimationFrame(frame);
      frame = null;
      cleanupAnimeInstances(animations);
    };
  }, [animationSignature, rows.length]);
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
      className="agent-startup-flow"
      ref={wrapperRef}
      sx={{
        height: "100%",
        minHeight: { xs: 520, md: 620 },
        width: "100%",
        overflow: "hidden",
        borderRadius: 2,
        background: "rgba(3,7,18,0.28)",
        position: "relative",
        "@keyframes agentStartupFlowSpin": {
          "100%": { transform: "rotate(360deg)" },
        },
      }}
    >
      <Box sx={{ position: "absolute", top: 8, right: 8, zIndex: 10, display: "flex", gap: 0.5 }}>
        <Tooltip title="Copy current node layout JSON" arrow>
          <IconButton
            size="small"
            onClick={handleCopyLayout}
            sx={{
              color: MAGIC_UI.accentA,
              border: `1px solid ${MAGIC_UI.panelBorder}`,
              background: "rgba(7,11,24,0.86)",
              "&:hover": { background: "rgba(88,166,255,0.14)" },
            }}
          >
            <ContentCopyRoundedIcon sx={{ fontSize: 17 }} />
          </IconButton>
        </Tooltip>
      </Box>
      <GlobalStyles
        styles={{
          ".agent-startup-flow .react-flow__handle": {
            pointerEvents: "none",
          },
          ".agent-startup-flow .react-flow__attribution": {
            display: "none",
          },
          ".agent-startup-flow-helper-line": {
            position: "absolute",
            zIndex: 9,
            pointerEvents: "none",
            background: MAGIC_UI.accentA,
            boxShadow: `0 0 12px ${MAGIC_UI.accentA}`,
            opacity: 0.95,
          },
          ".agent-startup-flow-helper-line-vertical": {
            top: 0,
            width: 1,
            height: "100%",
          },
          ".agent-startup-flow-helper-line-horizontal": {
            left: 0,
            height: 1,
            width: "100%",
          },
        }}
      />
      <ReactFlow
        nodes={editableNodes}
        edges={edges}
        nodeTypes={STARTUP_FLOW_NODE_TYPES}
        defaultViewport={viewport}
        minZoom={0.2}
        maxZoom={1.25}
        onNodesChange={handleNodesChange}
        nodesDraggable
        nodesConnectable={false}
        elementsSelectable
        panOnDrag
        zoomOnScroll={false}
        zoomOnPinch
        panOnScroll={false}
        zoomOnDoubleClick={false}
        preventScrolling={false}
        selectionOnDrag={false}
        onNodeDragStart={(_, node) => computeGuides(node)}
        onNodeDrag={handleNodeDrag}
        onNodeDragStop={handleNodeDragStop}
        onMove={handleMove}
        onNodeContextMenu={handleNodeContextMenu}
        onPaneContextMenu={handlePaneContextMenu}
        proOptions={{ hideAttribution: true }}
        style={{ background: "transparent" }}
      >
        <Background variant="lines" gap={42} size={1} color="rgba(148, 163, 184, 0.14)" />
      </ReactFlow>
      {activeGuides.map((guide, index) =>
        guide.xPx != null ? (
          <Box
            key={`x-${index}`}
            className="agent-startup-flow-helper-line agent-startup-flow-helper-line-vertical"
            sx={{ left: `${guide.xPx}px` }}
          />
        ) : (
          <Box
            key={`y-${index}`}
            className="agent-startup-flow-helper-line agent-startup-flow-helper-line-horizontal"
            sx={{ top: `${guide.yPx}px` }}
          />
        )
      )}
    </Box>
  );
}
