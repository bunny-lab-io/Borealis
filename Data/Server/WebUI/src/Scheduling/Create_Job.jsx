import React, { useEffect, useMemo, useState, useCallback, useRef } from "react";
import {
  Paper,
  Box,
  Typography,
  Tabs,
  Tab,
  TextField,
  Button,
  IconButton,
  Checkbox,
  FormControl,
  FormControlLabel,
  Select,
  InputLabel,
  Menu,
  MenuItem,
  Divider,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  Table,
  TableHead,
  TableRow,
  TableCell,
  TableBody,
  TableSortLabel,
  GlobalStyles,
  CircularProgress
} from "@mui/material";
import {
  Add as AddIcon,
  Delete as DeleteIcon,
  FilterList as FilterListIcon,
  PendingActions as PendingActionsIcon,
  Sync as SyncIcon,
  Timer as TimerIcon,
  Check as CheckIcon,
  Error as ErrorIcon,
  Refresh as RefreshIcon
} from "@mui/icons-material";
import { SimpleTreeView, TreeItem } from "@mui/x-tree-view";
import { LocalizationProvider } from "@mui/x-date-pickers/LocalizationProvider";
import { DateTimePicker } from "@mui/x-date-pickers/DateTimePicker";
import { AdapterDayjs } from "@mui/x-date-pickers/AdapterDayjs";
import dayjs from "dayjs";
import Prism from "prismjs";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-powershell";
import "prismjs/components/prism-batch";
import "prismjs/themes/prism-okaidia.css";
import Editor from "react-simple-code-editor";
import ReactFlow, { Handle, Position } from "reactflow";
import "reactflow/dist/style.css";

const hiddenHandleStyle = {
  width: 12,
  height: 12,
  border: "none",
  background: "transparent",
  opacity: 0,
  pointerEvents: "none"
};

const STATUS_META = {
  pending: { label: "Pending", color: "#aab2bf", Icon: PendingActionsIcon },
  running: { label: "Running", color: "#58a6ff", Icon: SyncIcon },
  expired: { label: "Expired", color: "#aab2bf", Icon: TimerIcon },
  success: { label: "Success", color: "#00d18c", Icon: CheckIcon },
  failed: { label: "Failed", color: "#ff4f4f", Icon: ErrorIcon }
};

const DEVICE_COLUMNS = [
  { key: "hostname", label: "Hostname" },
  { key: "online", label: "Status" },
  { key: "site", label: "Site" },
  { key: "ran_on", label: "Ran On" },
  { key: "job_status", label: "Job Status" },
  { key: "output", label: "StdOut / StdErr" }
];

function StatusNode({ data }) {
  const { label, color, count, onClick, isActive, Icon } = data || {};
  const displayCount = Number.isFinite(count) ? count : Number(count) || 0;
  const borderColor = color || "#333";
  const activeGlow = color ? `${color}55` : "rgba(88,166,255,0.35)";
  const handleClick = useCallback((event) => {
    event?.preventDefault();
    event?.stopPropagation();
    onClick && onClick();
  }, [onClick]);
  return (
    <Box
      onClick={handleClick}
      sx={{
        px: 5.4,
        py: 3.8,
        backgroundColor: "#1f1f1f",
        borderRadius: 1.5,
        border: `1px solid ${borderColor}`,
        boxShadow: isActive ? `0 0 0 2px ${activeGlow}` : "none",
        cursor: "pointer",
        minWidth: 324,
        textAlign: "left",
        transition: "border-color 0.2s ease, box-shadow 0.2s ease, transform 0.2s ease",
        transform: isActive ? "translateY(-2px)" : "none",
        display: "flex",
        alignItems: "flex-start",
        justifyContent: "flex-start"
      }}
    >
      <Handle type="target" position={Position.Left} id="left-top" style={{ ...hiddenHandleStyle, top: "32%", transform: "translateY(-50%)" }} isConnectable={false} />
      <Handle type="target" position={Position.Left} id="left-bottom" style={{ ...hiddenHandleStyle, top: "68%", transform: "translateY(-50%)" }} isConnectable={false} />
      <Handle type="source" position={Position.Right} id="right-top" style={{ ...hiddenHandleStyle, top: "32%", transform: "translateY(-50%)" }} isConnectable={false} />
      <Handle type="source" position={Position.Right} id="right-bottom" style={{ ...hiddenHandleStyle, top: "68%", transform: "translateY(-50%)" }} isConnectable={false} />
      <Box sx={{ display: "flex", alignItems: "center", gap: 1.2 }}>
        {Icon ? <Icon sx={{ color: color || "#e6edf3", fontSize: 32 }} /> : null}
        <Typography variant="subtitle2" sx={{ fontWeight: 600, color: color || "#e6edf3", userSelect: "none", fontSize: "1.3rem" }}>
          {`${displayCount} ${label || ""}`}
        </Typography>
      </Box>
    </Box>
  );
}

function SectionHeader({ title, action }) {
  return (
    <Box sx={{
      mt: 2,
      mb: 1,
      display: "flex",
      alignItems: "center",
      justifyContent: "space-between"
    }}>
      <Typography variant="subtitle1" sx={{ color: "#7db7ff" }}>{title}</Typography>
      {action || null}
    </Box>
  );
}

// Recursive renderer for both Scripts and Workflows trees
function renderTreeNodes(nodes = [], map = {}) {
  return nodes.map((n) => (
    <TreeItem key={n.id} itemId={n.id} label={n.label}>
      {n.children && n.children.length ? renderTreeNodes(n.children, map) : null}
    </TreeItem>
  ));
}

// --- Scripts tree helpers (reuse approach from Quick_Job) ---
function buildScriptTree(scripts, folders) {
  const map = {};
  const rootNode = { id: "root_s", label: "Scripts", path: "", isFolder: true, children: [] };
  map[rootNode.id] = rootNode;
  (folders || []).forEach((f) => {
    const parts = (f || "").split("/");
    let children = rootNode.children; let parentPath = "";
    parts.forEach((part) => {
      const path = parentPath ? `${parentPath}/${part}` : part;
      let node = children.find((n) => n.id === path);
      if (!node) { node = { id: path, label: part, path, isFolder: true, children: [] }; children.push(node); map[path] = node; }
      children = node.children; parentPath = path;
    });
  });
  (scripts || []).forEach((s) => {
    const parts = (s.rel_path || "").split("/");
    let children = rootNode.children; let parentPath = "";
    parts.forEach((part, idx) => {
      const path = parentPath ? `${parentPath}/${part}` : part;
      const isFile = idx === parts.length - 1;
      let node = children.find((n) => n.id === path);
      if (!node) {
        node = { id: path, label: isFile ? (s.name || s.file_name || part) : part, path, isFolder: !isFile, fileName: s.file_name, script: isFile ? s : null, children: [] };
        children.push(node); map[path] = node;
      }
      if (!isFile) { children = node.children; parentPath = path; }
    });
  });
  return { root: [rootNode], map };
}

// --- Ansible tree helpers (reuse scripts tree builder) ---
function buildAnsibleTree(playbooks, folders) {
  return buildScriptTree(playbooks, folders);
}

// --- Workflows tree helpers (reuse approach from Workflow_List) ---
function buildWorkflowTree(workflows, folders) {
  const map = {};
  const rootNode = { id: "root_w", label: "Workflows", path: "", isFolder: true, children: [] };
  map[rootNode.id] = rootNode;
  (folders || []).forEach((f) => {
    const parts = (f || "").split("/");
    let children = rootNode.children; let parentPath = "";
    parts.forEach((part) => {
      const path = parentPath ? `${parentPath}/${part}` : part;
      let node = children.find((n) => n.id === path);
      if (!node) { node = { id: path, label: part, path, isFolder: true, children: [] }; children.push(node); map[path] = node; }
      children = node.children; parentPath = path;
    });
  });
  (workflows || []).forEach((w) => {
    const parts = (w.rel_path || "").split("/");
    let children = rootNode.children; let parentPath = "";
    parts.forEach((part, idx) => {
      const path = parentPath ? `${parentPath}/${part}` : part;
      const isFile = idx === parts.length - 1;
      let node = children.find((n) => n.id === path);
      if (!node) {
        node = { id: path, label: isFile ? (w.tab_name?.trim() || w.file_name) : part, path, isFolder: !isFile, fileName: w.file_name, workflow: isFile ? w : null, children: [] };
        children.push(node); map[path] = node;
      }
      if (!isFile) { children = node.children; parentPath = path; }
    });
  });
  return { root: [rootNode], map };
}

function normalizeVariableDefinitions(vars = []) {
  return (Array.isArray(vars) ? vars : [])
    .map((raw) => {
      if (!raw || typeof raw !== "object") return null;
      const name = typeof raw.name === "string" ? raw.name.trim() : typeof raw.key === "string" ? raw.key.trim() : "";
      if (!name) return null;
      const label = typeof raw.label === "string" && raw.label.trim() ? raw.label.trim() : name;
      const type = typeof raw.type === "string" ? raw.type.toLowerCase() : "string";
      const required = Boolean(raw.required);
      const description = typeof raw.description === "string" ? raw.description : "";
      let defaultValue = "";
      if (Object.prototype.hasOwnProperty.call(raw, "default")) defaultValue = raw.default;
      else if (Object.prototype.hasOwnProperty.call(raw, "defaultValue")) defaultValue = raw.defaultValue;
      else if (Object.prototype.hasOwnProperty.call(raw, "default_value")) defaultValue = raw.default_value;
      return { name, label, type, required, description, default: defaultValue };
    })
    .filter(Boolean);
}

function coerceVariableValue(type, value) {
  if (type === "boolean") {
    if (typeof value === "boolean") return value;
    if (typeof value === "number") return value !== 0;
    if (value == null) return false;
    const str = String(value).trim().toLowerCase();
    if (!str) return false;
    return ["true", "1", "yes", "on"].includes(str);
  }
  if (type === "number") {
    if (value == null || value === "") return "";
    if (typeof value === "number" && Number.isFinite(value)) return String(value);
    const parsed = Number(value);
    return Number.isFinite(parsed) ? String(parsed) : "";
  }
  return value == null ? "" : String(value);
}

