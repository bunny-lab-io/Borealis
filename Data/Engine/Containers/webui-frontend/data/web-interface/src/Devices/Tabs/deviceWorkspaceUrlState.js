const WORKSPACE_KEY_VALUES = Object.freeze(["inventory", "remote_ops", "protection", "history", "config"]);

export const WORKSPACE_KEYS = new Set(WORKSPACE_KEY_VALUES);

export const WORKSPACE_VIEW_DEFAULTS = Object.freeze({
  remote_ops: "shell",
  inventory: "summary",
  config: "metadata",
});

export const WORKSPACE_VIEW_OPTIONS = Object.freeze({
  remote_ops: ["shell", "files", "registry", "processes", "services", "agent_updates"],
  inventory: ["summary", "software", "patches"],
  config: ["metadata"],
});

export const LEGACY_TAB_TO_WORKSPACE = Object.freeze({
  command: { workspace: "inventory", view: "summary" },
  device_summary: { workspace: "inventory", view: "summary" },
  summary: { workspace: "inventory", view: "summary" },
  file_management: { workspace: "remote_ops", view: "files" },
  registry: { workspace: "remote_ops", view: "registry" },
  registry_editor: { workspace: "remote_ops", view: "registry" },
  installed_software: { workspace: "inventory", view: "software" },
  software: { workspace: "inventory", view: "software" },
  patch_management: { workspace: "inventory", view: "patches" },
  patches: { workspace: "inventory", view: "patches" },
  metadata_fields: { workspace: "config", view: "metadata" },
  metadata: { workspace: "config", view: "metadata" },
  services: { workspace: "remote_ops", view: "services" },
  process_management: { workspace: "remote_ops", view: "processes" },
  processes: { workspace: "remote_ops", view: "processes" },
  watchdogs: { workspace: "protection" },
  activity_history: { workspace: "history" },
  activity: { workspace: "history" },
  remote_shell: { workspace: "remote_ops", view: "shell" },
  shell: { workspace: "remote_ops", view: "shell" },
  agent_update: { workspace: "remote_ops", view: "agent_updates" },
  agent_updates: { workspace: "remote_ops", view: "agent_updates" },
  agent_health: { workspace: "inventory", view: "summary" },
  health: { workspace: "inventory", view: "summary" },
});

const REMOTE_OPS_CONTEXT_PARAMS = Object.freeze(["working_directory", "registry_path", "operation_id"]);
const EMPTY_CONTEXT_PARAMS = new Set();
const REMOTE_OPS_CONTEXT_PARAMS_BY_VIEW = Object.freeze({
  files: new Set(["working_directory"]),
  registry: new Set(["registry_path"]),
  agent_updates: new Set(["operation_id"]),
});

export const normalizeWorkspaceKey = (value, fallback = "inventory") => {
  const normalized = String(value || "").trim().toLowerCase();
  if (WORKSPACE_KEYS.has(normalized)) return normalized;
  const legacyTarget = LEGACY_TAB_TO_WORKSPACE[normalized];
  if (legacyTarget?.workspace && WORKSPACE_KEYS.has(legacyTarget.workspace)) {
    return legacyTarget.workspace;
  }
  return fallback;
};

export const normalizeWorkspaceView = (workspaceKey, value) => {
  const allowedViews = WORKSPACE_VIEW_OPTIONS[workspaceKey] || [];
  const fallback = WORKSPACE_VIEW_DEFAULTS[workspaceKey] || "";
  const normalized = String(value || "").trim().toLowerCase();
  if (allowedViews.includes(normalized)) return normalized;
  return fallback;
};

export const pruneDeviceWorkspaceContextParams = (params, workspaceKey, viewKey = "") => {
  const normalizedWorkspace = normalizeWorkspaceKey(workspaceKey);
  const normalizedView = normalizeWorkspaceView(normalizedWorkspace, viewKey);
  const retainedParams =
    normalizedWorkspace === "remote_ops"
      ? REMOTE_OPS_CONTEXT_PARAMS_BY_VIEW[normalizedView] || EMPTY_CONTEXT_PARAMS
      : EMPTY_CONTEXT_PARAMS;
  REMOTE_OPS_CONTEXT_PARAMS.forEach((paramKey) => {
    if (!retainedParams.has(paramKey)) {
      params.delete(paramKey);
    }
  });
  return params;
};

export const createDeviceWorkspaceSearch = (currentSearch, workspaceKey, viewKey = "") => {
  const params = new URLSearchParams(currentSearch || "");
  const normalizedWorkspace = normalizeWorkspaceKey(workspaceKey);
  const normalizedView = normalizeWorkspaceView(normalizedWorkspace, viewKey);
  params.set("tab", normalizedWorkspace);
  if (normalizedView) {
    params.set("view", normalizedView);
  } else {
    params.delete("view");
  }
  pruneDeviceWorkspaceContextParams(params, normalizedWorkspace, normalizedView);
  return params.toString() ? `?${params.toString()}` : "";
};
