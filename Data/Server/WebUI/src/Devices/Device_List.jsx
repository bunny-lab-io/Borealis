////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Device_List.jsx

import React, { useState, useEffect, useCallback, useMemo, useRef } from "react";
import {
  Paper,
  Box,
  Typography,
  Button,
  IconButton,
  Menu,
  MenuItem,
  Popover,
  TextField,
  Tooltip,
  Checkbox,
} from "@mui/material";
import MoreVertIcon from "@mui/icons-material/MoreVert";
import ViewColumnIcon from "@mui/icons-material/ViewColumn";
import AddIcon from "@mui/icons-material/Add";
import CachedIcon from "@mui/icons-material/Cached";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import { DeleteDeviceDialog, CreateCustomViewDialog, RenameCustomViewDialog } from "../Dialogs.jsx";
import QuickJob from "../Scheduling/Quick_Job.jsx";
import AddDevice from "./Add_Device.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const myTheme = themeQuartz.withParams({
  accentColor: "#FFA6FF",
  backgroundColor: "#1f2836",
  browserColorScheme: "dark",
  chromeBackgroundColor: {
    ref: "foregroundColor",
    mix: 0.07,
    onto: "backgroundColor",
  },
  fontFamily: {
    googleFont: "IBM Plex Sans",
  },
  foregroundColor: "#FFF",
  headerFontSize: 14,
});

const themeClassName = myTheme.themeName || "ag-theme-quartz";
const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';

const getOsIconClass = (osName) => {
  const value = (osName || "").toString().toLowerCase();
  if (!value) return "";

  if (value.includes("mac") || value.includes("os x") || value.includes("darwin")) {
    return "fa-brands fa-apple";
  }

  if (value.includes("win")) {
    return "fa-brands fa-windows";
  }

  if (
    value.includes("linux") ||
    value.includes("ubuntu") ||
    value.includes("debian") ||
    value.includes("fedora") ||
    value.includes("red hat") ||
    value.includes("centos") ||
    value.includes("suse") ||
    value.includes("rhel")
  ) {
    return "fa-brands fa-linux";
  }

  return "";
};

const DescriptionCellRenderer = React.memo(function DescriptionCellRenderer(props) {
  const { value, data, onSaveDescription, fontFamily } = props;
  const safeValue = typeof value === "string" ? value : value == null ? "" : String(value);
  const [draft, setDraft] = useState(safeValue);
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!editing && !saving) {
      setDraft(safeValue);
    }
  }, [safeValue, editing, saving]);

  const handleFocus = useCallback((event) => {
    event.stopPropagation();
    setEditing(true);
    setError("");
  }, []);

  const handleChange = useCallback((event) => {
    setDraft(event.target.value);
  }, []);

  const handleKeyDown = useCallback(
    async (event) => {
      event.stopPropagation();
      if (event.key === "Enter") {
        event.preventDefault();
        const trimmed = (draft || "").trim();
        if (trimmed === safeValue.trim()) {
          setEditing(false);
          setDraft(safeValue);
          setError("");
          return;
        }
        if (typeof onSaveDescription !== "function" || !data) {
          setEditing(false);
          setError("");
          return;
        }
        setSaving(true);
        setError("");
        const ok = await onSaveDescription(data, trimmed);
        setSaving(false);
        if (ok) {
          setEditing(false);
        } else {
          setError("Failed to save description");
        }
      } else if (event.key === "Escape") {
        event.preventDefault();
        setDraft(safeValue);
        setEditing(false);
        setError("");
      }
    },
    [data, draft, onSaveDescription, safeValue]
  );

  const handleBlur = useCallback(
    (event) => {
      event.stopPropagation();
      if (saving) return;
      setEditing(false);
      setDraft(safeValue);
      setError("");
    },
    [saving, safeValue]
  );

  const stopPropagation = useCallback((event) => {
    event.stopPropagation();
  }, []);

  const backgroundColor = saving
    ? "rgba(255,255,255,0.04)"
    : editing
    ? "rgba(255,255,255,0.16)"
    : "rgba(255,255,255,0.02)";

  return (
    <TextField
      value={draft}
      onFocus={handleFocus}
      onChange={handleChange}
      onKeyDown={handleKeyDown}
      onBlur={handleBlur}
      onClick={stopPropagation}
      onMouseDown={stopPropagation}
      variant="outlined"
      size="small"
      fullWidth
      disabled={saving}
      error={Boolean(error)}
      helperText={error || undefined}
      FormHelperTextProps={
        error
          ? { sx: { minHeight: 18, fontSize: "0.75rem" } }
          : { sx: { display: "none" } }
      }
      sx={{
        mt: 0.5,
        mb: 0.5,
        '& .MuiOutlinedInput-root': {
          backgroundColor,
          transition: "background-color 0.2s ease, border-color 0.2s ease",
          color: "rgba(255,255,255,0.85)",
          fontFamily: fontFamily || gridFontFamily,
          fontSize: "0.875rem",
          height: 34,
          py: 0,
          pr: 0,
          '& fieldset': {
            borderColor: editing ? "#FFA6FF" : "rgba(255,255,255,0.25)",
          },
          '&:hover fieldset': {
            borderColor: "#FFA6FF",
          },
          '&.Mui-focused fieldset': {
            borderColor: "#FFA6FF",
          },
          '&.Mui-disabled': {
            backgroundColor: "rgba(255,255,255,0.08)",
          },
        },
        '& .MuiOutlinedInput-input': {
          color: "rgba(255,255,255,0.85)",
          py: 0.75,
          px: 1.5,
        },
        '& .MuiFormHelperText-root': {
          color: "#ff7b7b",
          mt: 0.25,
        },
      }}
      inputProps={{
        sx: {
          textOverflow: "ellipsis",
        },
      }}
    />
  );
});

function formatLastSeen(tsSec, offlineAfter = 300) {
  if (!tsSec) return "unknown";
  const now = Date.now() / 1000;
  if (now - tsSec <= offlineAfter) return "Currently Online";
  const d = new Date(tsSec * 1000);
  const date = d.toLocaleDateString("en-US", {
    month: "2-digit",
    day: "2-digit",
    year: "numeric",
  });
  const time = d.toLocaleTimeString("en-US", {
    hour: "numeric",
    minute: "2-digit",
  });
  return `${date} @ ${time}`;
}

function statusFromHeartbeat(tsSec, offlineAfter = 300) {
  if (!tsSec) return "Offline";
  const now = Date.now() / 1000;
  return now - tsSec <= offlineAfter ? "Online" : "Offline";
}

function formatUptime(seconds) {
  const total = Number(seconds);
  if (!Number.isFinite(total) || total <= 0) return "";
  const parts = [];
  const days = Math.floor(total / 86400);
  if (days) parts.push(`${days}d`);
  const hours = Math.floor((total % 86400) / 3600);
  if (hours) parts.push(`${hours}h`);
  const minutes = Math.floor((total % 3600) / 60);
  if (minutes) parts.push(`${minutes}m`);
  const secondsPart = Math.floor(total % 60);
  if (!parts.length && secondsPart) parts.push(`${secondsPart}s`);
  return parts.join(' ');
}

