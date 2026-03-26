export const WORKFLOW_RUNTIME_NODE_TYPES = {
  triggerManual: "workflow_trigger_manual",
  triggerScheduledJob: "workflow_trigger_scheduled_job",
  triggerWebhook: "workflow_trigger_webhook",
  agentFilter: "workflow_agent_filter",
  agentArray: "workflow_agent_array",
  executeAssembly: "workflow_execute_assembly",
  executeSubworkflow: "workflow_execute_subworkflow",
};

export const WORKFLOW_RUNTIME_EDGE_ROUTE_ALWAYS = "always";
export const WORKFLOW_RUNTIME_EDGE_ROUTE_ON_SUCCESS = "on_success";
export const WORKFLOW_RUNTIME_EDGE_ROUTE_ON_WARNING = "on_warning";
export const WORKFLOW_RUNTIME_EDGE_ROUTE_ON_FAILED = "on_failed";

export const WORKFLOW_RUNTIME_EDGE_ROUTES = [
  { value: WORKFLOW_RUNTIME_EDGE_ROUTE_ALWAYS, label: "Always", color: "#58a6ff" },
  { value: WORKFLOW_RUNTIME_EDGE_ROUTE_ON_SUCCESS, label: "On Success", color: "#00d18c" },
  { value: WORKFLOW_RUNTIME_EDGE_ROUTE_ON_WARNING, label: "On Warning", color: "#ff8c00" },
  { value: WORKFLOW_RUNTIME_EDGE_ROUTE_ON_FAILED, label: "On Failed", color: "#ff4f4f" },
];

export const WORKFLOW_RUNTIME_TERMINAL_STATUSES = [
  "Pending",
  "Running",
  "Success",
  "Warning",
  "Failed",
  "Timed Out",
  "Skipped",
];

export const WORKFLOW_TRIGGER_TYPE_BY_SOURCE = {
  manual: WORKFLOW_RUNTIME_NODE_TYPES.triggerManual,
  scheduled_job: WORKFLOW_RUNTIME_NODE_TYPES.triggerScheduledJob,
  webhook: WORKFLOW_RUNTIME_NODE_TYPES.triggerWebhook,
  subworkflow: WORKFLOW_RUNTIME_NODE_TYPES.triggerManual,
};

export const WORKFLOW_RUNTIME_PORT_DIRECTIONS = {
  input: "input",
  output: "output",
};

export const WORKFLOW_RUNTIME_PORT_KINDS = {
  action: "action",
  data: "data",
};

export const WORKFLOW_RUNTIME_PORT_CARDINALITY = {
  single: "single",
  multi: "multi",
};

const DEFAULT_WORKFLOW_NODE_LABELS = Object.freeze({
  [WORKFLOW_RUNTIME_NODE_TYPES.triggerManual]: "Trigger - Manual",
  [WORKFLOW_RUNTIME_NODE_TYPES.triggerScheduledJob]: "Trigger - Scheduled Job",
  [WORKFLOW_RUNTIME_NODE_TYPES.triggerWebhook]: "Trigger - Webhook",
  [WORKFLOW_RUNTIME_NODE_TYPES.agentFilter]: "Device Filter",
  [WORKFLOW_RUNTIME_NODE_TYPES.agentArray]: "List of Devices",
  [WORKFLOW_RUNTIME_NODE_TYPES.executeAssembly]: "Execute Assembly",
  [WORKFLOW_RUNTIME_NODE_TYPES.executeSubworkflow]: "Execute Subworkflow",
});

const LEGACY_WORKFLOW_NODE_LABEL_ALIASES = Object.freeze({
  [WORKFLOW_RUNTIME_NODE_TYPES.agentFilter]: ["Agent Filter"],
  [WORKFLOW_RUNTIME_NODE_TYPES.agentArray]: ["Agent Array"],
});

