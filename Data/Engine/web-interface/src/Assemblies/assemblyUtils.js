/**
 * Shared assembly utilities for normalizing API payloads, decoding legacy script bodies,
 * and building frontend-friendly indexes from the cache-backed assemblies REST API.
 */

import { resolveDomainMeta } from "./Assembly_Badges";

const KIND_PREFIX = {
  ansible: "Ansible_Playbooks",
  workflow: "Workflows",
  script: "Scripts"
};

const TRIM_PREFIX_MATCHERS = [
  /^scripts\//,
  /^ansible_playbooks\//,
  /^workflows\//
];

const FALLBACK_ASSEMBLY_NAME = "Assembly";

const toLowerSafe = (value) => (typeof value === "string" ? value.toLowerCase() : "");

const sanitizeNameForPath = (value, fallback = FALLBACK_ASSEMBLY_NAME) => {
  if (typeof value !== "string" || !value.trim()) return fallback;
  return value.trim().replace(/[^a-zA-Z0-9._-]+/g, "_");
};

export function decodeBase64String(data = "") {
  if (typeof data !== "string") {
    return { success: false, value: "" };
  }

  const trimmed = data.trim();
  if (!trimmed) {
    return { success: true, value: "" };
  }

  const sanitized = trimmed.replace(/\s+/g, "");

  try {
    if (typeof window !== "undefined" && typeof window.atob === "function") {
      const binary = window.atob(sanitized);
      if (typeof TextDecoder !== "undefined") {
        try {
          const decoder = new TextDecoder("utf-8", { fatal: false });
          return {
            success: true,
            value: decoder.decode(Uint8Array.from(binary, (c) => c.charCodeAt(0)))
          };
        } catch {
          // fall through to manual reconstruction
        }
      }

      let decoded = "";
      for (let i = 0; i < binary.length; i += 1) {
        decoded += String.fromCharCode(binary.charCodeAt(i));
      }
      try {
        return { success: true, value: decodeURIComponent(escape(decoded)) };
      } catch {
        return { success: true, value: decoded };
      }
    }
  } catch {
    // fall through to Buffer fallback
  }

  try {
    if (typeof Buffer !== "undefined") {
      return { success: true, value: Buffer.from(sanitized, "base64").toString("utf-8") };
    }
  } catch {
    // ignore Buffer decode errors
  }

  return { success: false, value: "" };
}

export function normalizeVariablesFromServer(vars = []) {
  return (Array.isArray(vars) ? vars : []).map((v, idx) => ({
    id: `${Date.now()}_${idx}_${Math.random().toString(36).slice(2, 8)}`,
    name: v?.name || v?.key || "",
    label: v?.label || "",
    type: v?.type || "string",
    defaultValue: v?.default ?? v?.default_value ?? "",
    required: Boolean(v?.required),
    description: v?.description || ""
  }));
}

export function normalizeFilesFromServer(files = []) {
  return (Array.isArray(files) ? files : []).map((f, idx) => ({
    id: `${Date.now()}_${idx}_${Math.random().toString(36).slice(2, 8)}`,
    fileName: f?.file_name || f?.name || "file.bin",
    size: f?.size || 0,
    mimeType: f?.mime_type || f?.mimeType || "",
    data: f?.data || ""
  }));
}

export function normalizeAssemblyVariables(vars = []) {
  return normalizeVariablesFromServer(vars).map((v) => ({
    name: v.name || "",
    label: v.label || v.name || "",
    type: (v.type || "string").toLowerCase(),
    required: Boolean(v.required),
    description: v.description || "",
    default: v.defaultValue ?? ""
  }));
}

export function normalizeAssemblyFiles(files = []) {
  return normalizeFilesFromServer(files);
}

export function normalizeAssemblyPath(kind = "script", rawPath = "", fallbackName = "") {
  const kindKey = toLowerSafe(kind);
  const prefix = KIND_PREFIX[kindKey] || KIND_PREFIX.script;

  let candidate = typeof rawPath === "string" ? rawPath : "";
  candidate = candidate.replace(/\\/g, "/").replace(/^\/+/, "").trim();

  const lowered = candidate.toLowerCase();
  const prefixLower = prefix.toLowerCase();
  if (lowered.startsWith(`${prefixLower}/`)) {
    candidate = candidate.slice(prefix.length + 1);
  }

  if (!candidate) {
    const safeName = sanitizeNameForPath(fallbackName, FALLBACK_ASSEMBLY_NAME);
    candidate = safeName;
  }

  const normalized = `${prefix}/${candidate}`.replace(/\/+/g, "/");
  return normalized;
}

