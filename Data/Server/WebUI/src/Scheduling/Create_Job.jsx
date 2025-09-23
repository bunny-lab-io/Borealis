import React, { useEffect, useMemo, useState, useCallback } from "react";
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
  FormControlLabel,
  Select,
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
  TableSortLabel
} from "@mui/material";
import { Add as AddIcon, Delete as DeleteIcon } from "@mui/icons-material";
import { SimpleTreeView, TreeItem } from "@mui/x-tree-view";
import { LocalizationProvider } from "@mui/x-date-pickers/LocalizationProvider";
import { DateTimePicker } from "@mui/x-date-pickers/DateTimePicker";
import { AdapterDayjs } from "@mui/x-date-pickers/AdapterDayjs";
import dayjs from "dayjs";

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
        node = { id: path, label: isFile ? s.file_name : part, path, isFolder: !isFile, fileName: s.file_name, script: isFile ? s : null, children: [] };
        children.push(node); map[path] = node;
      }
      if (!isFile) { children = node.children; parentPath = path; }
    });
  });
  return { root: [rootNode], map };
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

function ComponentCard({ comp, onRemove }) {
  return (
    <Paper sx={{ bgcolor: "#2a2a2a", border: "1px solid #3a3a3a", p: 1.2, mb: 1.2, borderRadius: 1 }}>
      <Box sx={{ display: "flex", gap: 2 }}>
        <Box sx={{ flex: 1 }}>
          <Typography variant="subtitle2" sx={{ color: "#e6edf3" }}>
            {comp.type === "script" ? comp.name : comp.name}
          </Typography>
          <Typography variant="body2" sx={{ color: "#aaa" }}>
            {comp.description || (comp.type === "script" ? comp.path : comp.path)}
          </Typography>
        </Box>
        <Divider orientation="vertical" flexItem sx={{ borderColor: "#333" }} />
        <Box sx={{ flex: 1 }}>
          <Typography variant="subtitle2" sx={{ color: "#ccc", mb: 1 }}>Variables (placeholder)</Typography>
          <FormControlLabel control={<Checkbox size="small" />} label={<Typography variant="body2">Example toggle</Typography>} />
          <Box sx={{ display: "flex", gap: 1, mt: 1 }}>
            <TextField size="small" label="Param A" variant="outlined" fullWidth
              InputLabelProps={{ shrink: true }}
              sx={{
                "& .MuiOutlinedInput-root": { bgcolor: "#1b1b1b" },
                "& .MuiInputLabel-root": { color: "#aaa" },
                "& .MuiInputBase-input": { color: "#e6edf3" }
              }}
            />
            <Select size="small" value={"install"} sx={{ minWidth: 160 }}>
              <MenuItem value="install">Install/Update existing installation</MenuItem>
              <MenuItem value="uninstall">Uninstall</MenuItem>
            </Select>
          </Box>
        </Box>
        <Box>
          <IconButton onClick={onRemove} size="small" sx={{ color: "#ff6666" }}>
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
  // Components the job will run: {type:'script'|'workflow', path, name, description}
  const [components, setComponents] = useState([]);
  const [targets, setTargets] = useState([]); // array of hostnames
  const [scheduleType, setScheduleType] = useState("immediately");
  const [startDateTime, setStartDateTime] = useState(() => dayjs().add(5, "minute").second(0));
  const [stopAfterEnabled, setStopAfterEnabled] = useState(false);
  const [expiration, setExpiration] = useState("no_expire");
  const [execContext, setExecContext] = useState("system");

  // dialogs state
  const [addCompOpen, setAddCompOpen] = useState(false);
  const [compTab, setCompTab] = useState("scripts");
  const [scriptTree, setScriptTree] = useState([]); const [scriptMap, setScriptMap] = useState({});
  const [workflowTree, setWorkflowTree] = useState([]); const [workflowMap, setWorkflowMap] = useState({});
  const [selectedNodeId, setSelectedNodeId] = useState("");

  const [addTargetOpen, setAddTargetOpen] = useState(false);
  const [availableDevices, setAvailableDevices] = useState([]); // [{hostname, display, online}]
  const [selectedTargets, setSelectedTargets] = useState({}); // map hostname->bool
  const [deviceSearch, setDeviceSearch] = useState("");

  const isValid = useMemo(() => {
    const base = jobName.trim().length > 0 && components.length > 0 && targets.length > 0;
    if (!base) return false;
    if (scheduleType !== "immediately") {
      return !!startDateTime;
    }
    return true;
  }, [jobName, components.length, targets.length, scheduleType, startDateTime]);

  const [confirmOpen, setConfirmOpen] = useState(false);
  const editing = !!(initialJob && initialJob.id);

  // --- Job History (only when editing) ---
  const [historyRows, setHistoryRows] = useState([]);
  const [historyOrderBy, setHistoryOrderBy] = useState("started_ts");
  const [historyOrder, setHistoryOrder] = useState("desc");

  const fmtTs = useCallback((ts) => {
    if (!ts) return '';
    try {
      const d = new Date(Number(ts) * 1000);
      return d.toLocaleString(undefined, { year:'numeric', month:'2-digit', day:'2-digit', hour:'numeric', minute:'2-digit' });
    } catch { return ''; }
  }, []);

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
      setDeviceRows(Array.isArray(dev.devices) ? dev.devices : []);
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

  const sortedHistory = useMemo(() => {
    const dir = historyOrder === 'asc' ? 1 : -1;
    const key = historyOrderBy;
    return [...historyRows].sort((a, b) => {
      const A = a?.[key];
      const B = b?.[key];
      if (key === 'started_ts' || key === 'finished_ts' || key === 'scheduled_ts') {
        return ((A || 0) - (B || 0)) * dir;
      }
      return String(A ?? '').localeCompare(String(B ?? '')) * dir;
    });
  }, [historyRows, historyOrderBy, historyOrder]);

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
            <TableRow key={r.id}>
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
  const sumCounts = (o, k) => Number((o?.result_counts||{})[k] || 0);
  const counts = jobSummary?.result_counts || {};

  const ProgressSummary = () => (
    <Box sx={{ p: 2, bgcolor: '#1b1b1b', border: '1px solid #333', borderRadius: 1, mb: 2 }}>
      <Typography variant="subtitle1" sx={{ color: '#7db7ff', mb: 1 }}>Job Progress</Typography>
      <Box sx={{ display: 'flex', gap: 3, flexWrap: 'wrap' }}>
        {[
          ['pending','Pending','#999999'],
          ['running','Running','#58a6ff'],
          ['success','Success','#00d18c'],
          ['failed','Failed','#ff4f4f'],
          ['expired','Expired','#777777'],
          ['timed_out','Timed Out','#b36ae2']
        ].map(([key,label,color]) => (
          <Box key={key} sx={{ color: '#ddd', display: 'flex', alignItems: 'center', gap: 1 }}>
            <span style={{ display:'inline-block', width:10, height:10, borderRadius:10, background: color }} />
            <span>{label}: {Number((counts||{})[key] || 0)}</span>
          </Box>
        ))}
      </Box>
    </Box>
  );

  // --- Devices breakdown ---
  const [deviceRows, setDeviceRows] = useState([]);
  const deviceSorted = useMemo(() => deviceRows, [deviceRows]);

  useEffect(() => {
    if (initialJob && initialJob.id) {
      setJobName(initialJob.name || "");
      const comps = Array.isArray(initialJob.components) ? initialJob.components : [];
      setComponents(comps.length ? comps : []);
      setTargets(Array.isArray(initialJob.targets) ? initialJob.targets : []);
      setScheduleType(initialJob.schedule_type || initialJob.schedule?.type || "immediately");
      setStartDateTime(initialJob.start_ts ? dayjs(Number(initialJob.start_ts) * 1000).second(0) : (initialJob.schedule?.start ? dayjs(initialJob.schedule.start).second(0) : dayjs().add(5, "minute").second(0)));
      setStopAfterEnabled(Boolean(initialJob.duration_stop_enabled));
      setExpiration(initialJob.expiration || "no_expire");
      setExecContext(initialJob.execution_context || "system");
    }
  }, [initialJob]);

  const openAddComponent = async () => {
    setAddCompOpen(true);
    try {
      // scripts
      const sResp = await fetch("/api/scripts/list");
      if (sResp.ok) {
        const sData = await sResp.json();
        const { root, map } = buildScriptTree(sData.scripts || [], sData.folders || []);
        setScriptTree(root); setScriptMap(map);
      } else { setScriptTree([]); setScriptMap({}); }
    } catch { setScriptTree([]); setScriptMap({}); }
    try {
      // workflows
      const wResp = await fetch("/api/storage/load_workflows");
      if (wResp.ok) {
        const wData = await wResp.json();
        const { root, map } = buildWorkflowTree(wData.workflows || [], wData.folders || []);
        setWorkflowTree(root); setWorkflowMap(map);
      } else { setWorkflowTree([]); setWorkflowMap({}); }
    } catch { setWorkflowTree([]); setWorkflowMap({}); }
  };

  const addSelectedComponent = () => {
    const map = compTab === "scripts" ? scriptMap : workflowMap;
    const node = map[selectedNodeId];
    if (!node || node.isFolder) return false;
    if (compTab === "scripts" && node.script) {
      setComponents((prev) => [
        ...prev,
        { type: "script", path: node.path, name: node.fileName || node.label, description: node.path }
      ]);
      setSelectedNodeId("");
      return true;
    } else if (compTab === "workflows" && node.workflow) {
      alert("Workflows within Scheduled Jobs are not supported yet");
      return false;
    }
    setSelectedNodeId("");
    return false;
  };

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
    const payload = {
      name: jobName,
      components,
      targets,
      schedule: { type: scheduleType, start: scheduleType !== "immediately" ? (() => { try { const d = startDateTime?.toDate?.() || new Date(startDateTime); d.setSeconds(0,0); return d.toISOString(); } catch { return startDateTime; } })() : null },
      duration: { stopAfterEnabled, expiration },
      execution_context: execContext
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
      { key: "components", label: "Scripts/Workflows" },
      { key: "targets", label: "Targets" },
      { key: "schedule", label: "Schedule" },
      { key: "context", label: "Execution Context" }
    ];
    if (editing) base.push({ key: 'history', label: 'Job History' });
    return base;
  }, [editing]);

  return (
    <Paper sx={{ m: 2, p: 0, bgcolor: "#1e1e1e" }} elevation={2}>
      <Box sx={{ p: 2, pb: 1 }}>
        <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>Create a Job</Typography>
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
              InputLabelProps={{ shrink: true }}
              error={jobName.trim().length === 0}
              helperText={jobName.trim().length === 0 ? "Job name is required" : ""}
            />
          </Box>
        )}

        {tab === 1 && (
          <Box>
            <SectionHeader
              title="Components"
              action={(
                <Button size="small" startIcon={<AddIcon />} onClick={openAddComponent}
                  sx={{ color: "#58a6ff", borderColor: "#58a6ff" }} variant="outlined">
                  Add Component
                </Button>
              )}
            />
            {components.length === 0 && (
              <Typography variant="body2" sx={{ color: "#888" }}>No components added yet.</Typography>
            )}
            {components.map((c, idx) => (
              <ComponentCard key={`${c.type}-${c.path}-${idx}`} comp={c}
                onRemove={() => setComponents((prev) => prev.filter((_, i) => i !== idx))}
              />
            ))}
            {components.length === 0 && (
              <Typography variant="caption" sx={{ color: "#ff6666" }}>At least one component is required.</Typography>
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
            <Select size="small" value={execContext} onChange={(e) => setExecContext(e.target.value)} sx={{ minWidth: 280 }}>
              <MenuItem value="system">Run as SYSTEM Account</MenuItem>
              <MenuItem value="current_user">Run as the Logged-In User</MenuItem>
            </Select>
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
              <ProgressSummary />
            </Box>

            <Box sx={{ mt: 2 }}>
              <Typography variant="subtitle1" sx={{ color: '#7db7ff', mb: 0.5 }}>Devices</Typography>
              <Typography variant="caption" sx={{ color: '#aaa' }}>Devices targeted by this scheduled job.  Individual job history is listed here.</Typography>
              <Table size="small">
                <TableHead>
                  <TableRow>
                    <TableCell>Hostname</TableCell>
                    <TableCell>Status</TableCell>
                    <TableCell>Site</TableCell>
                    <TableCell>Ran On</TableCell>
                    <TableCell>Job Status</TableCell>
                    <TableCell>StdOut / StdErr</TableCell>
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
                          {d.has_stdout ? <Button size="small" sx={{ color:'#58a6ff', textTransform:'none', minWidth:0, p:0 }}>StdOut</Button> : null}
                          {d.has_stderr ? <Button size="small" sx={{ color:'#ff4f4f', textTransform:'none', minWidth:0, p:0 }}>StdErr</Button> : null}
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

      {/* Bottom actions removed per design; actions live next to tabs. */}

      {/* Add Component Dialog */}
      <Dialog open={addCompOpen} onClose={() => setAddCompOpen(false)} fullWidth maxWidth="md"
        PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}
      >
        <DialogTitle>Select a Script or Workflow</DialogTitle>
        <DialogContent>
          <Box sx={{ display: "flex", gap: 2, mb: 1 }}>
            <Button size="small" variant={compTab === "scripts" ? "outlined" : "text"} onClick={() => setCompTab("scripts")}
              sx={{ textTransform: "none", color: "#58a6ff", borderColor: "#58a6ff" }}>
              Scripts
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
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setAddCompOpen(false)} sx={{ color: "#58a6ff" }}>Close</Button>
          <Button onClick={() => { const ok = addSelectedComponent(); if (ok) setAddCompOpen(false); }}
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
