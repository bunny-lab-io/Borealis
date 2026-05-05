import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogContent,
  DialogTitle,
  FormControl,
  IconButton,
  InputLabel,
  MenuItem,
  Paper,
  Select,
  Stack,
  Tab,
  Tabs,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import {
  ArrowBack as ArrowBackIcon,
  Check as CheckIcon,
  ContentCopy as ContentCopyIcon,
  Devices as DevicesIcon,
  PlayArrow as PlayArrowIcon,
  Refresh as RefreshIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import {
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";
import { CountSliderGroup } from "../Automation/Watchdogs/shared.jsx";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { useUrlTabState } from "../app/hooks/useUrlTabState.js";
import { APP_PATHS } from "../app/routes/paths.js";

ModuleRegistry.registerModules([AllCommunityModule]);

const PAGE_TITLE = "Automatic Device Onboarding";
const PAGE_SUBTITLE = "Enroll Linux devices over local-network SSH with stored machine credentials.";
const DEFAULT_BRANCH = "main";
const DEFAULT_SSH_PORT = 22;
const BOREALIS_GITHUB_REPO = "bunny-lab-io/Borealis";
const GITHUB_BRANCHES_API_URL = `https://api.github.com/repos/${BOREALIS_GITHUB_REPO}/branches`;
const ONBOARDING_TAB_URL_BY_KEY = Object.freeze({
  name: "job_name",
  scope: "scope",
  context: "ssh_context",
  schedule: "schedule",
  targets: "target_status",
});
const ONBOARDING_TAB_KEY_BY_URL = Object.freeze({
  job_name: "name",
  name: "name",
  scope: "scope",
  ssh_context: "context",
  context: "context",
  schedule: "schedule",
  target_status: "targets",
  targets: "targets",
});

const MAGIC_UI = {
  panelBg: "linear-gradient(145deg, rgba(7,10,24,0.96), rgba(6,10,28,0.92) 45%, rgba(14,8,30,0.95))",
  panelBorder: "rgba(148, 163, 184, 0.32)",
  textMuted: "#94a3b8",
  textBright: "#e2e8f0",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
  accentC: "#34d399",
  danger: "#fb7185",
};

const gridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});
const gridThemeClass = gridTheme.themeName || "ag-theme-quartz";
const gridFontFamily = '"IBM Plex Sans","Helvetica Neue",Arial,sans-serif';
const iconFontFamily = '"Quartz Regular"';

const PAGE_SX = {
  m: 0,
  p: { xs: 2, md: 3 },
  flexGrow: 1,
  minWidth: 0,
  minHeight: 0,
  display: "flex",
  flexDirection: "column",
  gap: 3,
  borderRadius: 0,
  background: "transparent",
  border: "none",
  boxShadow: "none",
  color: MAGIC_UI.textBright,
};