export function canonicalPathKey(domain, path) {
  const domainPart = (domain || "user").toLowerCase();
  const pathPart = (path || "").replace(/\\/g, "/").replace(/^\/+/, "").toLowerCase();
  return `${domainPart}:${pathPart}`;
}

export function normalizeAssemblyRecord(item, queueEntry = null) {
  if (!item || typeof item !== "object") {
    return null;
  }

  const assemblyGuid = (item.assembly_guid || item.assembly_id || "").toString().trim();
  if (!assemblyGuid) {
    return null;
  }

  const metadata = item.metadata && typeof item.metadata === "object" ? { ...item.metadata } : {};
  const domain = toLowerSafe(item.source || item.domain || metadata.domain || "user") || "user";
  const domainMeta = resolveDomainMeta(domain);

  let kind = toLowerSafe(item.assembly_kind || metadata.assembly_kind);
  const typeHint = toLowerSafe(item.assembly_type || metadata.assembly_type);
  if (!kind) {
    if (typeHint === "ansible") kind = "ansible";
    else if (typeHint === "workflow") kind = "workflow";
    else kind = "script";
  }

  let assemblyType = typeHint || (kind === "ansible" ? "ansible" : kind === "workflow" ? "workflow" : "powershell");
  if (!assemblyType) {
    assemblyType = kind === "ansible" ? "ansible" : kind === "workflow" ? "workflow" : "powershell";
  }

  const displayName =
    item.display_name ||
    metadata.display_name ||
    item.summary ||
    metadata.name ||
    sanitizeNameForPath(assemblyGuid);
  const summary = item.summary || metadata.summary || "";

  const rawPath =
    metadata.source_path ||
    metadata.rel_path ||
    metadata.legacy_path ||
    metadata.path ||
    metadata.relative_path ||
    "";
  const normalizedPath = normalizeAssemblyPath(kind, rawPath, displayName);
  const pathLower = normalizedPath.toLowerCase();
  let pathLowerNoPrefix = pathLower;
  TRIM_PREFIX_MATCHERS.forEach((matcher) => {
    if (pathLowerNoPrefix.match(matcher)) {
      pathLowerNoPrefix = pathLowerNoPrefix.replace(matcher, "");
    }
  });

  const queueDirtySince = queueEntry?.dirty_since || item.dirty_since || null;
  const queueLastPersisted = queueEntry?.last_persisted || item.last_persisted || null;
  const queueIsDirty =
    typeof queueEntry?.is_dirty === "string"
      ? queueEntry.is_dirty.toLowerCase() === "true"
      : queueEntry?.is_dirty ?? Boolean(item.is_dirty);

  const segments = normalizedPath.split("/").filter(Boolean);

  return {
    assemblyGuid,
    assemblyGuidLower: assemblyGuid.toLowerCase(),
    displayName,
    summary,
    kind,
    type: assemblyType,
    domain,
    domainLabel: domainMeta.label,
    metadata,
    tags: item.tags && typeof item.tags === "object" ? { ...item.tags } : {},
    path: normalizedPath,
    pathLower,
    pathLowerNoPrefix,
    segments,
    isDirty: queueIsDirty,
    dirtySince: queueDirtySince,
    lastPersisted: queueLastPersisted,
    payloadGuid: item.payload_guid,
    createdAt: item.created_at,
    updatedAt: item.updated_at,
    queueEntry,
    raw: item
  };
}

export function buildAssemblyIndex(items = [], queue = []) {
  const queueMap = new Map();
  (Array.isArray(queue) ? queue : []).forEach((entry) => {
    if (!entry || typeof entry !== "object") return;
    const guid = (entry.assembly_guid || entry.guid || "").toString().trim().toLowerCase();
    if (guid) {
      queueMap.set(guid, entry);
    }
  });

  const records = [];
  const byGuid = new Map();
  const byPath = new Map();

  (Array.isArray(items) ? items : []).forEach((item) => {
    const guidLower = (item?.assembly_guid || item?.assembly_id || "").toString().trim().toLowerCase();
    const queueEntry = guidLower ? queueMap.get(guidLower) || null : null;
    const record = normalizeAssemblyRecord(item, queueEntry);
    if (!record) return;
    records.push(record);
    byGuid.set(record.assemblyGuidLower, record);

    const registerPath = (path) => {
      if (!path) return;
      const key = path.toLowerCase();
      if (!byPath.has(key)) {
        byPath.set(key, record);
      }
    };

    registerPath(record.pathLower);
    registerPath(record.pathLowerNoPrefix);
  });

  records.sort((a, b) => {
    const left = a.pathLower;
    const right = b.pathLower;
    if (left < right) return -1;
    if (left > right) return 1;
    return a.displayName.localeCompare(b.displayName, undefined, { sensitivity: "base" });
  });

  const grouped = {
    scripts: records.filter((r) => r.kind === "script" && r.type !== "ansible"),
    ansible: records.filter((r) => r.kind === "ansible" || r.type === "ansible"),
    workflows: records.filter((r) => r.kind === "workflow")
  };

  return { records, byGuid, byPath, grouped };
}