function mergeComponentVariables(docVars = [], storedVars = [], storedValueMap = {}) {
  const definitions = normalizeVariableDefinitions(docVars);
  const overrides = {};
  const storedMeta = {};
  (Array.isArray(storedVars) ? storedVars : []).forEach((raw) => {
    if (!raw || typeof raw !== "object") return;
    const name = typeof raw.name === "string" ? raw.name.trim() : "";
    if (!name) return;
    if (Object.prototype.hasOwnProperty.call(raw, "value")) overrides[name] = raw.value;
    else if (Object.prototype.hasOwnProperty.call(raw, "default")) overrides[name] = raw.default;
    storedMeta[name] = {
      label: typeof raw.label === "string" && raw.label.trim() ? raw.label.trim() : name,
      type: typeof raw.type === "string" ? raw.type.toLowerCase() : undefined,
      required: Boolean(raw.required),
      description: typeof raw.description === "string" ? raw.description : "",
      default: Object.prototype.hasOwnProperty.call(raw, "default") ? raw.default : ""
    };
  });
  if (storedValueMap && typeof storedValueMap === "object") {
    Object.entries(storedValueMap).forEach(([key, val]) => {
      const name = typeof key === "string" ? key.trim() : "";
      if (name) overrides[name] = val;
    });
  }

  const used = new Set();
  const merged = definitions.map((def) => {
    const override = Object.prototype.hasOwnProperty.call(overrides, def.name) ? overrides[def.name] : undefined;
    used.add(def.name);
    return {
      ...def,
      value: override !== undefined ? coerceVariableValue(def.type, override) : coerceVariableValue(def.type, def.default)
    };
  });

  (Array.isArray(storedVars) ? storedVars : []).forEach((raw) => {
    if (!raw || typeof raw !== "object") return;
    const name = typeof raw.name === "string" ? raw.name.trim() : "";
    if (!name || used.has(name)) return;
    const meta = storedMeta[name] || {};
    const type = meta.type || (typeof overrides[name] === "boolean" ? "boolean" : typeof overrides[name] === "number" ? "number" : "string");
    const defaultValue = Object.prototype.hasOwnProperty.call(meta, "default") ? meta.default : "";
    const override = Object.prototype.hasOwnProperty.call(overrides, name)
      ? overrides[name]
      : Object.prototype.hasOwnProperty.call(raw, "value")
        ? raw.value
        : defaultValue;
    merged.push({
      name,
      label: meta.label || name,
      type,
      required: Boolean(meta.required),
      description: meta.description || "",
      default: defaultValue,
      value: coerceVariableValue(type, override)
    });
    used.add(name);
  });

  Object.entries(overrides).forEach(([nameRaw, val]) => {
    const name = typeof nameRaw === "string" ? nameRaw.trim() : "";
    if (!name || used.has(name)) return;
    const type = typeof val === "boolean" ? "boolean" : typeof val === "number" ? "number" : "string";
    merged.push({
      name,
      label: name,
      type,
      required: false,
      description: "",
      default: "",
      value: coerceVariableValue(type, val)
    });
    used.add(name);
  });

  return merged;
}

function ComponentCard({ comp, onRemove, onVariableChange, errors = {} }) {
  const variables = Array.isArray(comp.variables)
    ? comp.variables.filter((v) => v && typeof v.name === "string" && v.name)
    : [];
  const description = comp.description || comp.path || "";
  return (
    <Paper sx={{ bgcolor: "#2a2a2a", border: "1px solid #3a3a3a", p: 1.2, mb: 1.2, borderRadius: 1 }}>
      <Box sx={{ display: "flex", gap: 2 }}>
        <Box sx={{ flex: 1 }}>
          <Typography variant="subtitle2" sx={{ color: "#e6edf3" }}>
            {comp.type === "script" ? comp.name : comp.name}
          </Typography>
          <Typography variant="body2" sx={{ color: "#aaa" }}>
            {description}
          </Typography>
        </Box>
        <Divider orientation="vertical" flexItem sx={{ borderColor: "#333" }} />
        <Box sx={{ flex: 1 }}>
          <Typography variant="subtitle2" sx={{ color: "#ccc", mb: 1 }}>Variables</Typography>
          {variables.length ? (
            <Box sx={{ display: "flex", flexDirection: "column", gap: 1.5 }}>
              {variables.map((variable) => (
                <Box key={variable.name}>
                  {variable.type === "boolean" ? (
                    <>
                      <FormControlLabel
                        control={(
                          <Checkbox
                            size="small"
                            checked={Boolean(variable.value)}
                            onChange={(e) => onVariableChange(comp.localId, variable.name, e.target.checked)}
                          />
                        )}
                        label={
                          <Typography variant="body2">
                            {variable.label}
                            {variable.required ? " *" : ""}
                          </Typography>
                        }
                      />
                      {variable.description ? (
                        <Typography variant="caption" sx={{ color: "#888", ml: 3 }}>
                          {variable.description}
                        </Typography>
                      ) : null}
                    </>
                  ) : (
                    <TextField
                      fullWidth
                      size="small"
                      label={`${variable.label}${variable.required ? " *" : ""}`}
                      type={variable.type === "number" ? "number" : variable.type === "credential" ? "password" : "text"}
                      value={variable.value ?? ""}
                      onChange={(e) => onVariableChange(comp.localId, variable.name, e.target.value)}
                      InputLabelProps={{ shrink: true }}
                      sx={{
                        "& .MuiOutlinedInput-root": { bgcolor: "#1b1b1b", color: "#e6edf3" },
                        "& .MuiInputBase-input": { color: "#e6edf3" }
                      }}
                      error={Boolean(errors[variable.name])}
                      helperText={errors[variable.name] || variable.description || ""}
                    />
                  )}
                </Box>
              ))}
            </Box>
          ) : (
            <Typography variant="body2" sx={{ color: "#888" }}>No variables defined for this assembly.</Typography>
          )}
        </Box>
        <Box>
          <IconButton onClick={() => onRemove(comp.localId)} size="small" sx={{ color: "#ff6666" }}>
            <DeleteIcon fontSize="small" />
          </IconButton>
        </Box>
      </Box>
    </Paper>
  );
}

