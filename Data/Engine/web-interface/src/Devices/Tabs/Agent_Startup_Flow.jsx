import React, { useCallback, useEffect, useMemo, useState } from "react";
import { Box, Button, GlobalStyles, IconButton, Tooltip, Typography } from "@mui/material";
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
const RUNTIME_FLOW_NODE_WIDTH = 256;
const RUNTIME_FLOW_COLUMNS = Object.freeze([1570, 1885, 2200, 2515]);
const RUNTIME_FLOW_START_Y = 198;
const RUNTIME_FLOW_ROW_GAP = 106;
const STEADY_FLOW_X = 2940;
const STEADY_FLOW_Y = 315;

const STARTUP_FLOW_DEFINITIONS = Object.freeze([
  { id: "process_start", label: "Agent process started", milestoneKeys: ["process_start"], x: 20, y: 315 },
  { id: "server_config_loaded", label: "Server configuration loaded", milestoneKeys: ["server_config_loaded"], x: 360, y: 190 },
  { id: "identity_loaded", label: "Device identity loaded", milestoneKeys: ["identity_loaded"], x: 360, y: 440 },
  { id: "engine_authentication", label: "Engine authentication", milestoneKeys: ["authenticating", "authenticated"], x: 770, y: 315 },
  { id: "status_channel_online", label: "Status channel online", milestoneKeys: ["status_channel_online"], x: 1180, y: 100 },
  { id: "agent_role_loading", label: "Agent role loading", milestoneKeys: ["roles_loading", "roles_ready"], x: 1180, y: 315 },
  { id: "engine_socket", label: "Engine socket", milestoneKeys: ["socket_connecting", "socket_connected"], x: 1180, y: 530 },
]);

const STARTUP_FLOW_JUNCTIONS = Object.freeze([
  { id: "identity_join", x: 690, y: 340, sources: ["server_config_loaded", "identity_loaded"] },
  { id: "runtime_split", x: 1088, y: 340, sources: ["engine_authentication"] },
  { id: "runtime_health_split", x: 1488, y: 340, sources: ["agent_role_loading"] },
]);

