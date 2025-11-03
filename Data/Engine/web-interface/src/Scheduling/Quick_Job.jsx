import React, { useEffect, useState, useCallback } from "react";
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
  CircularProgress
} from "@mui/material";
import { Folder as FolderIcon, Description as DescriptionIcon } from "@mui/icons-material";
import { SimpleTreeView, TreeItem } from "@mui/x-tree-view";

function buildTree(items, rootLabel = "Scripts") {
  const map = {};
  const rootNode = {
    id: "root",
    label: rootLabel,
    path: "",
    isFolder: true,
    children: []
  };
  map[rootNode.id] = rootNode;

  (items || []).forEach((item) => {
    if (!item || typeof item !== "object") return;
    const metadata = item.metadata && typeof item.metadata === "object" ? item.metadata : {};
    const rawPath = String(metadata.source_path || metadata.legacy_path || "")
      .replace(/\\/g, "/")
      .replace(/^\/+/, "")
      .trim();
    const pathSegments = rawPath ? rawPath.split("/").filter(Boolean) : [];
    const segments = pathSegments.length
      ? pathSegments
      : [String(item.display_name || metadata.display_name || item.assembly_guid || "Assembly").trim() || "Assembly"];
    let children = rootNode.children;
    let parentPath = "";
    segments.forEach((segment, idx) => {
      const nodeId = parentPath ? `${parentPath}/${segment}` : segment;
      const isFile = idx === segments.length - 1;
      let node = children.find((n) => n.id === nodeId);
      if (!node) {
        node = {
          id: nodeId,
          label: isFile ? (item.display_name || metadata.display_name || segment) : segment,
          path: nodeId,
          isFolder: !isFile,
          script: isFile ? item : null,
          scriptPath: isFile ? (rawPath || nodeId) : undefined,
          children: []
        };
        children.push(node);
        map[nodeId] = node;
      } else if (isFile) {
        node.script = item;
        node.label = item.display_name || metadata.display_name || node.label;
        node.scriptPath = rawPath || nodeId;
      }
      if (!isFile) {
        children = node.children;
        parentPath = nodeId;
      }
    });
  });

  return { root: [rootNode], map };
}