export default function CreateJob({ onCancel, onCreated, initialJob = null }) {
  const [tab, setTab] = useState(0);
  const [jobName, setJobName] = useState("");
  const [pageTitleJobName, setPageTitleJobName] = useState("");
  // Components the job will run: {type:'script'|'workflow', path, name, description}
  const [components, setComponents] = useState([]);
  const [targets, setTargets] = useState([]); // array of hostnames
  const [scheduleType, setScheduleType] = useState("immediately");
  const [startDateTime, setStartDateTime] = useState(() => dayjs().add(5, "minute").second(0));
  const [stopAfterEnabled, setStopAfterEnabled] = useState(false);
  const [expiration, setExpiration] = useState("no_expire");
  const [execContext, setExecContext] = useState("system");
  const [credentials, setCredentials] = useState([]);
  const [credentialLoading, setCredentialLoading] = useState(false);
  const [credentialError, setCredentialError] = useState("");
  const [selectedCredentialId, setSelectedCredentialId] = useState("");
  const [useSvcAccount, setUseSvcAccount] = useState(true);

  const loadCredentials = useCallback(async () => {
    setCredentialLoading(true);
    setCredentialError("");
    try {
      const resp = await fetch("/api/credentials");
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      const list = Array.isArray(data?.credentials) ? data.credentials : [];
      list.sort((a, b) => String(a?.name || "").localeCompare(String(b?.name || "")));
      setCredentials(list);
    } catch (err) {
      setCredentials([]);
      setCredentialError(String(err.message || err));
    } finally {
      setCredentialLoading(false);
    }
  }, []);

  useEffect(() => {
    loadCredentials();
  }, [loadCredentials]);

  const remoteExec = useMemo(() => execContext === "ssh" || execContext === "winrm", [execContext]);
  const handleExecContextChange = useCallback((value) => {
    const normalized = String(value || "system").toLowerCase();
    setExecContext(normalized);
    if (normalized === "winrm") {
      setUseSvcAccount(true);
      setSelectedCredentialId("");
    } else {
      setUseSvcAccount(false);
    }
  }, []);
  const filteredCredentials = useMemo(() => {
    if (!remoteExec) return credentials;
    const target = execContext === "winrm" ? "winrm" : "ssh";
    return credentials.filter((cred) => String(cred.connection_type || "").toLowerCase() === target);
  }, [credentials, remoteExec, execContext]);

  useEffect(() => {
    if (!remoteExec) {
      return;
    }
    if (execContext === "winrm" && useSvcAccount) {
      setSelectedCredentialId("");
      return;
    }
    if (!filteredCredentials.length) {
      setSelectedCredentialId("");
      return;
    }
    if (!selectedCredentialId || !filteredCredentials.some((cred) => String(cred.id) === String(selectedCredentialId))) {
      setSelectedCredentialId(String(filteredCredentials[0].id));
    }
  }, [remoteExec, filteredCredentials, selectedCredentialId, execContext, useSvcAccount]);

  // dialogs state
  const [addCompOpen, setAddCompOpen] = useState(false);
  const [compTab, setCompTab] = useState("scripts");
  const [scriptTree, setScriptTree] = useState([]); const [scriptMap, setScriptMap] = useState({});
  const [workflowTree, setWorkflowTree] = useState([]); const [workflowMap, setWorkflowMap] = useState({});
  const [ansibleTree, setAnsibleTree] = useState([]); const [ansibleMap, setAnsibleMap] = useState({});
  const [selectedNodeId, setSelectedNodeId] = useState("");

  const [addTargetOpen, setAddTargetOpen] = useState(false);
  const [availableDevices, setAvailableDevices] = useState([]); // [{hostname, display, online}]
  const [selectedTargets, setSelectedTargets] = useState({}); // map hostname->bool
  const [deviceSearch, setDeviceSearch] = useState("");
  const [componentVarErrors, setComponentVarErrors] = useState({});
  const [deviceRows, setDeviceRows] = useState([]);
  const [deviceStatusFilter, setDeviceStatusFilter] = useState(null);
  const [deviceOrderBy, setDeviceOrderBy] = useState("hostname");
  const [deviceOrder, setDeviceOrder] = useState("asc");
  const [deviceFilters, setDeviceFilters] = useState({});
  const [filterAnchorEl, setFilterAnchorEl] = useState(null);
  const [activeFilterColumn, setActiveFilterColumn] = useState(null);
  const [pendingFilterValue, setPendingFilterValue] = useState("");

  const generateLocalId = useCallback(
    () => `${Date.now()}_${Math.random().toString(36).slice(2, 8)}`,
    []
  );

  const getDefaultFilterValue = useCallback((key) => (["online", "job_status", "output"].includes(key) ? "all" : ""), []);

  const isColumnFiltered = useCallback((key) => {
    if (!deviceFilters || typeof deviceFilters !== "object") return false;
    const value = deviceFilters[key];
    if (value == null) return false;
    if (typeof value === "string") {
      const trimmed = value.trim();
      if (!trimmed || trimmed === "all") return false;
      return true;
    }
    return true;
  }, [deviceFilters]);

  const openFilterMenu = useCallback((event, columnKey) => {
    setActiveFilterColumn(columnKey);
    setPendingFilterValue(deviceFilters[columnKey] ?? getDefaultFilterValue(columnKey));
    setFilterAnchorEl(event.currentTarget);
  }, [deviceFilters, getDefaultFilterValue]);

  const closeFilterMenu = useCallback(() => {
    setFilterAnchorEl(null);
    setActiveFilterColumn(null);
  }, []);

  const applyFilter = useCallback(() => {
    if (!activeFilterColumn) {
      closeFilterMenu();
      return;
    }
    const value = pendingFilterValue;
    setDeviceFilters((prev) => {
      const next = { ...(prev || {}) };
      if (!value || value === "all" || (typeof value === "string" && !value.trim())) {
        delete next[activeFilterColumn];
      } else {
        next[activeFilterColumn] = value;
      }
      return next;
    });
    closeFilterMenu();
  }, [activeFilterColumn, pendingFilterValue, closeFilterMenu]);

  const clearFilter = useCallback(() => {
    if (!activeFilterColumn) {
      closeFilterMenu();
      return;
    }
    setDeviceFilters((prev) => {
      const next = { ...(prev || {}) };
      delete next[activeFilterColumn];
      return next;
    });
    setPendingFilterValue(getDefaultFilterValue(activeFilterColumn));
    closeFilterMenu();
  }, [activeFilterColumn, closeFilterMenu, getDefaultFilterValue]);

  const renderFilterControl = () => {
    const columnKey = activeFilterColumn;
    if (!columnKey) return null;
    if (columnKey === "online") {
      return (
        <Select
          size="small"
          fullWidth
          value={typeof pendingFilterValue === "string" && pendingFilterValue ? pendingFilterValue : "all"}
          onChange={(e) => setPendingFilterValue(e.target.value)}
        >
          <MenuItem value="all">All Statuses</MenuItem>
          <MenuItem value="online">Online</MenuItem>
          <MenuItem value="offline">Offline</MenuItem>
        </Select>
      );
    }
    if (columnKey === "job_status") {
      const options = ["success", "failed", "running", "pending", "expired", "timed out"];
      return (
        <Select
          size="small"
          fullWidth
          value={typeof pendingFilterValue === "string" && pendingFilterValue ? pendingFilterValue : "all"}
          onChange={(e) => setPendingFilterValue(e.target.value)}
        >
          <MenuItem value="all">All Results</MenuItem>
          {options.map((opt) => (
            <MenuItem key={opt} value={opt}>{opt.replace(/\b\w/g, (m) => m.toUpperCase())}</MenuItem>
          ))}
        </Select>
      );
    }
    if (columnKey === "output") {
      return (
        <Select
          size="small"
          fullWidth
          value={typeof pendingFilterValue === "string" && pendingFilterValue ? pendingFilterValue : "all"}
          onChange={(e) => setPendingFilterValue(e.target.value)}
        >
          <MenuItem value="all">All Output</MenuItem>
          <MenuItem value="stdout">StdOut Only</MenuItem>
          <MenuItem value="stderr">StdErr Only</MenuItem>
          <MenuItem value="both">StdOut & StdErr</MenuItem>
          <MenuItem value="none">No Output</MenuItem>
        </Select>
      );
    }
    const placeholders = {
      hostname: "Filter hostname",
      site: "Filter site",
      ran_on: "Filter date/time"
    };
    const value = typeof pendingFilterValue === "string" ? pendingFilterValue : "";
    return (
      <TextField
        size="small"
        autoFocus
        fullWidth
        placeholder={placeholders[columnKey] || "Filter value"}
        value={value}
        onChange={(e) => setPendingFilterValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            applyFilter();
          }
        }}
      />
    );
  };

  const handleDeviceSort = useCallback((key) => {
    setDeviceOrderBy((prevKey) => {
      if (prevKey === key) {
        setDeviceOrder((prevDir) => (prevDir === "asc" ? "desc" : "asc"));
        return prevKey;
      }
      setDeviceOrder(key === "ran_on" ? "desc" : "asc");
      return key;
    });
  }, []);

  const fmtTs = useCallback((ts) => {
    if (!ts) return "";
    try {
      const d = new Date(Number(ts) * 1000);
      return d.toLocaleString(undefined, {
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "numeric",
        minute: "2-digit"
      });
    } catch {
      return "";
    }
  }, []);

  const deviceFiltered = useMemo(() => {
    const matchStatusFilter = (status, filterKey) => {
      if (filterKey === "pending") return status === "pending" || status === "scheduled" || status === "queued" || status === "";
      if (filterKey === "running") return status === "running";
      if (filterKey === "success") return status === "success";
      if (filterKey === "failed") return status === "failed" || status === "failure" || status === "timed out" || status === "timed_out" || status === "warning";
      if (filterKey === "expired") return status === "expired";
      return true;
    };

    return deviceRows.filter((row) => {
      const normalizedStatus = String(row?.job_status || "").trim().toLowerCase();
      if (deviceStatusFilter && !matchStatusFilter(normalizedStatus, deviceStatusFilter)) {
        return false;
      }
      if (deviceFilters && typeof deviceFilters === "object") {
        for (const [key, rawValue] of Object.entries(deviceFilters)) {
          if (rawValue == null) continue;
          if (typeof rawValue === "string") {
            const trimmed = rawValue.trim();
            if (!trimmed || trimmed === "all") continue;
          }
          if (key === "hostname") {
            const expected = String(rawValue || "").toLowerCase();
            if (!String(row?.hostname || "").toLowerCase().includes(expected)) return false;
          } else if (key === "online") {
            if (rawValue === "online" && !row?.online) return false;
            if (rawValue === "offline" && row?.online) return false;
          } else if (key === "site") {
            const expected = String(rawValue || "").toLowerCase();
            if (!String(row?.site || "").toLowerCase().includes(expected)) return false;
          } else if (key === "ran_on") {
            const expected = String(rawValue || "").toLowerCase();
            const formatted = fmtTs(row?.ran_on).toLowerCase();
            if (!formatted.includes(expected)) return false;
          } else if (key === "job_status") {
            const expected = String(rawValue || "").toLowerCase();
            if (!normalizedStatus.includes(expected)) return false;
          } else if (key === "output") {
            if (rawValue === "stdout" && !row?.has_stdout) return false;
            if (rawValue === "stderr" && !row?.has_stderr) return false;
            if (rawValue === "both" && (!row?.has_stdout || !row?.has_stderr)) return false;
            if (rawValue === "none" && (row?.has_stdout || row?.has_stderr)) return false;
          }
        }
      }
      return true;
    });
  }, [deviceRows, deviceStatusFilter, deviceFilters, fmtTs]);

  const deviceSorted = useMemo(() => {
    const arr = [...deviceFiltered];
    const dir = deviceOrder === "asc" ? 1 : -1;
    arr.sort((a, b) => {
      let delta = 0;
      switch (deviceOrderBy) {
        case "hostname":
          delta = String(a?.hostname || "").localeCompare(String(b?.hostname || ""));
          break;
        case "online":
          delta = Number(a?.online ? 1 : 0) - Number(b?.online ? 1 : 0);
          break;
        case "site":
          delta = String(a?.site || "").localeCompare(String(b?.site || ""));
          break;
        case "ran_on":
          delta = Number(a?.ran_on || 0) - Number(b?.ran_on || 0);
          break;
        case "job_status":
          delta = String(a?.job_status || "").localeCompare(String(b?.job_status || ""));
          break;
        case "output": {
          const score = (row) => (row?.has_stdout ? 2 : 0) + (row?.has_stderr ? 1 : 0);
          delta = score(a) - score(b);
          break;
        }
        default:
          delta = 0;
      }
      if (delta === 0) {
        delta = String(a?.hostname || "").localeCompare(String(b?.hostname || ""));
      }
      return delta * dir;
    });
    return arr;
  }, [deviceFiltered, deviceOrder, deviceOrderBy]);

  const normalizeComponentPath = useCallback((type, rawPath) => {
    const trimmed = (rawPath || "").replace(/\\/g, "/").replace(/^\/+/, "").trim();
    if (!trimmed) return "";
    if (type === "script") {
      return trimmed.startsWith("Scripts/") ? trimmed : `Scripts/${trimmed}`;
    }
    return trimmed;
  }, []);

  const fetchAssemblyDoc = useCallback(async (type, rawPath) => {
    const normalizedPath = normalizeComponentPath(type, rawPath);
    if (!normalizedPath) return { doc: null, normalizedPath: "" };
    const trimmed = normalizedPath.replace(/\\/g, "/").replace(/^\/+/, "").trim();
    if (!trimmed) return { doc: null, normalizedPath: "" };
    let requestPath = trimmed;
    if (type === "script" && requestPath.toLowerCase().startsWith("scripts/")) {
      requestPath = requestPath.slice("Scripts/".length);
    } else if (type === "ansible" && requestPath.toLowerCase().startsWith("ansible_playbooks/")) {
      requestPath = requestPath.slice("Ansible_Playbooks/".length);
    }
    if (!requestPath) return { doc: null, normalizedPath };
    try {
      const island = type === "ansible" ? "ansible" : "scripts";
      const resp = await fetch(`/api/assembly/load?island=${island}&path=${encodeURIComponent(requestPath)}`);
      if (!resp.ok) {
        return { doc: null, normalizedPath };
      }
      const data = await resp.json();
      return { doc: data, normalizedPath };
    } catch {
      return { doc: null, normalizedPath };
    }
  }, [normalizeComponentPath]);

  const hydrateExistingComponents = useCallback(async (rawComponents = []) => {
    const results = [];
    for (const raw of rawComponents) {
      if (!raw || typeof raw !== "object") continue;
      const typeRaw = raw.type || raw.component_type || "script";
      if (typeRaw === "workflow") {
        results.push({
          ...raw,
          type: "workflow",
          variables: Array.isArray(raw.variables) ? raw.variables : [],
          localId: generateLocalId()
        });
        continue;
      }
      const type = typeRaw === "ansible" ? "ansible" : "script";
      const basePath = raw.path || raw.script_path || raw.rel_path || "";
      const { doc, normalizedPath } = await fetchAssemblyDoc(type, basePath);
      const assembly = doc?.assembly || {};
      const docVars = assembly?.variables || doc?.variables || [];
      const mergedVariables = mergeComponentVariables(docVars, raw.variables, raw.variable_values);
      results.push({
        ...raw,
        type,
        path: normalizedPath || basePath,
        name: raw.name || assembly?.name || raw.file_name || raw.tab_name || normalizedPath || basePath,
        description: raw.description || assembly?.description || normalizedPath || basePath,
        variables: mergedVariables,
        localId: generateLocalId()
      });
    }
    return results;
  }, [fetchAssemblyDoc, generateLocalId]);

  const sanitizeComponentsForSave = useCallback((items) => {
    return (Array.isArray(items) ? items : []).map((comp) => {
      if (!comp || typeof comp !== "object") return comp;
      const { localId, ...rest } = comp;
      const sanitized = { ...rest };
      if (Array.isArray(comp.variables)) {
        const valuesMap = {};
        sanitized.variables = comp.variables
          .filter((v) => v && typeof v.name === "string" && v.name)
          .map((v) => {
            const entry = {
              name: v.name,
              label: v.label || v.name,
              type: v.type || "string",
              required: Boolean(v.required),
              description: v.description || ""
            };
            if (Object.prototype.hasOwnProperty.call(v, "default")) entry.default = v.default;
            if (Object.prototype.hasOwnProperty.call(v, "value")) {
              entry.value = v.value;
              valuesMap[v.name] = v.value;
            }
            return entry;
          });
        if (!sanitized.variables.length) sanitized.variables = [];
        if (Object.keys(valuesMap).length) sanitized.variable_values = valuesMap;
        else delete sanitized.variable_values;
      }
      return sanitized;
    });
  }, []);

  const updateComponentVariable = useCallback((localId, name, value) => {
    if (!localId || !name) return;
    setComponents((prev) => prev.map((comp) => {
      if (!comp || comp.localId !== localId) return comp;
      const vars = Array.isArray(comp.variables) ? comp.variables : [];
      const nextVars = vars.map((variable) => {
        if (!variable || variable.name !== name) return variable;
        return { ...variable, value: coerceVariableValue(variable.type || "string", value) };
      });
      return { ...comp, variables: nextVars };
    }));
    setComponentVarErrors((prev) => {
      if (!prev[localId] || !prev[localId][name]) return prev;
      const next = { ...prev };
      const compErrors = { ...next[localId] };
      delete compErrors[name];
      if (Object.keys(compErrors).length) next[localId] = compErrors;
      else delete next[localId];
      return next;
    });
  }, []);

  const removeComponent = useCallback((localId) => {
    setComponents((prev) => prev.filter((comp) => comp.localId !== localId));
    setComponentVarErrors((prev) => {
      if (!prev[localId]) return prev;
      const next = { ...prev };
      delete next[localId];
      return next;
    });
  }, []);

  const isValid = useMemo(() => {
    const base = jobName.trim().length > 0 && components.length > 0 && targets.length > 0;
    if (!base) return false;
    const needsCredential = remoteExec && !(execContext === "winrm" && useSvcAccount);
    if (needsCredential && !selectedCredentialId) return false;
    if (scheduleType !== "immediately") {
      return !!startDateTime;
    }
    return true;
  }, [jobName, components.length, targets.length, scheduleType, startDateTime, remoteExec, selectedCredentialId, execContext, useSvcAccount]);

  const [confirmOpen, setConfirmOpen] = useState(false);
  const editing = !!(initialJob && initialJob.id);

  // --- Job History (only when editing) ---
  const [historyRows, setHistoryRows] = useState([]);
  const [historyOrderBy, setHistoryOrderBy] = useState("started_ts");
  const [historyOrder, setHistoryOrder] = useState("desc");
  const activityCacheRef = useRef(new Map());
  const [outputOpen, setOutputOpen] = useState(false);
  const [outputTitle, setOutputTitle] = useState("");
  const [outputSections, setOutputSections] = useState([]);
  const [outputLoading, setOutputLoading] = useState(false);
  const [outputError, setOutputError] = useState("");

  const loadHistory = useCallback(async () => {
    if (!editing) return;
    try {
      const [runsResp, jobResp, devResp] = await Promise.all([
        fetch(`/api/scheduled_jobs/${initialJob.id}/runs?days=30`),
        fetch(`/api/scheduled_jobs/${initialJob.id}`),
        fetch(`/api/scheduled_jobs/${initialJob.id}/devices`)
      ]);
      const runs = await runsResp.json();
      const job = await jobResp.json();
      const dev = await devResp.json();
      if (!runsResp.ok) throw new Error(runs.error || `HTTP ${runsResp.status}`);
      if (!jobResp.ok) throw new Error(job.error || `HTTP ${jobResp.status}`);
      if (!devResp.ok) throw new Error(dev.error || `HTTP ${devResp.status}`);
      setHistoryRows(Array.isArray(runs.runs) ? runs.runs : []);
      setJobSummary(job.job || {});
      const devices = Array.isArray(dev.devices) ? dev.devices.map((device) => ({
        ...device,
        activities: Array.isArray(device.activities) ? device.activities : [],
      })) : [];
      setDeviceRows(devices);
    } catch {
      setHistoryRows([]);
      setJobSummary({});
      setDeviceRows([]);
    }
  }, [editing, initialJob?.id]);

  useEffect(() => {
    if (!editing) return;
    let t;
    (async () => { try { await loadHistory(); } catch {} })();
    t = setInterval(loadHistory, 10000);
    return () => { if (t) clearInterval(t); };
  }, [editing, loadHistory]);

  const resultChip = (status) => {
    const map = {
      Success: { bg: '#00d18c', fg: '#000' },
      Running: { bg: '#58a6ff', fg: '#000' },
      Scheduled: { bg: '#999999', fg: '#fff' },
      Expired: { bg: '#777777', fg: '#fff' },
      Failed: { bg: '#ff4f4f', fg: '#fff' },
      Warning: { bg: '#ff8c00', fg: '#000' }
    };
    const c = map[status] || { bg: '#aaa', fg: '#000' };
    return (
      <span style={{ display: 'inline-block', padding: '2px 8px', borderRadius: 999, background: c.bg, color: c.fg, fontWeight: 600, fontSize: 12 }}>
        {status || ''}
      </span>
    );
  };

  const aggregatedHistory = useMemo(() => {
    if (!Array.isArray(historyRows) || historyRows.length === 0) return [];
    const map = new Map();
    historyRows.forEach((row) => {
      const key = row?.scheduled_ts || row?.started_ts || row?.finished_ts || row?.id;
      if (!key) return;
      const strKey = String(key);
      const existing = map.get(strKey) || {
        key: strKey,
        scheduled_ts: row?.scheduled_ts || null,
        started_ts: null,
        finished_ts: null,
        statuses: new Set()
      };
      if (!existing.scheduled_ts && row?.scheduled_ts) existing.scheduled_ts = row.scheduled_ts;
      if (row?.started_ts) {
        existing.started_ts = existing.started_ts == null ? row.started_ts : Math.min(existing.started_ts, row.started_ts);
      }
      if (row?.finished_ts) {
        existing.finished_ts = existing.finished_ts == null ? row.finished_ts : Math.max(existing.finished_ts, row.finished_ts);
      }
      if (row?.status) existing.statuses.add(String(row.status));
      map.set(strKey, existing);
    });
    const summaries = [];
    map.forEach((entry) => {
      const statuses = Array.from(entry.statuses).map((s) => String(s || "").trim().toLowerCase()).filter(Boolean);
      if (!statuses.length) return;
      const hasInFlight = statuses.some((s) => s === "running" || s === "pending" || s === "scheduled");
      if (hasInFlight) return;
      const hasFailure = statuses.some((s) => ["failed", "failure", "expired", "timed out", "timed_out", "warning"].includes(s));
      const allSuccess = statuses.every((s) => s === "success");
      const statusLabel = hasFailure ? "Failed" : (allSuccess ? "Success" : "Failed");
      summaries.push({
        key: entry.key,
        scheduled_ts: entry.scheduled_ts,
        started_ts: entry.started_ts,
        finished_ts: entry.finished_ts,
        status: statusLabel
      });
    });
    return summaries;
  }, [historyRows]);

  const sortedHistory = useMemo(() => {
    const dir = historyOrder === 'asc' ? 1 : -1;
    const key = historyOrderBy;
    return [...aggregatedHistory].sort((a, b) => {
      const getVal = (row) => {
        if (key === 'scheduled_ts' || key === 'started_ts' || key === 'finished_ts') {
          return Number(row?.[key] || 0);
        }
        return String(row?.[key] || '');
      };
      const A = getVal(a);
      const B = getVal(b);
      if (typeof A === 'number' && typeof B === 'number') {
        return (A - B) * dir;
      }
      return String(A).localeCompare(String(B)) * dir;
    });
  }, [aggregatedHistory, historyOrderBy, historyOrder]);

  const handleHistorySort = (col) => {
    if (historyOrderBy === col) setHistoryOrder(historyOrder === 'asc' ? 'desc' : 'asc');
    else { setHistoryOrderBy(col); setHistoryOrder('asc'); }
  };

  const renderHistory = () => (
    <Box sx={{ maxHeight: 400, overflowY: 'auto' }}>
      <Table size="small">
        <TableHead>
          <TableRow>
            <TableCell sortDirection={historyOrderBy === 'scheduled_ts' ? historyOrder : false}>
              <TableSortLabel active={historyOrderBy === 'scheduled_ts'} direction={historyOrderBy === 'scheduled_ts' ? historyOrder : 'asc'} onClick={() => handleHistorySort('scheduled_ts')}>
                Scheduled
              </TableSortLabel>
            </TableCell>
            <TableCell sortDirection={historyOrderBy === 'started_ts' ? historyOrder : false}>
              <TableSortLabel active={historyOrderBy === 'started_ts'} direction={historyOrderBy === 'started_ts' ? historyOrder : 'asc'} onClick={() => handleHistorySort('started_ts')}>
                Started
              </TableSortLabel>
            </TableCell>
            <TableCell sortDirection={historyOrderBy === 'finished_ts' ? historyOrder : false}>
              <TableSortLabel active={historyOrderBy === 'finished_ts'} direction={historyOrderBy === 'finished_ts' ? historyOrder : 'asc'} onClick={() => handleHistorySort('finished_ts')}>
                Finished
              </TableSortLabel>
            </TableCell>
            <TableCell>Status</TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {sortedHistory.map((r) => (
            <TableRow key={r.key}>
              <TableCell>{fmtTs(r.scheduled_ts)}</TableCell>
              <TableCell>{fmtTs(r.started_ts)}</TableCell>
              <TableCell>{fmtTs(r.finished_ts)}</TableCell>
              <TableCell>{resultChip(r.status)}</TableCell>
            </TableRow>
          ))}
          {sortedHistory.length === 0 && (
            <TableRow>
              <TableCell colSpan={4} sx={{ color: '#888' }}>No runs in the last 30 days.</TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </Box>
  );

  // --- Job Progress (summary) ---
  const [jobSummary, setJobSummary] = useState({});
  const counts = jobSummary?.result_counts || {};

  const deviceStatusCounts = useMemo(() => {
    const base = { pending: 0, running: 0, success: 0, failed: 0, expired: 0 };
    deviceRows.forEach((row) => {
      const normalized = String(row?.job_status || "").trim().toLowerCase();
      if (!normalized || normalized === "pending" || normalized === "scheduled" || normalized === "queued") {
        base.pending += 1;
      } else if (normalized === "running") {
        base.running += 1;
      } else if (normalized === "success") {
        base.success += 1;
      } else if (normalized === "expired") {
        base.expired += 1;
      } else if (normalized === "failed" || normalized === "failure" || normalized === "timed out" || normalized === "timed_out" || normalized === "warning") {
        base.failed += 1;
      } else {
        base.pending += 1;
      }
    });
    return base;
  }, [deviceRows]);

  const statusCounts = useMemo(() => {
    const merged = { pending: 0, running: 0, success: 0, failed: 0, expired: 0 };
    Object.keys(merged).forEach((key) => {
      const summaryVal = Number((counts || {})[key] ?? 0);
      const fallback = deviceStatusCounts[key] ?? 0;
      merged[key] = summaryVal > 0 ? summaryVal : fallback;
    });
    return merged;
  }, [counts, deviceStatusCounts]);

  const statusNodeTypes = useMemo(() => ({ statusNode: StatusNode }), []);

  const handleStatusNodeClick = useCallback((key) => {
    setDeviceStatusFilter((prev) => (prev === key ? null : key));
  }, []);

  const statusNodes = useMemo(() => [
    {
      id: "pending",
      type: "statusNode",
      position: { x: -420, y: 170 },
      data: {
        label: STATUS_META.pending.label,
        color: STATUS_META.pending.color,
        count: statusCounts.pending,
        Icon: STATUS_META.pending.Icon,
        onClick: () => handleStatusNodeClick("pending"),
        isActive: deviceStatusFilter === "pending"
      },
      draggable: false,
      selectable: false
    },
    {
      id: "running",
      type: "statusNode",
      position: { x: 0, y: 0 },
      data: {
        label: STATUS_META.running.label,
        color: STATUS_META.running.color,
        count: statusCounts.running,
        Icon: STATUS_META.running.Icon,
        onClick: () => handleStatusNodeClick("running"),
        isActive: deviceStatusFilter === "running"
      },
      draggable: false,
      selectable: false
    },
    {
      id: "expired",
      type: "statusNode",
      position: { x: 0, y: 340 },
      data: {
        label: STATUS_META.expired.label,
        color: STATUS_META.expired.color,
        count: statusCounts.expired,
        Icon: STATUS_META.expired.Icon,
        onClick: () => handleStatusNodeClick("expired"),
        isActive: deviceStatusFilter === "expired"
      },
      draggable: false,
      selectable: false
    },
    {
      id: "success",
      type: "statusNode",
      position: { x: 420, y: 0 },
      data: {
        label: STATUS_META.success.label,
        color: STATUS_META.success.color,
        count: statusCounts.success,
        Icon: STATUS_META.success.Icon,
        onClick: () => handleStatusNodeClick("success"),
        isActive: deviceStatusFilter === "success"
      },
      draggable: false,
      selectable: false
    },
    {
      id: "failed",
      type: "statusNode",
      position: { x: 420, y: 340 },
      data: {
        label: STATUS_META.failed.label,
        color: STATUS_META.failed.color,
        count: statusCounts.failed,
        Icon: STATUS_META.failed.Icon,
        onClick: () => handleStatusNodeClick("failed"),
        isActive: deviceStatusFilter === "failed"
      },
      draggable: false,
      selectable: false
    }
  ], [statusCounts, handleStatusNodeClick, deviceStatusFilter]);

  const statusEdges = useMemo(() => [
    {
      id: "pending-running",
      source: "pending",
      target: "running",
      sourceHandle: "right-top",
      targetHandle: "left-top",
      type: "smoothstep",
      animated: true,
      className: "status-flow-edge"
    },
    {
      id: "pending-expired",
      source: "pending",
      target: "expired",
      sourceHandle: "right-bottom",
      targetHandle: "left-bottom",
      type: "smoothstep",
      animated: true,
      className: "status-flow-edge"
    },
    {
      id: "running-success",
      source: "running",
      target: "success",
      sourceHandle: "right-top",
      targetHandle: "left-top",
      type: "smoothstep",
      animated: true,
      className: "status-flow-edge"
    },
    {
      id: "running-failed",
      source: "running",
      target: "failed",
      sourceHandle: "right-bottom",
      targetHandle: "left-bottom",
      type: "smoothstep",
      animated: true,
      className: "status-flow-edge"
    }
  ], []);

  const JobStatusFlow = () => (
    <Box sx={{ bgcolor: "#2e2e2e", border: "1px solid #2a2a2a", borderRadius: 1, mb: 2 }}>
      <GlobalStyles styles={{
        "@keyframes statusFlowDash": {
          "0%": { strokeDashoffset: 0 },
          "100%": { strokeDashoffset: -24 }
        },
        ".status-flow-edge .react-flow__edge-path": {
          strokeDasharray: "10 6",
          animation: "statusFlowDash 1.2s linear infinite",
          strokeWidth: 2,
          stroke: "#58a6ff"
        }
      }} />
      <Box sx={{ height: 380, p: 3 }}>
        <ReactFlow
          nodes={statusNodes}
          edges={statusEdges}
          nodeTypes={statusNodeTypes}
          fitView
          fitViewOptions={{ padding: 0.2 }}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable={false}
          panOnDrag={false}
          zoomOnScroll={false}
          zoomOnPinch={false}
          panOnScroll={false}
          zoomOnDoubleClick={false}
          preventScrolling={false}
          onNodeClick={(_, node) => {
            if (node?.id && STATUS_META[node.id]) handleStatusNodeClick(node.id);
          }}
          selectionOnDrag={false}
          proOptions={{ hideAttribution: true }}
          style={{ background: "transparent" }}
        />
      </Box>
      {deviceStatusFilter ? (
        <Box sx={{ mt: 1, px: 3, pb: 3, display: "flex", alignItems: "center", gap: 1.5 }}>
          <Typography variant="caption" sx={{ color: "#aaa" }}>
            Showing devices with {STATUS_META[deviceStatusFilter]?.label || deviceStatusFilter} results
          </Typography>
          <Button size="small" sx={{ color: "#58a6ff", textTransform: "none", p: 0 }} onClick={() => setDeviceStatusFilter(null)}>
            Clear Filter
          </Button>
        </Box>
      ) : null}
    </Box>
  );
  const inferLanguage = useCallback((path = "") => {
    const lower = String(path || "").toLowerCase();
    if (lower.endsWith(".ps1")) return "powershell";
    if (lower.endsWith(".bat")) return "batch";
    if (lower.endsWith(".sh")) return "bash";
    if (lower.endsWith(".yml") || lower.endsWith(".yaml")) return "yaml";
    return "powershell";
  }, []);

  const highlightCode = useCallback((code, lang) => {
    try {
      return Prism.highlight(code ?? "", Prism.languages[lang] || Prism.languages.markup, lang);
    } catch {
      return String(code || "");
    }
  }, []);

  const loadActivity = useCallback(async (activityId) => {
    const idNum = Number(activityId || 0);
    if (!idNum) return null;
    if (activityCacheRef.current.has(idNum)) {
      return activityCacheRef.current.get(idNum);
    }
    try {
      const resp = await fetch(`/api/device/activity/job/${idNum}`);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      activityCacheRef.current.set(idNum, data);
      return data;
    } catch {
      return null;
    }
  }, []);

  const handleViewDeviceOutput = useCallback(async (row, mode = "stdout") => {
    if (!row) return;
    const label = mode === "stderr" ? "StdErr" : "StdOut";
    const activities = Array.isArray(row.activities) ? row.activities : [];
    const relevant = activities.filter((act) => (mode === "stderr" ? act.has_stderr : act.has_stdout));
    setOutputTitle(`${label} - ${row.hostname || ""}`);
    setOutputSections([]);
    setOutputError("");
    setOutputLoading(true);
    setOutputOpen(true);
    if (!relevant.length) {
      setOutputError(`No ${label} available for this device.`);
      setOutputLoading(false);
      return;
    }
    const sections = [];
    for (const act of relevant) {
      const activityId = Number(act.activity_id || act.id || 0);
      if (!activityId) continue;
      const data = await loadActivity(activityId);
      if (!data) continue;
      const content = mode === "stderr" ? (data.stderr || "") : (data.stdout || "");
      const sectionTitle = act.component_name || data.script_name || data.script_path || `Activity ${activityId}`;
      sections.push({
        key: `${activityId}-${mode}`,
        title: sectionTitle,
        path: data.script_path || "",
        lang: inferLanguage(data.script_path || ""),
        content,
      });
    }
    if (!sections.length) {
      setOutputError(`No ${label} available for this device.`);
    }
    setOutputSections(sections);
    setOutputLoading(false);
  }, [inferLanguage, loadActivity]);

  useEffect(() => {
    let canceled = false;
    const hydrate = async () => {
      if (initialJob && initialJob.id) {
        setJobName(initialJob.name || "");
        setPageTitleJobName(typeof initialJob.name === "string" ? initialJob.name.trim() : "");
        setTargets(Array.isArray(initialJob.targets) ? initialJob.targets : []);
        setScheduleType(initialJob.schedule_type || initialJob.schedule?.type || "immediately");
        setStartDateTime(initialJob.start_ts ? dayjs(Number(initialJob.start_ts) * 1000).second(0) : (initialJob.schedule?.start ? dayjs(initialJob.schedule.start).second(0) : dayjs().add(5, "minute").second(0)));
        setStopAfterEnabled(Boolean(initialJob.duration_stop_enabled));
        setExpiration(initialJob.expiration || "no_expire");
        setExecContext(initialJob.execution_context || "system");
        setSelectedCredentialId(initialJob.credential_id ? String(initialJob.credential_id) : "");
        if ((initialJob.execution_context || "").toLowerCase() === "winrm") {
          setUseSvcAccount(initialJob.use_service_account !== false);
        } else {
          setUseSvcAccount(false);
        }
        const comps = Array.isArray(initialJob.components) ? initialJob.components : [];
        const hydrated = await hydrateExistingComponents(comps);
        if (!canceled) {
          setComponents(hydrated);
          setComponentVarErrors({});
        }
      } else if (!initialJob) {
        setPageTitleJobName("");
        setComponents([]);
        setComponentVarErrors({});
        setSelectedCredentialId("");
        setUseSvcAccount(true);
      }
    };
    hydrate();
    return () => {
      canceled = true;
    };
  }, [initialJob, hydrateExistingComponents]);

  const openAddComponent = async () => {
    setAddCompOpen(true);
    try {
      // scripts
      const sResp = await fetch("/api/assembly/list?island=scripts");
      if (sResp.ok) {
        const sData = await sResp.json();
        const { root, map } = buildScriptTree(sData.items || [], sData.folders || []);
        setScriptTree(root); setScriptMap(map);
      } else { setScriptTree([]); setScriptMap({}); }
    } catch { setScriptTree([]); setScriptMap({}); }
    try {
      // workflows
      const wResp = await fetch("/api/assembly/list?island=workflows");
      if (wResp.ok) {
        const wData = await wResp.json();
        const { root, map } = buildWorkflowTree(wData.items || [], wData.folders || []);
        setWorkflowTree(root); setWorkflowMap(map);
      } else { setWorkflowTree([]); setWorkflowMap({}); }
    } catch { setWorkflowTree([]); setWorkflowMap({}); }
    try {
      // ansible playbooks
      const aResp = await fetch("/api/assembly/list?island=ansible");
      if (aResp.ok) {
        const aData = await aResp.json();
        const { root, map } = buildAnsibleTree(aData.items || [], aData.folders || []);
        setAnsibleTree(root); setAnsibleMap(map);
      } else { setAnsibleTree([]); setAnsibleMap({}); }
    } catch { setAnsibleTree([]); setAnsibleMap({}); }
  };

  const addSelectedComponent = useCallback(async () => {
    const map = compTab === "scripts" ? scriptMap : (compTab === "ansible" ? ansibleMap : workflowMap);
    const node = map[selectedNodeId];
    if (!node || node.isFolder) return false;
    if (compTab === "workflows" && node.workflow) {
      alert("Workflows within Scheduled Jobs are not supported yet");
      return false;
    }
    if (compTab === "scripts" || compTab === "ansible") {
      const type = compTab === "scripts" ? "script" : "ansible";
      const rawPath = node.path || node.id || "";
      const { doc, normalizedPath } = await fetchAssemblyDoc(type, rawPath);
      const assembly = doc?.assembly || {};
      const docVars = assembly?.variables || doc?.variables || [];
      const mergedVars = mergeComponentVariables(docVars, [], {});
      setComponents((prev) => [
        ...prev,
        {
          type,
          path: normalizedPath || rawPath,
          name: assembly?.name || node.fileName || node.label,
          description: assembly?.description || normalizedPath || rawPath,
          variables: mergedVars,
          localId: generateLocalId()
        }
      ]);
      setSelectedNodeId("");
      return true;
    }
    setSelectedNodeId("");
    return false;
  }, [compTab, scriptMap, ansibleMap, workflowMap, selectedNodeId, fetchAssemblyDoc, generateLocalId]);

  const openAddTargets = async () => {
    setAddTargetOpen(true);
    setSelectedTargets({});
    try {
      const resp = await fetch("/api/agents");
      if (resp.ok) {
        const data = await resp.json();
        const list = Object.values(data || {}).map((a) => ({
          hostname: a.hostname || a.agent_hostname || a.id || "unknown",
          display: a.hostname || a.agent_hostname || a.id || "unknown",
          online: !!a.collector_active
        }));
        list.sort((a, b) => a.display.localeCompare(b.display));
        setAvailableDevices(list);
      } else {
        setAvailableDevices([]);
      }
    } catch {
      setAvailableDevices([]);
    }
  };

  const handleCreate = async () => {
    if (remoteExec && !(execContext === "winrm" && useSvcAccount) && !selectedCredentialId) {
      alert("Please select a credential for this execution context.");
      return;
    }
    const requiredErrors = {};
    components.forEach((comp) => {
      if (!comp || !comp.localId) return;
      (Array.isArray(comp.variables) ? comp.variables : []).forEach((variable) => {
        if (!variable || !variable.name || !variable.required) return;
        if ((variable.type || "string") === "boolean") return;
        const value = variable.value;
        if (value == null || value === "") {
          if (!requiredErrors[comp.localId]) requiredErrors[comp.localId] = {};
          requiredErrors[comp.localId][variable.name] = "Required";
        }
      });
    });
    if (Object.keys(requiredErrors).length) {
      setComponentVarErrors(requiredErrors);
      setTab(1);
      alert("Please fill in all required variable values.");
      return;
    }
    setComponentVarErrors({});
    const payloadComponents = sanitizeComponentsForSave(components);
    const payload = {
      name: jobName,
      components: payloadComponents,
      targets,
      schedule: { type: scheduleType, start: scheduleType !== "immediately" ? (() => { try { const d = startDateTime?.toDate?.() || new Date(startDateTime); d.setSeconds(0,0); return d.toISOString(); } catch { return startDateTime; } })() : null },
      duration: { stopAfterEnabled, expiration },
      execution_context: execContext,
      credential_id: remoteExec && !useSvcAccount && selectedCredentialId ? Number(selectedCredentialId) : null,
      use_service_account: execContext === "winrm" ? Boolean(useSvcAccount) : false
    };
    try {
      const resp = await fetch(initialJob && initialJob.id ? `/api/scheduled_jobs/${initialJob.id}` : "/api/scheduled_jobs", {
        method: initialJob && initialJob.id ? "PUT" : "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      });
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error || `HTTP ${resp.status}`);
      onCreated && onCreated(data.job || payload);
      onCancel && onCancel();
    } catch (err) {
      alert(String(err.message || err));
    }
  };

  const tabDefs = useMemo(() => {
    const base = [
      { key: "name", label: "Job Name" },
      { key: "components", label: "Assemblies" },
      { key: "targets", label: "Targets" },
      { key: "schedule", label: "Schedule" },
      { key: "context", label: "Execution Context" }
    ];
    if (editing) base.push({ key: 'history', label: 'Job History' });
    return base;
  }, [editing]);

  return (
    <Paper sx={{ m: 2, p: 0, bgcolor: "#1e1e1e", overflow: "auto" }} elevation={2}>
      <Box sx={{ p: 2, pb: 1 }}>
        <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>
          Create a Scheduled Job
          {pageTitleJobName && (
            <Box component="span" sx={{ color: "#aaa", fontSize: "inherit", fontWeight: 400 }}>
              {`: "${pageTitleJobName}"`}
            </Box>
          )}
        </Typography>
        <Typography variant="body2" sx={{ color: "#aaa" }}>
          Configure advanced schedulable automation jobs for one or more devices.
        </Typography>
      </Box>

      <Box sx={{
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        borderBottom: "1px solid #333",
        px: 2
      }}>
        <Tabs value={tab} onChange={(_, v) => setTab(v)} sx={{ minHeight: 36 }}>
          {tabDefs.map((t, i) => (
            <Tab key={t.key} label={t.label} sx={{ minHeight: 36 }} />
          ))}
        </Tabs>
        <Box sx={{ display: "flex", alignItems: "center", gap: 1, mb: 1 }}>
          <Button onClick={onCancel} sx={{ color: "#58a6ff", textTransform: "none" }}>Cancel</Button>
          <Button
            variant="outlined"
            onClick={() => (isValid ? setConfirmOpen(true) : null)}
            startIcon={<AddIcon />}
            disabled={!isValid}
            sx={{ color: isValid ? "#58a6ff" : "#666", borderColor: isValid ? "#58a6ff" : "#444", textTransform: "none" }}
          >
            {initialJob && initialJob.id ? "Save Changes" : "Create Job"}
          </Button>
        </Box>
      </Box>

      <Box sx={{ p: 2 }}>
        {tab === 0 && (
          <Box>
            <SectionHeader title="Name" />
            <TextField
              fullWidth={false}
              sx={{ width: { xs: "100%", sm: "60%", md: "50%" },
                "& .MuiOutlinedInput-root": { bgcolor: "#1b1b1b" },
                "& .MuiInputBase-input": { color: "#e6edf3" }
              }}
              placeholder="Example Job Name"
              value={jobName}
              onChange={(e) => setJobName(e.target.value)}
              onBlur={(e) => setPageTitleJobName(e.target.value.trim())}
              InputLabelProps={{ shrink: true }}
              error={jobName.trim().length === 0}
              helperText={jobName.trim().length === 0 ? "Job name is required" : ""}
            />
          </Box>
        )}

        {tab === 1 && (
          <Box>
            <SectionHeader
              title="Assemblies"
              action={(
                <Button size="small" startIcon={<AddIcon />} onClick={openAddComponent}
                  sx={{ color: "#58a6ff", borderColor: "#58a6ff" }} variant="outlined">
                  Add Assembly
                </Button>
              )}
            />
            {components.length === 0 && (
              <Typography variant="body2" sx={{ color: "#888" }}>No assemblies added yet.</Typography>
            )}
            {components.map((c) => (
              <ComponentCard
                key={c.localId || `${c.type}-${c.path}`}
                comp={c}
                onRemove={removeComponent}
                onVariableChange={updateComponentVariable}
                errors={componentVarErrors[c.localId] || {}}
              />
            ))}
            {components.length === 0 && (
              <Typography variant="caption" sx={{ color: "#ff6666" }}>At least one assembly is required.</Typography>
            )}
          </Box>
        )}

        {tab === 2 && (
          <Box>
            <SectionHeader
              title="Targets"
              action={(
                <Button size="small" startIcon={<AddIcon />} onClick={openAddTargets}
                  sx={{ color: "#58a6ff", borderColor: "#58a6ff" }} variant="outlined">
                  Add Target
                </Button>
              )}
            />
            <Table size="small">
              <TableHead>
                <TableRow>
                  <TableCell>Name</TableCell>
                  <TableCell>Status</TableCell>
                  <TableCell align="right">Actions</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {targets.map((h) => (
                  <TableRow key={h} hover>
                    <TableCell>{h}</TableCell>
                    <TableCell>—</TableCell>
                    <TableCell align="right">
                      <IconButton size="small" onClick={() => setTargets((prev) => prev.filter((x) => x !== h))} sx={{ color: "#ff6666" }}>
                        <DeleteIcon fontSize="small" />
                      </IconButton>
                    </TableCell>
                  </TableRow>
                ))}
                {targets.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={3} sx={{ color: "#888" }}>No targets selected.</TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
            {targets.length === 0 && (
              <Typography variant="caption" sx={{ color: "#ff6666" }}>At least one target is required.</Typography>
            )}
          </Box>
        )}

        {tab === 3 && (
          <Box>
            <SectionHeader title="Schedule" />
            <Box sx={{ display: "flex", gap: 2, flexWrap: "wrap" }}>
              <Box sx={{ minWidth: 260 }}>
                <Typography variant="subtitle2" sx={{ color: "#ccc", mb: 0.5 }}>Recurrence</Typography>
                <Select size="small" fullWidth value={scheduleType} onChange={(e) => setScheduleType(e.target.value)}>
                  <MenuItem value="immediately">Immediately</MenuItem>
                  <MenuItem value="once">At selected date and time</MenuItem>
                  <MenuItem value="every_5_minutes">Every 5 Minutes</MenuItem>
                  <MenuItem value="every_10_minutes">Every 10 Minutes</MenuItem>
                  <MenuItem value="every_15_minutes">Every 15 Minutes</MenuItem>
                  <MenuItem value="every_30_minutes">Every 30 Minutes</MenuItem>
                  <MenuItem value="every_hour">Every Hour</MenuItem>
                  <MenuItem value="daily">Daily</MenuItem>
                  <MenuItem value="weekly">Weekly</MenuItem>
                  <MenuItem value="monthly">Monthly</MenuItem>
                  <MenuItem value="yearly">Yearly</MenuItem>
                </Select>
              </Box>
              {(scheduleType !== "immediately") && (
                <Box sx={{ minWidth: 280 }}>
                  <Typography variant="subtitle2" sx={{ color: "#ccc", mb: 0.5 }}>Start date and execution time</Typography>
                  <LocalizationProvider dateAdapter={AdapterDayjs}>
                    <DateTimePicker
                      value={startDateTime}
                      onChange={(val) => setStartDateTime(val?.second ? val.second(0) : val)}
                      views={['year','month','day','hours','minutes']}
                      format="YYYY-MM-DD hh:mm A"
                      slotProps={{ textField: { size: "small" } }}
                    />
                  </LocalizationProvider>
                </Box>
              )}
            </Box>

            <Divider sx={{ my: 2, borderColor: "#333" }} />
            <SectionHeader title="Duration" />
            <FormControlLabel
              control={<Checkbox checked={stopAfterEnabled} onChange={(e) => setStopAfterEnabled(e.target.checked)} />}
              label={<Typography variant="body2">Stop running this job after</Typography>}
            />
            <Box sx={{ mt: 1, minWidth: 260, width: 260 }}>
              <Typography variant="subtitle2" sx={{ color: "#ccc", mb: 0.5 }}>Expiration</Typography>
              <Select size="small" fullWidth value={expiration} onChange={(e) => setExpiration(e.target.value)}>
                <MenuItem value="no_expire">Does not Expire</MenuItem>
                <MenuItem value="30m">30 Minutes</MenuItem>
                <MenuItem value="1h">1 Hour</MenuItem>
                <MenuItem value="2h">2 Hours</MenuItem>
                <MenuItem value="6h">6 Hours</MenuItem>
                <MenuItem value="12h">12 Hours</MenuItem>
                <MenuItem value="1d">1 Day</MenuItem>
                <MenuItem value="2d">2 Days</MenuItem>
                <MenuItem value="3d">3 Days</MenuItem>
              </Select>
            </Box>
          </Box>
        )}

        {tab === 4 && (
          <Box>
            <SectionHeader title="Execution Context" />
            <Select
              size="small"
              value={execContext}
              onChange={(e) => handleExecContextChange(e.target.value)}
              sx={{ minWidth: 320 }}
            >
              <MenuItem value="system">Run on agent as SYSTEM (device-local)</MenuItem>
              <MenuItem value="current_user">Run on agent as logged-in user (device-local)</MenuItem>
              <MenuItem value="ssh">Run from server via SSH (remote)</MenuItem>
              <MenuItem value="winrm">Run from server via WinRM (remote)</MenuItem>
            </Select>
            {remoteExec && (
              <Box sx={{ mt: 2, display: "flex", alignItems: "center", gap: 1.5, flexWrap: "wrap" }}>
                {execContext === "winrm" && (
                  <FormControlLabel
                    control={
                      <Checkbox
                        checked={useSvcAccount}
                        onChange={(e) => {
                          const checked = e.target.checked;
                          setUseSvcAccount(checked);
                          if (checked) {
                            setSelectedCredentialId("");
                          } else if (!selectedCredentialId && filteredCredentials.length) {
                            setSelectedCredentialId(String(filteredCredentials[0].id));
                          }
                        }}
                      />
                    }
                    label="Use Configured svcBorealis Account"
                  />
                )}
                <FormControl
                  size="small"
                  sx={{ minWidth: 320 }}
                  disabled={credentialLoading || !filteredCredentials.length || (execContext === "winrm" && useSvcAccount)}
                >
                  <InputLabel sx={{ color: "#aaa" }}>Credential</InputLabel>
                  <Select
                    value={selectedCredentialId}
                    label="Credential"
                    onChange={(e) => setSelectedCredentialId(e.target.value)}
                    sx={{ bgcolor: "#1f1f1f", color: "#fff" }}
                  >
                    {filteredCredentials.map((cred) => (
                      <MenuItem key={cred.id} value={String(cred.id)}>
                        {cred.name}
                      </MenuItem>
                    ))}
                  </Select>
                </FormControl>
                <Button
                  size="small"
                  variant="outlined"
                  startIcon={<RefreshIcon fontSize="small" />}
                  onClick={loadCredentials}
                  disabled={credentialLoading}
                  sx={{ color: "#58a6ff", borderColor: "#58a6ff" }}
                >
                  Refresh
                </Button>
                {credentialLoading && <CircularProgress size={18} sx={{ color: "#58a6ff" }} />}
                {!credentialLoading && credentialError && (
                  <Typography variant="body2" sx={{ color: "#ff8080" }}>
                    {credentialError}
                  </Typography>
                )}
                {execContext === "winrm" && useSvcAccount && (
                  <Typography variant="body2" sx={{ color: "#aaa" }}>
                    Runs with the agent&apos;s svcBorealis account.
                  </Typography>
                )}
                {!credentialLoading && !credentialError && !filteredCredentials.length && (!(execContext === "winrm" && useSvcAccount)) && (
                  <Typography variant="body2" sx={{ color: "#ff8080" }}>
                    No {execContext === "winrm" ? "WinRM" : "SSH"} credentials available. Create one under Access Management &gt; Credentials.
                  </Typography>
                )}
              </Box>
            )}
          </Box>
        )}

        {/* Job History tab (only when editing) */}
        {editing && tab === tabDefs.findIndex(t => t.key === 'history') && (
          <Box>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <Typography variant="h6" sx={{ color: '#58a6ff' }}>Job History</Typography>
              <Button size="small" variant="outlined" sx={{ color: '#ff6666', borderColor: '#ff6666', textTransform: 'none' }}
                onClick={async () => { try { await fetch(`/api/scheduled_jobs/${initialJob.id}/runs`, { method: 'DELETE' }); await loadHistory(); } catch {} }}
              >
                Clear Job History
              </Button>
            </Box>
            <Typography variant="caption" sx={{ color: '#aaa' }}>Showing the last 30 days of runs.</Typography>

            <Box sx={{ mt: 2 }}>
              <JobStatusFlow />
            </Box>

            <Box sx={{ mt: 2 }}>
              <Typography variant="subtitle1" sx={{ color: '#7db7ff', mb: 0.5 }}>Devices</Typography>
              <Typography variant="caption" sx={{ color: '#aaa' }}>Devices targeted by this scheduled job.  Individual job history is listed here.</Typography>
              <Table size="small">
                <TableHead>
                  <TableRow>
                    {DEVICE_COLUMNS.map((col) => (
                      <TableCell key={col.key}>
                        <Box sx={{ display: "flex", alignItems: "center", gap: 0.5 }}>
                          <TableSortLabel
                            active={deviceOrderBy === col.key}
                            direction={deviceOrderBy === col.key ? deviceOrder : "asc"}
                            onClick={() => handleDeviceSort(col.key)}
                          >
                            {col.label}
                          </TableSortLabel>
                          <IconButton
                            size="small"
                            onClick={(event) => openFilterMenu(event, col.key)}
                            sx={{ color: isColumnFiltered(col.key) ? "#58a6ff" : "#666" }}
                          >
                            <FilterListIcon fontSize="inherit" />
                          </IconButton>
                        </Box>
                      </TableCell>
                    ))}
                  </TableRow>
                </TableHead>
                <TableBody>
                  {deviceSorted.map((d, i) => (
                    <TableRow key={`${d.hostname}-${i}`} hover>
                      <TableCell>{d.hostname}</TableCell>
                      <TableCell>
                        <span style={{ display:'inline-block', width:10, height:10, borderRadius:10, background: d.online ? '#00d18c' : '#ff4f4f', marginRight:8, verticalAlign:'middle' }} />
                        {d.online ? 'Online' : 'Offline'}
                      </TableCell>
                      <TableCell>{d.site || ''}</TableCell>
                      <TableCell>{fmtTs(d.ran_on)}</TableCell>
                      <TableCell>{resultChip(d.job_status)}</TableCell>
                      <TableCell>
                        <Box sx={{ display:'flex', gap:1 }}>
                          {d.has_stdout ? (
                            <Button
                              size="small"
                              sx={{ color:'#58a6ff', textTransform:'none', minWidth:0, p:0 }}
                              onClick={(e) => { e.stopPropagation(); handleViewDeviceOutput(d, 'stdout'); }}
                            >
                              StdOut
                            </Button>
                          ) : null}
                          {d.has_stderr ? (
                            <Button
                              size="small"
                              sx={{ color:'#ff4f4f', textTransform:'none', minWidth:0, p:0 }}
                              onClick={(e) => { e.stopPropagation(); handleViewDeviceOutput(d, 'stderr'); }}
                            >
                              StdErr
                            </Button>
                          ) : null}
                        </Box>
                      </TableCell>
                    </TableRow>
                  ))}
                  {deviceSorted.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={6} sx={{ color:'#888' }}>No targets found for this job.</TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
              <Menu
                anchorEl={filterAnchorEl}
                open={Boolean(filterAnchorEl)}
                onClose={closeFilterMenu}
                disableAutoFocusItem
                PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#e6edf3", minWidth: 240 } }}
              >
                <Box sx={{ p: 1.5, display: "flex", flexDirection: "column", gap: 1 }}>
                  {renderFilterControl()}
                  <Box sx={{ display: "flex", justifyContent: "space-between", gap: 1 }}>
                    <Button size="small" sx={{ color: "#ff6666", textTransform: "none" }} onClick={clearFilter}>
                      Clear
                    </Button>
                    <Button size="small" sx={{ color: "#58a6ff", textTransform: "none" }} onClick={applyFilter}>
                      Apply
                    </Button>
                  </Box>
                </Box>
              </Menu>
            </Box>

            <Box sx={{ mt: 2 }}>
              <Typography variant="subtitle1" sx={{ color: '#7db7ff', mb: 0.5 }}>Past Job History</Typography>
              <Typography variant="caption" sx={{ color: '#aaa' }}>Historical job history summaries.  Detailed job history is not recorded.</Typography>
              <Box sx={{ mt: 1 }}>
                {renderHistory()}
              </Box>
            </Box>
          </Box>
        )}
      </Box>

      <Dialog open={outputOpen} onClose={() => setOutputOpen(false)} fullWidth maxWidth="md"
        PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}
      >
        <DialogTitle>{outputTitle}</DialogTitle>
        <DialogContent dividers>
          {outputLoading ? (
            <Typography variant="body2" sx={{ color: "#888" }}>Loading output…</Typography>
          ) : null}
          {!outputLoading && outputError ? (
            <Typography variant="body2" sx={{ color: "#888" }}>{outputError}</Typography>
          ) : null}
          {!outputLoading && !outputError ? (
            outputSections.map((section) => (
              <Box key={section.key} sx={{ mb: 2 }}>
                <Typography variant="subtitle2" sx={{ color: "#7db7ff" }}>{section.title}</Typography>
                {section.path ? (
                  <Typography variant="caption" sx={{ color: "#888", display: "block", mb: 0.5 }}>{section.path}</Typography>
                ) : null}
                <Box sx={{ border: "1px solid #333", borderRadius: 1, bgcolor: "#1e1e1e" }}>
                  <Editor
                    value={section.content ?? ""}
                    onValueChange={() => {}}
                    highlight={(code) => highlightCode(code, section.lang)}
                    padding={12}
                    style={{
                      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
                      fontSize: 12,
                      color: "#e6edf3",
                      minHeight: 160
                    }}
                    textareaProps={{ readOnly: true }}
                  />
                </Box>
              </Box>
            ))
          ) : null}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setOutputOpen(false)} sx={{ color: "#58a6ff" }}>Close</Button>
        </DialogActions>
      </Dialog>

      {/* Bottom actions removed per design; actions live next to tabs. */}

      {/* Add Component Dialog */}
      <Dialog open={addCompOpen} onClose={() => setAddCompOpen(false)} fullWidth maxWidth="md"
        PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}
      >
        <DialogTitle>Select an Assembly</DialogTitle>
        <DialogContent>
          <Box sx={{ display: "flex", gap: 2, mb: 1 }}>
            <Button size="small" variant={compTab === "scripts" ? "outlined" : "text"} onClick={() => setCompTab("scripts")}
              sx={{ textTransform: "none", color: "#58a6ff", borderColor: "#58a6ff" }}>
              Scripts
            </Button>
            <Button size="small" variant={compTab === "ansible" ? "outlined" : "text"} onClick={() => setCompTab("ansible")}
              sx={{ textTransform: "none", color: "#58a6ff", borderColor: "#58a6ff" }}>
              Ansible
            </Button>
            <Button size="small" variant={compTab === "workflows" ? "outlined" : "text"} onClick={() => setCompTab("workflows")}
              sx={{ textTransform: "none", color: "#58a6ff", borderColor: "#58a6ff" }}>
              Workflows
            </Button>
          </Box>
          {compTab === "scripts" && (
            <Paper sx={{ p: 1, bgcolor: "#1e1e1e", maxHeight: 400, overflow: "auto" }}>
              <SimpleTreeView onItemSelectionToggle={(_, id) => {
                  const n = scriptMap[id];
                  if (n && !n.isFolder) setSelectedNodeId(id);
                }}>
                {scriptTree.length ? (scriptTree.map((n) => (
                  <TreeItem key={n.id} itemId={n.id} label={n.label}>
                    {n.children && n.children.length ? renderTreeNodes(n.children, scriptMap) : null}
                  </TreeItem>
                ))) : (
                  <Typography variant="body2" sx={{ color: "#888", p: 1 }}>No scripts found.</Typography>
                )}
              </SimpleTreeView>
            </Paper>
          )}
          {compTab === "workflows" && (
            <Paper sx={{ p: 1, bgcolor: "#1e1e1e", maxHeight: 400, overflow: "auto" }}>
              <SimpleTreeView onItemSelectionToggle={(_, id) => {
                  const n = workflowMap[id];
                  if (n && !n.isFolder) setSelectedNodeId(id);
                }}>
                {workflowTree.length ? (workflowTree.map((n) => (
                  <TreeItem key={n.id} itemId={n.id} label={n.label}>
                    {n.children && n.children.length ? renderTreeNodes(n.children, workflowMap) : null}
                  </TreeItem>
                ))) : (
                  <Typography variant="body2" sx={{ color: "#888", p: 1 }}>No workflows found.</Typography>
                )}
              </SimpleTreeView>
            </Paper>
          )}
          {compTab === "ansible" && (
            <Paper sx={{ p: 1, bgcolor: "#1e1e1e", maxHeight: 400, overflow: "auto" }}>
              <SimpleTreeView onItemSelectionToggle={(_, id) => {
                  const n = ansibleMap[id];
                  if (n && !n.isFolder) setSelectedNodeId(id);
                }}>
                {ansibleTree.length ? (ansibleTree.map((n) => (
                  <TreeItem key={n.id} itemId={n.id} label={n.label}>
                    {n.children && n.children.length ? renderTreeNodes(n.children, ansibleMap) : null}
                  </TreeItem>
                ))) : (
                  <Typography variant="body2" sx={{ color: "#888", p: 1 }}>No playbooks found.</Typography>
                )}
              </SimpleTreeView>
            </Paper>
          )}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setAddCompOpen(false)} sx={{ color: "#58a6ff" }}>Close</Button>
          <Button onClick={async () => { const ok = await addSelectedComponent(); if (ok) setAddCompOpen(false); }}
            sx={{ color: "#58a6ff" }} disabled={!selectedNodeId}
          >Add</Button>
        </DialogActions>
      </Dialog>

      {/* Add Targets Dialog */}
      <Dialog open={addTargetOpen} onClose={() => setAddTargetOpen(false)} fullWidth maxWidth="md"
        PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}
      >
        <DialogTitle>Select Targets</DialogTitle>
        <DialogContent>
          <Box sx={{ mb: 2, display: "flex", gap: 2 }}>
            <TextField
              size="small"
              placeholder="Search devices..."
              value={deviceSearch}
              onChange={(e) => setDeviceSearch(e.target.value)}
              sx={{ flex: 1, "& .MuiOutlinedInput-root": { bgcolor: "#1b1b1b" }, "& .MuiInputBase-input": { color: "#e6edf3" } }}
            />
          </Box>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell width={40}></TableCell>
                <TableCell>Name</TableCell>
                <TableCell>Status</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {availableDevices
                .filter((d) => d.display.toLowerCase().includes(deviceSearch.toLowerCase()))
                .map((d) => (
                <TableRow key={d.hostname} hover onClick={() => setSelectedTargets((prev) => ({ ...prev, [d.hostname]: !prev[d.hostname] }))}>
                  <TableCell>
                    <Checkbox size="small" checked={!!selectedTargets[d.hostname]}
                      onChange={(e) => setSelectedTargets((prev) => ({ ...prev, [d.hostname]: e.target.checked }))}
                    />
                  </TableCell>
                  <TableCell>{d.display}</TableCell>
                  <TableCell>
                    <span style={{ display: "inline-block", width: 10, height: 10, borderRadius: 10, background: d.online ? "#00d18c" : "#ff4f4f", marginRight: 8, verticalAlign: "middle" }} />
                    {d.online ? "Online" : "Offline"}
                  </TableCell>
                </TableRow>
              ))}
              {availableDevices.length === 0 && (
                <TableRow><TableCell colSpan={3} sx={{ color: "#888" }}>No devices available.</TableCell></TableRow>
              )}
            </TableBody>
          </Table>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setAddTargetOpen(false)} sx={{ color: "#58a6ff" }}>Cancel</Button>
          <Button onClick={() => {
            const chosen = Object.keys(selectedTargets).filter((h) => selectedTargets[h]);
            setTargets((prev) => Array.from(new Set([...prev, ...chosen])));
            setAddTargetOpen(false);
          }} sx={{ color: "#58a6ff" }}>Add Selected</Button>
        </DialogActions>
      </Dialog>

      {/* Confirm Create Dialog */}
      <Dialog open={confirmOpen} onClose={() => setConfirmOpen(false)}
        PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}>
        <DialogTitle sx={{ pb: 0 }}>{initialJob && initialJob.id ? "Are you sure you wish to save changes?" : "Are you sure you wish to create this Job?"}</DialogTitle>
        <DialogActions sx={{ p: 2 }}>
          <Button onClick={() => setConfirmOpen(false)} sx={{ color: "#58a6ff" }}>Cancel</Button>
          <Button onClick={() => { setConfirmOpen(false); handleCreate(); }}
            variant="outlined" sx={{ color: "#58a6ff", borderColor: "#58a6ff" }}>
            Confirm
          </Button>
        </DialogActions>
      </Dialog>
    </Paper>
  );
}
