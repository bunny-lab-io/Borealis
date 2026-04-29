import React, { useMemo } from "react";
import { Box, GlobalStyles, Tooltip, Typography } from "@mui/material";
import CheckCircleRoundedIcon from "@mui/icons-material/CheckCircleRounded";
import RadioButtonUncheckedRoundedIcon from "@mui/icons-material/RadioButtonUncheckedRounded";
import ErrorOutlineRoundedIcon from "@mui/icons-material/ErrorOutlineRounded";
import AutorenewRoundedIcon from "@mui/icons-material/AutorenewRounded";
import RemoveCircleOutlineRoundedIcon from "@mui/icons-material/RemoveCircleOutlineRounded";
import ReactFlow, { Background, Handle, Position } from "reactflow";
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

const STARTUP_FLOW_NODE_WIDTH = 196;

const STARTUP_FLOW_DEFINITIONS = Object.freeze([
  { id: "process_start", label: "Agent process started", milestoneKeys: ["process_start"], x: 430, y: 0 },
  { id: "server_config_loaded", label: "Server configuration loaded", milestoneKeys: ["server_config_loaded"], x: 170, y: 108 },
  { id: "identity_loaded", label: "Device identity loaded", milestoneKeys: ["identity_loaded"], x: 690, y: 108 },
  { id: "engine_authentication", label: "Engine authentication", milestoneKeys: ["authenticating", "authenticated"], x: 430, y: 246 },
  { id: "status_channel_online", label: "Status channel online", milestoneKeys: ["status_channel_online"], x: 80, y: 410 },
  { id: "engine_socket", label: "Engine socket", milestoneKeys: ["socket_connecting", "socket_connected"], x: 430, y: 410 },
  { id: "agent_role_loading", label: "Agent role loading", milestoneKeys: ["roles_loading", "roles_ready"], x: 780, y: 410 },
  { id: "helper_broker_ready", label: "Current-user broker ready", milestoneKeys: ["helper_broker_ready"], x: 520, y: 570 },
  { id: "inventory_ready", label: "Inventory telemetry ready", milestoneKeys: ["inventory_ready"], x: 760, y: 570 },
  { id: "wireguard_tunnel", label: "WireGuard tunnel", milestoneKeys: ["wireguard_starting", "wireguard_online"], x: 1000, y: 570 },
  { id: "steady_state_online", label: "Agent steady state online", milestoneKeys: ["steady_state_online"], x: 430, y: 760 },
]);

const STARTUP_FLOW_JUNCTIONS = Object.freeze([
  { id: "identity_join", x: 519, y: 210, sources: ["server_config_loaded", "identity_loaded"] },
  { id: "runtime_split", x: 519, y: 372, sources: ["engine_authentication"] },
  { id: "roles_split", x: 869, y: 528, sources: ["agent_role_loading"] },
  {
    id: "steady_join",
    x: 519,
    y: 720,
    sources: ["status_channel_online", "engine_socket", "helper_broker_ready", "inventory_ready", "wireguard_tunnel"],
  },
]);

const STARTUP_FLOW_EDGES = Object.freeze([
  ["process_start", "server_config_loaded"],
  ["process_start", "identity_loaded"],
  ["server_config_loaded", "identity_join"],
  ["identity_loaded", "identity_join"],
  ["identity_join", "engine_authentication"],
  ["engine_authentication", "runtime_split"],
  ["runtime_split", "status_channel_online"],
  ["runtime_split", "engine_socket"],
  ["runtime_split", "agent_role_loading"],
  ["agent_role_loading", "roles_split"],
  ["roles_split", "helper_broker_ready"],
  ["roles_split", "inventory_ready"],
  ["roles_split", "wireguard_tunnel"],
  ["status_channel_online", "steady_join"],
  ["engine_socket", "steady_join"],
  ["helper_broker_ready", "steady_join"],
  ["inventory_ready", "steady_join"],
  ["wireguard_tunnel", "steady_join"],
  ["steady_join", "steady_state_online"],
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
        <Handle type="target" position={Position.Top} style={HIDDEN_HANDLE_STYLE} isConnectable={false} />
        <Handle type="source" position={Position.Bottom} style={HIDDEN_HANDLE_STYLE} isConnectable={false} />
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
      <Handle type="target" position={Position.Top} style={HIDDEN_HANDLE_STYLE} isConnectable={false} />
      <Handle type="source" position={Position.Bottom} style={HIDDEN_HANDLE_STYLE} isConnectable={false} />
    </Box>
  );
}

const STARTUP_FLOW_NODE_TYPES = Object.freeze({ startupMilestone: AgentStartupFlowNode, flowJunction: AgentStartupFlowJunction });

function useStartupFlowElements(milestones, formatTimestamp) {
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
    const nodes = [...milestoneNodes, ...junctionNodes];
    const edges = STARTUP_FLOW_EDGES.map(([source, target]) => {
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
  }, [formatTimestamp, milestones]);
}

export default function AgentStartupFlow({ milestones, formatTimestamp = (value) => String(value || "") }) {
  const rows = Array.isArray(milestones) ? milestones : [];
  const { nodes, edges } = useStartupFlowElements(rows, formatTimestamp);
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
        "@keyframes agentStartupFlowSpin": {
          "100%": { transform: "rotate(360deg)" },
        },
      }}
    >
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
        nodes={nodes}
        edges={edges}
        nodeTypes={STARTUP_FLOW_NODE_TYPES}
        fitView
        fitViewOptions={{ padding: 0.04, minZoom: 0.2, maxZoom: 1.25 }}
        minZoom={0.2}
        maxZoom={1.25}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={false}
        panOnDrag
        zoomOnScroll={false}
        zoomOnPinch
        panOnScroll={false}
        zoomOnDoubleClick={false}
        preventScrolling={false}
        selectionOnDrag={false}
        proOptions={{ hideAttribution: true }}
        style={{ background: "transparent" }}
      >
        <Background variant="lines" gap={42} size={1} color="rgba(148, 163, 184, 0.14)" />
      </ReactFlow>
    </Box>
  );
}
