////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Engine/Containers/webui-frontend/data/web-interface/src/Flow_Editor/Node_Configuration_Sidebar.jsx
import {
  Box,
  Typography,
  Tabs,
  Tab,
  TextField,
  MenuItem,
  IconButton,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  Button,
  Tooltip,
  CircularProgress,
} from "@mui/material";
import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useReactFlow } from "reactflow";
import ReactMarkdown from "react-markdown"; // Used for Node Usage Documentation
import EditIcon from "@mui/icons-material/Edit";
import PaletteIcon from "@mui/icons-material/Palette";
import DeleteRoundedIcon from "@mui/icons-material/DeleteRounded";
import { SketchPicker } from "react-color";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_INPUT_SX,
  DIALOG_PAPER_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";
import { buildAssemblyIndex, parseAssembliesCollectionPayload } from "../Assemblies/assemblyUtils.js";
import {
  decorateWorkflowEdge,
  getWorkflowEdgePortMetadata,
  getWorkflowRouteDescriptor,
  getWorkflowRuntimeDisplayLabel,
  WORKFLOW_RUNTIME_NODE_TYPES,
} from "./runtimeV1.js";

ModuleRegistry.registerModules([AllCommunityModule]);

// ---- NEW: Brightness utility for gradient ----
function darkenColor(hex, percent = 0.7) {
  if (!/^#[0-9A-Fa-f]{6}$/.test(hex)) return hex;
  let r = parseInt(hex.slice(1, 3), 16);
  let g = parseInt(hex.slice(3, 5), 16);
  let b = parseInt(hex.slice(5, 7), 16);
  r = Math.round(r * percent);
  g = Math.round(g * percent);
  b = Math.round(b * percent);
  return `#${r.toString(16).padStart(2,"0")}${g.toString(16).padStart(2,"0")}${b.toString(16).padStart(2,"0")}`;
}
// --------------------------------------------

const MIN_TARGET_SEARCH_LENGTH = 3;
const targetGridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 12,
});
const targetGridThemeClass = targetGridTheme.themeName || "ag-theme-quartz";
const targetGridFontFamily = '"IBM Plex Sans","Helvetica Neue",Arial,sans-serif';
const targetGridIconFontFamily = '"Quartz Regular"';
const TARGET_GRID_WRAPPER_SX = {
  width: "100%",
  borderRadius: 2.5,
  border: "1px solid rgba(148,163,184,0.26)",
  background: "linear-gradient(170deg, rgba(5,8,20,0.92), rgba(8,13,32,0.9))",
  overflow: "hidden",
  fontFamily: targetGridFontFamily,
  "--ag-font-family": targetGridFontFamily,
  "--ag-icon-font-family": targetGridIconFontFamily,
  "--ag-cell-horizontal-padding": "18px",
  "--ag-background-color": "#070b1a",
  "--ag-foreground-color": "#f4f7ff",
  "--ag-header-background-color": "rgba(15,23,42,0.92)",
  "--ag-header-foreground-color": "#cfe0ff",
  "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
  "--ag-row-hover-color": "rgba(73,156,196,0.2)",
  "--ag-selected-row-background-color": "rgba(125,211,252,0.16)",
  "--ag-border-color": "rgba(125,183,255,0.18)",
  "--ag-row-border-color": "rgba(125,183,255,0.14)",
  "--ag-checkbox-checked-color": "#7dd3fc",
  "& .ag-root-wrapper": {
    border: "none",
    borderRadius: 0,
    minHeight: "100%",
    background: "transparent",
  },
  "& .ag-root, & .ag-header, & .ag-center-cols-container, & .ag-paging-panel": {
    fontFamily: targetGridFontFamily,
    background: "transparent",
  },
  "& .ag-header": {
    borderBottom: "1px solid rgba(148,163,184,0.24)",
  },
  "& .ag-header-cell-label": {
    color: "#e2e8f0",
    fontWeight: 600,
    letterSpacing: 0.3,
  },
  "& .ag-row": {
    borderColor: "rgba(255,255,255,0.04)",
  },
  "& .ag-row:nth-of-type(even)": {
    backgroundColor: "rgba(15,23,42,0.32)",
  },
  "& .ag-row-selected": {
    backgroundColor: "rgba(125,211,252,0.2) !important",
    boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
  },
  "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    textAlign: "left",
    padding: "8px 12px 8px 18px",
    color: "#e2e8f0",
  },
  "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
    width: "100%",
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    padding: 0,
  },
  "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
    paddingLeft: "12px",
    paddingRight: "9px",
  },
};

function sortSelectedDevices(devices = []) {
  return [...devices].sort((left, right) => {
    const leftHost = String(left?.hostname || "").toLowerCase();
    const rightHost = String(right?.hostname || "").toLowerCase();
    if (leftHost !== rightHost) {
      return leftHost.localeCompare(rightHost, undefined, { sensitivity: "base", numeric: true });
    }
    return String(left?.site_name || "").localeCompare(String(right?.site_name || ""), undefined, {
      sensitivity: "base",
      numeric: true,
    });
  });
}

function normalizeSelectedDevices(rawValue) {
  const source = Array.isArray(rawValue)
    ? rawValue
    : Array.isArray(rawValue?.selected_devices)
    ? rawValue.selected_devices
    : Array.isArray(rawValue?.selectedDevices)
    ? rawValue.selectedDevices
    : [];
  return sortSelectedDevices(
    source
      .filter((entry) => entry && typeof entry === "object")
      .map((entry) => ({
        hostname: String(entry.hostname || "").trim(),
        site_id: entry.site_id ?? null,
        site_name: String(entry.site_name || entry.site || "").trim(),
        device_guid: String(entry.device_guid || entry.guid || "").trim(),
        agent_id: String(entry.agent_id || "").trim(),
        connection_type: String(entry.connection_type || "").trim(),
      }))
      .filter((entry) => entry.hostname || entry.device_guid)
  );
}

function buildPreviewEnvelope({
  status = "Pending",
  data = null,
  metadata = {},
  artifacts = {},
} = {}) {
  return {
    status,
    data,
    metadata: {
      preview_mode: "authoring",
      ...(metadata || {}),
    },
    artifacts: artifacts || {},
  };
}

function dedupePreviewTargets(targets = []) {
  const seen = new Set();
  const ordered = [];
  (Array.isArray(targets) ? targets : []).forEach((entry) => {
    if (!entry || typeof entry !== "object") return;
    const key = entry.device_guid
      ? `guid:${String(entry.device_guid).toLowerCase()}`
      : `host:${String(entry.hostname || "").toLowerCase()}::${String(entry.site_id ?? "")}`;
    if (seen.has(key)) return;
    seen.add(key);
    ordered.push(entry);
  });
  return ordered;
}

function previewTargetsFromSelectedDevices(devices = []) {
  return dedupePreviewTargets(
    normalizeSelectedDevices(devices).map((entry) => ({
      hostname: entry.hostname,
      site_id: entry.site_id,
      site_name: entry.site_name || "Not Configured",
      device_guid: entry.device_guid || "",
      agent_id: entry.agent_id || "",
      connection_type: entry.connection_type || "",
      preview_only: true,
    }))
  );
}

function previewPortInputs(inputEnvelope, portId) {
  return Array.isArray(inputEnvelope?.data?.inputs_by_port?.[portId]?.inputs)
    ? inputEnvelope.data.inputs_by_port[portId].inputs
    : [];
}

