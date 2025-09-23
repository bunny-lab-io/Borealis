////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Scheduled_Jobs_List.jsx

import React, { useState, useMemo } from "react";
import {
  Paper,
  Box,
  Typography,
  Button,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  TableSortLabel,
  Switch,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  Checkbox,
  Popover,
  TextField,
  IconButton
} from "@mui/material";
import FilterListIcon from "@mui/icons-material/FilterList";

export default function ScheduledJobsList({ onCreateJob, onEditJob, refreshToken }) {
  const [rows, setRows] = useState([]);
  const [orderBy, setOrderBy] = useState("name");
  const [order, setOrder] = useState("asc");
  const [selected, setSelected] = useState(new Set());
  const [bulkDeleteOpen, setBulkDeleteOpen] = useState(false);
  const [filters, setFilters] = useState({}); // {name, occurrence, lastRun, nextRun}
  const [filterAnchor, setFilterAnchor] = useState(null); // { id, anchorEl }
  const openFilter = (id) => (e) => setFilterAnchor({ id, anchorEl: e.currentTarget });
  const closeFilter = () => setFilterAnchor(null);
  const onFilterChange = (id) => (e) => setFilters((prev) => ({ ...prev, [id]: e.target.value }));

  const loadJobs = async () => {
    try {
      const resp = await fetch('/api/scheduled_jobs');
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error || `HTTP ${resp.status}`);
      const pretty = (st) => {
        const s = String(st || '').toLowerCase();
        const map = {
          'immediately': 'Immediately',
          'once': 'Once',
          'every_5_minutes': 'Every 5 Minutes',
          'every_10_minutes': 'Every 10 Minutes',
          'every_15_minutes': 'Every 15 Minutes',
          'every_30_minutes': 'Every 30 Minutes',
          'every_hour': 'Every Hour',
          'daily': 'Daily',
          'weekly': 'Weekly',
          'monthly': 'Monthly',
          'yearly': 'Yearly',
        };
        if (map[s]) return map[s];
        try {
          return s.replace(/_/g, ' ').replace(/^./, c => c.toUpperCase());
        } catch { return String(st || ''); }
      };
      const rows = (data.jobs || []).map((j) => {
        const compName = (Array.isArray(j.components) && j.components[0]?.name) || "Demonstration Component";
        const targetText = Array.isArray(j.targets) ? `${j.targets.length} device${j.targets.length!==1?'s':''}` : '';
        const occurrence = pretty(j.schedule_type || 'immediately');
        const fmt = (ts) => {
          if (!ts) return '';
          try {
            const d = new Date(Number(ts) * 1000);
            return d.toLocaleString(undefined, { year:'numeric', month:'2-digit', day:'2-digit', hour:'numeric', minute:'2-digit' });
          } catch { return ''; }
        };
        const result = j.last_status || (j.next_run_ts ? 'Scheduled' : '');
        return {
          id: j.id,
          name: j.name,
          scriptWorkflow: compName,
          target: targetText,
          occurrence,
          lastRun: fmt(j.last_run_ts),
          nextRun: fmt(j.next_run_ts || j.start_ts),
          result,
          resultsCounts: j.result_counts || { pending: (Array.isArray(j.targets)?j.targets.length:0) },
          enabled: !!j.enabled,
          raw: j
        };
      });
      setRows(rows);
    } catch (e) {
      console.warn('Failed to load jobs', e);
      setRows([]);
    }
  };

  // Initial load and polling each 5 seconds for live status updates
  React.useEffect(() => {
    let timer;
    (async () => { try { await loadJobs(); } catch {} })();
    timer = setInterval(loadJobs, 5000);
    return () => { if (timer) clearInterval(timer); };
  }, [refreshToken]);

  const handleSort = (col) => {
    if (orderBy === col) setOrder(order === "asc" ? "desc" : "asc");
    else {
      setOrderBy(col);
      setOrder("asc");
    }
  };

  const filtered = useMemo(() => {
    const f = filters || {};
    const match = (val, q) => String(val || "").toLowerCase().includes(String(q || "").toLowerCase());
    return rows.filter((r) => (
      (!f.name || match(r.name, f.name)) &&
      (!f.occurrence || match(r.occurrence, f.occurrence)) &&
      (!f.lastRun || match(r.lastRun, f.lastRun)) &&
      (!f.nextRun || match(r.nextRun, f.nextRun))
    ));
  }, [rows, filters]);

  const sorted = useMemo(() => {
    const dir = order === "asc" ? 1 : -1;
    return [...filtered].sort((a, b) => {
      const A = a[orderBy] || "";
      const B = b[orderBy] || "";
      return String(A).localeCompare(String(B)) * dir;
    });
  }, [filtered, orderBy, order]);

  // Selection helpers
  const anySelected = selected.size > 0;
  const allSelected = useMemo(() => (sorted.length > 0 && sorted.every(r => selected.has(r.id))), [sorted, selected]);
  const toggleSelect = (id, checked) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (checked) next.add(id); else next.delete(id);
      return next;
    });
  };
  const toggleSelectAll = (checked) => {
    if (checked) {
      setSelected(new Set(sorted.map(r => r.id)));
    } else {
      setSelected(new Set());
    }
  };

  const resultColor = (r) => {
    if (r === 'Success') return '#00d18c';
    if (r === 'Running') return '#58a6ff';
    if (r === 'Scheduled') return '#999999';
    if (r === 'Expired') return '#777777';
    if (r === 'Timed Out') return '#b36ae2';
    if (r === 'Warning') return '#ff8c00';
    if (r === 'Failed') return '#ff4f4f';
    return '#aaaaaa';
  };

  const ResultsBar = ({ counts }) => {
    const total = Math.max(1, Number(counts?.total_targets || 0));
    const seg = (n) => `${Math.round(((n||0)/total)*100)}%`;
    const styleSeg = (bg, w) => ({ display: 'inline-block', height: 8, background: bg, width: w });
    const s = counts || {};
    const sections = [
      { key: 'success', color: '#00d18c' },
      { key: 'running', color: '#58a6ff' },
      { key: 'failed', color: '#ff4f4f' },
      { key: 'timed_out', color: '#b36ae2' },
      { key: 'expired', color: '#777777' },
      { key: 'pending', color: '#999999' }
    ];
    return (
      <div>
        <div style={{ background: '#333', borderRadius: 2, overflow: 'hidden', width: 260 }}>
          {sections.map(({key,color}) => (s[key] ? <span key={key} style={styleSeg(color, seg(s[key]))} /> : null))}
        </div>
        <div style={{ color: '#aaa', fontSize: 12, marginTop: 4 }}>
          {(() => {
            const nonPendingKeys = ['success','running','failed','timed_out','expired'].filter(k => s[k]);
            if (nonPendingKeys.length === 0 && s['pending']) {
              // Pending-only: show simple "Scheduled" label under the bar
              return <span>Scheduled</span>;
            }
            return (
              <>
                {['success','running','failed','timed_out','expired','pending']
                  .filter(k => s[k])
                  .map((k) => (
                    <span key={k} style={{ marginRight: 10 }}>
                      <span style={{ display: 'inline-block', width: 8, height: 8, background: sections.find(x=>x.key===k).color, marginRight: 6 }} />
                      {s[k]} {k === 'pending' ? 'Scheduled' : k.replace('_',' ').replace(/^./, c=>c.toUpperCase())}
                    </span>
                  ))}
              </>
            );
          })()}
        </div>
      </div>
    );
  };

  return (
    <Paper sx={{ m: 2, p: 0, bgcolor: "#1e1e1e" }} elevation={2}>
      <Box
        sx={{
          p: 2,
          pb: 1,
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center"
        }}
      >
        <Box>
          <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>
            Scheduled Jobs
          </Typography>
          <Typography variant="body2" sx={{ color: "#aaa" }}>
            List of automation jobs with schedules, results, and actions.
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', gap: 1, alignItems: 'center' }}>
          <Button
            variant="outlined"
            size="small"
            disabled={!anySelected}
            sx={{ color: anySelected ? "#ff6666" : "#666", borderColor: anySelected ? "#ff6666" : "#444", textTransform: "none" }}
            onClick={() => setBulkDeleteOpen(true)}
          >
            Delete Job
          </Button>
          <Button
            variant="outlined"
            size="small"
            sx={{ color: "#58a6ff", borderColor: "#58a6ff", textTransform: "none" }}
            onClick={() => onCreateJob && onCreateJob()}
          >
            Create Job
          </Button>
        </Box>
      </Box>
      <Table size="small" sx={{ minWidth: 900 }}>
        <TableHead>
          <TableRow>
            <TableCell width={40}>
              <Checkbox
                size="small"
                checked={allSelected}
                indeterminate={!allSelected && anySelected}
                onChange={(e) => toggleSelectAll(e.target.checked)}
              />
            </TableCell>
            {[
              ["name", "Name"],
              ["scriptWorkflow", "Script / Workflow"],
              ["target", "Target"],
              ["occurrence", "Recurrence"],
              ["lastRun", "Last Run"],
              ["nextRun", "Next Run"],
              ["results", "Results"],
              ["enabled", "Enabled"]
            ].map(([key, label]) => (
              <TableCell key={key} sortDirection={orderBy === key ? order : false}>
                {key !== "results" ? (
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                    <TableSortLabel
                      active={orderBy === key}
                      direction={orderBy === key ? order : "asc"}
                      onClick={() => handleSort(key)}
                    >
                      {label}
                    </TableSortLabel>
                    {['name','occurrence','lastRun','nextRun'].includes(key) ? (
                      <IconButton size="small" onClick={openFilter(key)} sx={{ color: filters[key] ? '#58a6ff' : '#888' }}>
                        <FilterListIcon fontSize="inherit" />
                      </IconButton>
                    ) : null}
                  </Box>
                ) : (
                  label
                )}
              </TableCell>
            ))}
          </TableRow>
        </TableHead>
        <TableBody>
          {sorted.map((r, i) => (
            <TableRow key={i} hover>
              <TableCell width={40}>
                <Checkbox size="small" checked={selected.has(r.id)} onChange={(e) => toggleSelect(r.id, e.target.checked)} />
              </TableCell>
              <TableCell>
                <Button onClick={() => { const job = r.raw; if (job && typeof onEditJob === 'function') onEditJob(job); }}
                  sx={{ color: '#58a6ff', textTransform: 'none', p: 0, minWidth: 0 }}
                >
                  {r.name}
                </Button>
              </TableCell>
              <TableCell>{r.scriptWorkflow || "Demonstration Component"}</TableCell>
              <TableCell>{r.target}</TableCell>
              <TableCell>{r.occurrence}</TableCell>
              <TableCell>{r.lastRun}</TableCell>
              <TableCell>{r.nextRun}</TableCell>
              <TableCell>
                <ResultsBar counts={r.resultsCounts} />
              </TableCell>
              <TableCell>
                <Switch
                  checked={r.enabled}
                  onChange={async () => {
                    try {
                      await fetch(`/api/scheduled_jobs/${r.id}/toggle`, {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ enabled: !r.enabled })
                      });
                    } catch {}
                    setRows((prev) => prev.map((job, idx) => idx === i ? { ...job, enabled: !job.enabled } : job));
                  }}
                  size="small"
                />
              </TableCell>
            </TableRow>
          ))}
          {sorted.length === 0 && (
            <TableRow>
              <TableCell colSpan={9} sx={{ color: "#888" }}>
                No scheduled jobs found.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      <Dialog open={bulkDeleteOpen} onClose={() => setBulkDeleteOpen(false)}
        PaperProps={{ sx: { bgcolor: '#121212', color: '#fff' } }}
      >
        <DialogTitle>Are you sure you want to delete this job(s)?</DialogTitle>
        <DialogActions>
          <Button onClick={() => setBulkDeleteOpen(false)} sx={{ color: '#58a6ff' }}>Cancel</Button>
          <Button onClick={async () => {
            try {
              const ids = Array.from(selected);
              await Promise.allSettled(ids.map(id => fetch(`/api/scheduled_jobs/${id}`, { method: 'DELETE' })));
              setRows(prev => prev.filter(r => !selected.has(r.id)));
              setSelected(new Set());
            } catch {}
            setBulkDeleteOpen(false);
            try { await loadJobs(); } catch {}
          }} variant="outlined" sx={{ color: '#58a6ff', borderColor: '#58a6ff' }}>Confirm</Button>
        </DialogActions>
      </Dialog>

      {/* Column filter popover */}
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
              placeholder={`Filter ${(
                {
                  name: 'Name',
                  occurrence: 'Recurrence',
                  lastRun: 'Last Run',
                  nextRun: 'Next Run'
                })[filterAnchor.id] || ''}`}
              value={filters[filterAnchor.id] || ''}
              onChange={onFilterChange(filterAnchor.id)}
              onKeyDown={(e) => { if (e.key === 'Escape') closeFilter(); }}
              sx={{
                input: { color: '#fff' },
                minWidth: 220,
                '& .MuiOutlinedInput-root': { '& fieldset': { borderColor: '#555' }, '&:hover fieldset': { borderColor: '#888' } }
              }}
            />
            <Button
              variant="outlined"
              size="small"
              onClick={() => { setFilters((prev) => ({ ...prev, [filterAnchor.id]: '' })); closeFilter(); }}
              sx={{ textTransform: 'none', borderColor: '#555', color: '#bbb' }}
            >
              Clear
            </Button>
          </Box>
        )}
      </Popover>
    </Paper>
  );
}