const GRID_PANEL_SX = {
  width: "100%",
  height: "100%",
  fontFamily: gridFontFamily,
  "--ag-icon-font-family": iconFontFamily,
  "--ag-cell-horizontal-padding": "18px",
  "--ag-background-color": "#070b1a",
  "--ag-foreground-color": "#f4f7ff",
  "--ag-header-background-color": "#0f172a",
  "--ag-header-foreground-color": "#cfe0ff",
  "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
  "--ag-row-hover-color": "rgba(73,156,196,0.2)",
  "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
  "--ag-border-color": "rgba(125,183,255,0.18)",
  "--ag-row-border-color": "rgba(125,183,255,0.14)",
  "--ag-border-radius": "0px",
  borderRadius: 0,
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  background: "transparent",
  boxShadow: "none",
  overflow: "hidden",
  "& .ag-root-wrapper": {
    borderRadius: 0,
  },
  "& .ag-root, & .ag-header, & .ag-center-cols-container": {
    fontFamily: gridFontFamily,
    background: "transparent",
  },
  "& .ag-header": {
    backgroundColor: "rgba(3,7,18,0.9)",
    borderBottom: "1px solid rgba(148,163,184,0.25)",
  },
  "& .ag-header-cell-label": {
    color: MAGIC_UI.textBright,
    fontWeight: 600,
    letterSpacing: 0.3,
  },
  "& .ag-row:nth-of-type(even)": {
    backgroundColor: "rgba(15,23,42,0.32)",
  },
  "& .ag-row-hover": {
    backgroundColor: "rgba(125,183,255,0.08) !important",
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
    paddingTop: "8px",
    paddingBottom: "8px",
    paddingLeft: "18px",
    paddingRight: "12px",
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
  "& .ag-center-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper": {
    width: "100%",
    padding: 0,
  },
  "& .ag-center-cols-container .ag-cell.auto-col-tight .ag-cell-value, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight .ag-cell-value, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight .ag-cell-value": {
    width: "100%",
  },
};

const TAB_SECTION_SX = {
  width: "100%",
  display: "flex",
  flexDirection: "column",
  gap: 2,
  px: { xs: 1.5, md: 2 },
  py: { xs: 1.25, md: 1.75 },
};

const NAV_TAB_COLORS = {
  text: "#cbd5e1",
  textActive: "#e6f2ff",
  icon: "#8fbfff",
  iconActive: "#7db7ff",
  hover: "rgba(255,255,255,0.05)",
  activeBg: "linear-gradient(to top, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)",
};

const TABS_SX = {
  borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
  minHeight: 32,
  "& .MuiTabs-flexContainer": {
    minHeight: 32,
    alignItems: "stretch",
  },
  "& .MuiTab-root": {
    color: NAV_TAB_COLORS.text,
    textTransform: "none",
    fontWeight: 400,
    fontSize: "0.8rem",
    minHeight: 32,
    height: 32,
    opacity: 1,
    borderRadius: 1,
    py: 0.35,
    "& .MuiTab-iconWrapper": {
      color: NAV_TAB_COLORS.icon,
    },
    "&:hover": {
      background: NAV_TAB_COLORS.hover,
    },
  },
  "& .MuiTab-root.Mui-selected": {
    color: NAV_TAB_COLORS.textActive,
    fontWeight: 600,
    background: NAV_TAB_COLORS.activeBg,
    "& .MuiTab-iconWrapper": {
      color: NAV_TAB_COLORS.iconActive,
    },
  },
};

const FIELD_SX = {
  "& .MuiOutlinedInput-root": {
    borderRadius: 2,
    bgcolor: "rgba(5,9,18,0.85)",
    color: MAGIC_UI.textBright,
    "& fieldset": {
      borderColor: "rgba(148,163,184,0.35)",
    },
    "&:hover fieldset": {
      borderColor: MAGIC_UI.accentA,
    },
    "&.Mui-focused fieldset": {
      borderColor: MAGIC_UI.accentB,
      boxShadow: "0 0 0 1px rgba(192,132,252,0.3)",
    },
  },
  "& .MuiInputLabel-root": {
    color: MAGIC_UI.textMuted,
  },
};

const PRIMARY_BUTTON_SX = {
  borderRadius: 999,
  color: "#06101d",
  borderColor: "transparent",
  background: "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)",
  textTransform: "none",
  fontWeight: 700,
  "&:hover": {
    background: "linear-gradient(135deg, #91dcff 0%, #cfa0ff 100%)",
  },
};

const STATUS_THEME = {
  pending: { label: "Pending", text: "#fbbf24", background: "rgba(251,191,36,0.18)", border: "1px solid rgba(251,191,36,0.35)", dot: "#f59e0b" },
  running: { label: "Running", text: "#7dd3fc", background: "rgba(125,211,252,0.18)", border: "1px solid rgba(125,211,252,0.4)", dot: "#38bdf8" },
  waiting_approval: { label: "Waiting Approval", text: "#d8b4fe", background: "rgba(192,132,252,0.18)", border: "1px solid rgba(192,132,252,0.4)", dot: "#c084fc" },
  approved: { label: "Approved", text: "#60a5fa", background: "rgba(96,165,250,0.16)", border: "1px solid rgba(96,165,250,0.4)", dot: "#60a5fa" },
  completed: { label: "Completed", text: "#34d399", background: "rgba(52,211,153,0.18)", border: "1px solid rgba(52,211,153,0.42)", dot: "#34d399" },
  failed: { label: "Failed", text: "#fb7185", background: "rgba(251,113,133,0.18)", border: "1px solid rgba(251,113,133,0.45)", dot: "#fb7185" },
  ssh_unreachable: { label: "Unreachable", text: "#fb7185", background: "rgba(251,113,133,0.18)", border: "1px solid rgba(251,113,133,0.45)", dot: "#fb7185" },
  skipped: { label: "Skipped", text: "#fbbf24", background: "rgba(251,191,36,0.14)", border: "1px solid rgba(251,191,36,0.32)", dot: "#f59e0b" },
  denied: { label: "Denied", text: "#9aa0a6", background: "rgba(148,163,184,0.12)", border: "1px solid rgba(148,163,184,0.25)", dot: "#94a3b8" },
  expired: { label: "Expired", text: "#9aa0a6", background: "rgba(148,163,184,0.12)", border: "1px solid rgba(148,163,184,0.25)", dot: "#94a3b8" },
};

const SELECT_MENU_PROPS = {
  PaperProps: {
    sx: {
      bgcolor: "rgba(8,12,24,0.96)",
      color: MAGIC_UI.textBright,
      border: `1px solid ${MAGIC_UI.panelBorder}`,
    },
  },
};

const TARGET_AUTO_SIZE_COLUMNS = ["targetLabel", "statusLabel", "detail", "output"];
const TARGET_STATUS_FILTER_OPTIONS = [
  { key: "pending_approval", label: "Pending Approval" },
  { key: "skipped", label: "Skipped" },
  { key: "failed", label: "Failed" },
  { key: "completed", label: "Completed" },
  { key: "unreachable", label: "Unreachable" },
];

const FILTER_LABEL_SX = {
  color: "#58a6ff",
  fontSize: 11,
  fontWeight: 600,
  lineHeight: 1.1,
  pl: 1,
};

function toScopeText(entries) {
  if (Array.isArray(entries)) {
    return entries.map((entry) => String(entry || "").trim()).filter(Boolean).join("\n");
  }
  return String(entries || "");
}

function splitScopeEntries(value) {
  return String(value || "")
    .split(/[\n\r,;]+/)
    .map((entry) => entry.trim())
    .filter(Boolean);
}

function formatStatusLabel(status) {
  const key = String(status || "").trim().toLowerCase();
  return STATUS_THEME[key]?.label || key.replace(/_/g, " ").replace(/\b\w/g, (char) => char.toUpperCase()) || "Pending";
}

function targetStatusBucket(status) {
  const key = String(status || "pending").trim().toLowerCase();
  if (key === "ssh_unreachable") return "unreachable";
  if (["completed", "approved", "success", "installed"].includes(key)) return "completed";
  if (["skipped", "denied", "expired", "already_enrolled", "already_pending", "unsupported_os"].includes(key)) return "skipped";
  if (["failed", "failure", "error"].includes(key)) return "failed";
  return "pending_approval";
}

function targetVisibleForStatusFilter(row, activeFilter) {
  const bucket = targetStatusBucket(row?.status);
  if (activeFilter) {
    return bucket === activeFilter;
  }
  return bucket !== "unreachable";
}

function datetimeLocalValue(epochSeconds) {
  if (!epochSeconds) return "";
  const date = new Date(Number(epochSeconds) * 1000);
  if (Number.isNaN(date.getTime())) return "";
  const offsetMs = date.getTimezoneOffset() * 60000;
  return new Date(date.getTime() - offsetMs).toISOString().slice(0, 16);
}

function isoFromDatetimeLocal(value) {
  if (!value) return null;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  return date.toISOString();
}

function targetOutputContent(target, mode) {
  return String(mode === "stderr" ? target?.stderr_snippet || "" : target?.stdout_snippet || "").trim();
}

function normalizeBranchName(value) {
  return String(value || DEFAULT_BRANCH).trim() || DEFAULT_BRANCH;
}

function SectionHeader({ title, detail, action }) {
  return (
    <Box sx={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: 2, flexWrap: "wrap" }}>
      <Box sx={{ minWidth: 0 }}>
        <Typography variant="h6" sx={{ color: MAGIC_UI.textBright, fontWeight: 700, fontSize: "1rem" }}>
          {title}
        </Typography>
        {detail ? (
          <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted, mt: 0.4 }}>
            {detail}
          </Typography>
        ) : null}
      </Box>
      {action || null}
    </Box>
  );
}