function definePort({
  id,
  label,
  direction,
  kind,
  cardinality = WORKFLOW_RUNTIME_PORT_CARDINALITY.multi,
  required = false,
}) {
  return Object.freeze({
    id,
    label,
    direction,
    kind,
    cardinality,
    required: Boolean(required),
  });
}

const ACTION_OUT = definePort({
  id: "action",
  label: "Action",
  direction: WORKFLOW_RUNTIME_PORT_DIRECTIONS.output,
  kind: WORKFLOW_RUNTIME_PORT_KINDS.action,
});

const TARGETS_OUT = definePort({
  id: "targets",
  label: "Targets",
  direction: WORKFLOW_RUNTIME_PORT_DIRECTIONS.output,
  kind: WORKFLOW_RUNTIME_PORT_KINDS.data,
});

const JOB_OUTPUT_OUT = definePort({
  id: "job_output",
  label: "Job Output",
  direction: WORKFLOW_RUNTIME_PORT_DIRECTIONS.output,
  kind: WORKFLOW_RUNTIME_PORT_KINDS.data,
});

const TRIGGER_IN = definePort({
  id: "trigger",
  label: "Trigger",
  direction: WORKFLOW_RUNTIME_PORT_DIRECTIONS.input,
  kind: WORKFLOW_RUNTIME_PORT_KINDS.action,
  required: true,
});

const TARGETS_IN = definePort({
  id: "targets",
  label: "Targets",
  direction: WORKFLOW_RUNTIME_PORT_DIRECTIONS.input,
  kind: WORKFLOW_RUNTIME_PORT_KINDS.data,
  required: true,
});

export const WORKFLOW_RUNTIME_NODE_PORTS = Object.freeze({
  [WORKFLOW_RUNTIME_NODE_TYPES.triggerManual]: Object.freeze({
    inputs: Object.freeze([]),
    outputs: Object.freeze([ACTION_OUT]),
  }),
  [WORKFLOW_RUNTIME_NODE_TYPES.triggerScheduledJob]: Object.freeze({
    inputs: Object.freeze([]),
    outputs: Object.freeze([ACTION_OUT]),
  }),
  [WORKFLOW_RUNTIME_NODE_TYPES.triggerWebhook]: Object.freeze({
    inputs: Object.freeze([]),
    outputs: Object.freeze([ACTION_OUT]),
  }),
  [WORKFLOW_RUNTIME_NODE_TYPES.agentFilter]: Object.freeze({
    inputs: Object.freeze([]),
    outputs: Object.freeze([TARGETS_OUT]),
  }),
  [WORKFLOW_RUNTIME_NODE_TYPES.agentArray]: Object.freeze({
    inputs: Object.freeze([]),
    outputs: Object.freeze([TARGETS_OUT]),
  }),
  [WORKFLOW_RUNTIME_NODE_TYPES.executeAssembly]: Object.freeze({
    inputs: Object.freeze([TRIGGER_IN, TARGETS_IN]),
    outputs: Object.freeze([ACTION_OUT, JOB_OUTPUT_OUT]),
  }),
  [WORKFLOW_RUNTIME_NODE_TYPES.executeSubworkflow]: Object.freeze({
    inputs: Object.freeze([TRIGGER_IN]),
    outputs: Object.freeze([ACTION_OUT, JOB_OUTPUT_OUT]),
  }),
});

function normalizePortId(value) {
  return String(value || "").trim().toLowerCase();
}

function edgeRouteValue(edge) {
  return String(edge?.data?.route_on || edge?.data?.routeOn || WORKFLOW_RUNTIME_EDGE_ROUTE_ALWAYS)
    .trim()
    .toLowerCase();
}

function nodeLabel(node) {
  return getWorkflowRuntimeDisplayLabel(node?.type, node?.data?.label || node?.label || node?.type || "Node");
}

function edgeLabel(edge) {
  return String(edge?.id || `${edge?.source || "unknown"}-${edge?.target || "unknown"}`).trim();
}

function portCountsMap() {
  return new Map();
}

