import { decodeBase64String } from "../Assemblies/assemblyUtils";
import { inspectWorkflowRuntimeV1, validateWorkflowRuntimeV1 } from "./runtimeV1";

export function generateWorkflowAssemblyGuid() {
  try {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
      return crypto.randomUUID().toUpperCase();
    }
  } catch {
    // fall through to manual GUID generation
  }

  const template = "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx";
  return template.replace(/[xy]/g, (ch) => {
    const random = Math.floor(Math.random() * 16);
    const value = ch === "x" ? random : ((random & 0x3) | 0x8);
    return value.toString(16);
  }).toUpperCase();
}

export function encodeWorkflowPayloadBase64(payload) {
  const normalized = JSON.stringify(payload ?? {}, null, 0);
  try {
    if (typeof TextEncoder !== "undefined" && typeof window !== "undefined" && typeof window.btoa === "function") {
      const encoder = new TextEncoder();
      const bytes = encoder.encode(normalized);
      let binary = "";
      bytes.forEach((byte) => {
        binary += String.fromCharCode(byte);
      });
      return window.btoa(binary);
    }
  } catch {
    // fall through to alternate encoders
  }

  try {
    if (typeof Buffer !== "undefined") {
      return Buffer.from(normalized, "utf-8").toString("base64");
    }
  } catch {
    // ignored
  }

  throw new Error("Base64 encoding is unavailable in this environment.");
}

export function buildWorkflowAuthoringDocument({
  assemblyGuid = null,
  name = "Workflow",
  description = "",
  nodes = [],
  edges = [],
}) {
  const workflowPayload = {
    tab_name: name,
    nodes: Array.isArray(nodes) ? nodes : [],
    edges: Array.isArray(edges) ? edges : [],
  };
  return {
    assembly_guid: assemblyGuid || undefined,
    name,
    description: typeof description === "string" ? description : "",
    type: "workflow",
    workflow: encodeWorkflowPayloadBase64(workflowPayload),
  };
}

export function extractWorkflowCanvasDocument(document) {
  const root = document && typeof document === "object" ? document : {};
  const parseWorkflowPayload = (value) => {
    if (!value) return null;
    if (typeof value === "object" && !Array.isArray(value)) {
      return value;
    }
    if (typeof value !== "string") {
      return null;
    }

    const text = value.trim();
    const decoded = decodeBase64String(text);
    if (decoded.success) {
      try {
        const parsedDecoded = JSON.parse(decoded.value);
        if (parsedDecoded && typeof parsedDecoded === "object" && !Array.isArray(parsedDecoded)) {
          return parsedDecoded;
        }
      } catch {
        // fall through to raw JSON parsing for legacy documents
      }
    }

    try {
      const parsed = JSON.parse(text);
      return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : null;
    } catch {
      return null;
    }
  };

  const workflowPayload =
    parseWorkflowPayload(root.workflow) ||
    parseWorkflowPayload(root.payload) ||
    (root.payload && typeof root.payload === "object" && !Array.isArray(root.payload) ? root.payload : null) ||
    (Array.isArray(root.nodes) || Array.isArray(root.edges) ? root : null) ||
    {};

  const assemblyGuidRaw =
    typeof root.assembly_guid === "string"
      ? root.assembly_guid
      : typeof root.assemblyGuid === "string"
        ? root.assemblyGuid
        : typeof workflowPayload?.assembly_guid === "string"
          ? workflowPayload.assembly_guid
          : typeof workflowPayload?.assemblyGuid === "string"
            ? workflowPayload.assemblyGuid
            : "";

  return {
    assemblyGuid: assemblyGuidRaw.trim() || null,
    tabName:
      workflowPayload?.tab_name ||
      root?.tab_name ||
      root?.name ||
      root?.display_name ||
      "Workflow",
    description:
      root?.description ||
      root?.summary ||
      workflowPayload?.description ||
      "",
    nodes: Array.isArray(workflowPayload?.nodes) ? workflowPayload.nodes : [],
    edges: Array.isArray(workflowPayload?.edges) ? workflowPayload.edges : [],
    payload: workflowPayload,
  };
}

export function validateWorkflowRuntimeDocument({ nodes = [], edges = [], sourceType = "manual" } = {}) {
  return validateWorkflowRuntimeV1({ nodes, edges, sourceType });
}

export function inspectWorkflowRuntimeDocument({ nodes = [], edges = [], sourceType = "manual" } = {}) {
  return inspectWorkflowRuntimeV1({ nodes, edges, sourceType });
}