const STARTUP_FLOW_EDGES = Object.freeze([
  ["process_start", "server_config_loaded"],
  ["process_start", "identity_loaded"],
  ["server_config_loaded", "identity_join"],
  ["identity_loaded", "identity_join"],
  ["identity_join", "engine_authentication"],
  ["engine_authentication", "runtime_split"],
  ["runtime_split", "status_channel_online"],
  ["runtime_split", "agent_role_loading"],
  ["runtime_split", "engine_socket"],
  ["agent_role_loading", "runtime_health_split"],
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

function deriveJunctionState(sourceIds, stateByKey) {
  const states = sourceIds.map((sourceId) => stateByKey[sourceId] || "pending");
  if (states.some((state) => state === "failed")) return "failed";
  if (states.some((state) => state === "active")) return "active";
  if (states.length && states.every((state) => state === "complete" || state === "skipped")) return "complete";
  return "pending";
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

function buildRuntimeNodeId(entry, index) {
  return `runtime_health_${index}_${String(entry?.id || entry?.name || "node").replace(/[^a-zA-Z0-9_-]+/g, "_")}`;
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

function AgentStartupFlowJunction({ data }) {
  const state = normalizeMilestoneState(data?.state);
  const meta = STARTUP_STATE_META[state] || STARTUP_STATE_META.pending;
  return (
    <Box
      sx={{
        width: 18,
        height: 18,
        borderRadius: "50%",
        border: `1px solid ${meta.color}`,
        background: "rgba(7,11,24,0.96)",
        boxShadow: `0 0 18px ${meta.color}55`,
      }}
    >
      <Handle type="target" position={Position.Left} style={HIDDEN_HANDLE_STYLE} isConnectable={false} />
      <Handle type="source" position={Position.Right} style={HIDDEN_HANDLE_STYLE} isConnectable={false} />
    </Box>
  );
}

function AgentRuntimeHealthNode({ data }) {
  const state = normalizeRuntimeHealthState(data?.entry?.statusCode);
  const color = getRuntimeStatusColor(data?.entry?.statusCode);
  const meta = STARTUP_STATE_META[state] || STARTUP_STATE_META.pending;
  const Icon = meta.Icon;
  const status = String(data?.entry?.status || "Unknown").trim();
  const checked = String(data?.entry?.lastCheckedText || "").trim();
  return (
    <Button
      type="button"
      onClick={(event) => {
        event.preventDefault();
        event.stopPropagation();
        if (typeof data?.onOpen === "function") data.onOpen(data.entry);
      }}
      sx={{
        width: RUNTIME_FLOW_NODE_WIDTH,
        minHeight: 76,
        px: 1.15,
        py: 0.9,
        borderRadius: 2,
        border: `1px solid ${color}`,
        background: `linear-gradient(145deg, rgba(7,11,24,0.96), ${color}18)`,
        boxShadow: "0 14px 32px rgba(2,6,23,0.48)",
        color: MAGIC_UI.textBright,
        display: "flex",
        alignItems: "center",
        justifyContent: "flex-start",
        gap: 1,
        textAlign: "left",
        textTransform: "none",
        overflow: "hidden",
        cursor: "pointer",
        "&:hover": {
          background: `linear-gradient(145deg, rgba(10,17,33,0.98), ${color}24)`,
          boxShadow: `0 0 0 1px ${color}44, 0 18px 38px rgba(2,6,23,0.55)`,
        },
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
            animation: state === "active" ? "agentStartupFlowSpin 1.15s linear infinite" : "none",
          }}
        />
      </Box>
      <Box sx={{ minWidth: 0, flex: 1 }}>
        <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.78rem", fontWeight: 740, lineHeight: 1.2 }} noWrap>
          {data?.label || "Runtime check"}
        </Typography>
        <Typography sx={{ mt: 0.2, color, fontSize: "0.7rem", fontWeight: 680, lineHeight: 1.2 }} noWrap>
          {status}
        </Typography>
        {checked ? (
          <Typography sx={{ mt: 0.15, color: MAGIC_UI.textMuted, fontSize: "0.66rem", lineHeight: 1.2 }} noWrap>
            {checked}
          </Typography>
        ) : null}
      </Box>
    </Button>
  );
}

const STARTUP_FLOW_NODE_TYPES = Object.freeze({
  startupMilestone: AgentStartupFlowNode,
  flowJunction: AgentStartupFlowJunction,
  runtimeHealth: AgentRuntimeHealthNode,
});

function useStartupFlowElements(milestones, runtimeRows, formatTimestamp, onRuntimeNodeOpen) {
  return useMemo(() => {
    const milestoneByKey = buildMilestoneLookup(milestones);
    const stateByKey = {};
    const milestoneNodes = STARTUP_FLOW_DEFINITIONS.map((definition) => {
      const groupedMilestones = resolveGroupedMilestones(definition, milestoneByKey);
      const state = resolveGroupedState(groupedMilestones);
      const displayMilestone = selectGroupedMilestone(groupedMilestones, state) || {};
      stateByKey[definition.id] = state;
      return {
        id: definition.id,
        type: "startupMilestone",
        position: { x: definition.x, y: definition.y },
        data: {
          id: definition.id,
          label: definition.label,
          detail: displayMilestone.detail || STARTUP_STATE_META[state]?.label || "",
          state,
          timestampText: displayMilestone.timestamp ? formatTimestamp(displayMilestone.timestamp) : "",
        },
        draggable: false,
        selectable: false,
      };
    });
    const junctionNodes = STARTUP_FLOW_JUNCTIONS.map((junction) => {
      const state = deriveJunctionState(junction.sources, stateByKey);
      stateByKey[junction.id] = state;
      return {
        id: junction.id,
        type: "flowJunction",
        position: { x: junction.x, y: junction.y },
        data: { state },
        draggable: false,
        selectable: false,
      };
    });
    const runtimeNodes = (Array.isArray(runtimeRows) ? runtimeRows : []).map((entry, index) => {
      const columnIndex = index % RUNTIME_FLOW_COLUMNS.length;
      const rowIndex = Math.floor(index / RUNTIME_FLOW_COLUMNS.length);
      const id = buildRuntimeNodeId(entry, index);
      const state = normalizeRuntimeHealthState(entry?.statusCode);
      stateByKey[id] = state;
      return {
        id,
        type: "runtimeHealth",
        position: { x: RUNTIME_FLOW_COLUMNS[columnIndex], y: RUNTIME_FLOW_START_Y + rowIndex * RUNTIME_FLOW_ROW_GAP },
        data: {
          label: entry?.name || `Runtime ${index + 1}`,
          entry,
          onOpen: onRuntimeNodeOpen,
        },
        draggable: false,
        selectable: false,
      };
    });
    const runtimeNodeIds = runtimeNodes.map((node) => node.id);
    const runtimeJoinY = STEADY_FLOW_Y + 24;
    const steadyJoinY = STEADY_FLOW_Y + 24;
    const steadyStateY = STEADY_FLOW_Y;
    const runtimeJoinSources = runtimeNodeIds.length ? runtimeNodeIds : ["runtime_health_split"];
    const runtimeHealthJoinState = deriveJunctionState(runtimeJoinSources, stateByKey);
    stateByKey.runtime_health_join = runtimeHealthJoinState;
    const steadyJoinState = deriveJunctionState(["status_channel_online", "engine_socket", "runtime_health_join"], stateByKey);
    stateByKey.steady_join = steadyJoinState;
    const dynamicJunctions = [
      {
        id: "runtime_health_join",
        type: "flowJunction",
        position: { x: 2820, y: runtimeJoinY },
        data: { state: runtimeHealthJoinState },
        draggable: false,
        selectable: false,
      },
      {
        id: "steady_join",
        type: "flowJunction",
        position: { x: 2880, y: steadyJoinY },
        data: { state: steadyJoinState },
        draggable: false,
        selectable: false,
      },
    ];
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
      draggable: false,
      selectable: false,
    };
    const runtimeEdges = runtimeNodeIds.length
      ? runtimeNodeIds.flatMap((runtimeNodeId) => [
          ["runtime_health_split", runtimeNodeId],
          [runtimeNodeId, "runtime_health_join"],
        ])
      : [["runtime_health_split", "runtime_health_join"]];
    const allEdges = [
      ...STARTUP_FLOW_EDGES,
      ...runtimeEdges,
      ["status_channel_online", "steady_join"],
      ["engine_socket", "steady_join"],
      ["runtime_health_join", "steady_join"],
      ["steady_join", "steady_state_online"],
    ];
    const nodes = [...milestoneNodes, ...junctionNodes, ...runtimeNodes, ...dynamicJunctions, steadyStateNode];
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
  }, [formatTimestamp, milestones, onRuntimeNodeOpen, runtimeRows]);
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

export default function AgentStartupFlow({
  milestones,
  runtimeRows = [],
  formatTimestamp = (value) => String(value || ""),
  onRuntimeNodeOpen = null,
}) {
  const rows = Array.isArray(milestones) ? milestones : [];
  const { nodes, edges } = useStartupFlowElements(rows, runtimeRows, formatTimestamp, onRuntimeNodeOpen);
  const [editableNodes, setEditableNodes] = useState(nodes);
  useEffect(() => {
    setEditableNodes(nodes);
  }, [nodes]);
  const handleNodesChange = useCallback((changes) => {
    setEditableNodes((currentNodes) => applyNodeChanges(changes, currentNodes));
  }, []);
  const handleNodeClick = useCallback((_, node) => {
    if (node?.type === "runtimeHealth" && typeof node?.data?.onOpen === "function") {
      node.data.onOpen(node.data.entry);
    }
  }, []);
  const handleNodeContextMenu = useCallback((event, node) => {
    event.preventDefault();
    copyTextToClipboard(buildNodeCopyPayload(node));
  }, []);
  const handlePaneContextMenu = useCallback(
    (event) => {
      event.preventDefault();
      copyTextToClipboard(
        JSON.stringify(
          editableNodes.map((node) => ({
            id: node.id,
            type: node.type,
            label: node.data?.label || node.data?.entry?.name || "",
            x: Math.round(Number(node.position?.x || 0)),
            y: Math.round(Number(node.position?.y || 0)),
          })),
          null,
          2
        )
      );
    },
    [editableNodes]
  );
  const handleCopyLayout = useCallback(
    () =>
      copyTextToClipboard(
        JSON.stringify(
          editableNodes.map((node) => ({
            id: node.id,
            type: node.type,
            label: node.data?.label || node.data?.entry?.name || "",
            x: Math.round(Number(node.position?.x || 0)),
            y: Math.round(Number(node.position?.y || 0)),
          })),
          null,
          2
        )
      ),
    [editableNodes]
  );
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
          "@keyframes agentStartupFlowDash": {
            "0%": { strokeDashoffset: 0 },
            "100%": { strokeDashoffset: -26 },
          },
          ".agent-startup-flow .react-flow__edge.animated .react-flow__edge-path": {
            animation: "agentStartupFlowDash 1.1s linear infinite",
          },
          ".agent-startup-flow .react-flow__handle": {
            pointerEvents: "none",
          },
          ".agent-startup-flow .react-flow__attribution": {
            display: "none",
          },
        }}
      />
      <ReactFlow
        nodes={editableNodes}
        edges={edges}
        nodeTypes={STARTUP_FLOW_NODE_TYPES}
        fitView
        fitViewOptions={{ padding: 0.04, minZoom: 0.2, maxZoom: 1.25 }}
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
        onNodeClick={handleNodeClick}
        onNodeContextMenu={handleNodeContextMenu}
        onPaneContextMenu={handlePaneContextMenu}
        proOptions={{ hideAttribution: true }}
        style={{ background: "transparent" }}
      >
        <Background variant="lines" gap={42} size={1} color="rgba(148, 163, 184, 0.14)" />
      </ReactFlow>
    </Box>
  );
}
