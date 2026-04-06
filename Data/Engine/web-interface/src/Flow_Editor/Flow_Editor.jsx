import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, Typography } from "@mui/material";
import { ReactFlowProvider } from "reactflow";

import FlowEditorCanvas from "./Flow_Editor_Canvas.jsx";
import FlowEditorSidebar from "./Flow_Editor_Sidebar.jsx";
import FlowEditorStatusBar from "./Flow_Editor_Status_Bar.jsx";
import {
  buildWorkflowAuthoringDocument,
  extractWorkflowCanvasDocument,
  generateWorkflowAssemblyGuid,
  inspectWorkflowRuntimeDocument,
  validateWorkflowRuntimeDocument,
} from "./workflowDocuments";
import {
  decorateWorkflowEdge,
  getWorkflowRuntimeDisplayLabel,
  WORKFLOW_RUNTIME_NODE_TYPES,
} from "./runtimeV1.js";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";

const WORKFLOW_CHILD_STATUS_BADGE_ORDER = [
  "Success",
  "Warning",
  "Failed",
  "Timed Out",
  "Skipped",
  "Running",
  "Pending",
];

const WORKFLOW_DRAFT_STORAGE_KEY = "borealis_workflow_editor_draft_v1";

function createWorkflowDocumentId(prefix = "flow") {
  return `${prefix}_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
}

function createDefaultWorkflowDocument(overrides = {}) {
  return {
    id: createWorkflowDocumentId(),
    tab_name: "Flow 1",
    description: "",
    nodes: [],
    edges: [],
    assemblyGuid: null,
    domain: "user",
    exportMetadata: null,
    readOnly: false,
    workflowRunId: null,
    ...overrides,
  };
}

function parseWorkflowRunEnvelope(value) {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    return value;
  }
  if (typeof value !== "string") {
    return {};
  }
  try {
    const parsed = JSON.parse(value);
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : {};
  } catch {
    return {};
  }
}

function summarizeWorkflowRunNodePresentation(runRow, nodeType) {
  const outputEnvelope = parseWorkflowRunEnvelope(runRow?.output_envelope);
  const outputData = outputEnvelope?.data && typeof outputEnvelope.data === "object" ? outputEnvelope.data : {};
  const outputMetadata =
    outputEnvelope?.metadata && typeof outputEnvelope.metadata === "object" ? outputEnvelope.metadata : {};
  const childResults = Array.isArray(outputData.job_output)
    ? outputData.job_output
    : Array.isArray(outputData.results)
    ? outputData.results
    : [];
  const statusCounts = new Map();
  childResults.forEach((result) => {
    const key = String(result?.status || "").trim();
    if (!key) return;
    statusCounts.set(key, (statusCounts.get(key) || 0) + 1);
  });
  const badgeParts = WORKFLOW_CHILD_STATUS_BADGE_ORDER
    .filter((status) => statusCounts.has(status))
    .map((status) => `${statusCounts.get(status)} ${status}`);
  const badgeLabel =
    String(nodeType || "").trim() === WORKFLOW_RUNTIME_NODE_TYPES.executeAssembly && badgeParts.length > 1
      ? badgeParts.join(" | ")
      : String(runRow?.status || "").trim();
  const runtimeTargetCount =
    Number.isFinite(Number(outputMetadata?.target_count))
      ? Number.parseInt(outputMetadata.target_count, 10)
      : Array.isArray(outputData?.targets)
      ? outputData.targets.length
      : null;
  return {
    badgeLabel,
    runtimeTargetCount,
  };
}

function normalizeWorkflowCanvasNode(node, runRow = null) {
  const baseNode = node && typeof node === "object" ? node : {};
  const presentation = runRow ? summarizeWorkflowRunNodePresentation(runRow, baseNode?.type) : null;
  const normalizedLabel = getWorkflowRuntimeDisplayLabel(
    baseNode?.type,
    baseNode?.data?.label || baseNode?.label || ""
  );
  return {
    ...baseNode,
    data: {
      ...(baseNode?.data || {}),
      label: normalizedLabel,
      ...(runRow
        ? {
            node_execution_status: runRow?.status || "",
            runtimeStatus: runRow?.status || "",
            node_status_badge_label: presentation?.badgeLabel || "",
            runtime_target_count: presentation?.runtimeTargetCount,
          }
        : {}),
    },
  };
}

function decorateWorkflowCanvasEdges(edges = [], nodes = []) {
  const nodesById = (Array.isArray(nodes) ? nodes : []).reduce((acc, node) => {
    if (node?.id) {
      acc[String(node.id)] = node;
    }
    return acc;
  }, {});
  return (Array.isArray(edges) ? edges : []).map((edge) => decorateWorkflowEdge(edge, { nodesById }));
}

async function enrichWorkflowCanvasNodesWithFilterCounts(nodes = []) {
  const normalizedNodes = (Array.isArray(nodes) ? nodes : []).map((node) => normalizeWorkflowCanvasNode(node));
  const unresolvedFilterIds = [
    ...new Set(
      normalizedNodes
        .filter((node) => String(node?.type || "").trim() === WORKFLOW_RUNTIME_NODE_TYPES.agentFilter)
        .map((node) =>
          Number.parseInt(node?.data?.filter_id || node?.data?.agent_filter_id || node?.data?.selected_filter_id, 10)
        )
        .filter((filterId) => Number.isFinite(filterId) && filterId > 0)
        .filter((filterId) => {
          const matchNode = normalizedNodes.find(
            (node) =>
              String(node?.type || "").trim() === WORKFLOW_RUNTIME_NODE_TYPES.agentFilter &&
              Number.parseInt(
                node?.data?.filter_id || node?.data?.agent_filter_id || node?.data?.selected_filter_id,
                10
              ) === filterId
          );
          return !Number.isFinite(Number(matchNode?.data?.matching_device_count));
        }),
    ),
  ];

  if (!unresolvedFilterIds.length) {
    return normalizedNodes;
  }

  const countByFilterId = new Map();
  await Promise.all(
    unresolvedFilterIds.map(async (filterId) => {
      try {
        const response = await fetch(`/api/device_filters/${encodeURIComponent(String(filterId))}`, {
          cache: "no-store",
          credentials: "include",
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          return;
        }
        const count = payload?.filter?.matching_device_count;
        if (Number.isFinite(Number(count))) {
          countByFilterId.set(filterId, Number.parseInt(count, 10));
        }
      } catch {
        /* keep node usable even if filter count enrichment fails */
      }
    })
  );

  return normalizedNodes.map((node) => {
    if (String(node?.type || "").trim() !== WORKFLOW_RUNTIME_NODE_TYPES.agentFilter) {
      return node;
    }
    const filterId = Number.parseInt(
      node?.data?.filter_id || node?.data?.agent_filter_id || node?.data?.selected_filter_id,
      10
    );
    if (!Number.isFinite(filterId) || filterId <= 0 || !countByFilterId.has(filterId)) {
      return node;
    }
    return {
      ...node,
      data: {
        ...(node?.data || {}),
        matching_device_count: countByFilterId.get(filterId),
      },
    };
  });
}

function normalizeSavedWorkflowDraft(rawValue) {
  const raw = rawValue && typeof rawValue === "object" ? rawValue : {};
  return createDefaultWorkflowDocument({
    id: typeof raw.id === "string" && raw.id.trim() ? raw.id : createWorkflowDocumentId(),
    tab_name: typeof raw.tab_name === "string" && raw.tab_name.trim() ? raw.tab_name : "Flow 1",
    description: typeof raw.description === "string" ? raw.description : "",
    nodes: Array.isArray(raw.nodes) ? raw.nodes : [],
    edges: Array.isArray(raw.edges) ? raw.edges : [],
    assemblyGuid: typeof raw.assemblyGuid === "string" && raw.assemblyGuid.trim() ? raw.assemblyGuid.trim() : null,
    domain: typeof raw.domain === "string" && raw.domain.trim() ? raw.domain.trim().toLowerCase() : "user",
    exportMetadata: raw.exportMetadata && typeof raw.exportMetadata === "object" ? raw.exportMetadata : null,
    readOnly: false,
    workflowRunId: null,
  });
}

export default function FlowEditor({
  routeState = null,
  navigateTo,
  onPageMetaChange,
}) {
  const [workflowDoc, setWorkflowDoc] = useState(() => createDefaultWorkflowDocument());
  const [workflowMode, setWorkflowMode] = useState("editor");
  const [workflowRun, setWorkflowRun] = useState(null);
  const [nodeRunDetails, setNodeRunDetails] = useState({});
  const [workflowAccessWarning, setWorkflowAccessWarning] = useState({
    open: false,
    message: "",
    hiddenDevices: [],
    hiddenFilters: [],
  });
  const fileInputRef = useRef(null);
  const lastRouteSignatureRef = useRef(null);

  const routeRunId = useMemo(() => {
    const parsed = Number(routeState?.runId);
    return Number.isInteger(parsed) && parsed > 0 ? parsed : null;
  }, [routeState?.runId]);

  const routeAssemblyGuid = useMemo(() => {
    const value = typeof routeState?.assemblyGuid === "string" ? routeState.assemblyGuid.trim() : "";
    return value || null;
  }, [routeState?.assemblyGuid]);

  const routeSignature = routeRunId
    ? `run:${routeRunId}`
    : routeAssemblyGuid
    ? `assembly:${routeAssemblyGuid.toLowerCase()}`
    : "editor:new";

  const readOnly = Boolean(workflowDoc?.readOnly || workflowMode === "run");

  useEffect(() => {
    onPageMetaChange?.(null);
    return () => {
      onPageMetaChange?.(null);
    };
  }, [onPageMetaChange]);

  useEffect(() => {
    if (workflowMode !== "editor") {
      return undefined;
    }
    const timeoutId = window.setTimeout(() => {
      try {
        localStorage.setItem(
          WORKFLOW_DRAFT_STORAGE_KEY,
          JSON.stringify({
            id: workflowDoc?.id || createWorkflowDocumentId(),
            tab_name: workflowDoc?.tab_name || "Flow 1",
            description: workflowDoc?.description || "",
            nodes: Array.isArray(workflowDoc?.nodes) ? workflowDoc.nodes : [],
            edges: Array.isArray(workflowDoc?.edges) ? workflowDoc.edges : [],
            assemblyGuid: workflowDoc?.assemblyGuid || null,
            domain: workflowDoc?.domain || "user",
            exportMetadata: workflowDoc?.exportMetadata || null,
          })
        );
      } catch {
        /* draft persistence is best-effort */
      }
    }, 500);
    return () => window.clearTimeout(timeoutId);
  }, [workflowDoc, workflowMode]);

  const setNodes = useCallback((callbackOrArray) => {
    setWorkflowDoc((prev) => ({
      ...prev,
      nodes:
        typeof callbackOrArray === "function"
          ? callbackOrArray(Array.isArray(prev?.nodes) ? prev.nodes : [])
          : callbackOrArray,
    }));
  }, []);

  const setEdges = useCallback((callbackOrArray) => {
    setWorkflowDoc((prev) => ({
      ...prev,
      edges:
        typeof callbackOrArray === "function"
          ? callbackOrArray(Array.isArray(prev?.edges) ? prev.edges : [])
          : callbackOrArray,
    }));
  }, []);

  const enforceWorkflowEditorAccess = useCallback(async (assemblyGuid) => {
    const normalizedGuid = String(assemblyGuid || "").trim();
    if (!normalizedGuid) {
      return { allowed: true };
    }

    const response = await fetch(`/api/workflows/${encodeURIComponent(normalizedGuid)}/editor-access`, {
      credentials: "include",
      cache: "no-store",
    });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
    }
    if (payload?.allowed) {
      return { allowed: true, payload };
    }

    setWorkflowAccessWarning({
      open: true,
      message:
        payload?.message ||
        "This workflow references targets outside your assigned sites and cannot be opened.",
      hiddenDevices: Array.isArray(payload?.hidden_devices) ? payload.hidden_devices : [],
      hiddenFilters: Array.isArray(payload?.hidden_filters) ? payload.hidden_filters : [],
    });
    setWorkflowRun(null);
    setNodeRunDetails({});
    setWorkflowMode("editor");
    setWorkflowDoc(createDefaultWorkflowDocument());
    return { allowed: false, payload };
  }, []);

  const loadWorkflowRunSnapshot = useCallback(async (runId, cancelledRef) => {
    try {
      const resp = await fetch(`/api/workflows/runs/${encodeURIComponent(String(runId))}`);
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.message || data?.error || `HTTP ${resp.status}`);
      }
      if (cancelledRef.current) return;

      const nodeRuns = Array.isArray(data?.node_runs) ? data.node_runs : [];
      const nodeRunMap = new Map(nodeRuns.map((row) => [row.node_id, row]));
      const snapshot = data?.graph_snapshot || {};
      const hydratedNodes = (Array.isArray(snapshot?.nodes) ? snapshot.nodes : []).map((node) => ({
        ...normalizeWorkflowCanvasNode(node, nodeRunMap.get(node?.id)),
        draggable: false,
      }));
      const hydratedEdges = decorateWorkflowCanvasEdges(
        Array.isArray(snapshot?.edges) ? snapshot.edges : [],
        hydratedNodes
      );

      setWorkflowDoc(
        createDefaultWorkflowDocument({
          id: `workflow_run_${data.id}`,
          tab_name: data?.workflow_name || `Workflow Run ${data?.id || ""}`.trim(),
          description: "",
          nodes: hydratedNodes,
          edges: hydratedEdges,
          assemblyGuid: data?.workflow_guid || null,
          domain: "user",
          exportMetadata: data,
          readOnly: true,
          workflowRunId: data?.id || null,
        })
      );
      setWorkflowRun(data);
      setNodeRunDetails({});
      setWorkflowMode("run");
    } catch (error) {
      console.error("Failed to load workflow run snapshot:", error);
    }
  }, []);

  const loadWorkflowEditorResource = useCallback(async (assemblyGuid, cancelledRef) => {
    try {
      const access = await enforceWorkflowEditorAccess(assemblyGuid);
      if (!access?.allowed || cancelledRef.current) {
        return;
      }

      const resp = await fetch(`/api/assemblies/${encodeURIComponent(String(assemblyGuid).trim())}/export`);
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.message || data?.error || `HTTP ${resp.status}`);
      }
      if (cancelledRef.current) return;

      const parsedWorkflow = extractWorkflowCanvasDocument(data);
      const normalizedNodes = await enrichWorkflowCanvasNodesWithFilterCounts(parsedWorkflow.nodes);
      if (cancelledRef.current) return;
      const normalizedEdges = decorateWorkflowCanvasEdges(parsedWorkflow.edges, normalizedNodes);

      setWorkflowDoc(
        createDefaultWorkflowDocument({
          id: `workflow_${String(assemblyGuid).trim().toLowerCase()}`,
          tab_name: parsedWorkflow.tabName || data?.name || "Workflow",
          description: parsedWorkflow.description || "",
          nodes: normalizedNodes,
          edges: normalizedEdges,
          assemblyGuid: parsedWorkflow.assemblyGuid || String(assemblyGuid).trim(),
          domain: (data?.domain || "user").toLowerCase(),
          exportMetadata: data,
          readOnly: false,
          workflowRunId: null,
        })
      );
      setWorkflowRun(null);
      setNodeRunDetails({});
      setWorkflowMode("editor");
    } catch (error) {
      console.error("Failed to load workflow editor resource:", error);
    }
  }, [enforceWorkflowEditorAccess]);

  useEffect(() => {
    if (lastRouteSignatureRef.current === routeSignature) {
      return undefined;
    }
    lastRouteSignatureRef.current = routeSignature;

    const cancelledRef = { current: false };
    if (routeRunId) {
      loadWorkflowRunSnapshot(routeRunId, cancelledRef);
      return () => {
        cancelledRef.current = true;
      };
    }

    if (routeAssemblyGuid) {
      loadWorkflowEditorResource(routeAssemblyGuid, cancelledRef);
      return () => {
        cancelledRef.current = true;
      };
    }

    setWorkflowRun(null);
    setNodeRunDetails({});
    setWorkflowMode("editor");
    try {
      const saved = localStorage.getItem(WORKFLOW_DRAFT_STORAGE_KEY);
      if (saved) {
        const parsed = JSON.parse(saved);
        setWorkflowDoc(normalizeSavedWorkflowDraft(parsed));
      } else {
        setWorkflowDoc(createDefaultWorkflowDocument());
      }
    } catch {
      setWorkflowDoc(createDefaultWorkflowDocument());
    }

    return () => {
      cancelledRef.current = true;
    };
  }, [loadWorkflowEditorResource, loadWorkflowRunSnapshot, routeAssemblyGuid, routeRunId, routeSignature]);

  const workflowDiagnostics = useMemo(() => {
    if (workflowMode === "run") {
      return { errors: [], warnings: [], hasLegacyPortIssues: false };
    }
    return inspectWorkflowRuntimeDocument({
      nodes: Array.isArray(workflowDoc?.nodes) ? workflowDoc.nodes : [],
      edges: Array.isArray(workflowDoc?.edges) ? workflowDoc.edges : [],
      sourceType: "manual",
    });
  }, [workflowDoc?.edges, workflowDoc?.nodes, workflowMode]);

  const nodeRunLookup = useMemo(() => {
    const merged = {};
    const nodeRuns = Array.isArray(workflowRun?.node_runs) ? workflowRun.node_runs : [];
    nodeRuns.forEach((row) => {
      if (row?.node_id) {
        merged[row.node_id] = row;
      }
    });
    Object.entries(nodeRunDetails || {}).forEach(([nodeId, row]) => {
      if (nodeId) {
        merged[nodeId] = { ...(merged[nodeId] || {}), ...(row || {}) };
      }
    });
    return merged;
  }, [nodeRunDetails, workflowRun?.node_runs]);

  const handleWorkflowRunNodeSelection = useCallback(
    async (nodeId) => {
      if (!routeRunId || !nodeId) return;
      if (nodeRunDetails?.[nodeId]) return;
      try {
        const resp = await fetch(
          `/api/workflows/runs/${encodeURIComponent(String(routeRunId))}/nodes/${encodeURIComponent(String(nodeId))}`
        );
        const data = await resp.json().catch(() => ({}));
        if (!resp.ok) {
          throw new Error(data?.message || data?.error || `HTTP ${resp.status}`);
        }
        setNodeRunDetails((prev) => ({
          ...(prev || {}),
          [nodeId]: data,
        }));
      } catch (error) {
        console.error("Failed to load workflow node run details:", error);
      }
    },
    [nodeRunDetails, routeRunId]
  );

  const handleExportFlow = useCallback(() => {
    const existingGuid = typeof workflowDoc?.assemblyGuid === "string" ? workflowDoc.assemblyGuid.trim() : "";
    const workflowGuid = existingGuid || generateWorkflowAssemblyGuid();
    if (!existingGuid) {
      setWorkflowDoc((prev) => ({ ...prev, assemblyGuid: workflowGuid }));
    }
    const payload = buildWorkflowAuthoringDocument({
      assemblyGuid: workflowGuid,
      name: workflowDoc?.tab_name || "Flow 1",
      description: workflowDoc?.description || workflowDoc?.exportMetadata?.description || "",
      nodes: Array.isArray(workflowDoc?.nodes) ? workflowDoc.nodes : [],
      edges: Array.isArray(workflowDoc?.edges) ? workflowDoc.edges : [],
    });
    const fileName = `${workflowDoc?.tab_name || "workflow"}.json`;
    const blob = new Blob([JSON.stringify(payload, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = fileName;
    anchor.click();
    URL.revokeObjectURL(url);
  }, [workflowDoc]);

  const handleImportFlow = useCallback(() => {
    if (fileInputRef.current) {
      fileInputRef.current.value = null;
      fileInputRef.current.click();
    }
  }, []);

  const handleFileInputChange = useCallback(
    (event) => {
      const file = event.target.files && event.target.files[0];
      if (!file) return;

      const reader = new FileReader();
      reader.onload = () => {
        try {
          const parsed = JSON.parse(reader.result);
          const importedWorkflow = extractWorkflowCanvasDocument(parsed);
          setWorkflowDoc(
            createDefaultWorkflowDocument({
              id: createWorkflowDocumentId(),
              tab_name: importedWorkflow.tabName || file.name.replace(/\.json$/i, ""),
              description: importedWorkflow.description || "",
              nodes: Array.isArray(importedWorkflow.nodes) ? importedWorkflow.nodes : [],
              edges: Array.isArray(importedWorkflow.edges) ? importedWorkflow.edges : [],
              assemblyGuid: importedWorkflow.assemblyGuid || null,
              domain: "user",
              exportMetadata: null,
              readOnly: false,
              workflowRunId: null,
            })
          );
          setWorkflowRun(null);
          setNodeRunDetails({});
          setWorkflowMode("editor");
          lastRouteSignatureRef.current = "editor:new";
          navigateTo?.("workflow-editor", { assemblyGuid: null, runId: null, replace: true });
        } catch (error) {
          console.error("Failed to import workflow:", error);
        }
      };
      reader.readAsText(file);
      event.target.value = "";
    },
    [navigateTo]
  );

  const handleSaveFlow = useCallback(
    async (name) => {
      const trimmedName = String(name || "").trim();
      if (!trimmedName) return null;

      const document = buildWorkflowAuthoringDocument({
        assemblyGuid: workflowDoc?.assemblyGuid || null,
        name: trimmedName,
        description: workflowDoc?.description || workflowDoc?.exportMetadata?.description || "",
        nodes: Array.isArray(workflowDoc?.nodes) ? workflowDoc.nodes : [],
        edges: Array.isArray(workflowDoc?.edges) ? workflowDoc.edges : [],
      });

      try {
        const resp = await fetch("/api/assemblies/import", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            document,
            domain: workflowDoc?.domain || "user",
            assembly_guid: workflowDoc?.assemblyGuid || undefined,
          }),
        });
        const data = await resp.json().catch(() => ({}));
        if (!resp.ok) {
          throw new Error(data?.error || data?.message || `HTTP ${resp.status}`);
        }

        const savedAssemblyGuid = data?.assembly_guid || workflowDoc?.assemblyGuid || null;
        const nextDomain = (data?.source || data?.domain || workflowDoc?.domain || "user").toLowerCase();
        setWorkflowDoc((prev) => ({
          ...prev,
          tab_name: trimmedName,
          description: prev?.description || prev?.exportMetadata?.description || "",
          assemblyGuid: savedAssemblyGuid || prev?.assemblyGuid || null,
          domain: nextDomain,
        }));

        if (savedAssemblyGuid && !routeRunId) {
          navigateTo?.("workflow-editor", {
            assemblyGuid: savedAssemblyGuid,
            runId: null,
            replace: true,
          });
        }

        return {
          assemblyGuid: savedAssemblyGuid,
          domain: nextDomain,
        };
      } catch (error) {
        console.error("Failed to save workflow:", error);
        return null;
      }
    },
    [navigateTo, routeRunId, workflowDoc]
  );

  const handleRenameFlow = useCallback(
    async (name) => {
      const normalizedGuid = typeof workflowDoc?.assemblyGuid === "string" ? workflowDoc.assemblyGuid.trim() : "";
      const normalizedDomain = (workflowDoc?.domain || "user").toLowerCase();
      if (!normalizedGuid || normalizedDomain !== "user") {
        return;
      }
      await handleSaveFlow(name);
    },
    [handleSaveFlow, workflowDoc]
  );

  const handleDeleteFlow = useCallback(async () => {
    const normalizedGuid = typeof workflowDoc?.assemblyGuid === "string" ? workflowDoc.assemblyGuid.trim() : "";
    const normalizedDomain = (workflowDoc?.domain || "user").toLowerCase();
    if (!normalizedGuid || normalizedDomain !== "user") {
      return;
    }

    try {
      const resp = await fetch(`/api/assemblies/${encodeURIComponent(normalizedGuid)}`, {
        method: "DELETE",
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.error || data?.message || `HTTP ${resp.status}`);
      }

      const emptyDocument = createDefaultWorkflowDocument();
      setWorkflowDoc(emptyDocument);
      setWorkflowRun(null);
      setNodeRunDetails({});
      setWorkflowMode("editor");
      lastRouteSignatureRef.current = "editor:new";
      navigateTo?.("workflow-editor", { assemblyGuid: null, runId: null, replace: true });
    } catch (error) {
      console.error("Failed to delete workflow:", error);
    }
  }, [navigateTo, workflowDoc]);

  const handleTriggerWorkflow = useCallback(async () => {
    const validationErrors = validateWorkflowRuntimeDocument({
      nodes: Array.isArray(workflowDoc?.nodes) ? workflowDoc.nodes : [],
      edges: Array.isArray(workflowDoc?.edges) ? workflowDoc.edges : [],
      sourceType: "manual",
    });
    if (validationErrors.length) {
      alert(validationErrors.join("\n"));
      return;
    }

    const saved = await handleSaveFlow(workflowDoc?.tab_name || "workflow");
    const workflowGuid = saved?.assemblyGuid || workflowDoc?.assemblyGuid || null;
    if (!workflowGuid) {
      alert("Save this workflow before triggering it.");
      return;
    }

    try {
      const resp = await fetch("/api/workflows/run", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ workflow_guid: workflowGuid }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.message || data?.error || `HTTP ${resp.status}`);
      }
      const runId = data?.run?.id;
      if (!runId) {
        throw new Error("Workflow runtime did not return a run id.");
      }
      navigateTo?.("workflow-editor", { runId });
    } catch (error) {
      console.error("Failed to trigger workflow:", error);
      alert(String(error?.message || error || "Failed to trigger workflow"));
    }
  }, [handleSaveFlow, navigateTo, workflowDoc]);

  const handleCloseAccessWarning = useCallback(() => {
    setWorkflowAccessWarning({
      open: false,
      message: "",
      hiddenDevices: [],
      hiddenFilters: [],
    });
    navigateTo?.("assemblies", { replace: true });
  }, [navigateTo]);

  return (
    <>
      <Box
        className="flow-editor-shell"
        sx={{ display: "flex", flexDirection: "column", flexGrow: 1, overflow: "hidden", minWidth: 0 }}
      >
        <Box sx={{ display: "flex", flexGrow: 1, overflow: "hidden", minWidth: 0 }}>
          <FlowEditorSidebar
            handleExportFlow={handleExportFlow}
            handleImportFlow={handleImportFlow}
            handleSaveFlow={handleSaveFlow}
            handleRenameFlow={handleRenameFlow}
            handleDeleteFlow={handleDeleteFlow}
            handleTriggerWorkflow={handleTriggerWorkflow}
            fileInputRef={fileInputRef}
            onFileInputChange={handleFileInputChange}
            currentTabName={workflowDoc?.tab_name}
            currentAssemblyGuid={workflowDoc?.assemblyGuid}
            currentDomain={workflowDoc?.domain}
            readOnly={readOnly}
            workflowRun={workflowRun}
          />
          <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, overflow: "hidden", minWidth: 0 }}>
            {workflowMode !== "run" && workflowDiagnostics?.hasLegacyPortIssues ? (
              <Box
                sx={{
                  mx: 1.5,
                  mt: 1.2,
                  mb: 0.8,
                  px: 1.5,
                  py: 1.15,
                  borderRadius: 2,
                  border: "1px solid rgba(251,191,36,0.28)",
                  background: "linear-gradient(135deg, rgba(120,53,15,0.42), rgba(32,18,7,0.72))",
                  color: "#fef3c7",
                  boxShadow: "0 10px 24px rgba(2,8,23,0.22)",
                }}
              >
                <Typography sx={{ fontSize: "0.82rem", fontWeight: 700, color: "#fde68a" }}>
                  Legacy Workflow Wiring Detected
                </Typography>
                <Typography sx={{ fontSize: "0.76rem", mt: 0.35, color: "#fcd34d", lineHeight: 1.45 }}>
                  This workflow uses older Borealis workflow edges without the new named ports. You can edit it here,
                  but Borealis will block manual runs, webhook launches, and Scheduled Job selection until those
                  edges are reconnected to the new port rows.
                </Typography>
                {workflowDiagnostics?.errors?.length ? (
                  <Typography sx={{ fontSize: "0.73rem", mt: 0.55, color: "#fde68a" }}>
                    First issue: {workflowDiagnostics.errors[0]}
                  </Typography>
                ) : null}
              </Box>
            ) : null}
            <Box sx={{ flexGrow: 1, position: "relative", minWidth: 0 }}>
              <ReactFlowProvider id={workflowDoc?.id || "flow_editor"}>
                <FlowEditorCanvas
                  flowId={workflowDoc?.id || "flow_editor"}
                  nodes={Array.isArray(workflowDoc?.nodes) ? workflowDoc.nodes : []}
                  edges={Array.isArray(workflowDoc?.edges) ? workflowDoc.edges : []}
                  setNodes={setNodes}
                  setEdges={setEdges}
                  readOnly={readOnly}
                  nodeRunLookup={nodeRunLookup}
                  onSelectedNodeChange={(nodeId) => {
                    if (workflowMode === "run" && nodeId) {
                      handleWorkflowRunNodeSelection(nodeId);
                    }
                  }}
                />
              </ReactFlowProvider>
            </Box>
            <FlowEditorStatusBar
              nodeCount={Array.isArray(workflowDoc?.nodes) ? workflowDoc.nodes.length : 0}
              mode={workflowMode === "run" ? "run" : "editor"}
              workflowRun={workflowRun}
            />
          </Box>
        </Box>
      </Box>

      <Dialog
        open={workflowAccessWarning.open}
        onClose={handleCloseAccessWarning}
        fullWidth
        maxWidth="sm"
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Workflow Target Access Restricted"
            subtitle="This workflow references targets outside your assigned site scope, so Borealis returned you to the assembly list."
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Typography sx={{ color: "#cbd5e1", fontSize: "0.82rem" }}>
            {workflowAccessWarning.message ||
              "This workflow contains device or filter targets that you are not allowed to access."}
          </Typography>
          {workflowAccessWarning.hiddenFilters.length ? (
            <>
              <Typography sx={{ color: "#f8fafc", fontWeight: 700, fontSize: "0.82rem", mt: 1.5, mb: 0.8 }}>
                Hidden Filters
              </Typography>
              {workflowAccessWarning.hiddenFilters.map((entry, index) => (
                <Typography
                  key={`hidden-filter-${index}`}
                  sx={{ color: "#cbd5e1", fontSize: "0.8rem", mb: 0.45 }}
                >
                  {entry.filter_name || `Filter ${entry.filter_id || ""}`.trim()}
                </Typography>
              ))}
            </>
          ) : null}
          {workflowAccessWarning.hiddenDevices.length ? (
            <>
              <Typography sx={{ color: "#f8fafc", fontWeight: 700, fontSize: "0.82rem", mt: 1.5, mb: 0.8 }}>
                Hidden Devices
              </Typography>
              {workflowAccessWarning.hiddenDevices.map((entry, index) => (
                <Typography
                  key={`hidden-device-${index}`}
                  sx={{ color: "#cbd5e1", fontSize: "0.8rem", mb: 0.45 }}
                >
                  {entry.hostname}
                  {entry.site_name ? ` (${entry.site_name})` : ""}
                </Typography>
              ))}
            </>
          ) : null}
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={handleCloseAccessWarning} sx={DIALOG_BUTTON_SX}>
            Close
          </Button>
        </DialogActions>
      </Dialog>
    </>
  );
}