function sortChildren(node) {
  if (!node || !Array.isArray(node.children) || !node.children.length) return;
  node.children.sort((a, b) => {
    if (a.isFolder !== b.isFolder) {
      return a.isFolder ? -1 : 1;
    }
    return (a.label || "").localeCompare(b.label || "", undefined, { sensitivity: "base" });
  });
  node.children.forEach(sortChildren);
}

export function buildAssemblyTree(records = [], { rootLabel = "Assemblies", includeDomain = true } = {}) {
  const map = {};
  const root = {
    id: "root",
    label: rootLabel,
    isFolder: true,
    children: []
  };
  map[root.id] = root;

  const domainNodes = new Map();
  const sortedRecords = Array.isArray(records) ? [...records] : [];
  sortedRecords.sort((a, b) => a.pathLower.localeCompare(b.pathLower));

  sortedRecords.forEach((record) => {
    let parent = root;
    if (includeDomain) {
      let domainNode = domainNodes.get(record.domain);
      if (!domainNode) {
        domainNode = {
          id: `domain:${record.domain}`,
          label: record.domainLabel || record.domain,
          isFolder: true,
          domain: record.domain,
          children: []
        };
        domainNodes.set(record.domain, domainNode);
        root.children.push(domainNode);
        map[domainNode.id] = domainNode;
      }
      parent = domainNode;
    }

    const segments = record.segments.length ? record.segments : [record.displayName || record.assemblyGuid];
    let aggregate = "";
    segments.forEach((segment, index) => {
      const safeSegment = segment || record.displayName || FALLBACK_ASSEMBLY_NAME;
      aggregate = aggregate ? `${aggregate}/${safeSegment}` : safeSegment;
      const nodeKey = canonicalPathKey(record.domain, aggregate);
      let node = map[nodeKey];
      const isLeaf = index === segments.length - 1;
      if (!node) {
        node = {
          id: nodeKey,
          label: isLeaf ? record.displayName || safeSegment : safeSegment,
          isFolder: !isLeaf,
          domain: record.domain,
          children: []
        };
        parent.children.push(node);
        map[nodeKey] = node;
      }
      if (isLeaf) {
        node.assembly = record;
        node.assemblyGuid = record.assemblyGuid;
        node.path = record.path;
        node.label = record.displayName || safeSegment;
        map[`assembly:${record.assemblyGuidLower}`] = node;
      } else {
        parent = node;
      }
    });
  });

  sortChildren(root);
  return { root: root.children, map };
}

