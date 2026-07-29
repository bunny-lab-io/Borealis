const OPERATOR_PRESENCE_PAGE_LABELS = Object.freeze({
  login: "Login",
  sites: "Sites",
  devices: "Devices",
  "agent-devices": "Agent Devices",
  "ssh-devices": "SSH Devices",
  "winrm-devices": "WinRM Devices",
  device: "Device Details",
  filters: "Filters",
  filter: "Filter Editor",
  jobs: "Scheduled Jobs",
  job: "Scheduled Job",
  assemblies: "Assemblies",
  "script-assembly": "Script Assembly",
  "ansible-playbook": "Ansible Playbook",
  workflow: "Workflow Editor",
  credentials: "Credentials",
  users: "Users",
  "site-assignment": "Site Assignment",
  server: "Server Info",
  "backup-restore": "Backup/Restore",
  "device-approvals": "Device Approvals",
  "dev-tools": "Dev Tools",
  "page-template": "Page Style Template",
});

export function formatOperatorPresencePage(pageKey, pageTitle) {
  const title = String(pageTitle || "").trim();
  if (title) return title;

  const normalizedKey = String(pageKey || "").trim();
  if (OPERATOR_PRESENCE_PAGE_LABELS[normalizedKey]) {
    return OPERATOR_PRESENCE_PAGE_LABELS[normalizedKey];
  }
  if (!normalizedKey) return "";
  return normalizedKey
    .replace(/[_-]+/g, " ")
    .replace(/\b\w/g, (character) => character.toUpperCase());
}
