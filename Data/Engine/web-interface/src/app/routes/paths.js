function appendQuery(basePath, params) {
  const query = params.toString();
  return query ? `${basePath}?${query}` : basePath;
}

function normalizedString(value) {
  return String(value || "").trim();
}

export const APP_PATHS = {
  login: "/login",
  sites: "/sites",
  devices: "/devices",
  deviceApprovals: "/devices/approvals",
  agentDevices: "/devices/agents",
  sshDevices: "/devices/ssh",
  winrmDevices: "/devices/winrm",
  device: (deviceId) => `/devices/${encodeURIComponent(normalizedString(deviceId))}`,
  filters: "/filters",
  filterNew: "/filters/new",
  filter: (filterId) => `/filters/${encodeURIComponent(normalizedString(filterId))}`,
  jobs: "/jobs",
  jobNew: "/jobs/new",
  job: (jobId) => `/jobs/${encodeURIComponent(normalizedString(jobId))}`,
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
  users: "/users",
  siteAssignment: "/users/site-assignment",
  server: "/server",
  logs: "/logs",
  pageTemplate: "/page-template",
};

export function buildSiteAssignmentPath(usernames = []) {
  const params = new URLSearchParams();
  usernames
    .map((value) => normalizedString(value))
    .filter(Boolean)
    .forEach((username) => params.append("user", username));
  return appendQuery(APP_PATHS.siteAssignment, params);
}