function previewTargetCountFromEnvelope(envelope) {
  const metadataCount = envelope?.metadata?.target_count;
  if (Number.isFinite(Number(metadataCount))) {
    return Number.parseInt(metadataCount, 10);
  }
  const targets = envelope?.data?.targets;
  if (Array.isArray(targets)) {
    return targets.length;
  }
  return 0;
}

function previewJobOutputFromEnvelope(envelope) {
  return Array.isArray(envelope?.data?.job_output) ? envelope.data.job_output : [];
}

function previewNormalizeStatus(value) {
  const normalized = String(value || "").trim().toLowerCase();
  if (normalized === "success") return "Success";
  if (normalized === "warning") return "Warning";
  if (normalized === "failed" || normalized === "failure" || normalized === "error") return "Failed";
  if (normalized === "timed out" || normalized === "timed_out" || normalized === "timeout") return "Timed Out";
  if (normalized === "skipped" || normalized === "inactive") return "Skipped";
  if (normalized === "running") return "Running";
  if (normalized === "pending") return "Pending";
  return "";
}

function previewRouteMatchesStatus(route, status) {
  const normalizedStatus = previewNormalizeStatus(status);
  if (normalizedStatus === "Skipped") return false;
  if (route === "on_success") return normalizedStatus === "Success";
  if (route === "on_warning") return normalizedStatus === "Warning";
  if (route === "on_failed") return normalizedStatus === "Failed" || normalizedStatus === "Timed Out";
  return ["Success", "Warning", "Failed", "Timed Out"].includes(normalizedStatus);
}

function previewTargetsFromJobOutput(jobOutput = []) {
  return dedupePreviewTargets(
    (Array.isArray(jobOutput) ? jobOutput : [])
      .filter((entry) => entry && typeof entry === "object")
      .map((entry) => ({
        hostname: String(entry.hostname || "").trim(),
        site_id: entry.site_id ?? null,
        site_name: String(entry.site_name || "Not Configured").trim(),
        device_guid: String(entry.device_guid || "").trim(),
        agent_id: String(entry.agent_id || "").trim(),
        preview_only: true,
      }))
      .filter((entry) => entry.hostname || entry.device_guid)
  );
}

function buildPreviewRoutedJobOutputEnvelope(sourceEnvelope, route) {
  const jobOutput = previewJobOutputFromEnvelope(sourceEnvelope);
  const allPendingLike =
    jobOutput.length > 0 &&
    jobOutput.every((entry) => {
      const status = previewNormalizeStatus(entry?.status || "Pending");
      return status === "Pending" || status === "Running";
    });
  const matchingRecords =
    route === "always" && allPendingLike
      ? jobOutput.map((entry) => ({ ...entry }))
      : jobOutput.filter((entry) => previewRouteMatchesStatus(route, entry?.status));
  const routedTargets = previewTargetsFromJobOutput(matchingRecords);
  return buildPreviewEnvelope({
    status: matchingRecords.length
      ? allPendingLike
        ? "Pending"
        : matchingRecords.some((entry) => previewNormalizeStatus(entry?.status) === "Failed" || previewNormalizeStatus(entry?.status) === "Timed Out")
        ? "Failed"
        : matchingRecords.some((entry) => previewNormalizeStatus(entry?.status) === "Warning")
        ? "Warning"
        : "Success"
      : "Skipped",
    data: {
      job_output: matchingRecords,
      targets: routedTargets,
    },
    metadata: {
      route_on: route,
      filtered_job_output_count: matchingRecords.length,
      target_count: routedTargets.length,
      source_output_status: previewNormalizeStatus(sourceEnvelope?.status || ""),
    },
  });
}