function incrementPortCount(collection, nodeId, portId) {
  if (!collection.has(nodeId)) {
    collection.set(nodeId, portCountsMap());
  }
  const nodePorts = collection.get(nodeId);
  nodePorts.set(portId, (nodePorts.get(portId) || 0) + 1);
}

function portCount(collection, nodeId, portId) {
  return collection.get(nodeId)?.get(portId) || 0;
}

function isFiniteDeviceCount(value) {
  return Number.isFinite(Number(value)) && Number(value) >= 0;
}

function formatDeviceCountLabel(value) {
  const count = Number(value);
  if (!Number.isFinite(count) || count < 0) return "";
  return `${count} Device${count === 1 ? "" : "s"}`;
}

function normalizeNamedCount(value) {
  if (!isFiniteDeviceCount(value)) return null;
  return Math.max(0, Number.parseInt(value, 10));
}

function legacyAliasesForType(type) {
  return LEGACY_WORKFLOW_NODE_LABEL_ALIASES[String(type || "").trim()] || [];
}

function shouldUseDefaultWorkflowLabel(type, label) {
  const trimmedLabel = String(label || "").trim();
  if (!trimmedLabel) return true;
  const defaultLabel = DEFAULT_WORKFLOW_NODE_LABELS[String(type || "").trim()];
  if (trimmedLabel === defaultLabel) return true;
  return legacyAliasesForType(type).includes(trimmedLabel);
}

function normalizedStoredDevices(nodeData = {}) {
  if (Array.isArray(nodeData.selected_devices)) {
    return nodeData.selected_devices;
  }
  if (Array.isArray(nodeData.selectedDevices)) {
    return nodeData.selectedDevices;
  }
  if (Array.isArray(nodeData.devices)) {
    return nodeData.devices;
  }
  return [];
}

export function getWorkflowRuntimeDisplayLabel(type, label) {
  const normalizedType = String(type || "").trim();
  if (!normalizedType) {
    return String(label || "").trim() || "Node";
  }
  const defaultLabel = DEFAULT_WORKFLOW_NODE_LABELS[normalizedType];
  if (!defaultLabel) {
    return String(label || "").trim() || normalizedType;
  }
  return shouldUseDefaultWorkflowLabel(normalizedType, label)
    ? defaultLabel
    : String(label || "").trim();
}

export function getWorkflowRuntimeTargetCount(node) {
  const nodeType = String(node?.type || "").trim();
  const data = node?.data && typeof node.data === "object" ? node.data : {};
  const explicitCount =
    normalizeNamedCount(data.runtime_target_count) ??
    normalizeNamedCount(data.target_count) ??
    normalizeNamedCount(data.matching_device_count) ??
    normalizeNamedCount(data.selected_count);
  if (explicitCount !== null) {
    return explicitCount;
  }
  if (nodeType === WORKFLOW_RUNTIME_NODE_TYPES.agentArray) {
    const selectedDevices = normalizedStoredDevices(data);
    return selectedDevices.length;
  }
  return null;
}

function workflowDataEdgeStroke(existingStroke = "") {
  const normalized = String(existingStroke || "").trim().toLowerCase();
  if (!normalized) return "#58a6ff";
  if (["#fff", "#ffffff", "white", "rgb(255,255,255)", "rgb(255, 255, 255)"].includes(normalized)) {
    return "#58a6ff";
  }
  return existingStroke;
}

export function getWorkflowRuntimeAutoEdgeLabel(edge, metadata = null) {
  const resolvedMetadata = metadata || {};
  if (resolvedMetadata.isActionEdge || resolvedMetadata.isJobOutputRouteEdge) {
    return getWorkflowRouteDescriptor(edgeRouteValue(edge)).label;
  }
  const sourceNode = resolvedMetadata.sourceNode || null;
  const sourcePort = resolvedMetadata.sourcePort || null;
  if (
    sourceNode &&
    sourcePort?.id === "targets" &&
    [
      WORKFLOW_RUNTIME_NODE_TYPES.agentArray,
      WORKFLOW_RUNTIME_NODE_TYPES.agentFilter,
    ].includes(String(sourceNode.type || "").trim())
  ) {
    const targetCount = getWorkflowRuntimeTargetCount(sourceNode);
    if (targetCount !== null) {
      return formatDeviceCountLabel(targetCount);
    }
  }
  return "";
}

