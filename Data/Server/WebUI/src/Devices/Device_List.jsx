////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Device_List.jsx

import React, { useState, useEffect, useCallback, useMemo, useRef } from "react";
import {
  Paper,
  Box,
  Typography,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  TableSortLabel,
  Checkbox,
  Button,
  IconButton,
  Menu,
  MenuItem,
  Popover,
  TextField,
  Tooltip
} from "@mui/material";
import MoreVertIcon from "@mui/icons-material/MoreVert";
import FilterListIcon from "@mui/icons-material/FilterList";
import ViewColumnIcon from "@mui/icons-material/ViewColumn";
import AddIcon from "@mui/icons-material/Add";
import CachedIcon from "@mui/icons-material/Cached";
import { DeleteDeviceDialog, CreateCustomViewDialog, RenameCustomViewDialog } from "../Dialogs.jsx";
import QuickJob from "../Scheduling/Quick_Job.jsx";

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

export default function DeviceList({ onSelectDevice }) {
  const [rows, setRows] = useState([]);
  const [orderBy, setOrderBy] = useState("status");
  const [order, setOrder] = useState("desc");
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [selected, setSelected] = useState(null);
  const [confirmOpen, setConfirmOpen] = useState(false);
  // Track selection by agent id to avoid duplicate hostname collisions
  const [selectedIds, setSelectedIds] = useState(() => new Set());
  const [quickJobOpen, setQuickJobOpen] = useState(false);

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
  const dragColId = useRef(null);
  const [colChooserAnchor, setColChooserAnchor] = useState(null);

  // Per-column filters
  const [filters, setFilters] = useState({});
  const [filterAnchor, setFilterAnchor] = useState(null); // { id, anchorEl }

  const [sites, setSites] = useState([]); // sites list for assignment
  const [assignDialogOpen, setAssignDialogOpen] = useState(false);
  const [assignSiteId, setAssignSiteId] = useState(null);
  const [assignTargets, setAssignTargets] = useState([]); // hostnames

  const [repoHash, setRepoHash] = useState(null);
  const lastRepoFetchRef = useRef(0);

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
          type,
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
          connectionEndpoint,
          isRemote: connectionType === 'ssh',
        };
      });

      setRows(normalized);
    } catch (e) {
      console.warn('Failed to load devices:', e);
      setRows([]);
    }
  }, [repoHash, fetchLatestRepoHash, computeAgentVersion]);

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
          setFilters((prev) => ({ ...prev, ...obj }));
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
        setFilters((f) => ({ ...f, site }));
        localStorage.removeItem('device_list_initial_site_filter');
      }
    } catch {}
  }, [COL_LABELS.site]);

  const applyView = useCallback((view) => {
    if (!view || view.id === "default") {
      setColumns(defaultColumns);
      setFilters({});
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
      setFilters(view.filters && typeof view.filters === "object" ? view.filters : {});
    } catch {
      setColumns(defaultColumns);
      setFilters({});
    }
  }, [COL_LABELS, defaultColumns]);

  const filtered = useMemo(() => {
    // Apply simple contains filter per column based on displayed string
    const activeFilters = Object.entries(filters).filter(([, v]) => (v || "").trim() !== "");
    if (!activeFilters.length) return rows;
    const toDisplay = (colId, row) => {
      switch (colId) {
        case "status":
          return row.status || "";
        case "site":
          return row.site || "Not Configured";
        case "hostname":
          return row.hostname || "";
        case "description":
          return row.description || "";
        case "lastUser":
          return row.lastUser || "";
        case "type":
          return row.type || "";
        case "os":
          return row.os || "";
        case "agentVersion":
          return row.agentVersion || "";
        case "internalIp":
          return row.internalIp || "";
        case "externalIp":
          return row.externalIp || "";
        case "lastReboot":
          return row.lastReboot || "";
        case "created":
          return formatCreated(row.created, row.createdTs);
        case "lastSeen":
          return formatLastSeen(row.lastSeen);
        case "agentId":
          return row.agentId || "";
        case "agentHash":
          return row.agentHash || "";
        case "agentGuid":
          return row.agentGuid || "";
        case "domain":
          return row.domain || "";
        case "uptime":
          return row.uptimeDisplay || (row.uptime ? String(row.uptime) : "");
        case "memory":
          return row.memoryRaw || row.memory || "";
        case "network":
          return row.networkRaw || row.network || "";
        case "software":
          return row.softwareRaw || row.software || "";
        case "storage":
          return row.storageRaw || row.storage || "";
        case "cpu":
          return row.cpuRaw || row.cpu || "";
        case "siteDescription":
          return row.siteDescription || "";
        default:
          return "";
      }
    };
    return rows.filter((r) =>
      activeFilters.every(([k, val]) =>
        toDisplay(k, r).toLowerCase().includes(String(val).toLowerCase())
      )
    );
  }, [rows, filters]);

  const sorted = useMemo(() => {
    const dir = order === "asc" ? 1 : -1;
    return [...filtered].sort((a, b) => {
      // Support numeric sort for created/lastSeen/uptime
      if (orderBy === "lastSeen") return ((a.lastSeen || 0) - (b.lastSeen || 0)) * dir;
      if (orderBy === "created") return ((a.createdTs || 0) - (b.createdTs || 0)) * dir;
      if (orderBy === "uptime") return ((a.uptime || 0) - (b.uptime || 0)) * dir;
      const A = a[orderBy];
      const B = b[orderBy];
      return String(A || "").localeCompare(String(B || "")) * dir;
    });
  }, [filtered, orderBy, order]);

  const handleSort = (col) => {
    if (orderBy === col) setOrder(order === "asc" ? "desc" : "asc");
    else {
      setOrderBy(col);
      setOrder("asc");
    }
  };

  const statusColor = (s) => (s === "Online" ? "#00d18c" : "#ff4f4f");

  const openMenu = (e, row) => {
    setMenuAnchor(e.currentTarget);
    setSelected(row);
  };

  const closeMenu = () => setMenuAnchor(null);

  const confirmDelete = () => {
    closeMenu();
    setConfirmOpen(true);
  };

  const handleDelete = async () => {
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
    setConfirmOpen(false);
    setSelected(null);
  };

  const isAllChecked = sorted.length > 0 && sorted.every((r) => selectedIds.has(r.id));
  const isIndeterminate = selectedIds.size > 0 && !isAllChecked;
  const toggleAll = (e) => {
    const checked = e.target.checked;
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (checked) sorted.forEach((r) => next.add(r.id));
      else next.clear();
      return next;
    });
  };

  const toggleOne = (id) => (e) => {
    const checked = e.target.checked;
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (checked) next.add(id);
      else next.delete(id);
      return next;
    });
  };

  // Column drag handlers
  const onHeaderDragStart = (colId) => (e) => {
    dragColId.current = colId;
    try { e.dataTransfer.setData("text/plain", colId); } catch {}
  };
  const onHeaderDragOver = (e) => { e.preventDefault(); };
  const onHeaderDrop = (targetColId) => (e) => {
    e.preventDefault();
    const fromId = dragColId.current;
    if (!fromId || fromId === targetColId) return;
    setColumns((prev) => {
      const cur = [...prev];
      const fromIdx = cur.findIndex((c) => c.id === fromId);
      const toIdx = cur.findIndex((c) => c.id === targetColId);
      if (fromIdx < 0 || toIdx < 0) return prev;
      const [moved] = cur.splice(fromIdx, 1);
      cur.splice(toIdx, 0, moved);
      return cur;
    });
    dragColId.current = null;
  };

  // Filter popover handlers
  const openFilter = (id) => (e) => setFilterAnchor({ id, anchorEl: e.currentTarget });
  const closeFilter = () => setFilterAnchor(null);
  const onFilterChange = (id) => (e) => setFilters((prev) => ({ ...prev, [id]: e.target.value }));

  const formatCreated = (created, createdTs) => {
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
  };

  return (
    <Paper sx={{ m: 2, p: 0, bgcolor: "#1e1e1e" }} elevation={2}>
      {/* Header area with title on left and controls on right */}
      <Box sx={{ p: 2, pb: 1, display: "flex", flexDirection: 'column', gap: 1 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>
            Device Inventory
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
      <Table size="small" sx={{ minWidth: 820 }}>
        <TableHead>
          <TableRow>
            <TableCell padding="checkbox">
              <Checkbox
                indeterminate={isIndeterminate}
                checked={isAllChecked}
                onChange={toggleAll}
                sx={{ color: "#777" }}
              />
            </TableCell>
            {columns.map((col) => (
              <TableCell
                key={col.id}
                sortDirection={orderBy === col.id ? order : false}
                draggable
                onDragStart={onHeaderDragStart(col.id)}
                onDragOver={onHeaderDragOver}
                onDrop={onHeaderDrop(col.id)}
              >
                <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
                  <TableSortLabel
                    active={orderBy === col.id}
                    direction={orderBy === col.id ? order : "asc"}
                    onClick={() => handleSort(col.id)}
                  >
                    {col.label}
                  </TableSortLabel>
                  <IconButton
                    size="small"
                    onClick={openFilter(col.id)}
                    sx={{ color: filters[col.id] ? "#58a6ff" : "#888" }}
                  >
                    <FilterListIcon fontSize="inherit" />
                  </IconButton>
                </Box>
              </TableCell>
            ))}
            <TableCell />
          </TableRow>
        </TableHead>
        <TableBody>
          {sorted.map((r, i) => (
            <TableRow key={r.id || i} hover>
              <TableCell padding="checkbox" onClick={(e) => e.stopPropagation()}>
                <Checkbox
                  checked={selectedIds.has(r.id)}
                  onChange={toggleOne(r.id)}
                  sx={{ color: "#777" }}
                />
              </TableCell>
              {columns.map((col) => {
                switch (col.id) {
                  case "status":
                    return (
                      <TableCell key={col.id}>
                        <Box
                          sx={{
                            display: "inline-block",
                            px: 1.2,
                            py: 0.25,
                            borderRadius: 999,
                            bgcolor: statusColor(r.status),
                            color: "#fff",
                            fontWeight: 600,
                            fontSize: "12px",
                          }}
                        >
                          {r.status}
                        </Box>
                      </TableCell>
                    );
                  case "agentVersion":
                    return <TableCell key={col.id}>{r.agentVersion || ""}</TableCell>;
                  case "site":
                    return <TableCell key={col.id}>{r.site || "Not Configured"}</TableCell>;
                  case "hostname":
                    return (
                      <TableCell
                        key={col.id}
                        onClick={() => onSelectDevice && onSelectDevice(r)}
                        sx={{
                          color: "#58a6ff",
                          "&:hover": {
                            cursor: onSelectDevice ? "pointer" : "default",
                            textDecoration: onSelectDevice ? "underline" : "none",
                          },
                        }}
                      >
                        <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
                          {r.isRemote && (
                            <Box
                              component="span"
                              sx={{
                                display: "inline-flex",
                                alignItems: "center",
                                px: 0.75,
                                py: 0.1,
                                borderRadius: 999,
                                bgcolor: "#2a3b28",
                                color: "#7cffc4",
                                fontSize: "11px",
                                fontWeight: 600
                              }}
                            >
                              SSH
                            </Box>
                          )}
                          <span>{r.hostname}</span>
                        </Box>
                      </TableCell>
                    );
                  case "description":
                    return <TableCell key={col.id}>{r.description || ""}</TableCell>;
                  case "lastUser":
                    return <TableCell key={col.id}>{r.lastUser || ""}</TableCell>;
                  case "type":
                    return <TableCell key={col.id}>{r.type || ""}</TableCell>;
                  case "os":
                    return <TableCell key={col.id}>{r.os}</TableCell>;
                  case "internalIp":
                    return <TableCell key={col.id}>{r.internalIp || ""}</TableCell>;
                  case "externalIp":
                    return <TableCell key={col.id}>{r.externalIp || ""}</TableCell>;
                  case "lastReboot":
                    return <TableCell key={col.id}>{r.lastReboot || ""}</TableCell>;
                  case "created":
                    return (
                      <TableCell key={col.id}>{formatCreated(r.created, r.createdTs)}</TableCell>
                    );
                  case "lastSeen":
                    return (
                      <TableCell key={col.id}>{formatLastSeen(r.lastSeen)}</TableCell>
                    );
                  case "agentId":
                    return <TableCell key={col.id}>{r.agentId || ""}</TableCell>;
                  case "agentHash":
                    return <TableCell key={col.id}>{r.agentHash || ""}</TableCell>;
                  case "agentGuid":
                    return <TableCell key={col.id}>{r.agentGuid || ""}</TableCell>;
                  case "domain":
                    return <TableCell key={col.id}>{r.domain || ""}</TableCell>;
                  case "uptime":
                    return <TableCell key={col.id}>{r.uptimeDisplay || ''}</TableCell>;
                  case "memory":
                    return <TableCell key={col.id}>{r.memory || ""}</TableCell>;
                  case "network":
                    return <TableCell key={col.id}>{r.network || ""}</TableCell>;
                  case "software":
                    return <TableCell key={col.id}>{r.software || ""}</TableCell>;
                  case "storage":
                    return <TableCell key={col.id}>{r.storage || ""}</TableCell>;
                  case "cpu":
                    return <TableCell key={col.id}>{r.cpu || ""}</TableCell>;
                  case "siteDescription":
                    return <TableCell key={col.id}>{r.siteDescription || ""}</TableCell>;
                  default:
                    return <TableCell key={col.id}>{String(r[col.id] || "")}</TableCell>;
                }
              })}
              <TableCell align="right">
                <IconButton
                  size="small"
                  onClick={(e) => {
                    e.stopPropagation();
                    openMenu(e, r);
                  }}
                  sx={{ color: "#ccc" }}
                >
                  <MoreVertIcon fontSize="small" />
                </IconButton>
              </TableCell>
            </TableRow>
          ))}
          {sorted.length === 0 && (
            <TableRow>
              <TableCell colSpan={columns.length + 2} sx={{ color: "#888" }}>
                No agents connected.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
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
      {/* Filter popover */}
      <Popover
        open={Boolean(filterAnchor)}
        anchorEl={filterAnchor?.anchorEl || null}
        onClose={closeFilter}
        anchorOrigin={{ vertical: "bottom", horizontal: "left" }}
        PaperProps={{ sx: { bgcolor: "#1e1e1e", p: 1 } }}
      >
        {filterAnchor && (
          <Box sx={{ display: "flex", gap: 1, alignItems: "center" }}>
            <TextField
              autoFocus
              size="small"
              placeholder={`Filter ${columns.find((c) => c.id === filterAnchor.id)?.label || ""}`}
              value={filters[filterAnchor.id] || ""}
              onChange={onFilterChange(filterAnchor.id)}
              onKeyDown={(e) => { if (e.key === "Escape") closeFilter(); }}
              sx={{
                input: { color: "#fff" },
                minWidth: 220,
                "& .MuiOutlinedInput-root": {
                  "& fieldset": { borderColor: "#555" },
                  "&:hover fieldset": { borderColor: "#888" },
                },
              }}
            />
            <Button
              variant="outlined"
              size="small"
              onClick={() => {
                setFilters((prev) => ({ ...prev, [filterAnchor.id]: "" }));
                closeFilter();
              }}
              sx={{ textTransform: "none", borderColor: "#555", color: "#bbb" }}
            >
              Clear
            </Button>
          </Box>
        )}
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
    </Paper>
  );
}
