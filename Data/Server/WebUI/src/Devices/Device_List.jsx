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
  TextField
} from "@mui/material";
import MoreVertIcon from "@mui/icons-material/MoreVert";
import FilterListIcon from "@mui/icons-material/FilterList";
import { DeleteDeviceDialog } from "../Dialogs.jsx";
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

  // Column configuration and rearranging state
  const defaultColumns = useMemo(
    () => [
      { id: "status", label: "Status" },
      { id: "hostname", label: "Hostname" },
      { id: "lastUser", label: "Last User" },
      { id: "type", label: "Type" },
      { id: "os", label: "OS" },
      { id: "created", label: "Created" }
    ],
    []
  );
  const [columns, setColumns] = useState(defaultColumns);
  const dragColId = useRef(null);

  // Per-column filters
  const [filters, setFilters] = useState({});
  const [filterAnchor, setFilterAnchor] = useState(null); // { id, anchorEl }

  // Cache device details to avoid re-fetching every refresh
  const [detailsByHost, setDetailsByHost] = useState({}); // hostname -> { lastUser, created, createdTs }

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
        };
      });
      setRows(arr);

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
                setDetailsByHost((prev) => ({
                  ...prev,
                  [h]: { lastUser, created: createdRaw, createdTs, type: deviceType },
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
            };
          })
        );
      }
    } catch (e) {
      console.warn("Failed to load agents:", e);
      setRows([]);
    }
  }, [detailsByHost]);

  useEffect(() => {
    fetchAgents();
    const t = setInterval(fetchAgents, 5000);
    return () => clearInterval(t);
  }, [fetchAgents]);

  const filtered = useMemo(() => {
    // Apply simple contains filter per column based on displayed string
    const activeFilters = Object.entries(filters).filter(([, v]) => (v || "").trim() !== "");
    if (!activeFilters.length) return rows;
    const toDisplay = (colId, row) => {
      switch (colId) {
        case "status":
          return row.status || "";
        case "hostname":
          return row.hostname || "";
        case "lastUser":
          return row.lastUser || "";
        case "type":
          return row.type || "";
        case "os":
          return row.os || "";
        case "created":
          return formatCreated(row.created, row.createdTs);
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
      <Box sx={{ p: 2, pb: 1, display: "flex", alignItems: "center", justifyContent: "space-between" }}>
        <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>
          Devices
        </Typography>
        <Box>
          <Button
            variant="outlined"
            size="small"
            disabled={selectedIds.size === 0}
            onClick={() => setQuickJobOpen(true)}
            sx={{
              mr: 1,
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
                  case "lastUser":
                    return <TableCell key={col.id}>{r.lastUser || ""}</TableCell>;
                  case "type":
                    return <TableCell key={col.id}>{r.type || ""}</TableCell>;
                  case "os":
                    return <TableCell key={col.id}>{r.os}</TableCell>;
                  case "created":
                    return (
                      <TableCell key={col.id}>{formatCreated(r.created, r.createdTs)}</TableCell>
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
        <MenuItem onClick={confirmDelete}>Delete</MenuItem>
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
    </Paper>
  );
}