function buildAuthoringNodePreview({
  node,
  nodesById,
  incomingByNode,
  outgoingByNode,
  cache,
  stack,
}) {
  if (!node?.id) {
    return {
      inputEnvelope: buildPreviewEnvelope({
        status: "Warning",
        data: { inputs: [], inputs_by_port: {} },
        metadata: { reason: "missing_node" },
      }),
      outputEnvelope: buildPreviewEnvelope({
        status: "Warning",
        data: null,
        metadata: { reason: "missing_node" },
      }),
      downstream: [],
    };
  }

  if (cache.has(node.id)) {
    return cache.get(node.id);
  }

  if (stack.has(node.id)) {
    const cyclicPreview = {
      inputEnvelope: buildPreviewEnvelope({
        status: "Warning",
        data: { inputs: [], inputs_by_port: {} },
        metadata: { reason: "cycle_detected", target_node_id: node.id },
      }),
      outputEnvelope: buildPreviewEnvelope({
        status: "Warning",
        data: null,
        metadata: { reason: "cycle_detected", node_id: node.id },
      }),
      downstream: [],
    };
    cache.set(node.id, cyclicPreview);
    return cyclicPreview;
  }

  stack.add(node.id);

  const incomingEdges = incomingByNode.get(node.id) || [];
  const inputRecords = incomingEdges
    .map((edge) => {
    const portMetadata = getWorkflowEdgePortMetadata(edge, nodesById);
    const sourceNode = portMetadata.sourceNode || nodesById[String(edge?.source || "").trim()] || null;
    const sourcePreview = sourceNode
      ? buildAuthoringNodePreview({
          node: sourceNode,
          nodesById,
          incomingByNode,
          outgoingByNode,
          cache,
          stack,
        })
      : null;
    const route = portMetadata?.supportsRouteSelection
      ? getWorkflowRouteDescriptor(edge?.data?.route_on).value
      : "always";
    const sourceOutputEnvelope = sourcePreview?.outputEnvelope || buildPreviewEnvelope({ status: "Pending", data: null });
    const routedSourceOutput = portMetadata?.isJobOutputRouteEdge
      ? buildPreviewRoutedJobOutputEnvelope(sourceOutputEnvelope, route)
      : sourceOutputEnvelope;
    const isMatched = portMetadata?.isActionEdge
      ? previewRouteMatchesStatus(route, sourceOutputEnvelope?.status)
      : portMetadata?.isJobOutputRouteEdge
      ? previewJobOutputFromEnvelope(routedSourceOutput).length > 0
      : true;
    if (!isMatched) {
      return null;
    }
    return {
      edge_id: edge?.id || "",
      source_node_id: edge?.source || "",
      source_port_id: portMetadata?.sourcePort?.id || edge?.sourceHandle || "",
      source_port_label: portMetadata?.sourcePort?.label || edge?.sourceHandle || "",
      target_port_id: portMetadata?.targetPort?.id || edge?.targetHandle || "",
      target_port_label: portMetadata?.targetPort?.label || edge?.targetHandle || "",
      port_kind: portMetadata?.sourcePort?.kind || portMetadata?.targetPort?.kind || "data",
      route_on: route,
      status: routedSourceOutput?.status || sourceOutputEnvelope?.status || "Pending",
      output: routedSourceOutput,
    };
  })
    .filter(Boolean);

  const inputsByPort = inputRecords.reduce((acc, entry) => {
    const portId = String(entry?.target_port_id || "default").trim() || "default";
    if (!acc[portId]) {
      acc[portId] = {
        label: entry?.target_port_label || portId,
        kind: entry?.port_kind || "data",
        inputs: [],
      };
    }
    acc[portId].inputs.push(entry);
    return acc;
  }, {});

  const inputEnvelope = buildPreviewEnvelope({
    status: "Success",
    data: {
      inputs: inputRecords,
      inputs_by_port: inputsByPort,
    },
    metadata: {
      target_node_id: node.id,
      matched_input_count: inputRecords.length,
      matched_port_count: Object.keys(inputsByPort).length,
    },
    artifacts: {},
  });

  const nodeType = String(node?.type || "").trim();
  const nodeData = node?.data && typeof node.data === "object" ? node.data : {};
  const triggerInputs = previewPortInputs(inputEnvelope, "trigger");
  const targetInputs = previewPortInputs(inputEnvelope, "targets");

  let outputEnvelope = null;

  if (nodeType === WORKFLOW_RUNTIME_NODE_TYPES.triggerManual) {
    outputEnvelope = buildPreviewEnvelope({
      status: "Success",
      data: { trigger_source: "manual" },
      metadata: { source_type: "manual" },
    });
  } else if (nodeType === WORKFLOW_RUNTIME_NODE_TYPES.triggerScheduledJob) {
    outputEnvelope = buildPreviewEnvelope({
      status: "Success",
      data: { trigger_source: "scheduled_job" },
      metadata: { source_type: "scheduled_job" },
    });
  } else if (nodeType === WORKFLOW_RUNTIME_NODE_TYPES.triggerWebhook) {
    outputEnvelope = buildPreviewEnvelope({
      status: "Success",
      data: { trigger_source: "webhook" },
      metadata: { source_type: "webhook" },
    });
  } else if (nodeType === WORKFLOW_RUNTIME_NODE_TYPES.agentArray) {
    const targets = previewTargetsFromSelectedDevices(nodeData);
    outputEnvelope = buildPreviewEnvelope({
      status: targets.length ? "Success" : "Warning",
      data: {
        target_definition: normalizeSelectedDevices(nodeData),
        targets,
      },
      metadata: {
        target_node_type: nodeType,
        selected_count: targets.length,
        target_count: targets.length,
      },
    });
  } else if (nodeType === WORKFLOW_RUNTIME_NODE_TYPES.agentFilter) {
    const filterId = Number.parseInt(
      nodeData?.filter_id || nodeData?.agent_filter_id || nodeData?.selected_filter_id,
      10
    );
    const matchingDeviceCount = Number.isFinite(Number(nodeData?.matching_device_count))
      ? Number.parseInt(nodeData.matching_device_count, 10)
      : 0;
    outputEnvelope = buildPreviewEnvelope({
      status: filterId > 0 ? (matchingDeviceCount > 0 ? "Success" : "Warning") : "Failed",
      data: {
        target_definition: filterId > 0 ? [{ kind: "filter", filter_id: filterId }] : [],
        filter: {
          id: Number.isFinite(filterId) ? filterId : null,
          name: String(nodeData?.filter_name || nodeData?.selected_filter_name || "").trim(),
          site_mode: String(nodeData?.site_mode || "global").trim(),
          site_names: Array.isArray(nodeData?.site_names) ? nodeData.site_names : [],
        },
      },
      metadata: {
        target_node_type: nodeType,
        target_count: matchingDeviceCount,
        scope_summary: String(nodeData?.scope_summary || nodeData?.scopeSummary || "").trim(),
        targets_materialized: false,
      },
    });
  } else if (nodeType === WORKFLOW_RUNTIME_NODE_TYPES.executeAssembly) {
    const knownTargets = dedupePreviewTargets(
      targetInputs.flatMap((entry) => (Array.isArray(entry?.output?.data?.targets) ? entry.output.data.targets : []))
    );
    const countedTargets = targetInputs.reduce(
      (total, entry) => total + previewTargetCountFromEnvelope(entry?.output),
      0
    );
    const knownTargetCount = knownTargets.length;
    const previewTargetCount = Math.max(knownTargetCount, countedTargets);
    const jobOutputPreview = knownTargets.map((entry) => ({
      hostname: entry.hostname || "",
      site_id: entry.site_id ?? null,
      site_name: entry.site_name || "Not Configured",
      device_guid: entry.device_guid || "",
      agent_id: entry.agent_id || "",
      status: "Pending",
      stdout: "",
      stderr: "",
      preview_only: true,
    }));
    outputEnvelope = buildPreviewEnvelope({
      status: !String(nodeData?.assembly_guid || "").trim()
        ? "Failed"
        : triggerInputs.length && targetInputs.length
        ? "Pending"
        : "Warning",
      data: {
        results: jobOutputPreview.map((entry) => ({
          hostname: entry.hostname,
          status: entry.status,
          stdout: "",
          stderr: "",
        })),
        job_output: jobOutputPreview,
      },
      metadata: {
        assembly_guid: String(nodeData?.assembly_guid || "").trim(),
        execution_mode: String(nodeData?.execution_mode || "system").trim(),
        target_count: previewTargetCount,
        known_target_count: knownTargetCount,
        trigger_input_count: triggerInputs.length,
        target_input_count: targetInputs.length,
        note:
          previewTargetCount > knownTargetCount
            ? "Some upstream target counts are known, but exact device identities resolve at runtime."
            : "Per-device execution status populates at runtime.",
      },
      artifacts: { activity_ids: [] },
    });
  } else if (nodeType === WORKFLOW_RUNTIME_NODE_TYPES.executeSubworkflow) {
    outputEnvelope = buildPreviewEnvelope({
      status: String(nodeData?.workflow_guid || "").trim() ? "Pending" : "Failed",
      data: {
        job_output: [],
        child_workflow_guid: String(nodeData?.workflow_guid || "").trim(),
      },
      metadata: {
        export_key: String(nodeData?.export_key || "").trim(),
        note: "Child workflow result data is populated after runtime execution.",
      },
    });
  } else {
    outputEnvelope = buildPreviewEnvelope({
      status: "Pending",
      data: {
        node_type: nodeType || "unknown",
        node_data: nodeData,
      },
      metadata: {
        note: "This node does not expose a workflow-runtime preview adapter yet.",
      },
    });
  }

  const outgoingEdges = outgoingByNode.get(node.id) || [];
  const downstream = outgoingEdges.map((edge) => {
    const portMetadata = getWorkflowEdgePortMetadata(edge, nodesById);
    return {
      edge_id: edge?.id || "",
      route_on: portMetadata?.supportsRouteSelection ? getWorkflowRouteDescriptor(edge?.data?.route_on).label : "Always",
      source_port_id: portMetadata?.sourcePort?.id || edge?.sourceHandle || "",
      source_port_label: portMetadata?.sourcePort?.label || edge?.sourceHandle || "",
      target_node_id: edge?.target || "",
      target_node_label: getWorkflowRuntimeDisplayLabel(
        portMetadata?.targetNode?.type,
        portMetadata?.targetNode?.data?.label || edge?.target || "Node"
      ),
      target_port_id: portMetadata?.targetPort?.id || edge?.targetHandle || "",
      target_port_label: portMetadata?.targetPort?.label || edge?.targetHandle || "",
      port_kind: portMetadata?.sourcePort?.kind || "data",
    };
  });

  stack.delete(node.id);
  const preview = { inputEnvelope, outputEnvelope, downstream };
  cache.set(node.id, preview);
  return preview;
}