export function parseAssemblyExport(exportDoc) {
  if (!exportDoc || typeof exportDoc !== "object") {
    return {
      metadata: {},
      payload: {},
      kind: "script",
      type: "powershell",
      script: "",
      variables: [],
      variablesDetailed: [],
      files: [],
      rawVariables: [],
      rawFiles: [],
      timeoutSeconds: 0,
      sites: { mode: "all", values: [] }
    };
  }

  const metadata = exportDoc.metadata && typeof exportDoc.metadata === "object" ? { ...exportDoc.metadata } : {};
  let payload = exportDoc.payload;
  if (typeof payload === "string") {
    try {
      payload = JSON.parse(payload);
    } catch {
      payload = {};
    }
  }
  if (!payload || typeof payload !== "object") {
    payload = {};
  }

  const kindHint = toLowerSafe(exportDoc.assembly_kind || metadata.assembly_kind || payload.assembly_kind);
  const typeHint = toLowerSafe(exportDoc.assembly_type || metadata.assembly_type || payload.type);
  let kind = "script";
  if (kindHint === "ansible" || typeHint === "ansible") {
    kind = "ansible";
  } else if (kindHint === "workflow" || (Array.isArray(payload.nodes) && Array.isArray(payload.edges))) {
    kind = "workflow";
  }
  let type = typeHint;
  if (!type) {
    if (kind === "ansible") type = "ansible";
    else if (kind === "workflow") type = "workflow";
    else type = "powershell";
  }

  const scriptLines = Array.isArray(payload.script_lines) ? payload.script_lines : null;
  let scriptSource = "";
  if (typeof payload.script === "string") scriptSource = payload.script;
  else if (typeof payload.content === "string") scriptSource = payload.content;
  else if (scriptLines) scriptSource = scriptLines.map((line) => (line == null ? "" : String(line))).join("\n");

  const encodingHint = toLowerSafe(
    payload.script_encoding ||
      payload.scriptEncoding ||
      metadata.script_encoding ||
      metadata.scriptEncoding
  );
  let script = "";
  if (typeof scriptSource === "string") {
    if (encodingHint && ["base64", "b64", "base-64"].includes(encodingHint)) {
      const decoded = decodeBase64String(scriptSource);
      script = decoded.success ? decoded.value : scriptSource;
    } else {
      const decoded = decodeBase64String(scriptSource);
      script = decoded.success ? decoded.value : scriptSource.replace(/\r\n/g, "\n");
    }
  }

  const variablesRaw = Array.isArray(payload.variables)
    ? payload.variables
    : Array.isArray(metadata.variables)
      ? metadata.variables
      : [];
  const filesRaw = Array.isArray(payload.files)
    ? payload.files
    : Array.isArray(metadata.files)
      ? metadata.files
      : [];

  const variablesDetailed = normalizeVariablesFromServer(variablesRaw);
  const variables = normalizeAssemblyVariables(variablesRaw);
  const files = normalizeAssemblyFiles(filesRaw);

  const timeoutCandidate =
    payload.timeout_seconds ??
    payload.timeout ??
    metadata.timeout_seconds ??
    metadata.timeoutSeconds ??
    metadata.timeout ??
    null;
  const timeoutSeconds = Number.isFinite(Number(timeoutCandidate)) ? Number(timeoutCandidate) : 0;

  const sites =
    (payload.sites && typeof payload.sites === "object" ? payload.sites : null) ||
    (metadata.sites && typeof metadata.sites === "object" ? metadata.sites : null) ||
    { mode: "all", values: [] };

  return {
    metadata,
    payload,
    kind,
    type,
    script,
    variables,
    variablesDetailed,
    files,
    rawVariables: variablesRaw,
    rawFiles: filesRaw,
    timeoutSeconds,
    sites
  };
}

export function resolveAssemblyForComponent(index, component = {}, kindHint = null) {
  if (!index || typeof index !== "object") return null;
  const { byGuid, byPath } = index;
  const guidRaw =
    component?.assembly_guid ||
    component?.assemblyGuid ||
    component?.assembly_id ||
    component?.assemblyID ||
    component?.guid;
  if (guidRaw) {
    const guid = guidRaw.toString().trim().toLowerCase();
    if (guid && byGuid?.has(guid)) {
      return byGuid.get(guid);
    }
  }

  const determineKind = () => {
    const componentType = toLowerSafe(component?.type || component?.component_type || component?.mode);
    if (componentType === "ansible" || componentType === "playbook") return "ansible";
    if (componentType === "workflow") return "workflow";
    if (kindHint) return kindHint;
    return "script";
  };

  const kind = determineKind();
  const candidatePaths = [];
  const pushCandidate = (value) => {
    if (!value || typeof value !== "string") return;
    const normalized = normalizeAssemblyPath(kind, value);
    if (!candidatePaths.includes(normalized)) {
      candidatePaths.push(normalized);
    }
    const trimmed = normalized.toLowerCase().replace(TRIM_PREFIX_MATCHERS[0], "");
    if (trimmed && !candidatePaths.includes(trimmed)) {
      candidatePaths.push(trimmed);
    }
  };

  const rawPath =
    component?.path ||
    component?.script_path ||
    component?.scriptPath ||
    component?.playbook_path ||
    component?.playbookPath ||
    component?.workflow_path ||
    component?.workflowPath ||
    "";
  if (rawPath) {
    pushCandidate(rawPath);
    pushCandidate(rawPath.replace(/\\/g, "/"));
  }

  const nameHint = component?.name || component?.tab_name || component?.display_name;
  if (nameHint) {
    pushCandidate(nameHint);
  }

  for (const candidate of candidatePaths) {
    const key = candidate.toLowerCase();
    if (byPath?.has(key)) {
      return byPath.get(key);
    }
  }

  return null;
}
