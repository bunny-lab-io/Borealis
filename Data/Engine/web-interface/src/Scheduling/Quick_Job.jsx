import React, { useEffect, useState, useCallback, useMemo, useRef } from "react";
import {
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  Button,
  Box,
  Typography,
  Paper,
  FormControlLabel,
  Checkbox,
  TextField,
  FormControl,
  InputLabel,
  Select,
  MenuItem,
  CircularProgress,
  Chip
} from "@mui/material";
import { Folder as FolderIcon, Description as DescriptionIcon } from "@mui/icons-material";
import { SimpleTreeView, TreeItem } from "@mui/x-tree-view";
import { DomainBadge } from "../Assemblies/Assembly_Badges";
import {
  buildAssemblyIndex,
  buildAssemblyTree,
  normalizeAssemblyPath,
  parseAssemblyExport
} from "../Assemblies/assemblyUtils";

const DIALOG_SHELL_SX = {
  backgroundImage: "linear-gradient(120deg,#040711 0%,#0b1222 55%,#020617 100%)",
  border: "1px solid rgba(148,163,184,0.35)",
  boxShadow: "0 28px 60px rgba(2,6,12,0.65)",
  borderRadius: 3,
  color: "#e2e8f0",
  overflow: "hidden"
};

const GLASS_PANEL_SX = {
  backgroundColor: "rgba(15,23,42,0.78)",
  border: "1px solid rgba(148,163,184,0.35)",
  borderRadius: 3,
  boxShadow: "0 16px 40px rgba(2,6,15,0.45)",
  backdropFilter: "blur(22px)"
};

const PRIMARY_PILL_GRADIENT = "linear-gradient(135deg,#34d399,#22d3ee)";
const SECONDARY_PILL_GRADIENT = "linear-gradient(135deg,#7dd3fc,#c084fc)";

