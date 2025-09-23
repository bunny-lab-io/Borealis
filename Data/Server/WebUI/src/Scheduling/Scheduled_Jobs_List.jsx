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
  IconButton,
  Menu,
  MenuItem,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions
} from "@mui/material";
import { MoreHoriz as MoreHorizIcon } from "@mui/icons-material";

export default function ScheduledJobsList({ onCreateJob, onEditJob, refreshToken }) {
  const [rows, setRows] = useState([]);
  const [orderBy, setOrderBy] = useState("name");
  const [order, setOrder] = useState("asc");
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [menuRow, setMenuRow] = useState(null);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const loadJobs = async () => {
    try {
      const resp = await fetch('/api/scheduled_jobs');
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error || `HTTP ${resp.status}`);
      const rows = (data.jobs || []).map((j) => {
        const compName = (Array.isArray(j.components) && j.components[0]?.name) || "Demonstration Component";
        const targetText = Array.isArray(j.targets) ? `${j.targets.length} device${j.targets.length!==1?'s':''}` : '';
        const occurrence = (j.schedule_type || 'immediately').replace(/^./, (c) => c.toUpperCase());
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

  const sorted = useMemo(() => {
    const dir = order === "asc" ? 1 : -1;
    return [...rows].sort((a, b) => {
      const A = a[orderBy] || "";
      const B = b[orderBy] || "";
      return String(A).localeCompare(String(B)) * dir;
    });
  }, [rows, orderBy, order]);

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
          {['success','running','failed','timed_out','expired','pending']
            .filter(k => s[k])
            .map((k,i) => (
              <span key={k} style={{ marginRight: 10 }}>
                <span style={{ display: 'inline-block', width: 8, height: 8, background: sections.find(x=>x.key===k).color, marginRight: 6 }} />
                {s[k]} {k.replace('_',' ').replace(/^./, c=>c.toUpperCase())}
              </span>
            ))}
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
        <Button
          variant="outlined"
          size="small"
          sx={{ color: "#58a6ff", borderColor: "#58a6ff", textTransform: "none" }}
          onClick={() => onCreateJob && onCreateJob()}
        >
          Create Job
        </Button>
      </Box>
      <Table size="small" sx={{ minWidth: 900 }}>
        <TableHead>
          <TableRow>
            {[
              ["name", "Name"],
              ["scriptWorkflow", "Script / Workflow"],
              ["target", "Target"],
              ["occurrence", "Schedule Occurrence"],
              ["lastRun", "Last Run"],
              ["nextRun", "Next Run"],
            ["result", "Result"],
            ["results", "Results"],
            ["enabled", "Enabled"],
            ["edit", "Edit Job"]
            ].map(([key, label]) => (
              <TableCell key={key} sortDirection={orderBy === key ? order : false}>
                {key !== "edit" && key !== "results" ? (
                  <TableSortLabel
                    active={orderBy === key}
                    direction={orderBy === key ? order : "asc"}
                    onClick={() => handleSort(key)}
                  >
                    {label}
                  </TableSortLabel>
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
              <TableCell>{r.name}</TableCell>
              <TableCell>{r.scriptWorkflow || "Demonstration Component"}</TableCell>
              <TableCell>{r.target}</TableCell>
              <TableCell>{r.occurrence}</TableCell>
              <TableCell>{r.lastRun}</TableCell>
              <TableCell>{r.nextRun}</TableCell>
              <TableCell>
                <span
                  style={{
                    display: "inline-block",
                    width: 10,
                    height: 10,
                    borderRadius: 10,
                    background: resultColor(r.result),
                    marginRight: 8,
                    verticalAlign: "middle"
                  }}
                />
                {r.result}
              </TableCell>
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
              <TableCell>
                <IconButton size="small" onClick={(e) => { setMenuAnchor(e.currentTarget); setMenuRow(r); }} sx={{ color: "#58a6ff" }}>
                  <MoreHorizIcon fontSize="small" />
                </IconButton>
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
      <Menu
        open={Boolean(menuAnchor)}
        anchorEl={menuAnchor}
        onClose={() => { setMenuAnchor(null); setMenuRow(null); }}
        PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" } }}
      >
        <MenuItem onClick={() => {
          const job = menuRow?.raw;
          setMenuAnchor(null); setMenuRow(null);
          if (job && onEditJob) onEditJob(job);
        }}>Edit</MenuItem>
        <MenuItem onClick={() => { setMenuAnchor(null); setDeleteOpen(true); }}>Delete</MenuItem>
      </Menu>

      <Dialog open={deleteOpen} onClose={() => setDeleteOpen(false)}
        PaperProps={{ sx: { bgcolor: '#121212', color: '#fff' } }}
      >
        <DialogTitle>Delete this Job?</DialogTitle>
        <DialogContent>
          <Typography variant="body2" sx={{ color: '#aaa' }}>
            This will permanently remove the scheduled job from the list.
          </Typography>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setDeleteOpen(false)} sx={{ color: '#58a6ff' }}>Cancel</Button>
          <Button onClick={async () => {
            try {
              if (menuRow?.id) {
                await fetch(`/api/scheduled_jobs/${menuRow.id}`, { method: 'DELETE' });
                setRows((prev) => prev.filter((r) => r.id !== menuRow.id));
              }
            } catch {}
            setDeleteOpen(false);
            setMenuRow(null);
            // Optionally reload to be safe
            try { await loadJobs(); } catch {}
          }} variant="outlined" sx={{ color: '#58a6ff', borderColor: '#58a6ff' }}>Delete</Button>
        </DialogActions>
      </Dialog>
    </Paper>
  );
}
