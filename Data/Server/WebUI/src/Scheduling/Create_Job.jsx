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
  TableBody
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
    <Paper sx={{ bgcolor: "#232323", border: "1px solid #333", p: 1, mb: 1 }}>
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
  // Pre-seed with a placeholder component to keep flows working during UI build-out
  const [components, setComponents] = useState([
    { type: "script", path: "demo/component", name: "Demonstration Component", description: "placeholder" }
  ]); // {type:'script'|'workflow', path, name, description}
  const [targets, setTargets] = useState([]); // array of hostnames
  const [scheduleType, setScheduleType] = useState("immediately");
  const [startDateTime, setStartDateTime] = useState(() => dayjs().add(5, "minute"));
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

  useEffect(() => {
    if (initialJob && initialJob.id) {
      setJobName(initialJob.name || "");
      const comps = Array.isArray(initialJob.components) ? initialJob.components : [];
      setComponents(comps.length ? comps : [{ type: "script", path: "demo/component", name: "Demonstration Component", description: "placeholder" }]);
      setTargets(Array.isArray(initialJob.targets) ? initialJob.targets : []);
      setScheduleType(initialJob.schedule_type || initialJob.schedule?.type || "immediately");
      setStartDateTime(initialJob.start_ts ? dayjs(Number(initialJob.start_ts) * 1000) : (initialJob.schedule?.start ? dayjs(initialJob.schedule.start) : dayjs().add(5, "minute")));
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
    if (!node || node.isFolder) return;
    if (compTab === "scripts" && node.script) {
      setComponents((prev) => [
        ...prev,
        { type: "script", path: node.path, name: node.fileName || node.label, description: node.path }
      ]);
    } else if (compTab === "workflows" && node.workflow) {
      setComponents((prev) => [
        ...prev,
        { type: "workflow", path: node.path, name: node.label, description: node.path }
      ]);
    }
    setSelectedNodeId("");
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
      schedule: { type: scheduleType, start: scheduleType !== "immediately" ? (startDateTime?.toISOString?.() || startDateTime) : null },
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

  const tabDefs = useMemo(() => ([
    { key: "name", label: "Job Name" },
    { key: "components", label: "Scripts/Workflows" },
    { key: "targets", label: "Targets" },
    { key: "schedule", label: "Schedule" },
    { key: "context", label: "Execution Context" }
  ]), []);

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
                      onChange={(val) => setStartDateTime(val)}
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
              <SimpleTreeView onItemSelectionToggle={(_, id) => setSelectedNodeId(id)}>
                {scriptTree.length ? scriptTree.map((n) => (
                  <TreeItem key={n.id} itemId={n.id} label={n.label}>
                    {n.children?.map((c) => (
                      <TreeItem key={c.id} itemId={c.id} label={c.label} />
                    ))}
                  </TreeItem>
                )) : (
                  <Typography variant="body2" sx={{ color: "#888", p: 1 }}>No scripts found.</Typography>
                )}
              </SimpleTreeView>
            </Paper>
          )}
          {compTab === "workflows" && (
            <Paper sx={{ p: 1, bgcolor: "#1e1e1e", maxHeight: 400, overflow: "auto" }}>
              <SimpleTreeView onItemSelectionToggle={(_, id) => setSelectedNodeId(id)}>
                {workflowTree.length ? workflowTree.map((n) => (
                  <TreeItem key={n.id} itemId={n.id} label={n.label}>
                    {n.children?.map((c) => (
                      <TreeItem key={c.id} itemId={c.id} label={c.label} />
                    ))}
                  </TreeItem>
                )) : (
                  <Typography variant="body2" sx={{ color: "#888", p: 1 }}>No workflows found.</Typography>
                )}
              </SimpleTreeView>
            </Paper>
          )}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setAddCompOpen(false)} sx={{ color: "#58a6ff" }}>Close</Button>
          <Button onClick={() => { addSelectedComponent(); setAddCompOpen(false); }}
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