export default function QuickJob({ open, onClose, hostnames = [] }) {
  const [assemblyPayload, setAssemblyPayload] = useState({ items: [], queue: [] });
  const [assembliesLoading, setAssembliesLoading] = useState(false);
  const [assembliesError, setAssembliesError] = useState("");
  const [selectedAssemblyGuid, setSelectedAssemblyGuid] = useState("");
  const [running, setRunning] = useState(false);
  const [error, setError] = useState("");
  const [runAsCurrentUser, setRunAsCurrentUser] = useState(false);
  const [mode, setMode] = useState("scripts"); // 'scripts' | 'ansible'
  const [credentials, setCredentials] = useState([]);
  const [credentialsLoading, setCredentialsLoading] = useState(false);
  const [credentialsError, setCredentialsError] = useState("");
  const [selectedCredentialId, setSelectedCredentialId] = useState("");
  const [useSvcAccount, setUseSvcAccount] = useState(true);
  const [variables, setVariables] = useState([]);
  const [variableValues, setVariableValues] = useState({});
  const [variableErrors, setVariableErrors] = useState({});
  const [variableStatus, setVariableStatus] = useState({ loading: false, error: "" });
  const assemblyExportCacheRef = useRef(new Map());

  const loadAssemblies = useCallback(async () => {
    setAssembliesLoading(true);
    setAssembliesError("");
    try {
      const resp = await fetch("/api/assemblies");
      if (!resp.ok) {
        const detail = await resp.text();
        throw new Error(detail || `HTTP ${resp.status}`);
      }
      const data = await resp.json();
      assemblyExportCacheRef.current.clear();
      setAssemblyPayload({
        items: Array.isArray(data?.items) ? data.items : [],
        queue: Array.isArray(data?.queue) ? data.queue : []
      });
    } catch (err) {
      console.error("Failed to load assemblies:", err);
      setAssemblyPayload({ items: [], queue: [] });
      setAssembliesError(err?.message || "Failed to load assemblies");
    } finally {
      setAssembliesLoading(false);
    }
  }, []);

  const assemblyIndex = useMemo(
    () => buildAssemblyIndex(assemblyPayload.items, assemblyPayload.queue),
    [assemblyPayload.items, assemblyPayload.queue]
  );

  const scriptTreeData = useMemo(
    () => buildAssemblyTree(assemblyIndex.grouped?.scripts || [], { rootLabel: "Scripts" }),
    [assemblyIndex]
  );

  const ansibleTreeData = useMemo(
    () => buildAssemblyTree(assemblyIndex.grouped?.ansible || [], { rootLabel: "Ansible Playbooks" }),
    [assemblyIndex]
  );

  const selectedAssembly = useMemo(() => {
    if (!selectedAssemblyGuid) return null;
    const guid = selectedAssemblyGuid.toLowerCase();
    return assemblyIndex.byGuid?.get(guid) || null;
  }, [selectedAssemblyGuid, assemblyIndex]);

  const loadAssemblyExport = useCallback(
    async (assemblyGuid) => {
      const cacheKey = assemblyGuid.toLowerCase();
      if (assemblyExportCacheRef.current.has(cacheKey)) {
        return assemblyExportCacheRef.current.get(cacheKey);
      }
      const resp = await fetch(`/api/assemblies/${encodeURIComponent(assemblyGuid)}/export`);
      if (!resp.ok) {
        throw new Error(`Failed to load assembly (HTTP ${resp.status})`);
      }
      const data = await resp.json();
      assemblyExportCacheRef.current.set(cacheKey, data);
      return data;
    },
    []
  );

  useEffect(() => {
    if (!open) {
      setSelectedAssemblyGuid("");
      return;
    }
    setSelectedAssemblyGuid("");
    setError("");
    setVariables([]);
    setVariableValues({});
    setVariableErrors({});
    setVariableStatus({ loading: false, error: "" });
    setUseSvcAccount(true);
    setSelectedCredentialId("");
    if (!assemblyPayload.items.length && !assembliesLoading) {
      loadAssemblies();
    }
  }, [open, loadAssemblies, assemblyPayload.items.length, assembliesLoading]);

  useEffect(() => {
    if (!open) return;
    setSelectedAssemblyGuid("");
    setVariables([]);
    setVariableValues({});
    setVariableErrors({});
    setVariableStatus({ loading: false, error: "" });
  }, [mode, open]);

  useEffect(() => {
    if (!open || mode !== "ansible") return;
    let canceled = false;
    setCredentialsLoading(true);
    setCredentialsError("");
    (async () => {
      try {
        const resp = await fetch("/api/credentials");
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        const data = await resp.json();
        if (canceled) return;
        const list = Array.isArray(data?.credentials)
          ? data.credentials.filter((cred) => {
              const conn = String(cred.connection_type || "").toLowerCase();
              return conn === "ssh" || conn === "winrm";
            })
          : [];
        list.sort((a, b) => String(a?.name || "").localeCompare(String(b?.name || "")));
        setCredentials(list);
      } catch (err) {
        if (!canceled) {
          setCredentials([]);
          setCredentialsError(String(err.message || err));
        }
      } finally {
        if (!canceled) setCredentialsLoading(false);
      }
    })();
    return () => {
      canceled = true;
    };
  }, [open, mode]);

  useEffect(() => {
    if (!open) {
      setSelectedCredentialId("");
    }
  }, [open]);

  useEffect(() => {
    if (mode !== "ansible" || useSvcAccount) return;
    if (!credentials.length) {
      setSelectedCredentialId("");
      return;
    }
    if (!selectedCredentialId || !credentials.some((cred) => String(cred.id) === String(selectedCredentialId))) {
      setSelectedCredentialId(String(credentials[0].id));
    }
  }, [mode, credentials, selectedCredentialId, useSvcAccount]);

  const renderNodes = (nodes = []) =>
    nodes.map((n) => (
      <TreeItem
        key={n.id}
        itemId={n.id}
        label={
          <Box sx={{ display: "flex", alignItems: "center" }}>
            {n.isFolder ? (
              <FolderIcon fontSize="small" sx={{ mr: 1, color: "#ccc" }} />
            ) : (
              <DescriptionIcon fontSize="small" sx={{ mr: 1, color: "#ccc" }} />
            )}
            <Typography variant="body2" sx={{ color: "#e6edf3" }}>{n.label}</Typography>
          </Box>
        }
      >
        {n.children && n.children.length ? renderNodes(n.children) : null}
      </TreeItem>
    ));

  const onItemSelect = useCallback(
    (_e, itemId) => {
      const treeData = mode === "ansible" ? ansibleTreeData : scriptTreeData;
      const node = treeData.map[itemId];
      if (node && !node.isFolder && node.assemblyGuid) {
        setSelectedAssemblyGuid(node.assemblyGuid);
        setError("");
        setVariableErrors({});
      }
    },
    [mode, ansibleTreeData, scriptTreeData]
  );

  const deriveInitialValue = (variable) => {
    const { type, default: defaultValue } = variable;
    if (type === "boolean") {
      if (typeof defaultValue === "boolean") return defaultValue;
      if (defaultValue == null) return false;
      const str = String(defaultValue).trim().toLowerCase();
      if (!str) return false;
      return ["true", "1", "yes", "on"].includes(str);
    }
    if (type === "number") {
      if (defaultValue == null || defaultValue === "") return "";
      if (typeof defaultValue === "number" && Number.isFinite(defaultValue)) {
        return String(defaultValue);
      }
      const parsed = Number(defaultValue);
      return Number.isFinite(parsed) ? String(parsed) : "";
    }
    return defaultValue == null ? "" : String(defaultValue);
  };

  useEffect(() => {
    if (!selectedAssemblyGuid) {
      setVariables([]);
      setVariableValues({});
      setVariableErrors({});
      setVariableStatus({ loading: false, error: "" });
      return;
    }
    let canceled = false;
    (async () => {
      setVariableStatus({ loading: true, error: "" });
      try {
        const exportDoc = await loadAssemblyExport(selectedAssemblyGuid);
        if (canceled) return;
        const parsed = parseAssemblyExport(exportDoc);
        const defs = Array.isArray(parsed.variables) ? parsed.variables : [];
        setVariables(defs);
        const initialValues = {};
        defs.forEach((v) => {
          if (!v || !v.name) return;
          initialValues[v.name] = deriveInitialValue(v);
        });
        setVariableValues(initialValues);
        setVariableErrors({});
        setVariableStatus({ loading: false, error: "" });
      } catch (err) {
        if (canceled) return;
        setVariables([]);
        setVariableValues({});
        setVariableErrors({});
        setVariableStatus({ loading: false, error: err?.message || String(err) });
      }
    })();
    return () => {
      canceled = true;
    };
  }, [selectedAssemblyGuid, loadAssemblyExport]);

  const handleVariableChange = (variable, rawValue) => {
    const { name, type } = variable;
    if (!name) return;
    setVariableValues((prev) => ({
      ...prev,
      [name]: type === "boolean" ? Boolean(rawValue) : rawValue
    }));
    setVariableErrors((prev) => {
      if (!prev[name]) return prev;
      const next = { ...prev };
      delete next[name];
      return next;
    });
  };

  const buildVariablePayload = () => {
    const payload = {};
    variables.forEach((variable) => {
      if (!variable?.name) return;
      const { name, type } = variable;
      const hasOverride = Object.prototype.hasOwnProperty.call(variableValues, name);
      const raw = hasOverride ? variableValues[name] : deriveInitialValue(variable);
      if (type === "boolean") {
        payload[name] = Boolean(raw);
      } else if (type === "number") {
        if (raw === "" || raw === null || raw === undefined) {
          payload[name] = "";
        } else {
          const num = Number(raw);
          payload[name] = Number.isFinite(num) ? num : "";
        }
      } else {
        payload[name] = raw == null ? "" : String(raw);
      }
    });
    return payload;
  };

  const onRun = async () => {
    if (!selectedAssembly) {
      setError(mode === "ansible" ? "Please choose a playbook to run." : "Please choose a script to run.");
      return;
    }
    if (mode === "ansible" && !useSvcAccount && !selectedCredentialId) {
      setError("Select a credential to run this playbook.");
      return;
    }
    if (variables.length) {
      const errors = {};
      variables.forEach((variable) => {
        if (!variable) return;
        if (!variable.required) return;
        if (variable.type === "boolean") return;
        const hasOverride = Object.prototype.hasOwnProperty.call(variableValues, variable.name);
        const raw = hasOverride ? variableValues[variable.name] : deriveInitialValue(variable);
        if (raw == null || raw === "") {
          errors[variable.name] = "Required";
        }
      });
      if (Object.keys(errors).length) {
        setVariableErrors(errors);
        setError("Please fill in all required variable values.");
        return;
      }
    }
    setRunning(true);
    setError("");
    try {
      let resp;
      const variableOverrides = buildVariablePayload();
      const normalizedPath = normalizeAssemblyPath(
        mode === "ansible" ? "ansible" : "script",
        selectedAssembly.path || "",
        selectedAssembly.displayName
      );
      if (mode === "ansible") {
        resp = await fetch("/api/ansible/quick_run", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            playbook_path: normalizedPath,
            hostnames,
            variable_values: variableOverrides,
            credential_id: !useSvcAccount && selectedCredentialId ? Number(selectedCredentialId) : null,
            use_service_account: Boolean(useSvcAccount)
          })
        });
      } else {
        resp = await fetch("/api/scripts/quick_run", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            script_path: normalizedPath,
            hostnames,
            run_mode: runAsCurrentUser ? "current_user" : "system",
            variable_values: variableOverrides
          })
        });
      }
      const contentType = String(resp.headers.get("content-type") || "");
      let data = null;
      if (contentType.includes("application/json")) {
        data = await resp.json().catch(() => null);
      } else {
        const text = await resp.text().catch(() => "");
        if (text && text.trim()) {
          data = { error: text.trim() };
        }
      }
      if (!resp.ok) {
        const message = data?.error || data?.message || `HTTP ${resp.status}`;
        throw new Error(message);
      }
      onClose && onClose();
    } catch (err) {
      setError(String(err.message || err));
    } finally {
      setRunning(false);
    }
  };

  const credentialRequired = mode === "ansible" && !useSvcAccount;
  const disableRun =
    running ||
    !selectedAssembly ||
    (credentialRequired && (!selectedCredentialId || !credentials.length));
  const activeTreeData = mode === "ansible" ? ansibleTreeData : scriptTreeData;
  const treeItems = Array.isArray(activeTreeData.root) ? activeTreeData.root : [];
  const targetCount = hostnames.length;
  const hostPreview = hostnames.slice(0, 3).join(", ");
  const remainingHosts = Math.max(targetCount - 3, 0);

  return (
    <Dialog
      open={open}
      onClose={running ? undefined : onClose}
      fullWidth
      maxWidth="lg"
      PaperProps={{ sx: DIALOG_SHELL_SX }}
    >
      <DialogTitle sx={{ pb: 0 }}>
        <Box
          sx={{
            display: "flex",
            flexDirection: { xs: "column", sm: "row" },
            justifyContent: "space-between",
            gap: 2
          }}
        >
          <Box>
            <Typography sx={{ fontWeight: 600, letterSpacing: 0.4 }}>Quick Job</Typography>
            <Typography variant="body2" sx={{ color: "rgba(226,232,240,0.78)" }}>
              Dispatch {mode === "ansible" ? "playbooks" : "scripts"} through the runner.
            </Typography>
          </Box>
          <Box sx={{ display: "flex", gap: 1.5, flexWrap: "wrap" }}>
            <Paper sx={{ ...GLASS_PANEL_SX, px: 2, py: 1 }}>
              <Typography variant="caption" sx={{ textTransform: "uppercase", color: "rgba(226,232,240,0.7)", letterSpacing: 1 }}>
                Targets
              </Typography>
              <Typography variant="h6">{hostnames.length || "—"}</Typography>
            </Paper>
            <Paper sx={{ ...GLASS_PANEL_SX, px: 2, py: 1 }}>
              <Typography variant="caption" sx={{ textTransform: "uppercase", color: "rgba(226,232,240,0.7)", letterSpacing: 1 }}>
                Mode
              </Typography>
              <Typography variant="h6">{mode === "ansible" ? "Ansible" : "Script"}</Typography>
            </Paper>
          </Box>
        </Box>
      </DialogTitle>
      <DialogContent>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1 }}>
          <Button size="small" variant={mode === 'scripts' ? 'outlined' : 'text'} onClick={() => setMode('scripts')} sx={{ textTransform: 'none', color: '#58a6ff', borderColor: '#58a6ff' }}>Scripts</Button>
          <Button size="small" variant={mode === 'ansible' ? 'outlined' : 'text'} onClick={() => setMode('ansible')} sx={{ textTransform: 'none', color: '#58a6ff', borderColor: '#58a6ff' }}>Ansible</Button>
        </Box>
        <Typography variant="body2" sx={{ color: "#aaa", mb: 1 }}>
          Select a {mode === 'ansible' ? 'playbook' : 'script'} to run on {hostnames.length} device{hostnames.length !== 1 ? "s" : ""}.
        </Typography>
        {assembliesError ? (
          <Typography variant="body2" sx={{ color: "#ff8080", mb: 1 }}>{assembliesError}</Typography>
        ) : null}
        {mode === 'ansible' && (
          <Box sx={{ display: "flex", alignItems: "center", gap: 1.5, flexWrap: "wrap", mb: 2 }}>
            <FormControlLabel
              control={
                <Checkbox
                  checked={useSvcAccount}
                  onChange={(e) => {
                    const checked = e.target.checked;
                    setUseSvcAccount(checked);
                    if (checked) {
                      setSelectedCredentialId("");
                    } else if (!selectedCredentialId && credentials.length) {
                      setSelectedCredentialId(String(credentials[0].id));
                    }
                  }}
                  size="small"
                />
              }
              label="Use Configured svcBorealis Account"
              sx={{ mr: 2 }}
            />
            <FormControl
              size="small"
              sx={{ minWidth: 260 }}
              disabled={useSvcAccount || credentialsLoading || !credentials.length}
            >
              <InputLabel sx={{ color: "#aaa" }}>Credential</InputLabel>
              <Select
                value={selectedCredentialId}
                label="Credential"
                onChange={(e) => setSelectedCredentialId(e.target.value)}
                sx={{ bgcolor: "#1f1f1f", color: "#fff" }}
              >
                {credentials.map((cred) => {
                  const conn = String(cred.connection_type || "").toUpperCase();
                  return (
                    <MenuItem key={cred.id} value={String(cred.id)}>
                      {cred.name}
                      {conn ? ` (${conn})` : ""}
                    </MenuItem>
                  );
                })}
              </Select>
            </FormControl>
            {useSvcAccount && (
              <Typography variant="body2" sx={{ color: "#aaa" }}>
                Runs with the agent&apos;s svcBorealis account.
              </Typography>
            )}
            {credentialsLoading && <CircularProgress size={18} sx={{ color: "#58a6ff" }} />}
            {!credentialsLoading && credentialsError && (
              <Typography variant="body2" sx={{ color: "#ff8080" }}>{credentialsError}</Typography>
            )}
            {!useSvcAccount && !credentialsLoading && !credentialsError && !credentials.length && (
              <Typography variant="body2" sx={{ color: "#ff8080" }}>
                No SSH or WinRM credentials available. Create one under Access Management.
              </Typography>
            )}
          </Box>
        )}
        <Box sx={{ display: "flex", gap: 2 }}>
          <Paper sx={{ flex: 1, p: 1, bgcolor: "#1e1e1e", maxHeight: 400, overflow: "auto" }}>
            <SimpleTreeView sx={{ color: "#e6edf3" }} onItemSelectionToggle={onItemSelect}>
              {assembliesLoading ? (
                <Box sx={{ display: "flex", alignItems: "center", gap: 1, px: 1, py: 0.5, color: "#7db7ff" }}>
                  <CircularProgress size={18} sx={{ color: "#58a6ff" }} />
                  <Typography variant="body2">Loading assemblies…</Typography>
                </Box>
              ) : treeItems.length ? (
                renderNodes(treeItems)
              ) : (
                <Typography variant="body2" sx={{ color: "#888", p: 1 }}>
                  {mode === 'ansible' ? 'No playbooks found.' : 'No scripts found.'}
                </Typography>
              )}
            </SimpleTreeView>
          </Paper>
          <Box sx={{ width: 320 }}>
            <Typography variant="subtitle2" sx={{ color: "#ccc", mb: 1 }}>Selection</Typography>
            {selectedAssembly ? (
              <Box sx={{ display: "flex", flexDirection: "column", gap: 0.5 }}>
                <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
                  <Typography variant="body2" sx={{ color: "#e6edf3" }}>{selectedAssembly.displayName}</Typography>
                  <DomainBadge domain={selectedAssembly.domain} size="small" />
                </Box>
                <Typography variant="body2" sx={{ color: "#aaa" }}>{selectedAssembly.path}</Typography>
              </Box>
            ) : (
              <Typography variant="body2" sx={{ color: "#888" }}>
                {mode === 'ansible' ? 'No playbook selected' : 'No script selected'}
              </Typography>
            )}
            <Box sx={{ mt: 2 }}>
              {mode !== 'ansible' && (
                <>
                  <FormControlLabel
                    control={<Checkbox size="small" checked={runAsCurrentUser} onChange={(e) => setRunAsCurrentUser(e.target.checked)} />}
                    label={<Typography variant="body2">Run as currently logged-in user</Typography>}
                  />
                  <Typography variant="caption" sx={{ color: "#888" }}>
                    Unchecked = Run-As BUILTIN\SYSTEM
                  </Typography>
                </>
              )}
            </Box>
            <Box sx={{ mt: 3 }}>
              <Typography variant="subtitle2" sx={{ color: "#ccc", mb: 1 }}>Variables</Typography>
              {variableStatus.loading ? (
                <Typography variant="body2" sx={{ color: "#888" }}>Loading variables…</Typography>
              ) : variableStatus.error ? (
                <Typography variant="body2" sx={{ color: "#ff4f4f" }}>{variableStatus.error}</Typography>
              ) : variables.length ? (
                <Box sx={{ display: "flex", flexDirection: "column", gap: 1.5 }}>
                  {variables.map((variable) => (
                    <Box key={variable.name}>
                      {variable.type === "boolean" ? (
                        <FormControlLabel
                          control={(
                            <Checkbox
                              size="small"
                              checked={Boolean(variableValues[variable.name])}
                              onChange={(e) => handleVariableChange(variable, e.target.checked)}
                            />
                          )}
                          label={
                            <Typography variant="body2">
                              {variable.label}
                              {variable.required ? " *" : ""}
                            </Typography>
                          }
                        />
                      ) : (
                        <TextField
                          fullWidth
                          size="small"
                          label={`${variable.label}${variable.required ? " *" : ""}`}
                          type={variable.type === "number" ? "number" : variable.type === "credential" ? "password" : "text"}
                          value={variableValues[variable.name] ?? ""}
                          onChange={(e) => handleVariableChange(variable, e.target.value)}
                          InputLabelProps={{ shrink: true }}
                          sx={{
                            "& .MuiOutlinedInput-root": { bgcolor: "#1b1b1b", color: "#e6edf3" },
                            "& .MuiInputBase-input": { color: "#e6edf3" }
                          }}
                          error={Boolean(variableErrors[variable.name])}
                          helperText={variableErrors[variable.name] || variable.description || ""}
                        />
                      )}
                      {variable.type === "boolean" && variable.description ? (
                        <Typography variant="caption" sx={{ color: "#888", ml: 3 }}>
                          {variable.description}
                        </Typography>
                      ) : null}
                    </Box>
                  ))}
                </Box>
              ) : (
                <Typography variant="body2" sx={{ color: "#888" }}>No variables defined for this assembly.</Typography>
              )}
            </Box>
            {error && (
              <Typography variant="body2" sx={{ color: "#ff4f4f", mt: 1 }}>{error}</Typography>
            )}
          </Box>
        </Box>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose} disabled={running} sx={{ color: "#58a6ff" }}>Cancel</Button>
        <Button onClick={onRun} disabled={disableRun}
          sx={{ color: disableRun ? "#666" : "#58a6ff" }}
        >
          Run
        </Button>
      </DialogActions>
    </Dialog>
  );
}
