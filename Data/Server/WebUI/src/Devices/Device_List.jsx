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
import { DeleteDeviceDialog, CreateCustomViewDialog, RenameCustomViewDialog } from "../Dialogs.jsx";
import QuickJob from "../Scheduling/Quick_Job.jsx";

function formatLastSeen(tsSec, offlineAfter = 120) {
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

function statusFromHeartbeat(tsSec, offlineAfter = 120) {
  if (!tsSec) return "Offline";
  const now = Date.now() / 1000;
  return now - tsSec <= offlineAfter ? "Online" : "Offline";
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
    }),
    []
  );

  const defaultColumns = useMemo(
    () => [
      { id: "status", label: COL_LABELS.status },
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

  // Cache device details to avoid re-fetching every refresh
  const [detailsByHost, setDetailsByHost] = useState({}); // hostname -> cached fields
  const [siteMapping, setSiteMapping] = useState({}); // hostname -> { site_id, site_name }
  const [sites, setSites] = useState([]); // sites list for assignment
  const [assignDialogOpen, setAssignDialogOpen] = useState(false);
  const [assignSiteId, setAssignSiteId] = useState(null);
  const [assignTargets, setAssignTargets] = useState([]); // hostnames

  const fetchAgents = useCallback(async () => {
    try {
      const res = await fetch("/api/agents");
      const data = await res.json();
      const arr = Object.entries(data || {}).map(([id, a]) => {
        const hostname = a.hostname || id || "unknown";
        const details = detailsByHost[hostname] || {};
        return {
          id,
          hostname,
          status: statusFromHeartbeat(a.last_seen),
          lastSeen: a.last_seen || 0,
          os: a.agent_operating_system || a.os || "-",
          // Enriched fields from details cache
          lastUser: details.lastUser || "",
          type: a.device_type || details.type || "",
          created: details.created || "",
          createdTs: details.createdTs || 0,
          internalIp: details.internalIp || "",
          externalIp: details.externalIp || "",
          lastReboot: details.lastReboot || "",
          description: details.description || "",
        };
      });
      setRows(arr);

      // Fetch site mapping for these hostnames
      try {
        const hostCsv = arr.map((r) => r.hostname).filter(Boolean).map(encodeURIComponent).join(',');
        const resp = await fetch(`/api/sites/device_map?hostnames=${hostCsv}`);
        const mapData = await resp.json();
        const mapping = mapData?.mapping || {};
        setSiteMapping(mapping);
        setRows((prev) => prev.map((r) => ({ ...r, site: mapping[r.hostname]?.site_name || "Not Configured" })));
      } catch {}

      // Fetch missing details (last_user, created) for hosts not cached yet
      const hostsToFetch = arr
        .map((r) => r.hostname)
        .filter((h) => h && !detailsByHost[h]);
      if (hostsToFetch.length) {
        // Limit concurrency a bit
        const chunks = [];
        const size = 6;
        for (let i = 0; i < hostsToFetch.length; i += size) {
          chunks.push(hostsToFetch.slice(i, i + size));
        }
        for (const chunk of chunks) {
          await Promise.all(
            chunk.map(async (h) => {
              try {
                const resp = await fetch(`/api/device/details/${encodeURIComponent(h)}`);
                const det = await resp.json();
                const summary = det?.summary || {};
                const lastUser = summary.last_user || "";
                const createdRaw = summary.created || "";
                // Try to parse created to epoch seconds for sorting; fallback to 0
                let createdTs = 0;
                if (createdRaw) {
                  const parsed = Date.parse(createdRaw.replace(" ", "T"));
                  createdTs = isNaN(parsed) ? 0 : Math.floor(parsed / 1000);
                }
                const deviceType = (summary.device_type || "").trim();
                const internalIp = summary.internal_ip || "";
                const externalIp = summary.external_ip || "";
                const lastReboot = summary.last_reboot || "";
                const description = summary.description || "";
                setDetailsByHost((prev) => ({
                  ...prev,
                  [h]: {
                    lastUser,
                    created: createdRaw,
                    createdTs,
                    type: deviceType,
                    internalIp,
                    externalIp,
                    lastReboot,
                    description,
                  },
                }));
              } catch {
                // ignore per-host failure
              }
            })
          );
        }
        // After caching, refresh rows to apply newly available details
        setRows((prev) =>
          prev.map((r) => {
            const det = detailsByHost[r.hostname];
            if (!det) return r;
            return {
              ...r,
              lastUser: det.lastUser || r.lastUser,
              type: det.type || r.type,
              created: det.created || r.created,
              createdTs: det.createdTs || r.createdTs,
              internalIp: det.internalIp || r.internalIp,
              externalIp: det.externalIp || r.externalIp,
              lastReboot: det.lastReboot || r.lastReboot,
              description: det.description || r.description,
            };
          })
        );
      }
    } catch (e) {
      console.warn("Failed to load agents:", e);
      setRows([]);
    }
  }, [detailsByHost]);

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
    fetchAgents();
    const t = setInterval(fetchAgents, 5000);
    return () => clearInterval(t);
  }, [fetchAgents]);

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
      const site = localStorage.getItem('device_list_initial_site_filter');
      if (site && site.trim()) {
        setColumns((prev) => {
          const hasSite = prev.some((c) => c.id === 'site');
          if (hasSite) return prev;
          const next = [...prev];
          next.splice(1, 0, { id: 'site', label: COL_LABELS.site });
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
      // Support numeric sort for created/lastSeen
      if (orderBy === "lastSeen") return ((a.lastSeen || 0) - (b.lastSeen || 0)) * dir;
      if (orderBy === "created") return ((a.createdTs || 0) - (b.createdTs || 0)) * dir;
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
    try {
      await fetch(`/api/agent/${selected.id}`, { method: "DELETE" });
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
            Devices
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
        <Box sx={{ display: 'flex' }}>
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
                        {r.hostname}
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
                  default:
                    return <TableCell key={col.id} />;
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
          {[
            { id: 'site', label: 'Site' },
            { id: 'hostname', label: 'Hostname' },
            { id: 'os', label: 'Operating System' },
            { id: 'type', label: 'Device Type' },
            { id: 'lastUser', label: 'Last User' },
            { id: 'internalIp', label: 'Internal IP' },
            { id: 'externalIp', label: 'External IP' },
            { id: 'lastReboot', label: 'Last Reboot' },
            { id: 'created', label: 'Created' },
            { id: 'lastSeen', label: 'Last Seen' },
            { id: 'description', label: 'Description' },
          ].map((opt) => (
            <MenuItem key={opt.id} disableRipple onClick={(e) => e.stopPropagation()} sx={{ gap: 1 }}>
              <Checkbox
                size="small"
                checked={columns.some((c) => c.id === opt.id)}
                onChange={(e) => {
                  const checked = e.target.checked;
                  setColumns((prev) => {
                    // Keep 'status' always present; manage others per toggle
                    const exists = prev.some((c) => c.id === opt.id);
                    if (checked) {
                      if (exists) return prev;
                      // Append new column at the end with canonical label
                      const label = COL_LABELS[opt.id] || opt.label || opt.id;
                      return [...prev, { id: opt.id, label }];
                    }
                    // Remove column
                    return prev.filter((c) => c.id !== opt.id);
                  });
                }}
                sx={{ p: 0.3, color: '#bbb' }}
              />
              <Typography variant="body2" sx={{ color: '#ddd' }}>{opt.label}</Typography>
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
