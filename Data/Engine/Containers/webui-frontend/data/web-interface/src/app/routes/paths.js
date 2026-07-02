function appendQuery(basePath, params) {
  const query = params.toString();
  return query ? `${basePath}?${query}` : basePath;
}

function normalizedString(value) {
  return String(value || "").trim();
}

export const APP_PATHS = {
  login: "/login",
  bootstrapBackupRestore: "/restore-engine-config-backup",
  sites: "/sites",
  devices: "/devices",
  software: "/software",
  deviceApprovals: "/devices/approvals",
  agentDevices: "/devices/agents",
  sshDevices: "/devices/ssh",
  winrmDevices: "/devices/winrm",
  device: (deviceId) => `/devices/${encodeURIComponent(normalizedString(deviceId))}`,
  deviceRemoteDesktop: (deviceId) =>
    `/devices/${encodeURIComponent(normalizedString(deviceId))}/remote-desktop`,
  filters: "/filters",
  filterNew: "/filters/new",
  filter: (filterId) => `/filters/${encodeURIComponent(normalizedString(filterId))}`,
  jobs: "/jobs",
  jobNew: "/jobs/new",
  jobOnboardingNew: "/jobs/onboarding/new",
  jobOnboarding: (jobId) => `/jobs/onboarding/${encodeURIComponent(normalizedString(jobId))}`,
  job: (jobId) => `/jobs/${encodeURIComponent(normalizedString(jobId))}`,
  watchdogs: "/automation/watchdogs",
  watchdogNew: "/automation/watchdogs/new",
  watchdog: (watchdogId) => `/automation/watchdogs/${encodeURIComponent(normalizedString(watchdogId))}`,
  alerts: "/alerts",
  assemblies: "/assemblies",
  assemblyNewScript: "/assemblies/new/script",
  assemblyNewAnsible: "/assemblies/new/ansible_playbook",
  assemblyNewWorkflow: "/assemblies/new/workflow",
  assemblyScript: (assemblyGuid) =>
    `/assemblies/scripts/${encodeURIComponent(normalizedString(assemblyGuid))}`,
  assemblyAnsible: (assemblyGuid) =>
    `/assemblies/ansible_playbooks/${encodeURIComponent(normalizedString(assemblyGuid))}`,
  assemblyWorkflow: (workflowGuid) =>
    `/assemblies/workflows/${encodeURIComponent(normalizedString(workflowGuid))}`,
  assemblyWorkflowRun: (runId) =>
    `/assemblies/workflows/runs/${encodeURIComponent(normalizedString(runId))}`,
  credentials: "/credentials",
  directoryServices: "/directory-services",
  users: "/users",
  siteAssignment: "/users/site-assignment",
  backupRestore: "/backup-restore",
  server: "/server",
  logs: "/logs",
  metadataFields: "/metadata-fields",
  devTools: "/dev-tools",
  pageStyleTemplate: "/dev-tools?tab=page_style_template",
  pageTemplate: "/dev-tools?tab=page_style_template",
};

export function normalizeAppRedirectTarget(value) {
  const text = String(value || "").trim();
  if (!text || !text.startsWith("/") || text.startsWith("//")) {
    return "";
  }
  return text === APP_PATHS.login ? "" : text;
}

export function buildLoginPath(nextPath = "") {
  const params = new URLSearchParams();
  const normalizedNextPath = normalizeAppRedirectTarget(nextPath);
  if (normalizedNextPath) {
    params.set("next", normalizedNextPath);
  }
  return appendQuery(APP_PATHS.login, params);
}

export function buildSitesPath({ notAuthorized = false } = {}) {
  const params = new URLSearchParams();
  if (notAuthorized) {
    params.set("not_authorized", "1");
  }
  return appendQuery(APP_PATHS.sites, params);
}

export function buildSiteAssignmentPath(usernames = []) {
  const params = new URLSearchParams();
  usernames
    .map((value) => normalizedString(value))
    .filter(Boolean)
    .forEach((username) => params.append("user", username));
  return appendQuery(APP_PATHS.siteAssignment, params);
}