export default function DeviceList({
  onSelectDevice,
  filterMode = "all",
  title,
  showAddButton,
  addButtonLabel,
  defaultAddType,
}) {
  const [rows, setRows] = useState([]);
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [selected, setSelected] = useState(null);
  const [confirmOpen, setConfirmOpen] = useState(false);
  // Track selection by agent id to avoid duplicate hostname collisions
  const [selectedIds, setSelectedIds] = useState(() => new Set());
  const [quickJobOpen, setQuickJobOpen] = useState(false);
  const [addDeviceOpen, setAddDeviceOpen] = useState(false);
  const [addDeviceType, setAddDeviceType] = useState(null);
  const computedTitle = useMemo(() => {
    if (title) return title;
    switch (filterMode) {
      case "agent":
        return "Agent Devices";
      case "ssh":
        return "SSH Devices";
      case "winrm":
        return "WinRM Devices";
      default:
        return "Device Inventory";
    }
  }, [filterMode, title]);
  const derivedDefaultType = useMemo(() => {
    if (defaultAddType !== undefined) return defaultAddType;
    if (filterMode === "ssh" || filterMode === "winrm") return filterMode;
    return null;
  }, [defaultAddType, filterMode]);
  const derivedAddLabel = useMemo(() => {
    if (addButtonLabel) return addButtonLabel;
    if (filterMode === "ssh") return "Add SSH Device";
    if (filterMode === "winrm") return "Add WinRM Device";
    return "Add Device";
  }, [addButtonLabel, filterMode]);
  const derivedShowAddButton = useMemo(() => {
    if (typeof showAddButton === "boolean") return showAddButton;
    return filterMode !== "agent";
  }, [showAddButton, filterMode]);

  // Saved custom views (from server)
  const [views, setViews] = useState([]); // [{id, name, columns:[id], filters:{}}]
  const [selectedViewId, setSelectedViewId] = useState("default");
  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [newViewName, setNewViewName] = useState("");
  const [renameDialogOpen, setRenameDialogOpen] = useState(false);
  const [renameViewName, setRenameViewName] = useState("");
  const [renameTarget, setRenameTarget] = useState(null); // {id, name}
  const [viewActionAnchor, setViewActionAnchor] = useState(null); // anchor for per-item actions
  const [viewActionTarget, setViewActionTarget] = useState(null); // view object for actions

  // Column configuration and rearranging state
  const COL_LABELS = useMemo(
    () => ({
      status: "Status",
      agentVersion: "Agent Version",
      site: "Site",
      hostname: "Hostname",
      description: "Description",
      lastUser: "Last User",
      type: "Type",
      os: "OS",
      internalIp: "Internal IP",
      externalIp: "External IP",
      lastReboot: "Last Reboot",
      created: "Created",
      lastSeen: "Last Seen",
      agentId: "Agent ID",
      agentHash: "Agent Hash",
      agentGuid: "Agent GUID",
      domain: "Domain",
      uptime: "Uptime",
      memory: "Memory",
      network: "Network",
      software: "Software",
      storage: "Storage",
      cpu: "CPU",
      siteDescription: "Site Description",
    }),
    []
  );

  const defaultColumns = useMemo(
    () => [
      { id: "status", label: COL_LABELS.status },
      { id: "agentVersion", label: COL_LABELS.agentVersion },
      { id: "site", label: COL_LABELS.site },
      { id: "hostname", label: COL_LABELS.hostname },
      { id: "description", label: COL_LABELS.description },
      { id: "lastUser", label: COL_LABELS.lastUser },
      { id: "type", label: COL_LABELS.type },
      { id: "os", label: COL_LABELS.os },
    ],
    [COL_LABELS]
  );
  const [columns, setColumns] = useState(defaultColumns);
  const [colChooserAnchor, setColChooserAnchor] = useState(null);
  const gridRef = useRef(null);

  // Per-column filters
  const [filtersState, setFiltersState] = useState({});

  const sanitizeFilterModel = useCallback((raw) => {
    if (!raw || typeof raw !== "object") return {};
    const sanitized = {};
    Object.entries(raw).forEach(([key, value]) => {
      if (typeof value === "string") {
        const trimmed = value.trim();
        if (trimmed) {
          sanitized[key] = {
            filterType: "text",
            type: "contains",
            filter: trimmed,
          };
        }
        return;
      }
      if (!value || typeof value !== "object") return;
      const clone = JSON.parse(JSON.stringify(value));
      if (!clone.filterType) clone.filterType = "text";
      if (clone.filterType === "text") {
        if (typeof clone.filter === "string") {
          clone.filter = clone.filter.trim();
        }
        if (Array.isArray(clone.conditions)) {
          clone.conditions = clone.conditions
            .map((condition) => {
              if (!condition || typeof condition !== "object") return null;
              const condClone = { ...condition };
              if (typeof condClone.filter === "string") {
                condClone.filter = condClone.filter.trim();
              }
              if (
                !condClone.filter &&
                !["blank", "notBlank"].includes(condClone.type ?? "")
              ) {
                return null;
              }
              return condClone;
            })
            .filter(Boolean);
          if (!clone.conditions.length) {
            delete clone.conditions;
          }
        }
        if (
          !clone.filter &&
          !clone.conditions &&
          !["blank", "notBlank"].includes(clone.type ?? "")
        ) {
          return;
        }
      }
      sanitized[key] = clone;
    });
    return sanitized;
  }, []);

  const filterModelsEqual = useCallback(
    (a, b) => JSON.stringify(a ?? {}) === JSON.stringify(b ?? {}),
    []
  );

  const replaceFilters = useCallback(
    (raw) => {
      const sanitized =
        raw && typeof raw === "object" ? sanitizeFilterModel(raw) : {};
      setFiltersState((prev) =>
        filterModelsEqual(prev, sanitized) ? prev : sanitized
      );
    },
    [filterModelsEqual, sanitizeFilterModel]
  );

  const mergeFilters = useCallback(
    (raw) => {
      if (!raw || typeof raw !== "object") return;
      const sanitized = sanitizeFilterModel(raw);
      if (!Object.keys(sanitized).length) return;
      setFiltersState((prev) => {
        const base = prev || {};
        const next = { ...base };
        let changed = false;
        Object.entries(sanitized).forEach(([key, value]) => {
          if (!value) return;
          if (!next[key] || !filterModelsEqual(next[key], value)) {
            next[key] = value;
            changed = true;
          }
        });
        return changed ? next : base;
      });
    },
    [filterModelsEqual, sanitizeFilterModel]
  );

  const filters = filtersState;

  const [sites, setSites] = useState([]); // sites list for assignment
  const [assignDialogOpen, setAssignDialogOpen] = useState(false);
  const [assignSiteId, setAssignSiteId] = useState(null);
  const [assignTargets, setAssignTargets] = useState([]); // hostnames

  const [repoHash, setRepoHash] = useState(null);
  const lastRepoFetchRef = useRef(0);

  const gridWrapperClass = themeClassName;

  const fetchLatestRepoHash = useCallback(async (options = {}) => {
    const { force = false } = options || {};
    const now = Date.now();
    const elapsed = now - lastRepoFetchRef.current;
    if (!force && repoHash && elapsed >= 0 && elapsed < 60_000) {
      return repoHash;
    }
    try {
      const params = new URLSearchParams({ repo: "bunny-lab-io/Borealis", branch: "main" });
      if (force) {
        params.set("refresh", "1");
      }
      const resp = await fetch(`/api/repo/current_hash?${params.toString()}`);
      const json = await resp.json();
      const sha = (json?.sha || "").trim();
      if (!resp.ok || !sha) {
        const err = new Error(`Latest hash status ${resp.status}${json?.error ? ` - ${json.error}` : ""}`);
        err.response = json;
        throw err;
      }
      lastRepoFetchRef.current = now;
      setRepoHash((prev) => (sha ? sha : prev || null));
      return sha || null;
    } catch (err) {
      console.warn("Failed to fetch repository hash", err);
      if (!force && repoHash) {
        return repoHash;
      }
      lastRepoFetchRef.current = now;
      setRepoHash((prev) => prev || null);
      return null;
    }
  }, [repoHash]);

  const computeAgentVersion = useCallback((agentHashValue, repoHashValue) => {
    const agentHash = (agentHashValue || "").trim();
    const repo = (repoHashValue || "").trim();
    if (!repo) return agentHash ? "Unknown" : "Unknown";
    if (!agentHash) return "Needs Updated";
    return agentHash === repo ? "Up-to-Date" : "Needs Updated";
  }, []);

  const fetchDevices = useCallback(async (options = {}) => {
    const { refreshRepo = false } = options || {};
    let repoSha = repoHash;
    if (refreshRepo || !repoSha) {
      const fetched = await fetchLatestRepoHash({ force: refreshRepo });
      if (fetched) repoSha = fetched;
    }

    const hashById = new Map();
    const hashByGuid = new Map();
    const hashByHost = new Map();
    try {
      const hashResp = await fetch('/api/agent/hash_list');
      if (hashResp.ok) {
        const hashJson = await hashResp.json();
        const list = Array.isArray(hashJson?.agents) ? hashJson.agents : [];
        list.forEach((rec) => {
          if (!rec || typeof rec !== 'object') return;
          const hash = (rec.agent_hash || '').trim();
          if (!hash) return;
          const agentId = (rec.agent_id || '').trim();
          const guidRaw = (rec.agent_guid || '').trim().toLowerCase();
          const hostKey = (rec.hostname || '').trim().toLowerCase();
          const isMemory = (rec.source || '').trim() === 'memory';
          if (agentId && (!hashById.has(agentId) || isMemory)) {
            hashById.set(agentId, hash);
          }
          if (guidRaw && (!hashByGuid.has(guidRaw) || isMemory)) {
            hashByGuid.set(guidRaw, hash);
          }
          if (hostKey && (!hashByHost.has(hostKey) || isMemory)) {
            hashByHost.set(hostKey, hash);
          }
        });
      }
    } catch (err) {
      console.warn('Failed to fetch agent hash list', err);
    }

    try {
      const res = await fetch('/api/devices');
      if (!res.ok) {
        const err = new Error(`Failed to fetch devices (${res.status})`);
        try {
          err.response = await res.json();
        } catch {}
        throw err;
      }
      const payload = await res.json();
      const list = Array.isArray(payload?.devices) ? payload.devices : [];

      const normalizeJson = (value) => {
        if (!value) return '';
        try {
          return JSON.stringify(value);
        } catch {
          return '';
        }
      };

      const normalized = list.map((device, index) => {
        const summary = device && typeof device.summary === 'object' ? { ...device.summary } : {};
        const rawHostname = (device.hostname || summary.hostname || '').trim();
        const hostname = rawHostname || `device-${index + 1}`;
        const agentId = (device.agent_id || summary.agent_id || '').trim();
        const guidRaw = (device.agent_guid || summary.agent_guid || '').trim();
        const guidLookupKey = guidRaw.toLowerCase();
        const rowKey = guidRaw || agentId || hostname || `device-${index + 1}`;
        let agentHash = (device.agent_hash || summary.agent_hash || '').trim();
        if (agentId && hashById.has(agentId)) agentHash = hashById.get(agentId) || agentHash;
        if (!agentHash && guidLookupKey && hashByGuid.has(guidLookupKey)) {
          agentHash = hashByGuid.get(guidLookupKey) || agentHash;
        }
        const hostKey = hostname.trim().toLowerCase();
        if (!agentHash && hostKey && hashByHost.has(hostKey)) {
          agentHash = hashByHost.get(hostKey) || agentHash;
        }
        const lastSeen = Number(device.last_seen || summary.last_seen || 0) || 0;
        const status = device.status || statusFromHeartbeat(lastSeen);

        if (guidRaw && !summary.agent_guid) {
          summary.agent_guid = guidRaw;
        }

        let createdTs = Number(device.created_at || 0) || 0;
        let createdDisplay = summary.created || '';
        if (!createdTs && createdDisplay) {
          const parsed = Date.parse(createdDisplay.replace(' ', 'T'));
          if (!Number.isNaN(parsed)) createdTs = Math.floor(parsed / 1000);
        }
        if (!createdDisplay && device.created_at_iso) {
          try {
            createdDisplay = new Date(device.created_at_iso).toLocaleString();
          } catch {}
        }

        const osName =
          device.operating_system ||
          summary.operating_system ||
          summary.agent_operating_system ||
          "-";
        const type = (device.device_type || summary.device_type || '').trim();
        const lastUser = (device.last_user || summary.last_user || '').trim();
        const domain = (device.domain || summary.domain || '').trim();
        const internalIp = (device.internal_ip || summary.internal_ip || '').trim();
        const externalIp = (device.external_ip || summary.external_ip || '').trim();
        const lastReboot = (device.last_reboot || summary.last_reboot || '').trim();
        const uptimeSeconds = Number(
          device.uptime ||
            summary.uptime_sec ||
            summary.uptime_seconds ||
            summary.uptime ||
            0
        ) || 0;
        const connectionType = (device.connection_type || summary.connection_type || '').trim().toLowerCase();
        const connectionLabel = connectionType === 'ssh' ? 'SSH' : connectionType === 'winrm' ? 'WinRM' : '';
        const connectionEndpoint = (device.connection_endpoint || summary.connection_endpoint || '').trim();

        const memoryList = Array.isArray(device.memory) ? device.memory : [];
        const networkList = Array.isArray(device.network) ? device.network : [];
        const softwareList = Array.isArray(device.software) ? device.software : [];
        const storageList = Array.isArray(device.storage) ? device.storage : [];
        const cpuObj =
          (device.cpu && typeof device.cpu === 'object' && device.cpu) ||
          (summary.cpu && typeof summary.cpu === 'object' ? summary.cpu : {});

        const memoryDisplay = memoryList.length ? `${memoryList.length} module(s)` : '';
        const networkDisplay = networkList.length ? networkList.map((n) => n.adapter || n.name || '').filter(Boolean).join(', ') : '';
        const softwareDisplay = softwareList.length ? `${softwareList.length} item(s)` : '';
        const storageDisplay = storageList.length ? `${storageList.length} volume(s)` : '';
        const cpuDisplay = cpuObj.name || summary.processor || '';

        return {
          id: rowKey,
          hostname,
          status,
          lastSeen,
          lastSeenDisplay: formatLastSeen(lastSeen),
          os: osName,
          lastUser,
          type: type || connectionLabel || '',
          site: device.site_name || 'Not Configured',
          siteId: device.site_id || null,
          siteDescription: device.site_description || '',
          description: (device.description || summary.description || '').trim(),
          created: createdDisplay,
          createdTs,
          createdIso: device.created_at_iso || '',
          agentGuid: guidRaw,
          agentHash,
          agentVersion: computeAgentVersion(agentHash, repoSha),
          agentId,
          domain,
          internalIp,
          externalIp,
          lastReboot,
          uptime: uptimeSeconds,
          uptimeDisplay: formatUptime(uptimeSeconds),
          memory: memoryDisplay,
          memoryRaw: normalizeJson(memoryList),
          network: networkDisplay,
          networkRaw: normalizeJson(networkList),
          software: softwareDisplay,
          softwareRaw: normalizeJson(softwareList),
          storage: storageDisplay,
          storageRaw: normalizeJson(storageList),
          cpu: cpuDisplay,
          cpuRaw: normalizeJson(cpuObj),
          summary,
          details: device.details || {},
          connectionType,
          connectionLabel,
          connectionEndpoint,
          isRemote: Boolean(connectionLabel),
        };
      });

      let filtered = normalized;
      if (filterMode === "agent") {
        filtered = normalized.filter((row) => !row.connectionType);
      } else if (filterMode === "ssh") {
        filtered = normalized.filter((row) => row.connectionType === "ssh");
      } else if (filterMode === "winrm") {
        filtered = normalized.filter((row) => row.connectionType === "winrm");
      }

      setRows(filtered);
    } catch (e) {
      console.warn('Failed to load devices:', e);
      setRows([]);
    }
  }, [repoHash, fetchLatestRepoHash, computeAgentVersion, filterMode]);

  const fetchViews = useCallback(async () => {
    try {
      const res = await fetch("/api/device_list_views");
      const data = await res.json();
      if (data && Array.isArray(data.views)) setViews(data.views);
      else setViews([]);
    } catch {
      setViews([]);
    }
  }, []);

  useEffect(() => {
    // Initial load only; removed auto-refresh interval
    fetchDevices({ refreshRepo: true });
  }, [fetchDevices]);

  useEffect(() => {
    fetchViews();
  }, [fetchViews]);

  // Sites helper fetch
  const fetchSites = useCallback(async () => {
    try {
      const res = await fetch('/api/sites');
      const data = await res.json();
      setSites(Array.isArray(data?.sites) ? data.sites : []);
    } catch { setSites([]); }
  }, []);

  // Apply initial site filter from Sites page
  useEffect(() => {
    try {
      // General initial filters (set by global search)
      const json = localStorage.getItem('device_list_initial_filters');
      if (json) {
        const obj = JSON.parse(json);
        if (obj && typeof obj === 'object') {
          mergeFilters(obj);
          // Optionally ensure Site column exists when site filter is present
          if (obj.site) {
            setColumns((prev) => {
              if (prev.some((c) => c.id === 'site')) return prev;
              const hasAgentVersion = prev.some((c) => c.id === 'agentVersion');
              const remainder = prev.filter((c) => !['status', 'agentVersion'].includes(c.id));
              const base = [
                { id: 'status', label: COL_LABELS.status },
                ...(hasAgentVersion ? [{ id: 'agentVersion', label: COL_LABELS.agentVersion }] : []),
                { id: 'site', label: COL_LABELS.site },
              ];
              if (!hasAgentVersion) {
                return base.concat(prev.filter((c) => c.id !== 'status'));
              }
              return [...base, ...remainder];
            });
          }
        }
        localStorage.removeItem('device_list_initial_filters');
      }

      const site = localStorage.getItem('device_list_initial_site_filter');
      if (site && site.trim()) {
        setColumns((prev) => {
          const hasSite = prev.some((c) => c.id === 'site');
          if (hasSite) return prev;
          const next = [...prev];
          const agentIndex = next.findIndex((c) => c.id === 'agentVersion');
          const insertAt = agentIndex >= 0 ? agentIndex + 1 : 1;
          next.splice(insertAt, 0, { id: 'site', label: COL_LABELS.site });
          return next;
        });
        mergeFilters({ site });
        localStorage.removeItem('device_list_initial_site_filter');
      }
    } catch {}
  }, [COL_LABELS.site, mergeFilters]);

  const applyView = useCallback((view) => {
    if (!view || view.id === "default") {
      setColumns(defaultColumns);
      replaceFilters({});
      return;
    }
    try {
      const ids = Array.isArray(view.columns) ? view.columns : [];
      // Ensure status is present and first
      const finalIds = ["status", ...ids.filter((x) => x !== "status")];
      const mapped = finalIds
        .filter((id) => COL_LABELS[id])
        .map((id) => ({ id, label: COL_LABELS[id] }));
      setColumns(mapped.length ? mapped : defaultColumns);
      replaceFilters(
        view.filters && typeof view.filters === "object" ? view.filters : {}
      );
    } catch {
      setColumns(defaultColumns);
      replaceFilters({});
    }
  }, [COL_LABELS, defaultColumns, replaceFilters]);

  const statusTokenTheme = useMemo(
    () => ({
      Online: {
        text: "#00d18c",
        background: "rgba(0, 209, 140, 0.16)",
        border: "1px solid rgba(0, 209, 140, 0.45)",
        dot: "#00d18c",
      },
      Offline: {
        text: "#b0b8c8",
        background: "rgba(176, 184, 200, 0.14)",
        border: "1px solid rgba(176, 184, 200, 0.35)",
        dot: "#c3cada",
      },
      default: {
        text: "#e2e6f0",
        background: "rgba(226, 230, 240, 0.12)",
        border: "1px solid rgba(226, 230, 240, 0.25)",
        dot: "#e2e6f0",
      },
    }),
    []
  );

  const formatCreated = useCallback((created, createdTs) => {
    if (createdTs) {
      const d = new Date(createdTs * 1000);
      const mm = String(d.getMonth() + 1).padStart(2, "0");
      const dd = String(d.getDate()).padStart(2, "0");
      const yyyy = d.getFullYear();
      const hh = d.getHours() % 12 || 12;
      const min = String(d.getMinutes()).padStart(2, "0");
      const ampm = d.getHours() >= 12 ? "PM" : "AM";
      return `${mm}/${dd}/${yyyy} @ ${hh}:${min} ${ampm}`;
    }
    return created || "";
  }, []);

  const filterModel = useMemo(
    () => JSON.parse(JSON.stringify(filters || {})),
    [filters]
  );

  useEffect(() => {
    if (gridRef.current?.api) {
      gridRef.current.api.setFilterModel(filterModel);
    }
  }, [filterModel]);

  const handleFilterChanged = useCallback(
    (event) => {
      const model = event.api.getFilterModel() || {};
      replaceFilters(model);
    },
    [replaceFilters]
  );

  const handleSelectionChanged = useCallback(() => {
    const api = gridRef.current?.api;
    if (!api) return;
    const selectedNodes = api.getSelectedNodes();
    const ids = selectedNodes
      .map((node) => node.data?.id)
      .filter((id) => id !== undefined && id !== null);
    setSelectedIds(new Set(ids));
  }, []);

  const openMenu = useCallback((event, row) => {
    setMenuAnchor(event.currentTarget);
    setSelected(row);
  }, []);

  const closeMenu = useCallback(() => setMenuAnchor(null), []);

  const confirmDelete = useCallback(() => {
    closeMenu();
    setConfirmOpen(true);
  }, [closeMenu]);

  const handleDelete = useCallback(async () => {
    if (!selected) return;
    const targetAgentId = selected.agentId || selected.summary?.agent_id || selected.id;
    try {
      if (targetAgentId) {
        await fetch(`/api/agent/${encodeURIComponent(targetAgentId)}`, { method: "DELETE" });
      }
    } catch (e) {
      console.warn("Failed to remove agent", e);
    }
    setRows((r) => r.filter((x) => x.id !== selected.id));
    setSelectedIds((prev) => {
      if (!prev.has(selected.id)) return prev;
      const next = new Set(prev);
      next.delete(selected.id);
      return next;
    });
    setConfirmOpen(false);
    setSelected(null);
  }, [selected]);

  const hostnameCellRenderer = useCallback(
    (params) => {
      const row = params.data;
      if (!row) return null;
      const handleClick = (event) => {
        event.preventDefault();
        event.stopPropagation();
        if (onSelectDevice) onSelectDevice(row);
      };
      const label = row.connectionLabel || "";
      let badgeBg = "#2d3042";
      let badgeColor = "#a4c7ff";
      if (label === "SSH") {
        badgeBg = "#2a3b28";
        badgeColor = "#7cffc4";
      } else if (label === "WinRM") {
        badgeBg = "#352e3b";
        badgeColor = "#ffb6ff";
      }
      return (
        <Box component="span" sx={{ display: "flex", alignItems: "center", gap: 1 }}>
          {label ? (
            <Box
              component="span"
              sx={{
                display: "inline-flex",
                alignItems: "center",
                px: 0.75,
                py: 0.1,
                borderRadius: 999,
                bgcolor: badgeBg,
                color: badgeColor,
                fontSize: "11px",
                fontWeight: 600,
                textTransform: "uppercase",
              }}
            >
              {label}
            </Box>
          ) : null}
          <a
            href="#"
            onClick={handleClick}
            style={{ color: "#58a6ff", textDecoration: "none", fontWeight: 500 }}
          >
            {row.hostname || ""}
          </a>
        </Box>
      );
    },
    [onSelectDevice]
  );

  const statusCellRenderer = useCallback(
    (params) => {
      const status = params.value || "";
      if (!status) return null;
      const theme = statusTokenTheme[status] || statusTokenTheme.default;
      return (
        <Box
          component="span"
          sx={{
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            minWidth: 76,
            px: 1.5,
            py: 0.4,
            borderRadius: 999,
            backgroundColor: theme.background,
            border: theme.border,
            color: theme.text,
            fontWeight: 600,
            fontSize: "13px",
            lineHeight: 1,
            fontFamily: gridFontFamily,
            textTransform: "capitalize",
            gap: 0.75,
          }}
        >
          <Box
            component="span"
            sx={{
              width: 8,
              height: 8,
              borderRadius: "50%",
              backgroundColor: theme.dot,
              boxShadow: "0 0 0 2px rgba(0, 0, 0, 0.22)",
            }}
          />
          {status}
        </Box>
      );
    },
    [statusTokenTheme, gridFontFamily]
  );

  const osCellRenderer = useCallback((params) => {
    const rawValue = params.value;
    const label = typeof rawValue === "string" ? rawValue : rawValue == null ? "" : String(rawValue);
    const display = label.trim() || "-";
    const iconClass = getOsIconClass(label);

    return (
      <Box
        component="span"
        sx={{
          display: "inline-flex",
          alignItems: "center",
          gap: 1,
          color: "rgba(255,255,255,0.85)",
          fontFamily: gridFontFamily,
        }}
      >
        {iconClass ? (
          <Box
            component="i"
            className={iconClass}
            aria-hidden="true"
            sx={{
              fontSize: "1rem",
              width: "1.5rem",
              textAlign: "center",
              color: "rgba(255,255,255,0.75)",
            }}
          />
        ) : null}
        <Box component="span" sx={{ lineHeight: 1.3 }}>
          {display}
        </Box>
      </Box>
    );
  }, []);

  const actionCellRenderer = useCallback(
    (params) => {
      const row = params.data;
      if (!row) return null;
      const handleClick = (event) => {
        event.stopPropagation();
        openMenu(event, row);
      };
      return (
        <IconButton size="small" onClick={handleClick} sx={{ color: "#ccc" }}>
          <MoreVertIcon fontSize="small" />
        </IconButton>
      );
    },
    [openMenu]
  );

  const handleDescriptionSave = useCallback(
    async (row, nextDescription) => {
      if (!row) return false;
      const trimmed = (nextDescription || "").trim();
      const targetHost = (row.hostname || row.summary?.hostname || "").trim();
      if (!targetHost) return false;
      try {
        const resp = await fetch(`/api/device/description/${targetHost}`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ description: trimmed }),
        });
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        const matchValue = row.id || row.agentGuid || row.hostname || targetHost;
        setRows((prev) =>
          prev.map((item) => {
            const itemMatch = item.id || item.agentGuid || item.hostname || "";
            if (itemMatch !== matchValue) return item;
            const updated = {
              ...item,
              description: trimmed,
              summary: { ...(item.summary || {}), description: trimmed },
            };
            if (item.details) {
              updated.details = { ...item.details, description: trimmed };
            }
            return updated;
          })
        );
        setSelected((prev) => {
          if (!prev) return prev;
          const prevMatch = prev.id || prev.agentGuid || prev.hostname || "";
          if (prevMatch !== matchValue) return prev;
          const updated = {
            ...prev,
            description: trimmed,
            summary: { ...(prev.summary || {}), description: trimmed },
          };
          if (prev.details) {
            updated.details = { ...prev.details, description: trimmed };
          }
          return updated;
        });
        return true;
      } catch (e) {
        console.warn("Failed to save description", e);
        return false;
      }
    },
    [setRows, setSelected]
  );

  const columnDefs = useMemo(() => {
    const defs = columns.map((col) => {
      switch (col.id) {
        case "status":
          return {
            field: "status",
            headerName: col.label,
            cellRenderer: statusCellRenderer,
            width: 100,
            minWidth: 100,
            flex: 0,
          };
        case "agentVersion":
          return {
            field: "agentVersion",
            headerName: col.label,
            width: 140,
            minWidth: 150,
            flex: 0,
          };
        case "site":
          return {
            field: "site",
            headerName: col.label,
            valueGetter: (params) => params.data?.site || "Not Configured",
            width: 170,
            minWidth: 170,
            flex: 0,
          };
        case "hostname":
          return {
            field: "hostname",
            headerName: col.label,
            cellRenderer: hostnameCellRenderer,
            width: 210,
            minWidth: 210,
            flex: 0,
          };
        case "description":
          return {
            field: "description",
            headerName: col.label,
            width: 280,
            minWidth: 280,
            flex: 0,
            cellRenderer: DescriptionCellRenderer,
            cellRendererParams: {
              onSaveDescription: handleDescriptionSave,
              fontFamily: gridFontFamily,
            },
          };
        case "lastUser":
          return {
            field: "lastUser",
            headerName: col.label,
            width: 220,
            minWidth: 220,
            flex: 0,
          };
        case "type":
          return {
            field: "type",
            headerName: col.label,
            width: 170,
            minWidth: 170,
            flex: 0,
          };
        case "os":
          return {
            field: "os",
            headerName: col.label,
            width: 410,
            minWidth: 410,
            flex: 1,
            cellRenderer: osCellRenderer,
          };
        case "internalIp":
          return {
            field: "internalIp",
            headerName: col.label,
            width: 140,
            minWidth: 140,
            flex: 0,
          };
        case "externalIp":
          return {
            field: "externalIp",
            headerName: col.label,
            width: 140,
            minWidth: 140,
            flex: 0,
          };
        case "lastReboot":
          return {
            field: "lastReboot",
            headerName: col.label,
            width: 180,
            minWidth: 180,
            flex: 0,
          };
        case "created":
          return {
            field: "created",
            headerName: col.label,
            valueGetter: (params) =>
              formatCreated(params.data?.created, params.data?.createdTs),
            comparator: (a, b, nodeA, nodeB) =>
              (nodeA?.data?.createdTs || 0) - (nodeB?.data?.createdTs || 0),
            width: 200,
            minWidth: 200,
            flex: 0,
          };
        case "lastSeen":
          return {
            field: "lastSeen",
            headerName: col.label,
            valueGetter: (params) => formatLastSeen(params.data?.lastSeen),
            comparator: (a, b, nodeA, nodeB) =>
              (nodeA?.data?.lastSeen || 0) - (nodeB?.data?.lastSeen || 0),
            width: 200,
            minWidth: 200,
            flex: 0,
          };
        case "agentId":
          return {
            field: "agentId",
            headerName: col.label,
            width: 290,
            minWidth: 290,
            flex: 0,
          };
        case "agentHash":
          return {
            field: "agentHash",
            headerName: col.label,
            width: 365,
            minWidth: 365,
            flex: 0,
          };
        case "agentGuid":
          return {
            field: "agentGuid",
            headerName: col.label,
            width: 345,
            minWidth: 345,
            flex: 0,
          };
        case "domain":
          return {
            field: "domain",
            headerName: col.label,
            width: 160,
            minWidth: 160,
            flex: 0,
          };
        case "uptime":
          return {
            field: "uptime",
            headerName: col.label,
            valueGetter: (params) =>
              params.data?.uptimeDisplay ||
              formatUptime(params.data?.uptime || 0),
            comparator: (a, b, nodeA, nodeB) =>
              (nodeA?.data?.uptime || 0) - (nodeB?.data?.uptime || 0),
            width: 140,
            minWidth: 140,
            flex: 0,
          };
        case "memory":
        case "network":
        case "software":
        case "storage":
        case "cpu":
        case "siteDescription":
          return {
            field: col.id,
            headerName: col.label,
            minWidth: 200,
          };
        default:
          return {
            field: col.id,
            headerName: col.label,
          };
      }
    });
    return [
      {
        headerName: "",
        field: "__select__",
        width: 52,
        maxWidth: 52,
        checkboxSelection: true,
        headerCheckboxSelection: true,
        resizable: false,
        sortable: false,
        suppressMenu: true,
        filter: false,
        pinned: "left",
        lockPosition: true,
      },
      ...defs,
      {
        headerName: "",
        field: "__actions__",
        width: 64,
        maxWidth: 64,
        resizable: false,
        sortable: false,
        suppressMenu: true,
        filter: false,
        cellRenderer: actionCellRenderer,
        pinned: "right",
      },
    ];
  }, [
    columns,
    actionCellRenderer,
    formatCreated,
    handleDescriptionSave,
    hostnameCellRenderer,
    statusCellRenderer,
  ]);

  const defaultColDef = useMemo(
    () => ({
      sortable: true,
      filter: "agTextColumnFilter",
      resizable: true,
      flex: 1,
      minWidth: 160,
    }),
    []
  );

  const handleGridReady = useCallback(
    (params) => {
      params.api.setFilterModel(filterModel);
    },
    [filterModel]
  );

  const getRowId = useCallback(
    (params) =>
      params.data?.id ||
      params.data?.agentGuid ||
      params.data?.hostname ||
      String(params.rowIndex ?? ""),
    []
  );

  return (
    <Paper
      sx={{
        m: 2,
        p: 0,
        bgcolor: "#1e1e1e",
        fontFamily: gridFontFamily,
        color: "#f5f7fa",
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minWidth: 0,
        height: "100%",
      }}
      elevation={2}
    >
      {/* Header area with title on left and controls on right */}
      <Box sx={{ p: 2, pb: 1, display: "flex", flexDirection: 'column', gap: 1 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>
            {computedTitle}
          </Typography>
          <Box sx={{ display: 'flex', alignItems: 'center' }}>
            {/* Views dropdown + add button */}
            <Box sx={{ display: 'flex', alignItems: 'center', mr: 1 }}>
              <TextField
                select
                size="small"
                value={selectedViewId}
                onChange={(e) => {
                  const val = e.target.value;
                  setSelectedViewId(val);
                  if (val === "default") applyView({ id: "default" });
                  else {
                    const v = views.find((x) => String(x.id) === String(val));
                    if (v) applyView(v);
                  }
                }}
                sx={{
                  minWidth: 220,
                  mr: 0,
                  '& .MuiOutlinedInput-root': {
                    height: 32,
                    pr: 0,
                    borderTopRightRadius: 0,
                    borderBottomRightRadius: 0,
                    '& fieldset': { borderColor: '#555', borderRight: '1px solid #555' },
                    '&:hover fieldset': { borderColor: '#888' },
                  },
                  '& .MuiSelect-select': {
                    display: 'flex',
                    alignItems: 'center',
                    py: 0,
                  },
                }}
                SelectProps={{
                  MenuProps: {
                    PaperProps: { sx: { bgcolor: '#1e1e1e', color: '#fff' } },
                  },
                  renderValue: (val) => {
                    if (val === "default") return "Default View";
                    const v = views.find((x) => String(x.id) === String(val));
                    return v ? v.name : "Default View";
                  }
                }}
              >
                <MenuItem value="default">Default View</MenuItem>
                {views.map((v) => (
                  <MenuItem key={v.id} value={v.id} disableRipple>
                    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', width: '100%' }}>
                      <span>{v.name}</span>
                      <IconButton
                        size="small"
                        onClick={(e) => {
                          e.stopPropagation();
                          setViewActionAnchor(e.currentTarget);
                          setViewActionTarget(v);
                        }}
                        sx={{ color: '#ccc' }}
                      >
                        <MoreVertIcon fontSize="small" />
                      </IconButton>
                    </Box>
                  </MenuItem>
                ))}
              </TextField>
              <IconButton
                size="small"
                onClick={() => { setNewViewName(""); setCreateDialogOpen(true); }}
                sx={{
                  ml: '-1px',
                  border: '1px solid #555',
                  borderLeft: '1px solid #555',
                  borderRadius: '0 4px 4px 0',
                  color: '#bbb',
                  height: 32,
                  width: 32,
                }}
              >
                <AddIcon fontSize="small" />
              </IconButton>
            </Box>
            <Tooltip title="Refresh Devices to Detect Changes">
              <IconButton
                size="small"
                onClick={() => fetchDevices({ refreshRepo: true })}
                sx={{ color: "#bbb", mr: 1 }}
              >
                <CachedIcon fontSize="small" />
              </IconButton>
            </Tooltip>
            <Tooltip title="Column Chooser">
              <IconButton
                size="small"
                onClick={(e) => setColChooserAnchor(e.currentTarget)}
                sx={{ color: "#bbb", mr: 1 }}
              >
                <ViewColumnIcon fontSize="small" />
              </IconButton>
            </Tooltip>
            {derivedShowAddButton && (
              <Button
                variant="contained"
                size="small"
                startIcon={<AddIcon />}
                sx={{ bgcolor: "#58a6ff", color: "#0b0f19" }}
                onClick={() => {
                  setAddDeviceType(derivedDefaultType ?? null);
                  setAddDeviceOpen(true);
                }}
              >
                {derivedAddLabel}
              </Button>
            )}
          </Box>
        </Box>
        {/* Second row: Quick Job button aligned under header title */}
        <Box sx={{ display: 'flex', gap: 1 }}>
          <Button
            variant="outlined"
            size="small"
            disabled={selectedIds.size === 0}
            onClick={() => setQuickJobOpen(true)}
            sx={{
              color: selectedIds.size === 0 ? "#666" : "#58a6ff",
              borderColor: selectedIds.size === 0 ? "#333" : "#58a6ff",
              textTransform: "none"
            }}
          >
            Quick Job
          </Button>
        </Box>
      </Box>
      {/* The Size of the Grid itself and its margins relative to the overall page */}
      <Box sx={{ mt: '10px', px: 2, pb: 2, flexGrow: 1, minHeight: 0, display: "flex", flexDirection: "column" }}> 
        <Box
          className={gridWrapperClass}
          sx={{
            width: "100%",
            flexGrow: 1,
            minHeight: 0,
            height: "100%",
            fontFamily: gridFontFamily,
            "--ag-font-family": gridFontFamily,
            "--ag-icon-font-family": iconFontFamily,
            "& .ag-root-wrapper": {
              borderRadius: 1,
              minHeight: 400,
            },
            "& .ag-root, & .ag-header, & .ag-center-cols-container, & .ag-paging-panel": {
              fontFamily: gridFontFamily,
            },
            "& .ag-icon": {
              fontFamily: iconFontFamily,
            },
          }}
        >
          <AgGridReact
            ref={gridRef}
            rowData={rows}
            columnDefs={columnDefs}
            defaultColDef={defaultColDef}
            rowSelection="multiple"
            rowMultiSelectWithClick
            pagination
            paginationPageSize={25}
            animateRows
            onSelectionChanged={handleSelectionChanged}
            onFilterChanged={handleFilterChanged}
            onGridReady={handleGridReady}
            getRowId={getRowId}
            theme={myTheme}
            style={{
              width: "100%",
              height: "100%",
              fontFamily: gridFontFamily,
              "--ag-icon-font-family": iconFontFamily,
            }}
          />
        </Box>
      </Box>
      {/* View actions menu (rename/delete for custom views) */}
      <Menu
        anchorEl={viewActionAnchor}
        open={Boolean(viewActionAnchor)}
        onClose={() => { setViewActionAnchor(null); setViewActionTarget(null); }}
        PaperProps={{ sx: { bgcolor: '#1e1e1e', color: '#fff', fontSize: '13px' } }}
      >
        <MenuItem onClick={() => {
          const v = viewActionTarget;
          setViewActionAnchor(null);
          if (!v) return;
          setRenameTarget(v);
          setRenameViewName(v.name || "");
          setRenameDialogOpen(true);
        }}>Rename</MenuItem>
        <MenuItem sx={{ color: '#ff4f4f' }} onClick={async () => {
          const v = viewActionTarget;
          setViewActionAnchor(null);
          if (!v) return;
          try {
            await fetch(`/api/device_list_views/${encodeURIComponent(v.id)}`, { method: 'DELETE' });
          } catch {}
          setViews((prev) => prev.filter((x) => String(x.id) !== String(v.id)));
          if (String(selectedViewId) === String(v.id)) {
            setSelectedViewId('default');
            applyView({ id: 'default' });
          }
        }}>Delete</MenuItem>
      </Menu>

      {/* Create new custom view dialog */}
      <CreateCustomViewDialog
        open={createDialogOpen}
        value={newViewName}
        onChange={setNewViewName}
        onCancel={() => setCreateDialogOpen(false)}
        onSave={async () => {
          const name = (newViewName || '').trim();
          if (!name) return;
          // Build current config
          const cols = (columns || []).map((c) => c.id);
          const cfg = { name, columns: cols, filters };
          try {
            const res = await fetch('/api/device_list_views', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify(cfg)
            });
            if (res.ok) {
              const created = await res.json();
              setViews((prev) => [...prev, created].sort((a, b) => String(a.name).localeCompare(String(b.name))));
              setSelectedViewId(String(created.id));
              // Already applied in UI; we keep current state
              setCreateDialogOpen(false);
              setNewViewName('');
            }
          } catch {}
        }}
      />

      {/* Rename custom view dialog */}
      <RenameCustomViewDialog
        open={renameDialogOpen}
        value={renameViewName}
        onChange={setRenameViewName}
        onCancel={() => setRenameDialogOpen(false)}
        onSave={async () => {
          const v = renameTarget;
          const newName = (renameViewName || '').trim();
          if (!v || !newName) return;
          try {
            const res = await fetch(`/api/device_list_views/${encodeURIComponent(v.id)}`, {
              method: 'PUT',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ name: newName })
            });
            if (res.ok) {
              const updated = await res.json();
              setViews((prev) => prev.map((x) => String(x.id) === String(v.id) ? updated : x));
              setRenameDialogOpen(false);
              setRenameViewName('');
              setRenameTarget(null);
            }
          } catch {}
        }}
      />
      {/* Column chooser popover */}
      <Popover
        open={Boolean(colChooserAnchor)}
        anchorEl={colChooserAnchor}
        onClose={() => setColChooserAnchor(null)}
        anchorOrigin={{ vertical: "bottom", horizontal: "right" }}
        PaperProps={{ sx: { bgcolor: "#1e1e1e", color: '#fff', p: 1 } }}
      >
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5, p: 1 }}>
          {Object.entries(COL_LABELS)
            .filter(([id]) => id !== 'status')
            .map(([id, label]) => (
              <MenuItem key={id} disableRipple onClick={(e) => e.stopPropagation()} sx={{ gap: 1 }}>
                <Checkbox
                  size="small"
                  checked={columns.some((c) => c.id === id)}
                  onChange={(e) => {
                    const checked = e.target.checked;
                    setColumns((prev) => {
                      const exists = prev.some((c) => c.id === id);
                      if (checked) {
                        if (exists) return prev;
                        const nextLabel = COL_LABELS[id] || label || id;
                        return [...prev, { id, label: nextLabel }];
                      }
                      return prev.filter((c) => c.id !== id);
                    });
                  }}
                  sx={{ p: 0.3, color: '#bbb' }}
                />
                <Typography variant="body2" sx={{ color: '#ddd' }}>{label || id}</Typography>
              </MenuItem>
            ))}
          <Box sx={{ display: 'flex', gap: 1, pt: 0.5 }}>
            <Button
              size="small"
              variant="outlined"
              onClick={() => setColumns(defaultColumns)}
              sx={{ textTransform: 'none', borderColor: '#555', color: '#bbb' }}
            >
              Reset Default
            </Button>
          </Box>
        </Box>
      </Popover>
      <Menu
        anchorEl={menuAnchor}
        open={Boolean(menuAnchor)}
        onClose={closeMenu}
        PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" } }}
      >
        <MenuItem onClick={async () => {
          closeMenu();
          await fetchSites();
          const targets = new Set(selectedIds);
          if (selected && !targets.has(selected.id)) targets.add(selected.id);
          const idToHost = new Map(rows.map((r) => [r.id, r.hostname]));
          const hostnames = Array.from(targets).map((id) => idToHost.get(id)).filter(Boolean);
          setAssignTargets(hostnames);
          setAssignSiteId(null);
          setAssignDialogOpen(true);
        }}>Add to Site</MenuItem>
        <MenuItem onClick={async () => {
          closeMenu();
          await fetchSites();
          const targets = new Set(selectedIds);
          if (selected && !targets.has(selected.id)) targets.add(selected.id);
          const idToHost = new Map(rows.map((r) => [r.id, r.hostname]));
          const hostnames = Array.from(targets).map((id) => idToHost.get(id)).filter(Boolean);
          setAssignTargets(hostnames);
          setAssignSiteId(null);
          setAssignDialogOpen(true);
        }}>Move to Another Site</MenuItem>
        <MenuItem onClick={confirmDelete} sx={{ color: '#ff8a8a' }}>Delete</MenuItem>
      </Menu>
      <DeleteDeviceDialog
        open={confirmOpen}
        onCancel={() => setConfirmOpen(false)}
        onConfirm={handleDelete}
      />

      {quickJobOpen && (
        <QuickJob
          open={quickJobOpen}
          onClose={() => setQuickJobOpen(false)}
          hostnames={rows.filter((r) => selectedIds.has(r.id)).map((r) => r.hostname)}
        />
      )}
      {assignDialogOpen && (
        <Popover
          open={assignDialogOpen}
          onClose={() => setAssignDialogOpen(false)}
          anchorReference="anchorPosition"
          anchorPosition={{ top: Math.max(Math.floor(window.innerHeight*0.5), 200), left: Math.max(Math.floor(window.innerWidth*0.5), 300) }}
          PaperProps={{ sx: { bgcolor: '#1e1e1e', color: '#fff', p: 2, minWidth: 360 } }}
        >
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
            <Typography variant="subtitle1">Assign {assignTargets.length} device(s) to a site</Typography>
            <TextField
              select
              size="small"
              label="Select Site"
              value={assignSiteId ?? ''}
              onChange={(e) => setAssignSiteId(Number(e.target.value))}
              sx={{ '& .MuiOutlinedInput-root': { '& fieldset': { borderColor: '#444' }, '&:hover fieldset': { borderColor: '#666' } }, label: { color: '#aaa' } }}
            >
              {sites.map((s) => (
                <MenuItem key={s.id} value={s.id}>{s.name}</MenuItem>
              ))}
            </TextField>
            <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1 }}>
              <Button variant="outlined" size="small" onClick={() => setAssignDialogOpen(false)} sx={{ textTransform: 'none', borderColor: '#555', color: '#bbb' }}>Cancel</Button>
              <Button
                variant="outlined"
                size="small"
                disabled={!assignSiteId || assignTargets.length === 0}
                onClick={async () => {
                  try {
                    await fetch('/api/sites/assign', {
                      method: 'POST',
                      headers: { 'Content-Type': 'application/json' },
                      body: JSON.stringify({ site_id: assignSiteId, hostnames: assignTargets })
                    });
                  } catch {}
                  setAssignDialogOpen(false);
                  // Refresh mapping to update Site column
                  try {
                    const hostCsv = rows.map((r) => r.hostname).filter(Boolean).map(encodeURIComponent).join(',');
                    const resp = await fetch(`/api/sites/device_map?hostnames=${hostCsv}`);
                    const mapData = await resp.json();
                    const mapping = mapData?.mapping || {};
                    setSiteMapping(mapping);
                    setRows((prev) => prev.map((r) => ({ ...r, site: mapping[r.hostname]?.site_name || 'Not Configured' })));
                  } catch {}
                }}
                sx={{ textTransform: 'none', borderColor: '#58a6ff', color: '#58a6ff' }}
              >
                Assign
              </Button>
            </Box>
          </Box>
        </Popover>
      )}
      <AddDevice
        open={addDeviceOpen}
        defaultType={addDeviceType}
        onClose={() => {
          setAddDeviceOpen(false);
          setAddDeviceType(derivedDefaultType ?? null);
        }}
        onCreated={() => {
          setAddDeviceOpen(false);
          setAddDeviceType(derivedDefaultType ?? null);
          fetchDevices({ refreshRepo: true });
        }}
      />
    </Paper>
  );
}