export function normalizeWorkflowStatus(value) {
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

export function isWorkflowRuntimeNodeType(type) {
  return Boolean(WORKFLOW_RUNTIME_NODE_PORTS[String(type || "").trim()]);
}

export function getWorkflowRuntimePorts(type, direction = null) {
  const contract = WORKFLOW_RUNTIME_NODE_PORTS[String(type || "").trim()];
  if (!contract) return [];
  if (!direction) {
    return [...(contract.inputs || []), ...(contract.outputs || [])];
  }
  if (direction === WORKFLOW_RUNTIME_PORT_DIRECTIONS.input) {
    return [...(contract.inputs || [])];
  }
  if (direction === WORKFLOW_RUNTIME_PORT_DIRECTIONS.output) {
    return [...(contract.outputs || [])];
  }
  return [];
}

export function getWorkflowRuntimePort(type, direction, portId) {
  const normalizedId = normalizePortId(portId);
  return getWorkflowRuntimePorts(type, direction).find((port) => normalizePortId(port.id) === normalizedId) || null;
}

export function normalizeWorkflowEdgeRoute(route) {
  const normalized = String(route || WORKFLOW_RUNTIME_EDGE_ROUTE_ALWAYS).trim().toLowerCase();
  return WORKFLOW_RUNTIME_EDGE_ROUTES.some((entry) => entry.value === normalized)
    ? normalized
    : WORKFLOW_RUNTIME_EDGE_ROUTE_ALWAYS;
}

export function getWorkflowRouteDescriptor(route) {
  const normalized = normalizeWorkflowEdgeRoute(route);
  return (
    WORKFLOW_RUNTIME_EDGE_ROUTES.find((entry) => entry.value === normalized) ||
    WORKFLOW_RUNTIME_EDGE_ROUTES[0]
  );
}

export function getWorkflowEdgePortMetadata(edge, nodesById = {}) {
  const sourceNode = nodesById[String(edge?.source || "").trim()] || null;
  const targetNode = nodesById[String(edge?.target || "").trim()] || null;
  const sourcePort = sourceNode
    ? getWorkflowRuntimePort(
        sourceNode.type,
        WORKFLOW_RUNTIME_PORT_DIRECTIONS.output,
        edge?.sourceHandle
      )
    : null;
  const targetPort = targetNode
    ? getWorkflowRuntimePort(
        targetNode.type,
        WORKFLOW_RUNTIME_PORT_DIRECTIONS.input,
        edge?.targetHandle
      )
    : null;
  const isJobOutputRouteEdge = Boolean(
    sourcePort &&
      targetPort &&
      sourcePort.kind === "data" &&
      targetPort.kind === "data" &&
      normalizePortId(sourcePort.id) === "job_output"
  );
  return {
    sourceNode,
    targetNode,
    sourcePort,
    targetPort,
    isActionEdge: Boolean(sourcePort && targetPort && sourcePort.kind === "action" && targetPort.kind === "action"),
    isDataEdge: Boolean(sourcePort && targetPort && sourcePort.kind === "data" && targetPort.kind === "data"),
    isJobOutputRouteEdge,
    supportsRouteSelection: Boolean(
      sourcePort &&
        targetPort &&
        ((sourcePort.kind === "action" && targetPort.kind === "action") || isJobOutputRouteEdge)
    ),
  };
}

export function decorateWorkflowEdge(edge, { nodesById = {} } = {}) {
  const normalizedEdge = edge && typeof edge === "object" ? edge : {};
  const route = normalizeWorkflowEdgeRoute(edgeRouteValue(normalizedEdge));
  const metadata = getWorkflowEdgePortMetadata(normalizedEdge, nodesById);
  const autoLabel = getWorkflowRuntimeAutoEdgeLabel(normalizedEdge, metadata);
  if (metadata.supportsRouteSelection) {
    const descriptor = getWorkflowRouteDescriptor(route);
    return {
      ...normalizedEdge,
      type: normalizedEdge.type || "bezier",
      animated: true,
      label: descriptor.label,
      style: {
        ...(normalizedEdge.style || {}),
        strokeDasharray: "6 3",
        stroke: descriptor.color,
        strokeWidth: normalizedEdge.style?.strokeWidth ?? 1.6,
      },
      labelStyle: {
        ...(normalizedEdge.labelStyle || {}),
        fill: descriptor.color,
        fontWeight: normalizedEdge.labelStyle?.fontWeight || 700,
      },
      labelBgStyle: {
        ...(normalizedEdge.labelBgStyle || {}),
        fill: "#08111f",
        fillOpacity: normalizedEdge.labelBgStyle?.fillOpacity ?? 0.94,
        rx: normalizedEdge.labelBgStyle?.rx ?? 11,
        ry: normalizedEdge.labelBgStyle?.ry ?? 11,
        stroke: `${descriptor.color}66`,
        strokeWidth: normalizedEdge.labelBgStyle?.strokeWidth ?? 0.8,
      },
      labelBgPadding: normalizedEdge.labelBgPadding || [9, 4],
      data: {
        ...(normalizedEdge.data || {}),
        route_on: route,
      },
    };
  }
  const existingStyle = normalizedEdge.style || {};
  const existingStroke = workflowDataEdgeStroke(existingStyle.stroke);
  return {
    ...normalizedEdge,
    type: normalizedEdge.type || "bezier",
    animated: true,
    label: autoLabel || normalizedEdge.label || "",
    style: {
      ...(existingStyle || {}),
      strokeDasharray: existingStyle?.strokeDasharray || "6 3",
      stroke: existingStroke || "#58a6ff",
      strokeWidth: existingStyle?.strokeWidth ?? 1.45,
    },
    labelStyle: {
      ...(normalizedEdge.labelStyle || {}),
      fill: normalizedEdge.labelStyle?.fill || "#58a6ff",
      fontWeight: normalizedEdge.labelStyle?.fontWeight || 700,
    },
    labelBgStyle: {
      ...(normalizedEdge.labelBgStyle || {}),
      fill: normalizedEdge.labelBgStyle?.fill || "#08111f",
      fillOpacity: normalizedEdge.labelBgStyle?.fillOpacity ?? 0.94,
      rx: normalizedEdge.labelBgStyle?.rx ?? 11,
      ry: normalizedEdge.labelBgStyle?.ry ?? 11,
      stroke: normalizedEdge.labelBgStyle?.stroke || "rgba(88, 166, 255, 0.38)",
      strokeWidth: normalizedEdge.labelBgStyle?.strokeWidth ?? 0.8,
    },
    labelBgPadding: normalizedEdge.labelBgPadding || [9, 4],
    data: {
      ...(normalizedEdge.data || {}),
      route_on: WORKFLOW_RUNTIME_EDGE_ROUTE_ALWAYS,
    },
  };
}

export function inspectWorkflowRuntimeV1({ nodes = [], edges = [], sourceType = "manual" } = {}) {
  const errors = [];
  const warnings = [];
  const nodeIds = new Set();
  const nodeMap = new Map();
  const triggerCounts = {};
  const incomingPortCounts = new Map();
  const outgoingPortCounts = new Map();
  const incomingByTarget = new Map();
  const legacyEdgeIds = new Set();

  for (const node of Array.isArray(nodes) ? nodes : []) {
    if (!node || typeof node !== "object") {
      errors.push("Workflow nodes must be objects.");
      continue;
    }
    const nodeId = String(node.id || "").trim();
    if (!nodeId) {
      errors.push("Workflow contains a node with no id.");
      continue;
    }
    if (nodeIds.has(nodeId)) {
      errors.push(`Duplicate node id '${nodeId}'.`);
    }
    nodeIds.add(nodeId);
    nodeMap.set(nodeId, node);

    const nodeType = String(node.type || "").trim();
    if (!isWorkflowRuntimeNodeType(nodeType)) {
      errors.push(`Unsupported executable node '${nodeLabel(node)}' (${nodeType || "unknown"}).`);
    }
    if (Object.values(WORKFLOW_TRIGGER_TYPE_BY_SOURCE).includes(nodeType)) {
      triggerCounts[nodeType] = (triggerCounts[nodeType] || 0) + 1;
    }

    const data = node?.data && typeof node.data === "object" ? node.data : {};
    const selectedFilterId = Number.parseInt(
      data.filter_id || data.agent_filter_id || data.selected_filter_id,
      10
    );
    const selectedDevices = Array.isArray(data.selected_devices)
      ? data.selected_devices
      : Array.isArray(data.selectedDevices)
      ? data.selectedDevices
      : Array.isArray(data.devices)
      ? data.devices
      : [];

    if (
      nodeType === WORKFLOW_RUNTIME_NODE_TYPES.agentFilter &&
      (!Number.isFinite(selectedFilterId) || selectedFilterId <= 0)
    ) {
      errors.push(`Device Filter node '${nodeLabel(node)}' is missing a device filter selection.`);
    }
    if (
      nodeType === WORKFLOW_RUNTIME_NODE_TYPES.agentArray &&
      selectedDevices.length === 0
    ) {
      errors.push(`List of Devices node '${nodeLabel(node)}' does not contain any selected devices.`);
    }
    if (
      nodeType === WORKFLOW_RUNTIME_NODE_TYPES.executeAssembly &&
      !String(data.assembly_guid || data.assemblyGuid || "").trim()
    ) {
      errors.push(`Execute Assembly node '${String(data.label || "Execute Assembly")}' is missing an assembly selection.`);
    }
    if (
      nodeType === WORKFLOW_RUNTIME_NODE_TYPES.executeSubworkflow &&
      !String(data.workflow_guid || data.workflowGuid || "").trim()
    ) {
      errors.push(`Execute Subworkflow node '${String(data.label || "Execute Subworkflow")}' is missing a workflow selection.`);
    }
  }

  Object.entries(triggerCounts).forEach(([type, count]) => {
    if (count > 1) {
      errors.push(`Workflow may contain at most one '${type}' trigger node.`);
    }
  });

  const requiredTrigger = WORKFLOW_TRIGGER_TYPE_BY_SOURCE[String(sourceType || "manual").trim().toLowerCase()];
  if (requiredTrigger && triggerCounts[requiredTrigger] !== 1) {
    errors.push(`Workflow requires exactly one '${String(sourceType || "manual").replace(/_/g, " ")}' trigger node for this launch source.`);
  }

  const indegree = {};
  const adjacency = {};
  nodeIds.forEach((nodeId) => {
    indegree[nodeId] = 0;
    adjacency[nodeId] = [];
  });

  for (const edge of Array.isArray(edges) ? edges : []) {
    if (!edge || typeof edge !== "object") {
      errors.push("Workflow edges must be objects.");
      continue;
    }
    const source = String(edge.source || "").trim();
    const target = String(edge.target || "").trim();
    if (!nodeIds.has(source) || !nodeIds.has(target)) {
      errors.push("Workflow contains an edge that references a missing node.");
      continue;
    }

    const sourceNode = nodeMap.get(source);
    const targetNode = nodeMap.get(target);
    const sourceHandle = normalizePortId(edge.sourceHandle);
    const targetHandle = normalizePortId(edge.targetHandle);
    const sourcePort = getWorkflowRuntimePort(
      sourceNode?.type,
      WORKFLOW_RUNTIME_PORT_DIRECTIONS.output,
      sourceHandle
    );
    const targetPort = getWorkflowRuntimePort(
      targetNode?.type,
      WORKFLOW_RUNTIME_PORT_DIRECTIONS.input,
      targetHandle
    );
    const route = normalizeWorkflowEdgeRoute(edgeRouteValue(edge));
    const edgeId = edgeLabel(edge);

    if (isWorkflowRuntimeNodeType(sourceNode?.type) && !sourcePort) {
      legacyEdgeIds.add(edgeId);
      errors.push(
        `Workflow edge '${edgeId}' uses legacy output wiring on '${nodeLabel(sourceNode)}'. Reconnect it to a named output port.`
      );
    }
    if (isWorkflowRuntimeNodeType(targetNode?.type) && !targetPort) {
      legacyEdgeIds.add(edgeId);
      errors.push(
        `Workflow edge '${edgeId}' uses legacy input wiring on '${nodeLabel(targetNode)}'. Reconnect it to a named input port.`
      );
    }

    if (sourcePort && targetPort) {
      if (sourcePort.kind !== targetPort.kind) {
        errors.push(
          `Workflow edge '${edgeId}' connects incompatible ports ('${sourcePort.label}' to '${targetPort.label}').`
        );
      }
      if (
        sourcePort.kind === WORKFLOW_RUNTIME_PORT_KINDS.data &&
        route !== WORKFLOW_RUNTIME_EDGE_ROUTE_ALWAYS &&
        normalizePortId(sourcePort.id) !== "job_output"
      ) {
        warnings.push(
          `Workflow edge '${edgeId}' is a data edge. Route rules are only supported on Action edges and Job Output edges.`
        );
      }
      incrementPortCount(outgoingPortCounts, source, sourcePort.id);
      incrementPortCount(incomingPortCounts, target, targetPort.id);
    }

    indegree[target] += 1;
    adjacency[source].push(target);
    if (!incomingByTarget.has(target)) {
      incomingByTarget.set(target, []);
    }
    incomingByTarget.get(target).push(edge);
  }

  for (const [nodeId, node] of nodeMap.entries()) {
    const nodeType = String(node?.type || "").trim();
    if (!isWorkflowRuntimeNodeType(nodeType)) continue;
    const contract = WORKFLOW_RUNTIME_NODE_PORTS[nodeType];

    for (const port of contract.inputs || []) {
      const count = portCount(incomingPortCounts, nodeId, port.id);
      if (port.required && count === 0) {
        errors.push(`Node '${nodeLabel(node)}' requires a '${port.label}' input connection.`);
      }
      if (port.cardinality === WORKFLOW_RUNTIME_PORT_CARDINALITY.single && count > 1) {
        errors.push(`Node '${nodeLabel(node)}' allows only one '${port.label}' input connection.`);
      }
    }

    for (const port of contract.outputs || []) {
      const count = portCount(outgoingPortCounts, nodeId, port.id);
      if (port.cardinality === WORKFLOW_RUNTIME_PORT_CARDINALITY.single && count > 1) {
        errors.push(`Node '${nodeLabel(node)}' allows only one '${port.label}' output connection.`);
      }
    }
  }

  const queue = Object.keys(indegree).filter((nodeId) => indegree[nodeId] === 0);
  let visited = 0;
  while (queue.length) {
    const current = queue.shift();
    visited += 1;
    for (const next of adjacency[current] || []) {
      indegree[next] -= 1;
      if (indegree[next] === 0) {
        queue.push(next);
      }
    }
  }
  if (nodeIds.size && visited !== nodeIds.size) {
    errors.push("Workflow graph contains a cycle. Workflow Runtime v1 only supports acyclic graphs.");
  }

  return {
    errors,
    warnings,
    hasLegacyPortIssues: legacyEdgeIds.size > 0,
    legacyEdgeIds: [...legacyEdgeIds],
  };
}

export function validateWorkflowRuntimeV1(args = {}) {
  return inspectWorkflowRuntimeV1(args).errors;
}