function StatusPill({ status }) {
  const key = String(status || "pending").trim().toLowerCase() || "pending";
  const theme = STATUS_THEME[key] || STATUS_THEME.pending;
  const label = STATUS_THEME[key]?.label || formatStatusLabel(key);
  return (
    <Box
      component="span"
      sx={{
        display: "inline-flex",
        alignItems: "center",
        gap: 0.6,
        px: 1.2,
        py: 0.25,
        borderRadius: 999,
        background: theme.background,
        border: theme.border,
        color: theme.text,
        fontWeight: 700,
        fontSize: 12,
        textTransform: "uppercase",
        lineHeight: 1,
      }}
    >
      <Box
        component="span"
        sx={{
          width: 8,
          height: 8,
          borderRadius: "50%",
          backgroundColor: theme.dot,
          boxShadow: "0 0 0 2px rgba(8,12,24,0.65)",
        }}
      />
      {label}
    </Box>
  );
}

export default function CreateOnboardingJob() {
  const navigate = useNavigate();
  const params = useParams();
  const jobId = params?.jobId ? Number(params.jobId) : null;
  const editing = Number.isInteger(jobId) && jobId > 0;
  const [sites, setSites] = useState([]);
  const [credentials, setCredentials] = useState([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [targetRows, setTargetRows] = useState([]);
  const [branchRows, setBranchRows] = useState([]);
  const [branchesLoading, setBranchesLoading] = useState(false);
  const [branchLoadError, setBranchLoadError] = useState("");
  const [actioningApprovalId, setActioningApprovalId] = useState("");
  const [outputOpen, setOutputOpen] = useState(false);
  const [outputTitle, setOutputTitle] = useState("");
  const [outputSections, setOutputSections] = useState([]);
  const [copiedOutputKey, setCopiedOutputKey] = useState("");
  const [nameTouched, setNameTouched] = useState(false);
  const [targetStatusFilter, setTargetStatusFilter] = useState("");
  const targetGridApiRef = useRef(null);
  const [form, setForm] = useState({
    name: "",
    siteId: "",
    scope: "",
    exclusionScope: "",
    credentialId: "",
    branch: DEFAULT_BRANCH,
    sshPort: DEFAULT_SSH_PORT,
    scheduleType: "immediately",
    start: "",
    enabled: true,
  });

  const selectedSite = useMemo(
    () => sites.find((site) => String(site.id) === String(form.siteId)) || null,
    [form.siteId, sites]
  );

  const sshCredentials = useMemo(
    () => credentials.filter((credential) => String(credential.connection_type || "").toLowerCase() === "ssh"),
    [credentials]
  );

  const branchOptions = useMemo(() => {
    const currentBranch = normalizeBranchName(form.branch);
    const rows = Array.isArray(branchRows) ? branchRows : [];
    if (rows.some((branch) => branch.name === currentBranch)) {
      return rows;
    }
    return [
      {
        name: currentBranch,
        sha: "",
        protected: false,
        default: currentBranch === DEFAULT_BRANCH,
      },
      ...rows,
    ];
  }, [branchRows, form.branch]);

  const tabDefs = useMemo(() => {
    const tabs = [
      { key: "name", label: "Job Name" },
      { key: "scope", label: "Scope" },
      { key: "context", label: "SSH Context" },
      { key: "schedule", label: "Schedule" },
    ];
    if (editing) {
      tabs.push({ key: "targets", label: "Target Status" });
    }
    return tabs;
  }, [editing]);

  const { activeKey: activeTabUrlKey, setActiveKey: setActiveTabUrlKey } = useUrlTabState({
    param: "tab",
    defaultKey: tabDefs[0]?.key || "name",
    allowedKeys: tabDefs.map((tabDef) => tabDef.key),
    keyByUrl: ONBOARDING_TAB_KEY_BY_URL,
    urlByKey: ONBOARDING_TAB_URL_BY_KEY,
  });

  const activeTabKey = useMemo(() => {
    const fallbackKey = tabDefs[0]?.key || "name";
    return tabDefs.some((tabDef) => tabDef.key === activeTabUrlKey) ? activeTabUrlKey : fallbackKey;
  }, [activeTabUrlKey, tabDefs]);

  const activeTabIndex = useMemo(() => {
    const index = tabDefs.findIndex((tabDef) => tabDef.key === activeTabKey);
    return index >= 0 ? index : 0;
  }, [activeTabKey, tabDefs]);

  const setField = useCallback((key, value) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  }, []);

  const fetchInstallBranches = useCallback(async () => {
    setBranchesLoading(true);
    setBranchLoadError("");
    try {
      const tokenRes = await fetch("/api/github/token", {
        credentials: "include",
        cache: "no-store",
      });
      const tokenData = await tokenRes.json().catch(() => ({}));
      if (!tokenRes.ok) {
        const message = tokenData?.message || tokenData?.error || `GitHub token lookup failed (HTTP ${tokenRes.status}).`;
        throw new Error(message);
      }
      const githubToken = String(tokenData?.token || "").trim();
      if (!githubToken) {
        throw new Error(tokenData?.message || "GitHub API token is unavailable.");
      }

      const nextRows = [];
      for (let page = 1; page <= 10; page += 1) {
        const branchRes = await fetch(`${GITHUB_BRANCHES_API_URL}?per_page=100&page=${page}`, {
          cache: "no-store",
          headers: {
            Accept: "application/vnd.github+json",
            Authorization: `Bearer ${githubToken}`,
          },
        });
        if (!branchRes.ok) {
          const body = await branchRes.text().catch(() => "");
          throw new Error(`GitHub branch lookup failed (HTTP ${branchRes.status})${body ? `: ${body.slice(0, 180)}` : ""}`);
        }
        const pageRows = await branchRes.json().catch(() => []);
        if (!Array.isArray(pageRows)) {
          throw new Error("GitHub branch lookup returned an unexpected payload.");
        }
        pageRows.forEach((branch) => {
          const name = String(branch?.name || "").trim();
          if (!name) return;
          nextRows.push({
            name,
            sha: String(branch?.commit?.sha || "").trim(),
            protected: Boolean(branch?.protected),
            default: name === DEFAULT_BRANCH,
          });
        });
        if (pageRows.length < 100) {
          break;
        }
      }
      nextRows.sort((a, b) => {
        if (a.name === DEFAULT_BRANCH) return -1;
        if (b.name === DEFAULT_BRANCH) return 1;
        return a.name.localeCompare(b.name);
      });
      setBranchRows(nextRows);
      if (!nextRows.length) {
        setBranchLoadError("No GitHub branches returned.");
      }
    } catch (err) {
      setBranchRows([]);
      setBranchLoadError(err instanceof Error ? err.message : "GitHub branch lookup failed.");
    } finally {
      setBranchesLoading(false);
    }
  }, []);

  const loadData = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [sitesResp, credentialsResp, jobResp] = await Promise.all([
        fetch("/api/sites", { credentials: "include" }),
        fetch("/api/credentials", { credentials: "include" }),
        editing ? fetch(`/api/scheduled_jobs/${jobId}`, { credentials: "include" }) : Promise.resolve(null),
      ]);
      const sitesData = await sitesResp.json().catch(() => ({}));
      if (!sitesResp.ok) throw new Error(sitesData?.error || `Unable to load sites (${sitesResp.status})`);
      const credentialsData = await credentialsResp.json().catch(() => ({}));
      if (!credentialsResp.ok) throw new Error(credentialsData?.message || credentialsData?.error || `Unable to load credentials (${credentialsResp.status})`);
      setSites(Array.isArray(sitesData?.sites) ? sitesData.sites : []);
      setCredentials(Array.isArray(credentialsData?.credentials) ? credentialsData.credentials : []);

      if (editing && jobResp) {
        const jobData = await jobResp.json().catch(() => ({}));
        if (!jobResp.ok) throw new Error(jobData?.error || `Unable to load onboarding job (${jobResp.status})`);
        const job = jobData?.job || {};
        const firstTarget = Array.isArray(job.targets) ? job.targets.find((target) => target?.kind === "onboarding_scope") : null;
        const firstComponent = Array.isArray(job.components) ? job.components[0] || {} : {};
        setForm({
          name: job.name || "",
          siteId: firstTarget?.site_id ? String(firstTarget.site_id) : "",
          scope: toScopeText(firstTarget?.entries || []),
          exclusionScope: toScopeText(firstTarget?.exclusions || firstTarget?.exclude_entries || []),
          credentialId: job.credential_id ? String(job.credential_id) : "",
          branch: firstComponent.install_branch || firstComponent.repo_branch || firstComponent.branch || DEFAULT_BRANCH,
          sshPort: Number(firstComponent.ssh_port || firstComponent.port || DEFAULT_SSH_PORT),
          scheduleType: job.schedule_type || "immediately",
          start: datetimeLocalValue(job.start_ts),
          enabled: Boolean(job.enabled),
        });
        setNameTouched(Boolean(job.name));
      }
    } catch (err) {
      setError(err?.message || "Unable to load onboarding form.");
    } finally {
      setLoading(false);
    }
  }, [editing, jobId]);

  useEffect(() => {
    loadData();
  }, [loadData]);

  useEffect(() => {
    fetchInstallBranches();
  }, [fetchInstallBranches]);

  const loadTargets = useCallback(async () => {
    if (!editing || !jobId) return;
    try {
      const resp = await fetch(`/api/onboarding/jobs/${jobId}/targets`, { credentials: "include" });
      const data = await resp.json().catch(() => ({}));
      if (resp.ok) {
        setTargetRows(Array.isArray(data?.targets) ? data.targets : []);
      }
    } catch {
      /* status refresh is best effort */
    }
  }, [editing, jobId]);

  useEffect(() => {
    if (!editing) return undefined;
    loadTargets();
    const timer = setInterval(loadTargets, 5000);
    return () => clearInterval(timer);
  }, [editing, loadTargets]);

  const copyTextToClipboard = useCallback(async (value, promptTitle = "Copy text") => {
    const normalizedValue = String(value ?? "");
    if (!normalizedValue) return false;
    try {
      if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(normalizedValue);
        return true;
      }
      throw new Error("clipboard_unavailable");
    } catch {
      if (typeof window !== "undefined" && typeof window.prompt === "function") {
        window.prompt(promptTitle, normalizedValue);
      }
      return false;
    }
  }, []);

  const targetGridRows = useMemo(
    () => targetRows.map((target) => {
      const targetLabel = `${target.target_hostname || target.target_address || target.target_input || "Target"}${target.ssh_port ? `:${target.ssh_port}` : ""}`;
      const status = String(target.status || "pending").trim().toLowerCase() || "pending";
      const approvalStatus = String(target.approval_status || "").trim().toLowerCase();
      return {
        id: target.id || `${targetLabel}-${target.run_id || ""}`,
        targetLabel,
        status,
        statusBucket: targetStatusBucket(status),
        statusLabel: formatStatusLabel(status),
        detail: target.detail || "",
        approvalReference: target.approval_reference || "",
        approvalId: target.approval_id || "",
        approvalStatus,
        hasStdOut: Boolean(String(target.stdout_snippet || "").trim()),
        hasStdErr: Boolean(String(target.stderr_snippet || "").trim()),
        raw: target,
      };
    }),
    [targetRows]
  );

  const targetStatusCounts = useMemo(
    () => targetGridRows.reduce((acc, row) => {
      const bucket = row.statusBucket || targetStatusBucket(row.status);
      acc[bucket] = (acc[bucket] || 0) + 1;
      return acc;
    }, {}),
    [targetGridRows]
  );

  const visibleTargetGridRows = useMemo(
    () => targetGridRows.filter((row) => targetVisibleForStatusFilter(row, targetStatusFilter)),
    [targetGridRows, targetStatusFilter]
  );

  const handleCopyOutputSection = useCallback(async (section) => {
    const content = String(section?.content || "");
    if (!content) {
      setError("No output available to copy.");
      return;
    }
    const copied = await copyTextToClipboard(content, `Copy ${section?.title || "Output"}`);
    if (copied) {
      setCopiedOutputKey(String(section?.key || ""));
      setNotice("Output copied.");
      setTimeout(() => setCopiedOutputKey(""), 1400);
    } else {
      setNotice("Manual copy prompt opened.");
    }
  }, [copyTextToClipboard]);

  const handleViewTargetOutput = useCallback((target, mode = "stdout") => {
    const label = mode === "stderr" ? "StdErr" : "StdOut";
    const targetLabel = `${target?.target_hostname || target?.target_address || target?.target_input || "Target"}${target?.ssh_port ? `:${target.ssh_port}` : ""}`;
    const content = targetOutputContent(target, mode);
    setOutputTitle(`${label} - ${targetLabel}`);
    setOutputSections([
      {
        key: `${target?.id || targetLabel}-${mode}`,
        title: label,
        path: targetLabel,
        content,
      },
    ]);
    setCopiedOutputKey("");
    setOutputOpen(true);
  }, []);

  const handleCopyTargetOutput = useCallback(async (target, mode = "stdout") => {
    const label = mode === "stderr" ? "StdErr" : "StdOut";
    const content = targetOutputContent(target, mode);
    if (!content) {
      setError(`No ${label} available for this target.`);
      return;
    }
    const copied = await copyTextToClipboard(content, `Copy ${label}`);
    setNotice(copied ? `${label} copied.` : "Manual copy prompt opened.");
  }, [copyTextToClipboard]);

  const handleApproveTarget = useCallback(async (target) => {
    const approvalId = String(target?.approval_id || "").trim();
    if (!approvalId) return;
    setActioningApprovalId(approvalId);
    setError("");
    setNotice("");
    try {
      const resp = await fetch(`/api/admin/device-approvals/${encodeURIComponent(approvalId)}/approve`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({}),
      });
      const body = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        const conflictMessage = body?.error === "conflict_resolution_required"
          ? "Approval needs hostname conflict resolution from Device Approvals."
          : body?.error || `Approval failed (${resp.status})`;
        throw new Error(conflictMessage);
      }
      setNotice("Enrollment approved.");
      await loadTargets();
    } catch (err) {
      setError(err?.message || "Unable to approve enrollment.");
    } finally {
      setActioningApprovalId("");
    }
  }, [loadTargets]);

  const targetGridColumnDefs = useMemo(
    () => [
      { field: "targetLabel", headerName: "Target", minWidth: 220, filter: "agTextColumnFilter", cellClass: "auto-col-tight" },
      {
        field: "statusLabel",
        headerName: "Status",
        minWidth: 180,
        filter: false,
        cellClass: "auto-col-tight",
        cellRenderer: (params) => <StatusPill status={params.data?.status} />,
      },
      { field: "detail", headerName: "Detail", minWidth: 360, filter: "agTextColumnFilter", cellClass: "auto-col-tight" },
      {
        field: "output",
        headerName: "StdOut / StdErr",
        minWidth: 220,
        cellClass: "auto-col-tight",
        cellRenderer: (params) => {
          const row = params.data;
          const target = row?.raw;
          if (!row) return null;
          return (
            <Box sx={{ display: "flex", alignItems: "center", gap: 0.75, flexWrap: "wrap" }}>
              {row.hasStdOut ? (
                <Box sx={{ display: "inline-flex", alignItems: "center", gap: 0.25 }}>
                  <Button size="small" sx={{ color: MAGIC_UI.accentA, textTransform: "none", minWidth: 0, p: 0 }} onClick={(event) => { event.stopPropagation(); handleViewTargetOutput(target, "stdout"); }}>
                    StdOut
                  </Button>
                  <Tooltip title="Copy StdOut">
                    <IconButton size="small" sx={{ color: MAGIC_UI.accentA, p: 0.35 }} onClick={(event) => { event.stopPropagation(); void handleCopyTargetOutput(target, "stdout"); }}>
                      <ContentCopyIcon sx={{ fontSize: 15 }} />
                    </IconButton>
                  </Tooltip>
                </Box>
              ) : null}
              {row.hasStdOut && row.hasStdErr ? <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>/</Typography> : null}
              {row.hasStdErr ? (
                <Box sx={{ display: "inline-flex", alignItems: "center", gap: 0.25 }}>
                  <Button size="small" sx={{ color: MAGIC_UI.danger, textTransform: "none", minWidth: 0, p: 0 }} onClick={(event) => { event.stopPropagation(); handleViewTargetOutput(target, "stderr"); }}>
                    StdErr
                  </Button>
                  <Tooltip title="Copy StdErr">
                    <IconButton size="small" sx={{ color: MAGIC_UI.danger, p: 0.35 }} onClick={(event) => { event.stopPropagation(); void handleCopyTargetOutput(target, "stderr"); }}>
                      <ContentCopyIcon sx={{ fontSize: 15 }} />
                    </IconButton>
                  </Tooltip>
                </Box>
              ) : null}
            </Box>
          );
        },
      },
      {
        field: "actions",
        headerName: "Actions",
        minWidth: 170,
        flex: 1,
        cellClass: "auto-col-tight",
        cellRenderer: (params) => {
          const row = params.data;
          const target = row?.raw;
          const canApprove = Boolean(row?.approvalId) && row?.status === "waiting_approval" && (!row?.approvalStatus || row.approvalStatus === "pending");
          if (!canApprove) {
            return <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>-</Typography>;
          }
          const busy = actioningApprovalId === row.approvalId;
          return (
            <Button
              size="small"
              startIcon={busy ? <CircularProgress size={14} /> : <CheckIcon fontSize="small" />}
              disabled={busy}
              sx={{ color: MAGIC_UI.accentC, textTransform: "none", minWidth: 0, p: 0 }}
              onClick={(event) => {
                event.stopPropagation();
                void handleApproveTarget(target);
              }}
            >
              Approve
            </Button>
          );
        },
      },
    ],
    [actioningApprovalId, handleApproveTarget, handleCopyTargetOutput, handleViewTargetOutput]
  );

  const targetGridDefaultColDef = useMemo(
    () => ({
      sortable: true,
      resizable: true,
      filter: true,
    }),
    []
  );

  const autoSizeTargetGrid = useCallback(() => {
    const api = targetGridApiRef.current;
    if (!api || !visibleTargetGridRows.length) return;
    const run = () => {
      try {
        if (typeof api.autoSizeColumns === "function") {
          api.autoSizeColumns(TARGET_AUTO_SIZE_COLUMNS, true);
        }
      } catch {
        /* grid may not be ready yet */
      }
    };
    if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
      window.requestAnimationFrame(run);
    } else {
      setTimeout(run, 0);
    }
  }, [visibleTargetGridRows.length]);

  const handleTargetGridReady = useCallback((params) => {
    targetGridApiRef.current = params.api;
    autoSizeTargetGrid();
  }, [autoSizeTargetGrid]);

  useEffect(() => {
    autoSizeTargetGrid();
  }, [autoSizeTargetGrid]);

  useEffect(() => {
    if (nameTouched || !selectedSite || editing) return;
    setForm((prev) => ({
      ...prev,
      name: `Automatic Device Onboarding ${selectedSite.name || `Site ${selectedSite.id}`}`,
    }));
  }, [editing, nameTouched, selectedSite]);

  const submit = useCallback(async () => {
    setSaving(true);
    setError("");
    setNotice("");
    try {
      const entries = splitScopeEntries(form.scope);
      const exclusions = splitScopeEntries(form.exclusionScope);
      if (!form.siteId) throw new Error("Select site.");
      if (!entries.length) throw new Error("Enter at least one IP address, CIDR, range, or FQDN.");
      if (!form.credentialId) throw new Error("Select SSH credential.");
      const port = Number(form.sshPort || DEFAULT_SSH_PORT);
      if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error("SSH port must be 1-65535.");
      const siteName = selectedSite?.name || "";
      const payload = {
        job_kind: "onboarding",
        name: form.name || `Automatic Device Onboarding ${siteName || form.siteId}`,
        components: [
          {
            kind: "device_onboarding",
            name: "Linux SSH Device Onboarding",
            install_branch: form.branch || DEFAULT_BRANCH,
            ssh_port: port,
          },
        ],
        targets: [
          {
            kind: "onboarding_scope",
            site_id: Number(form.siteId),
            site_name: siteName,
            entries,
            exclusions,
          },
        ],
        schedule: {
          type: form.scheduleType || "immediately",
          start: form.scheduleType === "immediately" ? null : isoFromDatetimeLocal(form.start),
        },
        duration: { expiration: "no_expire" },
        execution_context: "onboarding_linux_ssh",
        credential_id: Number(form.credentialId),
        use_service_account: false,
        enabled: Boolean(form.enabled),
      };
      const resp = await fetch(editing ? `/api/scheduled_jobs/${jobId}` : "/api/scheduled_jobs", {
        method: editing ? "PUT" : "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const body = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(body?.message || body?.error || `Save failed (${resp.status})`);
      }
      const savedId = body?.job?.id || jobId;
      if (savedId && !editing) {
        setNotice("Onboarding deployment started.");
        navigate(APP_PATHS.jobOnboarding(savedId), { replace: true });
      } else if (editing && savedId) {
        setTargetRows([]);
        const redeployResp = await fetch(`/api/onboarding/jobs/${encodeURIComponent(savedId)}/redeploy`, {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({}),
        });
        const redeployBody = await redeployResp.json().catch(() => ({}));
        if (!redeployResp.ok) {
          throw new Error(redeployBody?.message || redeployBody?.error || `Re-deploy failed (${redeployResp.status})`);
        }
        setNotice("Onboarding re-deploy started.");
        setActiveTabUrlKey("targets");
        window.setTimeout(() => {
          void loadTargets();
        }, 1200);
      } else {
        setNotice("Onboarding job saved.");
      }
    } catch (err) {
      setError(err?.message || "Unable to save onboarding job.");
    } finally {
      setSaving(false);
    }
  }, [editing, form, jobId, loadTargets, navigate, selectedSite, setActiveTabUrlKey]);

  const pageHeaderActions = useMemo(
    () => {
      const actions = [
        {
          id: "onboarding-back",
          label: "Back",
          icon: <ArrowBackIcon />,
          tone: "secondary",
          onClick: () => navigate(APP_PATHS.jobs),
        },
      ];
      if (editing) {
        actions.push({
          id: "onboarding-refresh",
          label: "Refresh",
          icon: <RefreshIcon />,
          tone: "secondary",
          onClick: () => void loadTargets(),
        });
      }
      actions.push({
        id: "onboarding-save",
        label: editing ? "Re-Deploy" : "Deploy",
        icon: <PlayArrowIcon />,
        tone: "primary",
        loading: saving,
        onClick: submit,
      });
      return actions;
    },
    [editing, loadTargets, navigate, saving, submit]
  );

  useRoutePageChrome({
    title: PAGE_TITLE,
    subtitle: PAGE_SUBTITLE,
    Icon: DevicesIcon,
    actions: pageHeaderActions,
  });

  if (loading) {
    return (
      <Paper elevation={0} sx={{ p: 3, background: "transparent", color: "#e2e8f0" }}>
        <CircularProgress size={24} sx={{ color: "#7dd3fc" }} />
      </Paper>
    );
  }

  return (
    <Box sx={PAGE_SX}>
      <Stack spacing={2}>
          {error ? <Alert severity="error">{error}</Alert> : null}
          {notice ? <Alert severity="success">{notice}</Alert> : null}
          {saving ? (
            <Box sx={{ display: "flex", alignItems: "center", gap: 1, color: MAGIC_UI.textMuted }}>
              <CircularProgress size={18} sx={{ color: MAGIC_UI.accentA }} />
              <Typography variant="body2">{editing ? "Re-deploying onboarding job..." : "Deploying onboarding job..."}</Typography>
            </Box>
          ) : null}

            <Tabs
              value={activeTabIndex}
              onChange={(_, value) => setActiveTabUrlKey(tabDefs[value]?.key || "name")}
              variant="scrollable"
              scrollButtons="auto"
              TabIndicatorProps={{
                style: {
                  height: 3,
                  borderRadius: 3,
                  background: NAV_TAB_COLORS.iconActive,
                },
              }}
              sx={TABS_SX}
            >
              {tabDefs.map((tabDef) => (
                <Tab key={tabDef.key} label={tabDef.label} />
              ))}
            </Tabs>

            <Box sx={{ mt: 2, minHeight: 360 }}>
              {activeTabKey === "name" ? (
                <Box sx={TAB_SECTION_SX}>
                  <SectionHeader title="Job Name" />
                  <TextField
                    label="Job Name"
                    value={form.name}
                    onChange={(event) => {
                      setNameTouched(true);
                      setField("name", event.target.value);
                    }}
                    sx={{ maxWidth: 560, ...FIELD_SX }}
                    fullWidth
                  />
                </Box>
              ) : null}

              {activeTabKey === "scope" ? (
                <Box sx={TAB_SECTION_SX}>
                  <SectionHeader
                    title="Scope"
                    detail="Discovery scope defines eligible targets. Exclusion scope removes blacklisted IPs, FQDNs, CIDRs, and ranges before SSH attempts start."
                  />
                  <FormControl fullWidth sx={FIELD_SX}>
                    <InputLabel id="onboarding-site-label">Site</InputLabel>
                    <Select
                      labelId="onboarding-site-label"
                      label="Site"
                      value={form.siteId}
                      onChange={(event) => setField("siteId", event.target.value)}
                      MenuProps={SELECT_MENU_PROPS}
                    >
                      {sites.map((site) => (
                        <MenuItem key={site.id} value={String(site.id)}>
                          {site.name || `Site ${site.id}`}
                        </MenuItem>
                      ))}
                    </Select>
                  </FormControl>
                  <Stack direction={{ xs: "column", lg: "row" }} spacing={2}>
                    <TextField
                      label="Discovery Scope"
                      value={form.scope}
                      onChange={(event) => setField("scope", event.target.value)}
                      multiline
                      minRows={9}
                      placeholder={"192.168.1.10\n192.168.1.20-192.168.1.30\n192.168.2.0/24\nserver01.local"}
                      sx={{ flex: 1, ...FIELD_SX }}
                      fullWidth
                    />
                    <TextField
                      label="Exclusion Scope"
                      value={form.exclusionScope}
                      onChange={(event) => setField("exclusionScope", event.target.value)}
                      multiline
                      minRows={9}
                      placeholder={"192.168.1.1\n192.168.1.40-192.168.1.50\nprinter01.local"}
                      sx={{ flex: 1, ...FIELD_SX }}
                      fullWidth
                    />
                  </Stack>
                </Box>
              ) : null}

              {activeTabKey === "context" ? (
                <Box sx={TAB_SECTION_SX}>
                  <SectionHeader title="SSH Context" detail="Choose stored machine credentials and target agent branch for Linux SSH enrollment." />
                  <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
                    <FormControl fullWidth sx={FIELD_SX}>
                      <InputLabel id="onboarding-credential-label">SSH Credential</InputLabel>
                      <Select
                        labelId="onboarding-credential-label"
                        label="SSH Credential"
                        value={form.credentialId}
                        onChange={(event) => setField("credentialId", event.target.value)}
                        MenuProps={SELECT_MENU_PROPS}
                      >
                        {sshCredentials.map((credential) => (
                          <MenuItem
                            key={credential.id}
                            value={String(credential.id)}
                            disabled={Boolean(credential.secret_reset_required)}
                          >
                            {credential.name || `Credential ${credential.id}`}
                            {credential.secret_reset_required ? " (Secret Re-Entry Required)" : ""}
                          </MenuItem>
                        ))}
                      </Select>
                    </FormControl>
                    <TextField
                      label="SSH Port"
                      type="number"
                      value={form.sshPort}
                      onChange={(event) => setField("sshPort", event.target.value)}
                      sx={{ width: { xs: "100%", md: 180 }, ...FIELD_SX }}
                    />
                  </Stack>
                  <Stack direction={{ xs: "column", md: "row" }} spacing={2} alignItems={{ xs: "stretch", md: "center" }}>
                    <FormControl fullWidth error={Boolean(branchLoadError)} sx={FIELD_SX}>
                      <InputLabel id="onboarding-install-branch-label">Agent Install Branch</InputLabel>
                      <Select
                        labelId="onboarding-install-branch-label"
                        label="Agent Install Branch"
                        value={normalizeBranchName(form.branch)}
                        onChange={(event) => setField("branch", event.target.value)}
                        onOpen={() => {
                          if (!branchRows.length && !branchesLoading) {
                            void fetchInstallBranches();
                          }
                        }}
                        MenuProps={SELECT_MENU_PROPS}
                      >
                        {branchOptions.map((branch) => (
                          <MenuItem key={branch.name} value={branch.name}>
                            {branch.name}
                            {branch.default ? " (default)" : ""}
                            {branch.sha ? ` - ${branch.sha.slice(0, 12)}` : ""}
                          </MenuItem>
                        ))}
                        {branchesLoading ? <MenuItem disabled value="__loading">Loading branches...</MenuItem> : null}
                        {branchLoadError ? <MenuItem disabled value="__error">Branch lookup failed</MenuItem> : null}
                      </Select>
                      {branchLoadError ? (
                        <Typography variant="caption" sx={{ mt: 0.75, color: "#fca5a5" }}>
                          {branchLoadError}
                        </Typography>
                      ) : null}
                    </FormControl>
                    <Button
                      startIcon={branchesLoading ? <CircularProgress size={16} /> : <RefreshIcon fontSize="small" />}
                      disabled={branchesLoading}
                      onClick={() => void fetchInstallBranches()}
                      sx={{ ...PRIMARY_BUTTON_SX, minWidth: 132 }}
                    >
                      Refresh
                    </Button>
                  </Stack>
                </Box>
              ) : null}

              {activeTabKey === "schedule" ? (
                <Box sx={TAB_SECTION_SX}>
                  <SectionHeader title="Schedule" detail="Immediate jobs start now. Scheduled jobs use existing recurrence behavior." />
                  <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
                    <FormControl fullWidth sx={FIELD_SX}>
                      <InputLabel id="onboarding-schedule-label">Schedule</InputLabel>
                      <Select
                        labelId="onboarding-schedule-label"
                        label="Schedule"
                        value={form.scheduleType}
                        onChange={(event) => setField("scheduleType", event.target.value)}
                        MenuProps={SELECT_MENU_PROPS}
                      >
                        <MenuItem value="immediately">Immediately</MenuItem>
                        <MenuItem value="once">Once</MenuItem>
                        <MenuItem value="daily">Daily</MenuItem>
                        <MenuItem value="weekly">Weekly</MenuItem>
                        <MenuItem value="monthly">Monthly</MenuItem>
                      </Select>
                    </FormControl>
                    <TextField
                      label="Start"
                      type="datetime-local"
                      value={form.start}
                      onChange={(event) => setField("start", event.target.value)}
                      disabled={form.scheduleType === "immediately"}
                      fullWidth
                      InputLabelProps={{ shrink: true }}
                      sx={FIELD_SX}
                    />
                  </Stack>
                  <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
                    Remote installer creates normal pending device approvals after SSH deploy succeeds.
                  </Typography>
                </Box>
              ) : null}

              {activeTabKey === "targets" ? (
                <Box sx={{ ...TAB_SECTION_SX, minHeight: 0 }}>
                  <SectionHeader
                    title="Target Status"
                    detail="Use AG Grid filters to inspect current target attempts. StdOut and StdErr open in a separate viewer."
                  />
                  <Box
                    sx={{
                      display: "flex",
                      alignItems: { xs: "flex-start", md: "flex-end" },
                      justifyContent: "space-between",
                      flexWrap: "wrap",
                      gap: 2,
                    }}
                  >
                    <Box sx={{ display: "flex", flexDirection: "column", alignItems: "flex-start", gap: "8px" }}>
                      <Typography component="span" sx={FILTER_LABEL_SX}>
                        Status
                      </Typography>
                      <CountSliderGroup
                        options={TARGET_STATUS_FILTER_OPTIONS}
                        activeKey={targetStatusFilter}
                        counts={targetStatusCounts}
                        onChange={setTargetStatusFilter}
                      />
                    </Box>
                    <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
                      Showing {visibleTargetGridRows.length.toLocaleString()} of {targetGridRows.length.toLocaleString()} targets
                    </Typography>
                  </Box>
                  <Box
                    className={gridThemeClass}
                    sx={{
                      ...GRID_PANEL_SX,
                      height: { xs: 460, md: 620 },
                    }}
                  >
                    <AgGridReact
                      rowData={visibleTargetGridRows}
                      columnDefs={targetGridColumnDefs}
                      defaultColDef={targetGridDefaultColDef}
                      suppressCellFocus
                      headerHeight={44}
                      rowHeight={50}
                      pagination
                      paginationPageSize={20}
                      paginationPageSizeSelector={[20, 50, 100]}
                      overlayNoRowsTemplate="<span class='ag-overlay-no-rows-center'>No target attempts recorded yet.</span>"
                      getRowId={(params) => String(params.data?.id || params.rowIndex)}
                      onGridReady={handleTargetGridReady}
                      theme={gridTheme}
                    />
                  </Box>
                </Box>
              ) : null}
      </Stack>

      <Dialog
        open={outputOpen}
        onClose={() => setOutputOpen(false)}
        fullWidth
        maxWidth={false}
        PaperProps={{
          sx: {
            ...DIALOG_PAPER_SX,
            display: "flex",
            flexDirection: "column",
            width: "95vw",
            maxWidth: "95vw",
            height: "95vh",
            maxHeight: "95vh",
          },
        }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <Box sx={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: 2, flexWrap: "wrap" }}>
            <DialogHeaderBlock title={outputTitle} subtitle="Review remote onboarding output." />
            <Button onClick={() => setOutputOpen(false)} sx={PRIMARY_BUTTON_SX}>
              Close
            </Button>
          </Box>
        </DialogTitle>
        <DialogContent
          sx={{
            ...DIALOG_CONTENT_SX,
            display: "flex",
            flexDirection: "column",
            gap: 2,
            flex: 1,
            minHeight: 0,
            pb: 3,
            overflow: "hidden",
          }}
        >
          {outputSections.map((section) => (
            <Box key={section.key} sx={{ display: "flex", flexDirection: "column", flex: 1, minHeight: 0 }}>
              <Typography variant="subtitle2" sx={{ color: MAGIC_UI.textBright }}>
                {section.title}
              </Typography>
              <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted, display: "block", mb: 0.5 }}>
                {section.path}
              </Typography>
              <Box
                sx={{
                  border: `1px solid ${MAGIC_UI.panelBorder}`,
                  borderRadius: 2,
                  bgcolor: "rgba(4,7,17,0.65)",
                  position: "relative",
                  display: "flex",
                  flexDirection: "column",
                  flex: 1,
                  minHeight: 0,
                  overflow: "auto",
                }}
              >
                <Box sx={{ position: "absolute", top: 10, right: 10, zIndex: 1 }}>
                  <Tooltip title={copiedOutputKey === section.key ? "Copied" : "Copy output"}>
                    <IconButton
                      size="small"
                      disabled={!section.content}
                      onClick={() => void handleCopyOutputSection(section)}
                      sx={{
                        color: copiedOutputKey === section.key ? MAGIC_UI.accentC : MAGIC_UI.textMuted,
                        backgroundColor: "rgba(2,6,23,0.58)",
                        border: "1px solid rgba(148,163,184,0.2)",
                        "&:hover": {
                          backgroundColor: "rgba(8,15,33,0.82)",
                          color: copiedOutputKey === section.key ? MAGIC_UI.accentC : MAGIC_UI.textBright,
                        },
                      }}
                    >
                      {copiedOutputKey === section.key ? <CheckIcon fontSize="small" /> : <ContentCopyIcon fontSize="small" />}
                    </IconButton>
                  </Tooltip>
                </Box>
                <Box
                  component="pre"
                  sx={{
                    m: 0,
                    p: 1.5,
                    pr: 5.5,
                    minHeight: "100%",
                    whiteSpace: "pre",
                    color: "#e6edf3",
                    fontSize: 12,
                    lineHeight: 1.45,
                    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
                  }}
                >
                  {section.content || "No output captured."}
                </Box>
              </Box>
            </Box>
          ))}
        </DialogContent>
      </Dialog>
    </Paper>
  );
}