function SearchResultsDropdown({ items, loading, error, emptyText, onSelect, renderPrimary, renderSecondary }) {
  if (loading) {
    return (
      <Box
        sx={{
          mt: 1,
          borderRadius: 2,
          border: "1px solid rgba(148,163,184,0.24)",
          background: "rgba(7,12,24,0.92)",
          px: 1.5,
          py: 1.3,
          display: "flex",
          alignItems: "center",
          gap: 1,
        }}
      >
        <CircularProgress size={16} sx={{ color: "#7dd3fc" }} />
        <Typography sx={{ color: "#cbd5e1", fontSize: "0.82rem" }}>Searching…</Typography>
      </Box>
    );
  }
  if (error) {
    return (
      <Box
        sx={{
          mt: 1,
          borderRadius: 2,
          border: "1px solid rgba(248,113,113,0.28)",
          background: "rgba(38,12,18,0.78)",
          px: 1.5,
          py: 1.2,
        }}
      >
        <Typography sx={{ color: "#fecaca", fontSize: "0.8rem" }}>{error}</Typography>
      </Box>
    );
  }
  if (!items.length) {
    return (
      <Box
        sx={{
          mt: 1,
          borderRadius: 2,
          border: "1px solid rgba(148,163,184,0.18)",
          background: "rgba(7,12,24,0.78)",
          px: 1.5,
          py: 1.2,
        }}
      >
        <Typography sx={{ color: "#94a3b8", fontSize: "0.8rem" }}>{emptyText}</Typography>
      </Box>
    );
  }
  return (
    <Box
      sx={{
        mt: 1,
        borderRadius: 2,
        border: "1px solid rgba(148,163,184,0.24)",
        background: "rgba(7,12,24,0.92)",
        overflow: "hidden",
      }}
    >
      {items.map((item) => (
        <Box
          key={item.resultKey}
          onClick={() => onSelect?.(item)}
          sx={{
            px: 1.5,
            py: 1.1,
            cursor: "pointer",
            borderBottom: "1px solid rgba(148,163,184,0.12)",
            "&:last-of-type": { borderBottom: "none" },
            "&:hover": {
              background: "linear-gradient(90deg, rgba(125,211,252,0.12) 0%, rgba(125,211,252,0.04) 100%)",
            },
          }}
        >
          <Typography sx={{ color: "#e2e8f0", fontSize: "0.84rem", fontWeight: 600 }}>
            {renderPrimary?.(item)}
          </Typography>
          <Typography sx={{ color: "#94a3b8", fontSize: "0.76rem", mt: 0.3 }}>
            {renderSecondary?.(item)}
          </Typography>
        </Box>
      ))}
    </Box>
  );
}

