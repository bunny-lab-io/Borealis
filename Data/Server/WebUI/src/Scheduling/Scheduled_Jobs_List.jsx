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
          try { const d = new Date(Number(ts) * 1000); return d.toLocaleString(); } catch { return ''; }
        };
        return {
          id: j.id,
          name: j.name,
          scriptWorkflow: compName,
          target: targetText,
          occurrence,
          lastRun: '',
          nextRun: fmt(j.start_ts),
          result: 'Success',
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

  React.useEffect(() => { loadJobs(); }, []);
  React.useEffect(() => { loadJobs(); }, [refreshToken]);

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

  const resultColor = (r) =>
    r === "Success" ? "#00d18c" : r === "Warning" ? "#ff8c00" : "#ff4f4f";

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
              ["enabled", "Enabled"],
              ["edit", "Edit Job"]
            ].map(([key, label]) => (
              <TableCell key={key} sortDirection={orderBy === key ? order : false}>
                {key !== "edit" ? (
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
