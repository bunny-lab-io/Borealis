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
  TextField
} from "@mui/material";
import { Folder as FolderIcon, Description as DescriptionIcon } from "@mui/icons-material";
import { SimpleTreeView, TreeItem } from "@mui/x-tree-view";

function buildTree(items, folders, rootLabel = "Scripts") {
  const map = {};
  const rootNode = {
    id: "root",
    label: rootLabel,
    path: "",
    isFolder: true,
    children: []
  };
  map[rootNode.id] = rootNode;

  (folders || []).forEach((f) => {
    const parts = (f || "").split("/");
    let children = rootNode.children;
    let parentPath = "";
    parts.forEach((part) => {
      const path = parentPath ? `${parentPath}/${part}` : part;
      let node = children.find((n) => n.id === path);
      if (!node) {
        node = { id: path, label: part, path, isFolder: true, children: [] };
        children.push(node);
        map[path] = node;
      }
      children = node.children;
      parentPath = path;
    });
  });

  (items || []).forEach((s) => {
    const parts = (s.rel_path || "").split("/");
    let children = rootNode.children;
    let parentPath = "";
    parts.forEach((part, idx) => {
      const path = parentPath ? `${parentPath}/${part}` : part;
      const isFile = idx === parts.length - 1;
      let node = children.find((n) => n.id === path);
      if (!node) {
        node = {
          id: path,
          label: isFile ? (s.name || s.file_name || part) : part,
          path,
          isFolder: !isFile,
          fileName: s.file_name,
          script: isFile ? s : null,
          children: []
        };
        children.push(node);
        map[path] = node;
      }
      if (!isFile) {
        children = node.children;
        parentPath = path;
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
  const [variables, setVariables] = useState([]);
  const [variableValues, setVariableValues] = useState({});
  const [variableErrors, setVariableErrors] = useState({});
  const [variableStatus, setVariableStatus] = useState({ loading: false, error: "" });

  const loadTree = useCallback(async () => {
    try {
      const island = mode === 'ansible' ? 'ansible' : 'scripts';
      const resp = await fetch(`/api/assembly/list?island=${island}`);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      const { root, map } = buildTree(data.items || [], data.folders || [], mode === 'ansible' ? 'Ansible Playbooks' : 'Scripts');
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
      loadTree();
    }
  }, [open, loadTree]);

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
        const island = mode === "ansible" ? "ansible" : "scripts";
        const trimmed = (selectedPath || "").replace(/\\/g, "/").replace(/^\/+/, "").trim();
        if (!trimmed) {
          setVariables([]);
          setVariableValues({});
          setVariableErrors({});
          setVariableStatus({ loading: false, error: "" });
          return;
        }
        let relPath = trimmed;
        if (island === "scripts" && relPath.toLowerCase().startsWith("scripts/")) {
          relPath = relPath.slice("Scripts/".length);
        } else if (island === "ansible" && relPath.toLowerCase().startsWith("ansible_playbooks/")) {
          relPath = relPath.slice("Ansible_Playbooks/".length);
        }
        const resp = await fetch(`/api/assembly/load?island=${island}&path=${encodeURIComponent(relPath)}`);
        if (!resp.ok) throw new Error(`Failed to load assembly (HTTP ${resp.status})`);
        const data = await resp.json();
        const defs = normalizeVariables(data?.assembly?.variables || []);
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
  }, [selectedPath, mode]);

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
      if (mode === 'ansible') {
        const playbook_path = selectedPath; // relative to ansible island
        resp = await fetch("/api/ansible/quick_run", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ playbook_path, hostnames, variable_values: variableOverrides })
        });
      } else {
        // quick_run expects a path relative to Assemblies root with 'Scripts/' prefix
        const script_path = selectedPath.startsWith('Scripts/') ? selectedPath : `Scripts/${selectedPath}`;
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
        <Button onClick={onRun} disabled={running || !selectedPath}
          sx={{ color: running || !selectedPath ? "#666" : "#58a6ff" }}
        >
          Run
        </Button>
      </DialogActions>
    </Dialog>
  );
}