export default function QuickJob({ open, onClose, hostnames = [] }) {
  const [tree, setTree] = useState([]);
  const [nodeMap, setNodeMap] = useState({});
  const [selectedPath, setSelectedPath] = useState("");
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

  const loadTree = useCallback(async () => {
    try {
      const resp = await fetch("/api/assemblies");
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      const items = Array.isArray(data?.items) ? data.items : [];
      const filtered = items.filter((item) => {
        const kind = String(item?.assembly_kind || "").toLowerCase();
        const type = String(item?.assembly_type || "").toLowerCase();
        if (mode === "ansible") {
          return type === "ansible";
        }
        return kind === "script" && type !== "ansible";
      });
      const { root, map } = buildTree(filtered, mode === "ansible" ? "Ansible Playbooks" : "Scripts");
      setTree(root);
      setNodeMap(map);
    } catch (err) {
      console.error("Failed to load scripts:", err);
      setTree([]);
      setNodeMap({});
    }
  }, [mode]);

  useEffect(() => {
    if (open) {
      setSelectedPath("");
      setError("");
      setVariables([]);
      setVariableValues({});
      setVariableErrors({});
      setVariableStatus({ loading: false, error: "" });
      setUseSvcAccount(true);
      setSelectedCredentialId("");
      loadTree();
    }
  }, [open, loadTree]);

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

  const onItemSelect = (_e, itemId) => {
    const node = nodeMap[itemId];
    if (node && !node.isFolder) {
      setSelectedPath(node.path);
      setError("");
      setVariableErrors({});
    }
  };

  const normalizeVariables = (list) => {
    if (!Array.isArray(list)) return [];
    return list
      .map((raw) => {
        if (!raw || typeof raw !== "object") return null;
        const name = typeof raw.name === "string" ? raw.name.trim() : typeof raw.key === "string" ? raw.key.trim() : "";
        if (!name) return null;
        const type = typeof raw.type === "string" ? raw.type.toLowerCase() : "string";
        const label = typeof raw.label === "string" && raw.label.trim() ? raw.label.trim() : name;
        const description = typeof raw.description === "string" ? raw.description : "";
        const required = Boolean(raw.required);
        const defaultValue = raw.hasOwnProperty("default")
          ? raw.default
          : raw.hasOwnProperty("defaultValue")
            ? raw.defaultValue
            : raw.hasOwnProperty("default_value")
              ? raw.default_value
              : "";
        return { name, label, type, description, required, default: defaultValue };
      })
      .filter(Boolean);
  };

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
    if (!selectedPath) {
      setVariables([]);
      setVariableValues({});
      setVariableErrors({});
      setVariableStatus({ loading: false, error: "" });
      return;
    }
    let canceled = false;
    const loadAssembly = async () => {
      setVariableStatus({ loading: true, error: "" });
      try {
        const trimmed = (selectedPath || "").replace(/\\/g, "/").replace(/^\/+/, "").trim();
        if (!trimmed) {
          setVariables([]);
          setVariableValues({});
          setVariableErrors({});
          setVariableStatus({ loading: false, error: "" });
          return;
        }
        const node = nodeMap[trimmed];
        const script = node?.script;
        const assemblyGuid = script?.assembly_guid;
        if (!assemblyGuid) {
          setVariables([]);
          setVariableValues({});
          setVariableErrors({});
          setVariableStatus({ loading: false, error: "" });
          return;
        }
        const resp = await fetch(`/api/assemblies/${encodeURIComponent(assemblyGuid)}/export`);
        if (!resp.ok) throw new Error(`Failed to load assembly (HTTP ${resp.status})`);
        const data = await resp.json();
        const metadata = data?.metadata && typeof data.metadata === "object" ? data.metadata : {};
        const payload = data?.payload && typeof data.payload === "object" ? data.payload : {};
        const varsSource =
          (payload && payload.variables) ||
          metadata.variables ||
          [];
        const defs = normalizeVariables(varsSource);
        if (!canceled) {
          setVariables(defs);
          const initialValues = {};
          defs.forEach((v) => {
            initialValues[v.name] = deriveInitialValue(v);
          });
          setVariableValues(initialValues);
          setVariableErrors({});
          setVariableStatus({ loading: false, error: "" });
        }
      } catch (err) {
        if (!canceled) {
          setVariables([]);
          setVariableValues({});
          setVariableErrors({});
          setVariableStatus({ loading: false, error: err?.message || String(err) });
        }
      }
    };
    loadAssembly();
    return () => {
      canceled = true;
    };
  }, [selectedPath, mode, nodeMap]);

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
    if (!selectedPath) {
      setError(mode === 'ansible' ? "Please choose a playbook to run." : "Please choose a script to run.");
      return;
    }
    if (mode === 'ansible' && !useSvcAccount && !selectedCredentialId) {
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
      const node = nodeMap[selectedPath];
      if (mode === 'ansible') {
        const rawPath = (node?.scriptPath || selectedPath || "").replace(/\\/g, "/");
        const playbook_path = rawPath.toLowerCase().startsWith("ansible_playbooks/")
          ? rawPath
          : `Ansible_Playbooks/${rawPath}`;
        resp = await fetch("/api/ansible/quick_run", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            playbook_path,
            hostnames,
            variable_values: variableOverrides,
            credential_id: !useSvcAccount && selectedCredentialId ? Number(selectedCredentialId) : null,
            use_service_account: Boolean(useSvcAccount)
          })
        });
      } else {
        const rawPath = (node?.scriptPath || selectedPath || "").replace(/\\/g, "/");
        const script_path = rawPath.toLowerCase().startsWith("scripts/") ? rawPath : `Scripts/${rawPath}`;
        resp = await fetch("/api/scripts/quick_run", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            script_path,
            hostnames,
            run_mode: runAsCurrentUser ? "current_user" : "system",
            variable_values: variableOverrides
          })
        });
      }
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error || `HTTP ${resp.status}`);
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
    !selectedPath ||
    (credentialRequired && (!selectedCredentialId || !credentials.length));

  return (
    <Dialog open={open} onClose={running ? undefined : onClose} fullWidth maxWidth="md"
      PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}
    >
      <DialogTitle>Quick Job</DialogTitle>
      <DialogContent>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1 }}>
          <Button size="small" variant={mode === 'scripts' ? 'outlined' : 'text'} onClick={() => setMode('scripts')} sx={{ textTransform: 'none', color: '#58a6ff', borderColor: '#58a6ff' }}>Scripts</Button>
          <Button size="small" variant={mode === 'ansible' ? 'outlined' : 'text'} onClick={() => setMode('ansible')} sx={{ textTransform: 'none', color: '#58a6ff', borderColor: '#58a6ff' }}>Ansible</Button>
        </Box>
        <Typography variant="body2" sx={{ color: "#aaa", mb: 1 }}>
          Select a {mode === 'ansible' ? 'playbook' : 'script'} to run on {hostnames.length} device{hostnames.length !== 1 ? "s" : ""}.
        </Typography>
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
              {tree.length ? renderNodes(tree) : (
                <Typography variant="body2" sx={{ color: "#888", p: 1 }}>
                  {mode === 'ansible' ? 'No playbooks found.' : 'No scripts found.'}
                </Typography>
              )}
            </SimpleTreeView>
          </Paper>
          <Box sx={{ width: 320 }}>
            <Typography variant="subtitle2" sx={{ color: "#ccc", mb: 1 }}>Selection</Typography>
            <Typography variant="body2" sx={{ color: selectedPath ? "#e6edf3" : "#888" }}>
              {selectedPath || (mode === 'ansible' ? 'No playbook selected' : 'No script selected')}
            </Typography>
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
