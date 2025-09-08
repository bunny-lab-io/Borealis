import React, { useEffect, useMemo, useState, useCallback, useRef } from "react";
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
  Popover,
  TextField,
  MenuItem
} from "@mui/material";
import AddIcon from "@mui/icons-material/Add";
import DeleteIcon from "@mui/icons-material/DeleteOutline";
import FilterListIcon from "@mui/icons-material/FilterList";
import ViewColumnIcon from "@mui/icons-material/ViewColumn";
import { CreateSiteDialog, ConfirmDeleteDialog } from "../Dialogs.jsx";

export default function SiteList({ onOpenDevicesForSite }) {
  const [rows, setRows] = useState([]); // {id, name, description, device_count}
  const [orderBy, setOrderBy] = useState("name");
  const [order, setOrder] = useState("asc");
  const [selectedIds, setSelectedIds] = useState(() => new Set());

  // Columns configuration (similar style to Device_List)
  const COL_LABELS = useMemo(() => ({
    name: "Name",
    description: "Description",
    device_count: "Devices",
  }), []);
  const defaultColumns = useMemo(
    () => [
      { id: "name", label: COL_LABELS.name },
      { id: "description", label: COL_LABELS.description },
      { id: "device_count", label: COL_LABELS.device_count },
    ],
    [COL_LABELS]
  );
  const [columns, setColumns] = useState(defaultColumns);
  const dragColId = useRef(null);
  const [colChooserAnchor, setColChooserAnchor] = useState(null);

  const [filters, setFilters] = useState({});
  const [filterAnchor, setFilterAnchor] = useState(null); // { id, anchorEl }

  const [createOpen, setCreateOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const fetchSites = useCallback(async () => {
    try {
      const res = await fetch("/api/sites");
      const data = await res.json();
      setRows(Array.isArray(data?.sites) ? data.sites : []);
    } catch {
      setRows([]);
    }
  }, []);

  useEffect(() => { fetchSites(); }, [fetchSites]);

  const handleSort = (col) => {
    if (orderBy === col) setOrder(order === "asc" ? "desc" : "asc");
    else { setOrderBy(col); setOrder("asc"); }
  };

  const filtered = useMemo(() => {
    if (!filters || Object.keys(filters).length === 0) return rows;
    return rows.filter((r) =>
      Object.entries(filters).every(([k, v]) => {
        const val = String(v || "").toLowerCase();
        if (!val) return true;
        return String(r[k] ?? "").toLowerCase().includes(val);
      })
    );
  }, [rows, filters]);

  const sorted = useMemo(() => {
    const dir = order === "asc" ? 1 : -1;
    const arr = [...filtered];
    arr.sort((a, b) => {
      if (orderBy === "device_count") return ((a.device_count||0) - (b.device_count||0)) * dir;
      return String(a[orderBy] ?? "").localeCompare(String(b[orderBy] ?? "")) * dir;
    });
    return arr;
  }, [filtered, orderBy, order]);

  const onHeaderDragStart = (colId) => (e) => { dragColId.current = colId; try { e.dataTransfer.setData("text/plain", colId); } catch {} };
  const onHeaderDragOver = (e) => { e.preventDefault(); };
  const onHeaderDrop = (targetColId) => (e) => {
    e.preventDefault();
    const fromId = dragColId.current; if (!fromId || fromId === targetColId) return;
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

  const openFilter = (id) => (e) => setFilterAnchor({ id, anchorEl: e.currentTarget });
  const closeFilter = () => setFilterAnchor(null);
  const onFilterChange = (id) => (e) => setFilters((prev) => ({ ...prev, [id]: e.target.value }));

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
      if (checked) next.add(id); else next.delete(id);
      return next;
    });
  };

  return (
    <Paper sx={{ m: 2, p: 0, bgcolor: "#1e1e1e" }} elevation={2}>
      <Box sx={{ p: 2, pb: 1, display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>Sites</Typography>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <Button
            variant="outlined"
            size="small"
            startIcon={<DeleteIcon />}
            disabled={selectedIds.size === 0}
            onClick={() => setDeleteOpen(true)}
            sx={{ color: selectedIds.size ? '#ff8a8a' : '#666', borderColor: selectedIds.size ? '#ff4f4f' : '#333', textTransform: 'none' }}
          >
            Delete
          </Button>
          <Button
            variant="outlined"
            size="small"
            startIcon={<AddIcon />}
            onClick={() => setCreateOpen(true)}
            sx={{ color: '#58a6ff', borderColor: '#58a6ff', textTransform: 'none' }}
          >
            Create Site
          </Button>
        </Box>
      </Box>

      <Table size="small" sx={{ minWidth: 700 }}>
        <TableHead>
          <TableRow>
            <TableCell padding="checkbox">
              <Checkbox indeterminate={isIndeterminate} checked={isAllChecked} onChange={toggleAll} sx={{ color: '#777' }} />
            </TableCell>
            {columns.map((col) => (
              <TableCell key={col.id} sortDirection={orderBy === col.id ? order : false} draggable onDragStart={onHeaderDragStart(col.id)} onDragOver={onHeaderDragOver} onDrop={onHeaderDrop(col.id)}>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                  <TableSortLabel active={orderBy === col.id} direction={orderBy === col.id ? order : 'asc'} onClick={() => handleSort(col.id)}>
                    {col.label}
                  </TableSortLabel>
                  <IconButton size="small" onClick={openFilter(col.id)} sx={{ color: filters[col.id] ? '#58a6ff' : '#888' }}>
                    <FilterListIcon fontSize="inherit" />
                  </IconButton>
                </Box>
              </TableCell>
            ))}
          </TableRow>
        </TableHead>
        <TableBody>
          {sorted.map((r) => (
            <TableRow key={r.id} hover>
              <TableCell padding="checkbox" onClick={(e) => e.stopPropagation()}>
                <Checkbox checked={selectedIds.has(r.id)} onChange={toggleOne(r.id)} sx={{ color: '#777' }} />
              </TableCell>
              {columns.map((col) => {
                switch (col.id) {
                  case 'name':
                    return (
                      <TableCell
                        key={col.id}
                        onClick={() => {
                          if (onOpenDevicesForSite) onOpenDevicesForSite(r.name);
                        }}
                        sx={{ color: '#58a6ff', '&:hover': { cursor: 'pointer', textDecoration: 'underline' } }}
                      >
                        {r.name}
                      </TableCell>
                    );
                  case 'description':
                    return <TableCell key={col.id}>{r.description || ''}</TableCell>;
                  case 'device_count':
                    return <TableCell key={col.id}>{r.device_count ?? 0}</TableCell>;
                  default:
                    return <TableCell key={col.id} />;
                }
              })}
            </TableRow>
          ))}
          {sorted.length === 0 && (
            <TableRow>
              <TableCell colSpan={columns.length + 1} sx={{ color: '#888' }}>No sites defined.</TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>

      {/* Column chooser */}
      <Popover
        open={Boolean(colChooserAnchor)}
        anchorEl={colChooserAnchor}
        onClose={() => setColChooserAnchor(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        PaperProps={{ sx: { bgcolor: '#1e1e1e', color: '#fff', p: 1 } }}
      >
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5, p: 1 }}>
          {[
            { id: 'name', label: 'Name' },
            { id: 'description', label: 'Description' },
            { id: 'device_count', label: 'Devices' },
          ].map((opt) => (
            <MenuItem key={opt.id} disableRipple onClick={(e) => e.stopPropagation()} sx={{ gap: 1 }}>
              <Checkbox
                size="small"
                checked={columns.some((c) => c.id === opt.id)}
                onChange={(e) => {
                  const checked = e.target.checked;
                  setColumns((prev) => {
                    const exists = prev.some((c) => c.id === opt.id);
                    if (checked) {
                      if (exists) return prev;
                      return [...prev, { id: opt.id, label: opt.label }];
                    }
                    return prev.filter((c) => c.id !== opt.id);
                  });
                }}
                sx={{ p: 0.3, color: '#bbb' }}
              />
              <Typography variant="body2" sx={{ color: '#ddd' }}>{opt.label}</Typography>
            </MenuItem>
          ))}
          <Box sx={{ display: 'flex', gap: 1, pt: 0.5 }}>
            <Button size="small" variant="outlined" onClick={() => setColumns(defaultColumns)} sx={{ textTransform: 'none', borderColor: '#555', color: '#bbb' }}>
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
        anchorOrigin={{ vertical: 'bottom', horizontal: 'left' }}
        PaperProps={{ sx: { bgcolor: '#1e1e1e', p: 1 } }}
      >
        {filterAnchor && (
          <Box sx={{ display: 'flex', gap: 1, alignItems: 'center' }}>
            <TextField
              autoFocus
              size="small"
              placeholder={`Filter ${columns.find((c) => c.id === filterAnchor.id)?.label || ''}`}
              value={filters[filterAnchor.id] || ''}
              onChange={onFilterChange(filterAnchor.id)}
              onKeyDown={(e) => { if (e.key === 'Escape') closeFilter(); }}
              sx={{
                input: { color: '#fff' },
                minWidth: 220,
                '& .MuiOutlinedInput-root': { '& fieldset': { borderColor: '#555' }, '&:hover fieldset': { borderColor: '#888' } },
              }}
            />
            <Button variant="outlined" size="small" onClick={() => { setFilters((prev) => ({ ...prev, [filterAnchor.id]: '' })); closeFilter(); }} sx={{ textTransform: 'none', borderColor: '#555', color: '#bbb' }}>
              Clear
            </Button>
          </Box>
        )}
      </Popover>

      {/* Create site dialog */}
      <CreateSiteDialog
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        onCreate={async (name, description) => {
          try {
            const res = await fetch('/api/sites', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name, description }) });
            if (!res.ok) return;
            setCreateOpen(false);
            await fetchSites();
          } catch {}
        }}
      />

      {/* Delete confirmation */}
      <ConfirmDeleteDialog
        open={deleteOpen}
        message={`Delete ${selectedIds.size} selected site(s)? This cannot be undone.`}
        onCancel={() => setDeleteOpen(false)}
        onConfirm={async () => {
          try {
            const ids = Array.from(selectedIds);
            await fetch('/api/sites/delete', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ids }) });
          } catch {}
          setDeleteOpen(false);
          setSelectedIds(new Set());
          await fetchSites();
        }}
      />
    </Paper>
  );
}

