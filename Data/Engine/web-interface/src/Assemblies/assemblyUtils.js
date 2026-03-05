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

const firstText = (...values) => {
  for (const value of values) {
    if (value == null) continue;
    const text = String(value).trim();
    if (text) return text;
  }
  return "";
};

const parseObjectMaybe = (value) => {
  if (!value) return null;
  if (typeof value === "object") return value;
  if (typeof value !== "string") return null;
  const trimmed = value.trim();
  if (!trimmed) return null;
  try {
    const parsed = JSON.parse(trimmed);
    return parsed && typeof parsed === "object" ? parsed : null;
  } catch {
    return null;
  }
};

const normalizeKindFromHints = (kindHint, subtypeHint, payload = null) => {
  const kind = toLowerSafe(kindHint);
  const subtype = toLowerSafe(subtypeHint);
  if (kind === "ansible" || subtype === "ansible" || subtype === "playbook") return "ansible";
  if (kind === "workflow" || subtype === "workflow") return "workflow";
  if (kind === "script" || kind === "powershell" || kind === "batch" || kind === "bash") return "script";
  if (payload && Array.isArray(payload.nodes) && Array.isArray(payload.edges)) return "workflow";
  if (payload && (typeof payload.script === "string" || Array.isArray(payload.script_lines))) return "script";
  return "script";
};

const normalizeSubtypeFromHints = (subtypeHint, kind, payload = null) => {
  const subtype = toLowerSafe(subtypeHint);
  if (subtype) return subtype;
  if (kind === "workflow") return "workflow";
  if (kind === "ansible") return "ansible";
  if (payload && Array.isArray(payload.nodes) && Array.isArray(payload.edges)) return "workflow";
  return "powershell";
};

const queueGuidKey = (entry) =>
  firstText(
    entry?.assembly_guid,
    entry?.assemblyGuid,
    entry?.assembly_id,
    entry?.assemblyId,
    entry?.guid,
    entry?.id
  ).toLowerCase();

export function parseAssembliesCollectionPayload(rawPayload) {
  if (Array.isArray(rawPayload)) {
    return { items: rawPayload, queue: [] };
  }

  const root =
    rawPayload && typeof rawPayload === "object" && rawPayload.data && typeof rawPayload.data === "object"
      ? rawPayload.data
      : rawPayload;

  const items =
    (Array.isArray(root?.items) && root.items) ||
    (Array.isArray(root?.assemblies) && root.assemblies) ||
    (Array.isArray(root?.results) && root.results) ||
    (Array.isArray(root?.rows) && root.rows) ||
    [];
  const queue =
    (Array.isArray(root?.queue) && root.queue) ||
    (Array.isArray(root?.pending) && root.pending) ||
    (Array.isArray(root?.queued) && root.queued) ||
    (Array.isArray(root?.write_queue) && root.write_queue) ||
    [];

  return { items, queue };
}

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

  const assemblyGuid = firstText(
    item.assembly_guid,
    item.assemblyGuid,
    item.assembly_id,
    item.assemblyId,
    item.guid,
    item.id
  );
  if (!assemblyGuid) {
    return null;
  }

  const payloadFromItem =
    parseObjectMaybe(item.payload_json) ||
    parseObjectMaybe(item.payload) ||
    parseObjectMaybe(item.document) ||
    null;

  const domain = toLowerSafe(
    firstText(item.source, item.domain, item.assembly_domain, item.scope, item.origin, "user")
  ) || "user";
  const domainMeta = resolveDomainMeta(domain);

  const kindHint = firstText(item.assembly_type, item.assemblyType, item.kind, item.category);
  const subtypeHint = firstText(
    item.assembly_subtype,
    item.assemblySubtype,
    item.type,
    item.script_type,
    item.runtime_type,
    item.payload_type
  );
  const kind = normalizeKindFromHints(kindHint, subtypeHint, payloadFromItem);
  const assemblyType = normalizeSubtypeFromHints(subtypeHint, kind, payloadFromItem);

  const displayName =
    firstText(
      item.display_name,
      item.displayName,
      item.name,
      item.tab_name,
      item.tabName,
      item.title,
      item.summary
    ) ||
    sanitizeNameForPath(assemblyGuid);
  const summary = firstText(item.summary, item.description, item.desc);

  const rawPath = firstText(
    item.virtual_path,
    item.virtualPath,
    item.path,
    item.source_path,
    item.sourcePath,
    item.rel_path,
    item.relPath
  );
  const normalizedPath = normalizeAssemblyPath(kind, rawPath, displayName);
  const pathLower = normalizedPath.toLowerCase();
  let pathLowerNoPrefix = pathLower;
  TRIM_PREFIX_MATCHERS.forEach((matcher) => {
    if (pathLowerNoPrefix.match(matcher)) {
      pathLowerNoPrefix = pathLowerNoPrefix.replace(matcher, "");
    }
  });

  const queueDirtySince = queueEntry?.dirty_since || queueEntry?.dirtySince || item.dirty_since || item.dirtySince || null;
  const queueLastPersisted =
    queueEntry?.last_persisted || queueEntry?.lastPersisted || item.last_persisted || item.lastPersisted || null;
  const queueIsDirty =
    typeof queueEntry?.is_dirty === "string"
      ? queueEntry.is_dirty.toLowerCase() === "true"
      : queueEntry?.is_dirty ?? queueEntry?.isDirty ?? Boolean(item.is_dirty ?? item.isDirty);

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
    path: normalizedPath,
    pathLower,
    pathLowerNoPrefix,
    segments,
    isDirty: queueIsDirty,
    dirtySince: queueDirtySince,
    lastPersisted: queueLastPersisted,
    payloadGuid: firstText(item.payload_guid, item.payloadGuid),
    createdAt: firstText(item.created_at, item.createdAt),
    updatedAt: firstText(item.updated_at, item.updatedAt),
    queueEntry,
    raw: item
  };
}