function AgentFilterPickerField({ nodeData, disabled, onChange }) {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const selectedFilterId = Number.parseInt(
    nodeData?.filter_id || nodeData?.agent_filter_id || nodeData?.selected_filter_id,
    10
  );
  const selectedFilterName = String(
    nodeData?.filter_name || nodeData?.selected_filter_name || ""
  ).trim();
  const selectedScopeSummary = String(nodeData?.scope_summary || nodeData?.scopeSummary || "").trim();
  const selectedMatchingCount = Number.isFinite(Number(nodeData?.matching_device_count))
    ? Number.parseInt(nodeData.matching_device_count, 10)
    : null;
  const queryReady = query.trim().length >= MIN_TARGET_SEARCH_LENGTH;

  useEffect(() => {
    if (!queryReady) {
      setLoading(false);
      setError("");
      setResults([]);
      return undefined;
    }
    const controller = new AbortController();
    setLoading(true);
    setError("");
    const timer = window.setTimeout(async () => {
      try {
        const response = await fetch(`/api/device_filters/search?query=${encodeURIComponent(query.trim())}`, {
          cache: "no-store",
          credentials: "include",
          signal: controller.signal,
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
        }
        if (controller.signal.aborted) return;
        const nextResults = Array.isArray(payload?.filters)
          ? payload.filters.map((entry) => ({
              ...entry,
              resultKey: `filter-${entry.id}`,
            }))
          : [];
        setResults(nextResults);
      } catch (err) {
        if (controller.signal.aborted) return;
        setResults([]);
        setError(err?.message || "Filter search is unavailable.");
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    }, 180);
    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [query, queryReady]);

  useEffect(() => {
    if (!selectedFilterId || disabled) {
      return undefined;
    }
    const controller = new AbortController();
    const timer = window.setTimeout(async () => {
      try {
        const response = await fetch(`/api/device_filters/${encodeURIComponent(String(selectedFilterId))}`, {
          cache: "no-store",
          credentials: "include",
          signal: controller.signal,
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok || controller.signal.aborted) {
          return;
        }
        const record = payload?.filter && typeof payload.filter === "object" ? payload.filter : null;
        const nextCount = record?.matching_device_count;
        if (Number.isFinite(Number(nextCount)) && Number.parseInt(nextCount, 10) !== selectedMatchingCount) {
          onChange?.("matching_device_count", Number.parseInt(nextCount, 10));
        }
      } catch {
        // Keep the existing filter snapshot if the refresh fails.
      }
    }, 80);
    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [disabled, onChange, selectedFilterId, selectedMatchingCount]);

  const handleSelect = useCallback(
    (entry) => {
      if (!entry || disabled) return;
      onChange?.("filter_id", entry.id);
      onChange?.("agent_filter_id", entry.id);
      onChange?.("selected_filter_id", entry.id);
      onChange?.("filter_name", entry.name || "");
      onChange?.("selected_filter_name", entry.name || "");
      onChange?.("site_mode", entry.site_mode || "global");
      onChange?.("site_names", Array.isArray(entry.site_names) ? entry.site_names : []);
      onChange?.("scope_summary", entry.scope_summary || "");
      onChange?.("matching_device_count", Number.isFinite(Number(entry.matching_device_count)) ? Number.parseInt(entry.matching_device_count, 10) : "");
      setQuery("");
      setResults([]);
      setError("");
    },
    [disabled, onChange]
  );

  return (
    <Box sx={{ mb: 2 }}>
      <Typography variant="body2" sx={{ color: "#ccc", mb: 0.5 }}>
        Device Filter
      </Typography>
      <TextField
        fullWidth
        size="small"
        value={query}
        disabled={disabled}
        placeholder="Search device filters..."
        onChange={(event) => setQuery(event.target.value)}
        InputProps={{
          sx: {
            color: "#ccc",
            backgroundColor: "#1e1e1e",
            "& fieldset": { borderColor: "#444" },
            "&:hover fieldset": { borderColor: "#666" },
            "&.Mui-focused fieldset": { borderColor: "#58a6ff" },
          },
        }}
      />
      <Typography sx={{ color: "#64748b", fontSize: "0.74rem", mt: 0.6 }}>
        Minimum {MIN_TARGET_SEARCH_LENGTH} characters. Results are scoped to your visible sites.
      </Typography>
      {selectedFilterId > 0 ? (
        <Box
          sx={{
            mt: 1.15,
            borderRadius: 2,
            border: "1px solid rgba(125,211,252,0.22)",
            background: "rgba(8,14,30,0.8)",
            px: 1.35,
            py: 1.15,
          }}
        >
          <Typography sx={{ color: "#e2e8f0", fontSize: "0.84rem", fontWeight: 700 }}>
            {selectedFilterName || `Filter ${selectedFilterId}`}
          </Typography>
          <Typography sx={{ color: "#94a3b8", fontSize: "0.76rem", mt: 0.35 }}>
            {selectedScopeSummary || "Visible filter target"}
          </Typography>
          {selectedMatchingCount !== null ? (
            <Typography sx={{ color: "#7dd3fc", fontSize: "0.76rem", mt: 0.35, fontWeight: 600 }}>
              {selectedMatchingCount} Device{selectedMatchingCount === 1 ? "" : "s"}
            </Typography>
          ) : null}
        </Box>
      ) : null}
      {queryReady ? (
        <SearchResultsDropdown
          items={results}
          loading={loading}
          error={error}
          emptyText="No visible device filters matched your search."
          onSelect={handleSelect}
          renderPrimary={(item) => item.name || `Filter ${item.id}`}
          renderSecondary={(item) => item.scope_summary || item.description || "Saved device filter"}
        />
      ) : null}
    </Box>
  );
}

function AgentDeviceArrayPickerField({ nodeData, disabled, onChange }) {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const gridRef = useRef(null);
  const selectedDevices = useMemo(
    () => normalizeSelectedDevices(nodeData),
    [nodeData]
  );
  const queryReady = query.trim().length >= MIN_TARGET_SEARCH_LENGTH;

  useEffect(() => {
    if (!queryReady) {
      setLoading(false);
      setError("");
      setResults([]);
      return undefined;
    }
    const controller = new AbortController();
    setLoading(true);
    setError("");
    const timer = window.setTimeout(async () => {
      try {
        const response = await fetch(`/api/devices/search?hostname=${encodeURIComponent(query.trim())}`, {
          cache: "no-store",
          credentials: "include",
          signal: controller.signal,
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
        }
        if (controller.signal.aborted) return;
        const nextResults = Array.isArray(payload?.devices)
          ? payload.devices.map((entry) => ({
              ...entry,
              resultKey:
                String(entry?.agent_guid || "").trim().toLowerCase() ||
                `${String(entry?.hostname || "").trim().toLowerCase()}::${String(entry?.site_id ?? "").trim()}`,
              siteLabel: String(entry?.site_name || "").trim() || "Not Configured",
            }))
          : [];
        setResults(nextResults);
      } catch (err) {
        if (controller.signal.aborted) return;
        setResults([]);
        setError(err?.message || "Device search is unavailable.");
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    }, 180);
    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [query, queryReady]);

  const commitDevices = useCallback(
    (nextDevices) => {
      const normalized = sortSelectedDevices(nextDevices);
      onChange?.("selected_devices", normalized);
      onChange?.("selectedDevices", normalized);
    },
    [onChange]
  );

  const handleAddDevice = useCallback(
    (entry) => {
      if (!entry || disabled) return;
      const candidate = {
        hostname: String(entry.hostname || "").trim(),
        site_id: entry.site_id ?? null,
        site_name: String(entry.site_name || "").trim(),
        device_guid: String(entry.agent_guid || entry.device_guid || "").trim(),
        agent_id: String(entry.agent_id || "").trim(),
        connection_type: String(entry.connection_type || "").trim(),
      };
      const dedupeKey = candidate.device_guid
        ? `guid:${candidate.device_guid.toLowerCase()}`
        : `host:${candidate.hostname.toLowerCase()}::${String(candidate.site_id ?? "")}`;
      const deduped = [...selectedDevices];
      if (!deduped.some((device) => {
        const rowKey = device.device_guid
          ? `guid:${String(device.device_guid || "").toLowerCase()}`
          : `host:${String(device.hostname || "").toLowerCase()}::${String(device.site_id ?? "")}`;
        return rowKey === dedupeKey;
      })) {
        deduped.push(candidate);
      }
      commitDevices(deduped);
      setQuery("");
      setResults([]);
      setError("");
    },
    [commitDevices, disabled, selectedDevices]
  );

  const handleRemoveDevice = useCallback(
    (row) => {
      if (!row || disabled) return;
      commitDevices(
        selectedDevices.filter((device) => {
          const sameGuid = row.device_guid && device.device_guid && row.device_guid === device.device_guid;
          const sameHost =
            String(row.hostname || "").toLowerCase() === String(device.hostname || "").toLowerCase() &&
            String(row.site_id ?? "") === String(device.site_id ?? "");
          return !(sameGuid || sameHost);
        })
      );
    },
    [commitDevices, disabled, selectedDevices]
  );

  const columnDefs = useMemo(
    () => [
      {
        field: "site_name",
        headerName: "Site",
        minWidth: 150,
        flex: 1,
        suppressHeaderMenuButton: true,
        suppressHeaderContextMenu: true,
        resizable: true,
        sortable: true,
        cellClass: "auto-col-tight",
        valueGetter: (params) => params.data?.site_name || "Not Configured",
      },
      {
        field: "hostname",
        headerName: "Hostname",
        minWidth: 170,
        flex: 1.1,
        suppressHeaderMenuButton: true,
        suppressHeaderContextMenu: true,
        resizable: true,
        sortable: true,
        cellClass: "auto-col-tight",
        cellRenderer: (params) => (
          <Typography sx={{ color: "#7dd3fc", fontSize: "0.84rem", fontWeight: 600 }}>
            {params.value || "Unknown"}
          </Typography>
        ),
      },
      {
        colId: "action",
        headerName: "Action",
        minWidth: 100,
        maxWidth: 112,
        flex: 0.55,
        suppressHeaderMenuButton: true,
        suppressHeaderContextMenu: true,
        resizable: false,
        sortable: false,
        cellClass: "auto-col-tight",
        cellRenderer: (params) => (
          <IconButton
            size="small"
            disabled={disabled}
            onClick={() => handleRemoveDevice(params.data)}
            sx={{ color: "#fb7185" }}
          >
            <DeleteRoundedIcon fontSize="small" />
          </IconButton>
        ),
      },
    ],
    [disabled, handleRemoveDevice]
  );

  const defaultColDef = useMemo(
    () => ({
      editable: false,
      filter: false,
      suppressMovable: true,
      sortable: true,
      resizable: true,
    }),
    []
  );

  const targetGridRowSelection = useMemo(
    () => ({
      mode: "singleRow",
      checkboxes: false,
      headerCheckbox: false,
      enableClickSelection: true,
    }),
    []
  );

  return (
    <Box sx={{ mb: 2 }}>
      <Typography variant="body2" sx={{ color: "#ccc", mb: 0.5 }}>
        Selected Devices
      </Typography>
      <TextField
        fullWidth
        size="small"
        value={query}
        disabled={disabled}
        placeholder="Search devices by hostname..."
        onChange={(event) => setQuery(event.target.value)}
        InputProps={{
          sx: {
            color: "#ccc",
            backgroundColor: "#1e1e1e",
            "& fieldset": { borderColor: "#444" },
            "&:hover fieldset": { borderColor: "#666" },
            "&.Mui-focused fieldset": { borderColor: "#58a6ff" },
          },
        }}
      />
      <Typography sx={{ color: "#64748b", fontSize: "0.74rem", mt: 0.6 }}>
        Minimum {MIN_TARGET_SEARCH_LENGTH} characters. Results are scoped to your visible sites.
      </Typography>
      {queryReady ? (
        <SearchResultsDropdown
          items={results}
          loading={loading}
          error={error}
          emptyText="No visible devices matched your search."
          onSelect={handleAddDevice}
          renderPrimary={(item) => item.hostname || "Unknown Host"}
          renderSecondary={(item) => item.siteLabel || "Not Configured"}
        />
      ) : null}
      <Box sx={{ mt: 1.3 }}>
        <Box className={targetGridThemeClass} sx={{ ...TARGET_GRID_WRAPPER_SX, height: 250 }}>
          <AgGridReact
            ref={gridRef}
            rowData={selectedDevices}
            columnDefs={columnDefs}
            defaultColDef={defaultColDef}
            animateRows
            pagination
            paginationPageSize={20}
            paginationPageSizeSelector={[20, 50, 100]}
            suppressCellFocus
            rowSelection={targetGridRowSelection}
            domLayout="normal"
            overlayNoRowsTemplate="<span style='color:#94a3b8;'>No devices selected.</span>"
          />
        </Box>
      </Box>
    </Box>
  );
}

export default function NodeConfigurationSidebar({
  drawerOpen,
  setDrawerOpen,
  title,
  nodeData,
  setNodes,
  selectedNode,
  readOnly = false,
  runNodeRecord = null,
}) {
  const [activeTab, setActiveTab] = useState(0);
  const { setNodes: contextSetNodes, setEdges: contextSetEdges, getNodes, getEdges } = useReactFlow();
  // Use setNodes from props if provided, else fallback to context (for backward compatibility)
  const effectiveSetNodes = setNodes || contextSetNodes;
  const handleTabChange = (_, newValue) => setActiveTab(newValue);

  // Rename dialog state
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameValue, setRenameValue] = useState(title || "");

  // ---- NEW: Accent Color Picker ----
  const [colorDialogOpen, setColorDialogOpen] = useState(false);
  const accentColor = selectedNode?.data?.accentColor || "#58a6ff";
  // ----------------------------------
  const [dynamicOptionsMap, setDynamicOptionsMap] = useState({});

  useEffect(() => {
    let cancelled = false;
    const fields = Array.isArray(nodeData?.config) ? nodeData.config : [];
    const requestKeys = fields
      .filter((field) => field && typeof field === "object" && field.optionsSource)
      .map((field) => field.optionsSource);
    if (!requestKeys.length) {
      setDynamicOptionsMap({});
      return () => {
        cancelled = true;
      };
    }

    const loadOptions = async () => {
      const nextMap = {};
      try {
        if (requestKeys.some((key) => key === "workflows" || key === "assemblies_executable")) {
          const resp = await fetch("/api/assemblies");
          if (resp.ok) {
            const data = await resp.json();
            const normalized = parseAssembliesCollectionPayload(data);
            const index = buildAssemblyIndex(normalized.items, normalized.queue);
            nextMap.workflows = (index.grouped.workflows || []).map((record) => ({
              value: record.assemblyGuid,
              label: record.displayName || record.path || record.assemblyGuid,
            }));
            const executables = [...(index.grouped.scripts || []), ...(index.grouped.ansible || [])];
            nextMap.assemblies_executable = executables.map((record) => ({
              value: record.assemblyGuid,
              label: record.displayName || record.path || record.assemblyGuid,
            }));
          }
        }
      } catch {
        // keep empty options for failed lookups
      }
      if (!cancelled) {
        setDynamicOptionsMap(nextMap);
      }
    };

    loadOptions();
    return () => {
      cancelled = true;
    };
  }, [nodeData?.config]);

  const refreshWorkflowEdgesForNodes = useCallback(
    (nextNodes) => {
      contextSetEdges((currentEdges) => {
        const nodesById = (Array.isArray(nextNodes) ? nextNodes : []).reduce((acc, node) => {
          if (node?.id) {
            acc[String(node.id)] = node;
          }
          return acc;
        }, {});
        const nextEdges = (Array.isArray(currentEdges) ? currentEdges : []).map((edge) =>
          decorateWorkflowEdge(edge, { nodesById })
        );
        const currentJson = JSON.stringify(currentEdges || []);
        const nextJson = JSON.stringify(nextEdges);
        return currentJson === nextJson ? currentEdges : nextEdges;
      });
    },
    [contextSetEdges]
  );

  const authoringDebugPreview = useMemo(() => {
    if (!selectedNode?.id) {
      return null;
    }
    const allNodes = Array.isArray(getNodes?.()) ? getNodes() : [];
    const allEdges = Array.isArray(getEdges?.()) ? getEdges() : [];
    const nodesById = allNodes.reduce((acc, node) => {
      if (node?.id) {
        acc[String(node.id)] = node;
      }
      return acc;
    }, {});
    const incomingByNode = new Map();
    const outgoingByNode = new Map();
    allEdges.forEach((edge) => {
      const sourceId = String(edge?.source || "").trim();
      const targetId = String(edge?.target || "").trim();
      if (sourceId) {
        if (!outgoingByNode.has(sourceId)) {
          outgoingByNode.set(sourceId, []);
        }
        outgoingByNode.get(sourceId).push(edge);
      }
      if (targetId) {
        if (!incomingByNode.has(targetId)) {
          incomingByNode.set(targetId, []);
        }
        incomingByNode.get(targetId).push(edge);
      }
    });
    return buildAuthoringNodePreview({
      node: selectedNode,
      nodesById,
      incomingByNode,
      outgoingByNode,
      cache: new Map(),
      stack: new Set(),
    });
  }, [getEdges, getNodes, selectedNode]);

  const renderConfigFields = () => {
    const config = nodeData?.config || [];
    const nodeId = nodeData?.nodeId;

    const normalizeOptions = (opts = []) =>
      opts.map((opt) => {
        if (typeof opt === "string") {
          return { value: opt, label: opt, disabled: false };
        }
        if (opt && typeof opt === "object") {
          const val =
            opt.value ??
            opt.id ??
            opt.handle ??
            (typeof opt.label === "string" ? opt.label : "");
          const label =
            opt.label ??
            opt.name ??
            opt.title ??
            (typeof val !== "undefined" ? String(val) : "");
          return {
            value: typeof val === "undefined" ? "" : String(val),
            label: typeof label === "undefined" ? "" : String(label),
            disabled: Boolean(opt.disabled)
          };
        }
        return { value: String(opt ?? ""), label: String(opt ?? ""), disabled: false };
      });

    const updateNodeValue = (fieldKey, rawValue) => {
      if (!nodeId || readOnly) return;
      const newValue =
        fieldKey === "timeout_seconds" || fieldKey === "timeout_override_seconds"
          ? (rawValue === "" ? "" : Number(rawValue))
          : rawValue;
      effectiveSetNodes((nds) => {
        const nextNodes = nds.map((n) =>
          n.id === nodeId
            ? { ...n, data: { ...n.data, [fieldKey]: newValue } }
            : n
        );
        refreshWorkflowEdgesForNodes(nextNodes);
        return nextNodes;
      });
      if (typeof window !== "undefined" && window.BorealisValueBus) {
        window.BorealisValueBus[nodeId] = newValue;
      }
    };

    if (!config.length) {
      return (
        <Typography variant="body2" sx={{ color: "#94a3b8" }}>
          {readOnly ? "This node is read-only in workflow run mode." : "This node does not expose configurable fields."}
        </Typography>
      );
    }

    return config.map((field, index) => {
      const value = nodeData?.[field.key] ?? "";
      const isReadOnly = Boolean(field.readOnly);
      const fieldDisabled = readOnly || isReadOnly;
      const fieldType = String(field.type || "text").toLowerCase();

      if (fieldType === "agent_filter_picker") {
        return (
          <AgentFilterPickerField
            key={index}
            nodeData={nodeData}
            disabled={fieldDisabled}
            onChange={updateNodeValue}
          />
        );
      }

      if (fieldType === "agent_device_array_picker") {
        return (
          <AgentDeviceArrayPickerField
            key={index}
            nodeData={nodeData}
            disabled={fieldDisabled}
            onChange={updateNodeValue}
          />
        );
      }

      // ---- DYNAMIC DROPDOWN SUPPORT ----
      if (fieldType === "select") {
        let options = field.options || [];

        if (field.optionsKey && Array.isArray(nodeData?.[field.optionsKey])) {
          options = nodeData[field.optionsKey];
        } else if (field.optionsSource && Array.isArray(dynamicOptionsMap?.[field.optionsSource])) {
          options = dynamicOptionsMap[field.optionsSource];
        } else if (field.dynamicOptions && nodeData?.windowList && Array.isArray(nodeData.windowList)) {
          options = nodeData.windowList
            .map((win) => ({
              value: String(win.handle),
              label: `${win.title} (${win.handle})`
            }))
            .sort((a, b) => a.label.localeCompare(b.label, undefined, { sensitivity: "base" }));
        }

        options = normalizeOptions(options);

        // Handle dynamic options for things like Target Window
        if (field.dynamicOptions && (!nodeData?.windowList || !Array.isArray(nodeData.windowList))) {
          options = [];
        }

        return (
          <Box key={index} sx={{ mb: 2 }}>
            <Typography variant="body2" sx={{ color: "#ccc", mb: 0.5 }}>
              {field.label || field.key}
            </Typography>
            <TextField
              select
            fullWidth
            size="small"
            value={value}
            onChange={(e) => {
                updateNodeValue(field.key, e.target.value);
              }}
              disabled={fieldDisabled}
              placeholder={field.placeholder || ""}
              SelectProps={{
                MenuProps: {
                  PaperProps: {
                    sx: {
                      bgcolor: "#1e1e1e",
                      color: "#ccc",
                      border: "1px solid #58a6ff",
                      "& .MuiMenuItem-root": {
                        color: "#ccc",
                        fontSize: "0.85rem",
                        "&:hover": {
                          backgroundColor: "#2a2a2a"
                        },
                        "&.Mui-selected": {
                          backgroundColor: "#2c2c2c !important",
                          color: "#58a6ff"
                        },
                        "&.Mui-selected:hover": {
                          backgroundColor: "#2a2a2a !important"
                        }
                      }
                    }
                  }
                }
              }}
              sx={{
                "& .MuiOutlinedInput-root": {
                  backgroundColor: "#1e1e1e",
                  color: "#ccc",
                  fontSize: "0.85rem",
                  "& fieldset": {
                    borderColor: "#444"
                  },
                  "&:hover fieldset": {
                    borderColor: "#58a6ff"
                  },
                  "&.Mui-focused fieldset": {
                    borderColor: "#58a6ff"
                  }
                },
                "& .MuiSelect-select": {
                  backgroundColor: "#1e1e1e"
                }
              }}
            >
              {options.length === 0 ? (
                <MenuItem disabled value="">
                  {field.placeholder
                    ? field.placeholder
                    : field.label === "Target Window"
                    ? "No windows detected"
                    : "No options"}
                </MenuItem>
              ) : (
                options.map((opt, idx) => (
                  <MenuItem key={idx} value={opt.value} disabled={opt.disabled}>
                    {opt.label}
                  </MenuItem>
                ))
              )}
            </TextField>
          </Box>
        );
      }
      // ---- END DYNAMIC DROPDOWN SUPPORT ----

      return (
        <Box key={index} sx={{ mb: 2 }}>
          <Typography variant="body2" sx={{ color: "#ccc", mb: 0.5 }}>
            {field.label || field.key}
          </Typography>
          <TextField
            variant="outlined"
            size="small"
            fullWidth
            value={value}
            disabled={fieldDisabled}
            type={fieldType === "number" ? "number" : "text"}
            multiline={fieldType === "json" || Boolean(field.multiline)}
            minRows={fieldType === "json" ? 5 : field.multiline ? 3 : undefined}
            placeholder={field.placeholder || ""}
            InputProps={{
              readOnly: fieldDisabled,
              sx: {
                color: "#ccc",
                backgroundColor: "#1e1e1e",
                "& fieldset": { borderColor: "#444" },
                "&:hover fieldset": { borderColor: "#666" },
                "&.Mui-focused fieldset": { borderColor: "#58a6ff" }
              }
            }}
            onChange={(e) => {
              updateNodeValue(field.key, e.target.value);
            }}
          />
        </Box>
      );
    });
  };

  const renderDebugInfoTab = () => {
    const sections = runNodeRecord
      ? [
          ["Status", runNodeRecord.status || ""],
          ["Skip Reason", runNodeRecord.skip_reason || ""],
          ["Timeout Seconds", runNodeRecord.timeout_seconds ?? 0],
          ["Error", runNodeRecord.error || ""],
          ["Input Envelope", runNodeRecord.input_envelope || {}],
          ["Output Envelope", runNodeRecord.output_envelope || {}],
          ["Ignored Inputs", runNodeRecord.ignored_inputs || []],
          ["Linked Child Summary", runNodeRecord.linked_child_summary || {}],
        ]
      : authoringDebugPreview
      ? [
          [
            "Preview Mode",
            "Authoring preview generated from the current graph wiring and node configuration. Runtime results may differ.",
          ],
          ["Input Envelope", authoringDebugPreview.inputEnvelope || {}],
          ["Output Envelope", authoringDebugPreview.outputEnvelope || {}],
          ["Downstream Preview", authoringDebugPreview.downstream || []],
        ]
      : [];

    if (!sections.length) {
      return (
        <Typography variant="body2" sx={{ color: "#94a3b8" }}>
          Select a node to inspect its runtime output or authoring preview envelope.
        </Typography>
      );
    }

    return (
      <Box sx={{ display: "flex", flexDirection: "column", gap: 1.5 }}>
        {sections.map(([label, rawValue]) => {
          const isObject = rawValue && typeof rawValue === "object";
          const displayValue = isObject ? JSON.stringify(rawValue, null, 2) : String(rawValue || "");
          return (
            <Box
              key={label}
              sx={{
                borderRadius: "14px",
                border: "1px solid rgba(148, 163, 184, 0.18)",
                background: "rgba(15, 23, 42, 0.56)",
                overflow: "hidden",
              }}
            >
              <Box sx={{ px: 1.5, py: 0.9, borderBottom: "1px solid rgba(148, 163, 184, 0.14)" }}>
                <Typography sx={{ fontSize: "0.75rem", fontWeight: 700, color: "#7dd3fc" }}>{label}</Typography>
              </Box>
              <Box
                component="pre"
                sx={{
                  m: 0,
                  px: 1.5,
                  py: 1.25,
                  color: "#dbeafe",
                  fontSize: "0.74rem",
                  lineHeight: 1.5,
                  whiteSpace: "pre-wrap",
                  wordBreak: "break-word",
                  fontFamily: "'IBM Plex Mono','SFMono-Regular',Consolas,'Liberation Mono',Menlo,monospace",
                }}
              >
                {displayValue || "—"}
              </Box>
            </Box>
          );
        })}
      </Box>
    );
  };

  // ---- NEW: Accent Color Button ----
  const renderAccentColorButton = () => (
    <Tooltip title="Override Node Header/Accent Color">
      <IconButton
        size="small"
        aria-label="Override Node Color"
        disabled={readOnly}
        onClick={() => {
          if (readOnly) return;
          setColorDialogOpen(true);
        }}
        sx={{
          ml: 1,
          border: "1px solid #58a6ff",
          background: accentColor,
          color: "#222",
          width: 28, height: 28, p: 0
        }}
      >
        <PaletteIcon fontSize="small" />
      </IconButton>
    </Tooltip>
  );
  // ----------------------------------

  return (
    <>
      <Box
        onClick={() => setDrawerOpen(false)}
        sx={{
          position: "absolute",
          top: 0,
          left: 0,
          right: 0,
          bottom: 0,
          backgroundColor: "rgba(0, 0, 0, 0.3)",
          opacity: drawerOpen ? 1 : 0,
          pointerEvents: drawerOpen ? "auto" : "none",
          transition: "opacity 0.6s ease",
          zIndex: 10
        }}
      />

      <Box
        sx={{
          position: "absolute",
          top: 0,
          right: 0,
          bottom: 0,
          width: { xs: "100%", lg: 800 },
          maxWidth: "100%",
          bgcolor: "#2C2C2C",
          color: "#ccc",
          borderLeft: "1px solid #333",
          padding: 0,
          zIndex: 11,
          overflowY: "auto",
          transform: drawerOpen ? "translateX(0)" : "translateX(100%)",
          transition: "transform 0.3s ease"
        }}
        onClick={(e) => e.stopPropagation()}
      >
        <Box sx={{ backgroundColor: "#232323", borderBottom: "1px solid #333" }}>
          <Box sx={{ padding: "12px 16px" }}>
            <Box sx={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
              <Typography variant="h7" sx={{ color: "#0475c2", fontWeight: "bold" }}>
                {`${readOnly ? "Inspect" : "Edit"} ${title || "Node"}`}
              </Typography>
              <Box sx={{ display: "flex", alignItems: "center" }}>
                <IconButton
                  size="small"
                  aria-label="Rename Node"
                  disabled={readOnly}
                  onClick={() => {
                    if (readOnly) return;
                    setRenameValue(title || "");
                    setRenameOpen(true);
                  }}
                  sx={{ ml: 1, color: "#58a6ff" }}
                >
                  <EditIcon fontSize="small" />
                </IconButton>
                {/* ---- NEW: Accent Color Picker button next to pencil ---- */}
                {renderAccentColorButton()}
                {/* ------------------------------------------------------ */}
              </Box>
            </Box>
          </Box>
          <Tabs
            value={activeTab}
            onChange={handleTabChange}
            variant="fullWidth"
            textColor="inherit"
            TabIndicatorProps={{ style: { backgroundColor: "#ccc" } }}
            sx={{
              borderTop: "1px solid #333",
              borderBottom: "1px solid #333",
              minHeight: "36px",
              height: "36px"
            }}
          >
            <Tab
              label="Config"
              sx={{
                color: "#ccc",
                "&.Mui-selected": { color: "#ccc" },
                minHeight: "36px",
                height: "36px",
                textTransform: "none"
              }}
            />
            <Tab
              label="Usage Docs"
              sx={{
                color: "#ccc",
                "&.Mui-selected": { color: "#ccc" },
                minHeight: "36px",
                height: "36px",
                textTransform: "none"
              }}
            />
            <Tab
              label="Debug Info"
              sx={{
                color: "#ccc",
                "&.Mui-selected": { color: "#ccc" },
                minHeight: "36px",
                height: "36px",
                textTransform: "none"
              }}
            />

          </Tabs>
        </Box>

        <Box sx={{ padding: 2 }}>
          {activeTab === 0 && renderConfigFields()}
          {activeTab === 1 && (
            <Box sx={{ fontSize: "0.85rem", color: "#aaa" }}>
              <ReactMarkdown
                children={nodeData?.usage_documentation || "No usage documentation provided for this node."}
                components={{
                  h3: ({ node, ...props }) => (
                    <Typography variant="h6" sx={{ color: "#58a6ff", mb: 1 }} {...props} />
                  ),
                  p: ({ node, ...props }) => (
                    <Typography paragraph sx={{ mb: 1.5 }} {...props} />
                  ),
                  ul: ({ node, ...props }) => (
                    <ul style={{ marginBottom: "1em", paddingLeft: "1.2em" }} {...props} />
                  ),
                  li: ({ node, ...props }) => (
                    <li style={{ marginBottom: "0.5em" }} {...props} />
                  )
                }}
              />
            </Box>
          )}
          {activeTab === 2 ? renderDebugInfoTab() : null}
        </Box>
      </Box>

      {/* Rename Node Dialog */}
      <Dialog
        open={renameOpen}
        onClose={() => setRenameOpen(false)}
        maxWidth="xs"
        fullWidth
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock title="Rename Node" />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <TextField
            autoFocus
            fullWidth
            variant="outlined"
            label="Node Title"
            value={renameValue}
            onChange={(e) => setRenameValue(e.target.value)}
            sx={{ ...DIALOG_INPUT_SX, mt: 1.25 }}
          />
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button sx={DIALOG_BUTTON_SX} onClick={() => setRenameOpen(false)}>
            Cancel
          </Button>
          <Button
            sx={DIALOG_PRIMARY_BUTTON_SX}
            onClick={() => {
              // Use selectedNode (passed as prop) or nodeData?.nodeId as fallback
              const nodeId = selectedNode?.id || nodeData?.nodeId;
              if (!nodeId) {
                setRenameOpen(false);
                return;
              }
              effectiveSetNodes((nds) =>
                nds.map((n) =>
                  n.id === nodeId
                    ? { ...n, data: { ...n.data, label: renameValue } }
                    : n
                )
              );
              setRenameOpen(false);
            }}
          >
            Save
          </Button>
        </DialogActions>
      </Dialog>

      {/* ---- Accent Color Picker Dialog ---- */}
      <Dialog
        open={colorDialogOpen}
        onClose={() => setColorDialogOpen(false)}
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Pick Node Header/Accent Color"
            subtitle="Choose the accent color used for the node header and gradient treatment."
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <SketchPicker
            color={accentColor}
            onChangeComplete={(color) => {
              const nodeId = selectedNode?.id || nodeData?.nodeId;
              if (!nodeId) return;
              const accent = color.hex;
              const accentDark = darkenColor(accent, 0.7);
              effectiveSetNodes((nds) =>
                nds.map((n) =>
                  n.id === nodeId
                    ? {
                        ...n,
                        data: { ...n.data, accentColor: accent },
                        style: {
                          ...n.style,
                          "--borealis-accent": accent,
                          "--borealis-accent-dark": accentDark,
                          "--borealis-title": accent,
                        },
                      }
                    : n
                )
              );
            }}
            disableAlpha
            presetColors={[
              "#58a6ff", "#0475c2", "#00d18c", "#ff4f4f", "#ff8c00",
              "#6b21a8", "#0e7490", "#888", "#fff", "#000"
            ]}
          />
          <Box sx={{ mt: 2 }}>
            <Typography variant="body2" sx={{ color: "#cbd5e1" }}>
              The node's header text and accent gradient will use your selected color.<br />
              The accent gradient fades to a slightly darker version.
            </Typography>
            <Box sx={{ mt: 2, display: "flex", alignItems: "center" }}>
              <span style={{
                display: "inline-block",
                width: 48,
                height: 22,
                borderRadius: 4,
                border: "1px solid #888",
                background: `linear-gradient(to bottom, ${accentColor} 0%, ${darkenColor(accentColor, 0.7)} 100%)`
              }} />
              <span style={{ marginLeft: 10, color: accentColor, fontWeight: "bold" }}>
                {accentColor}
              </span>
            </Box>
          </Box>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={() => setColorDialogOpen(false)} sx={DIALOG_BUTTON_SX}>Close</Button>
        </DialogActions>
      </Dialog>
      {/* ---- END ACCENT COLOR PICKER DIALOG ---- */}
    </>
  );
}