export function buildAssemblyIndex(items = [], queue = []) {
  const queueMap = new Map();
  (Array.isArray(queue) ? queue : []).forEach((entry) => {
    if (!entry || typeof entry !== "object") return;
    const guid = queueGuidKey(entry);
    if (guid) {
      queueMap.set(guid, entry);
    }
  });

  const records = [];
  const byGuid = new Map();
  const byPath = new Map();

  (Array.isArray(items) ? items : []).forEach((item) => {
    const guidLower = firstText(
      item?.assembly_guid,
      item?.assemblyGuid,
      item?.assembly_id,
      item?.assemblyId,
      item?.guid,
      item?.id
    ).toLowerCase();
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

  const isWorkflow = (record) => record.kind === "workflow" || record.type === "workflow";
  const isAnsible = (record) => record.kind === "ansible" || record.type === "ansible";

  const grouped = {
    scripts: records.filter((r) => !isWorkflow(r) && !isAnsible(r)),
    ansible: records.filter((r) => isAnsible(r)),
    workflows: records.filter((r) => isWorkflow(r))
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

  const payloadDocCandidate =
    parseObjectMaybe(exportDoc.payload) ||
    parseObjectMaybe(exportDoc.payload_json) ||
    parseObjectMaybe(exportDoc.document) ||
    null;
  const looksLikeInlinePayload =
    Array.isArray(exportDoc?.nodes) ||
    Array.isArray(exportDoc?.edges) ||
    typeof exportDoc?.script === "string" ||
    typeof exportDoc?.content === "string" ||
    Array.isArray(exportDoc?.script_lines);
  const payload = payloadDocCandidate || (looksLikeInlinePayload ? exportDoc : {});

  const kindHint = firstText(exportDoc.assembly_type, exportDoc.kind, payload.assembly_type, payload.kind);
  const typeHint = firstText(
    exportDoc.assembly_subtype,
    exportDoc.type,
    payload.assembly_subtype,
    payload.type,
    payload.script_type
  );
  const kind = normalizeKindFromHints(kindHint, typeHint, payload);
  const type = normalizeSubtypeFromHints(typeHint, kind, payload);

  const scriptLines = Array.isArray(payload.script_lines) ? payload.script_lines : null;
  let scriptSource = "";
  if (typeof payload.script === "string") scriptSource = payload.script;
  else if (typeof payload.content === "string") scriptSource = payload.content;
  else if (scriptLines) scriptSource = scriptLines.map((line) => (line == null ? "" : String(line))).join("\n");

  const encodingHint = toLowerSafe(
    payload.script_encoding || payload.scriptEncoding
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

  const variablesRaw = Array.isArray(payload.variables) ? payload.variables : [];
  const filesRaw = Array.isArray(payload.files) ? payload.files : [];

  const variablesDetailed = normalizeVariablesFromServer(variablesRaw);
  const variables = normalizeAssemblyVariables(variablesRaw);
  const files = normalizeAssemblyFiles(filesRaw);

  const timeoutCandidate =
    payload.timeout_seconds ??
    payload.timeout ??
    null;
  const timeoutSeconds = Number.isFinite(Number(timeoutCandidate)) ? Number(timeoutCandidate) : 0;

  const sites =
    (payload.sites && typeof payload.sites === "object" ? payload.sites : null) ||
    { mode: "all", values: [] };

  const metadata = {
    assembly_guid: firstText(exportDoc.assembly_guid, exportDoc.assemblyGuid),
    display_name: firstText(exportDoc.display_name, exportDoc.displayName, exportDoc.name),
    summary: firstText(exportDoc.summary, exportDoc.description),
    domain: firstText(exportDoc.domain, exportDoc.source),
    assembly_type: kind,
    assembly_subtype: type
  };

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
    const componentType = toLowerSafe(
      component?.type ||
      component?.component_type ||
      component?.mode ||
      component?.assembly_type ||
      component?.assemblyType
    );
    const subtype = toLowerSafe(component?.assembly_subtype || component?.assemblySubtype || component?.script_type);
    if (componentType === "ansible" || componentType === "playbook") return "ansible";
    if (subtype === "ansible" || subtype === "playbook") return "ansible";
    if (componentType === "workflow") return "workflow";
    if (subtype === "workflow") return "workflow";
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
    let withoutPrefix = normalized.toLowerCase();
    TRIM_PREFIX_MATCHERS.forEach((matcher) => {
      if (withoutPrefix.match(matcher)) {
        withoutPrefix = withoutPrefix.replace(matcher, "");
      }
    });
    if (withoutPrefix && !candidatePaths.includes(withoutPrefix)) {
      candidatePaths.push(withoutPrefix);
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
