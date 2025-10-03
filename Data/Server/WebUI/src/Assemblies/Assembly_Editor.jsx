import React, { useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Paper,
  Typography,
  Button,
  Select,
  FormControl,
  InputLabel,
  TextField,
  MenuItem,
  Grid,
  RadioGroup,
  FormControlLabel,
  Radio,
  Checkbox,
  IconButton,
  Tooltip,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions
} from "@mui/material";
import { Add as AddIcon, Delete as DeleteIcon, UploadFile as UploadFileIcon } from "@mui/icons-material";
import Prism from "prismjs";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-powershell";
import "prismjs/components/prism-batch";
import "prismjs/themes/prism-okaidia.css";
import Editor from "react-simple-code-editor";
import { ConfirmDeleteDialog } from "../Dialogs";

const TYPE_OPTIONS_ALL = [
  { key: "ansible", label: "Ansible Playbook", prism: "yaml" },
  { key: "powershell", label: "PowerShell Script", prism: "powershell" },
  { key: "batch", label: "Batch Script", prism: "batch" },
  { key: "bash", label: "Bash Script", prism: "bash" }
];

const CATEGORY_OPTIONS = [
  { key: "script", label: "Script" },
  { key: "application", label: "Application" }
];

const VARIABLE_TYPE_OPTIONS = [
  { key: "string", label: "String" },
  { key: "number", label: "Number" },
  { key: "boolean", label: "Boolean" },
  { key: "credential", label: "Credential" }
];

function keyBy(arr) {
  return Object.fromEntries(arr.map((o) => [o.key, o]));
}

const TYPE_MAP = keyBy(TYPE_OPTIONS_ALL);

function highlightedHtml(code, prismLang) {
  try {
    const grammar = Prism.languages[prismLang] || Prism.languages.markup;
    return Prism.highlight(code ?? "", grammar, prismLang);
  } catch {
    return (code ?? "").replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c]));
  }
}

function sanitizeFileName(name = "") {
  const base = name.trim().replace(/[^a-zA-Z0-9._-]+/g, "_") || "assembly";
  return base.endsWith(".json") ? base : `${base}.json`;
}

function normalizeFolderPath(path = "") {
  if (!path) return "";
  return path
    .replace(/\\/g, "/")
    .replace(/^\/+|\/+$/g, "")
    .replace(/\/+/g, "/");
}

function formatBytes(size) {
  if (!size || Number.isNaN(size)) return "0 B";
  if (size < 1024) return `${size} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let idx = -1;
  let s = size;
  while (s >= 1024 && idx < units.length - 1) {
    s /= 1024;
    idx += 1;
  }
  return `${s.toFixed(1)} ${units[idx]}`;
}

function defaultAssembly(defaultType = "powershell") {
  return {
    name: "",
    description: "",
    category: defaultType === "ansible" ? "application" : "script",
    type: defaultType,
    script: "",
    timeoutSeconds: 0,
    sites: { mode: "all", values: [] },
    variables: [],
    files: []
  };
}

function normalizeVariablesFromServer(vars = []) {
  return (Array.isArray(vars) ? vars : []).map((v, idx) => ({
    id: `${Date.now()}_${idx}_${Math.random().toString(36).slice(2, 8)}`,
    name: v?.name || v?.key || "",
    label: v?.label || "",
    type: v?.type || "string",
    defaultValue: v?.default ?? v?.default_value ?? "",
    required: Boolean(v?.required),
    description: v?.description || ""
  }));
}

function normalizeFilesFromServer(files = []) {
  return (Array.isArray(files) ? files : []).map((f, idx) => ({
    id: `${Date.now()}_${idx}_${Math.random().toString(36).slice(2, 8)}`,
    fileName: f?.file_name || f?.name || "file.bin",
    size: f?.size || 0,
    mimeType: f?.mime_type || f?.mimeType || "",
    data: f?.data || ""
  }));
}

function fromServerDocument(doc = {}, defaultType = "powershell") {
  const assembly = defaultAssembly(defaultType);
  if (doc && typeof doc === "object") {
    assembly.name = doc.name || doc.display_name || assembly.name;
    assembly.description = doc.description || "";
    assembly.category = doc.category || assembly.category;
    assembly.type = doc.type || assembly.type;
    assembly.script = doc.script ?? doc.content ?? "";
    const timeout = doc.timeout_seconds ?? doc.timeout ?? 0;
    assembly.timeoutSeconds = Number.isFinite(Number(timeout)) ? Number(timeout) : 0;
    const sites = doc.sites || {};
    assembly.sites = {
      mode: sites.mode || (Array.isArray(sites.values) && sites.values.length ? "specific" : "all"),
      values: Array.isArray(sites.values) ? sites.values : []
    };
    assembly.variables = normalizeVariablesFromServer(doc.variables);
    assembly.files = normalizeFilesFromServer(doc.files);
  }
  return assembly;
}

function toServerDocument(assembly) {
  return {
    version: 1,
    name: assembly.name?.trim() || "",
    description: assembly.description || "",
    category: assembly.category || "script",
    type: assembly.type || "powershell",
    script: assembly.script ?? "",
    timeout_seconds: Number.isFinite(Number(assembly.timeoutSeconds)) ? Number(assembly.timeoutSeconds) : 0,
    sites: {
      mode: assembly.sites?.mode === "specific" ? "specific" : "all",
      values: Array.isArray(assembly.sites?.values)
        ? assembly.sites.values.filter((v) => v && v.trim()).map((v) => v.trim())
        : []
    },
    variables: (assembly.variables || []).map((v) => ({
      name: v.name?.trim() || "",
      label: v.label || "",
      type: v.type || "string",
      default: v.defaultValue ?? "",
      required: Boolean(v.required),
      description: v.description || ""
    })),
    files: (assembly.files || []).map((f) => ({
      file_name: f.fileName || "file.bin",
      size: f.size || 0,
      mime_type: f.mimeType || "",
      data: f.data || ""
    }))
  };
}

function RenameFileDialog({ open, value, onChange, onCancel, onSave }) {
  return (
    <Dialog open={open} onClose={onCancel} PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}>
      <DialogTitle>Rename Assembly File</DialogTitle>
      <DialogContent>
        <TextField
          autoFocus
          margin="dense"
          label="File Name"
          fullWidth
          variant="outlined"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          sx={{
            "& .MuiOutlinedInput-root": {
              backgroundColor: "#1e1e1e",
              color: "#e6edf3",
              "& fieldset": { borderColor: "#333" },
              "&:hover fieldset": { borderColor: "#555" }
            },
            "& .MuiInputLabel-root": { color: "#aaa" }
          }}
        />
      </DialogContent>
      <DialogActions>
        <Button onClick={onCancel} sx={{ color: "#58a6ff" }}>Cancel</Button>
        <Button onClick={onSave} sx={{ color: "#58a6ff" }}>Save</Button>
      </DialogActions>
    </Dialog>
  );
}

export default function AssemblyEditor({
  mode = "scripts",
  initialPath = "",
  initialContext = null,
  onConsumeInitialData,
  onSaved
}) {
  const isAnsible = mode === "ansible";
  const defaultType = isAnsible ? "ansible" : "powershell";
  const [assembly, setAssembly] = useState(() => defaultAssembly(defaultType));
  const [currentPath, setCurrentPath] = useState("");
  const [fileName, setFileName] = useState("");
  const [folderPath, setFolderPath] = useState(() => normalizeFolderPath(initialContext?.folder || ""));
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const contextNonceRef = useRef(null);

  const TYPE_OPTIONS = useMemo(
    () => (isAnsible ? TYPE_OPTIONS_ALL.filter((o) => o.key === "ansible") : TYPE_OPTIONS_ALL.filter((o) => o.key !== "ansible")),
    [isAnsible]
  );

  const island = isAnsible ? "ansible" : "scripts";

  useEffect(() => {
    if (!initialPath) return;
    let canceled = false;
    (async () => {
      try {
        const resp = await fetch(`/api/assembly/load?island=${encodeURIComponent(island)}&path=${encodeURIComponent(initialPath)}`);
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        const data = await resp.json();
        if (canceled) return;
        const rel = data.rel_path || initialPath;
        setCurrentPath(rel);
        setFolderPath(normalizeFolderPath(rel.split("/").slice(0, -1).join("/")));
        setFileName(data.file_name || rel.split("/").pop() || "");
        const doc = fromServerDocument(data.assembly || data, defaultType);
        setAssembly(doc);
      } catch (err) {
        console.error("Failed to load assembly:", err);
      } finally {
        if (!canceled && onConsumeInitialData) onConsumeInitialData();
      }
    })();
    return () => {
      canceled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialPath, island]);

  useEffect(() => {
    const ctx = initialContext;
    if (!ctx || !ctx.nonce) return;
    if (contextNonceRef.current === ctx.nonce) return;
    contextNonceRef.current = ctx.nonce;
    const doc = defaultAssembly(ctx.defaultType || defaultType);
    if (ctx.name) doc.name = ctx.name;
    if (ctx.description) doc.description = ctx.description;
    if (ctx.category) doc.category = ctx.category;
    if (ctx.type) doc.type = ctx.type;
    setAssembly(doc);
    setCurrentPath("");
    const suggested = ctx.suggestedFileName || ctx.name || "";
    setFileName(suggested ? sanitizeFileName(suggested) : "");
    setFolderPath(normalizeFolderPath(ctx.folder || ""));
    if (onConsumeInitialData) onConsumeInitialData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialContext?.nonce]);

  const prismLanguage = TYPE_MAP[assembly.type]?.prism || "powershell";

  const updateAssembly = (partial) => {
    setAssembly((prev) => ({ ...prev, ...partial }));
  };

  const handleSitesChange = (modeValue, values) => {
    setAssembly((prev) => ({
      ...prev,
      sites: {
        mode: modeValue,
        values: Array.isArray(values)
          ? values
          : ((values || "").split(/\r?\n/).map((v) => v.trim()).filter(Boolean))
      }
    }));
  };

  const addVariable = () => {
    setAssembly((prev) => ({
      ...prev,
      variables: [
        ...prev.variables,
        {
          id: `${Date.now()}_${Math.random().toString(36).slice(2, 8)}`,
          name: "",
          label: "",
          type: "string",
          defaultValue: "",
          required: false,
          description: ""
        }
      ]
    }));
  };

  const updateVariable = (id, partial) => {
    setAssembly((prev) => ({
      ...prev,
      variables: prev.variables.map((v) => (v.id === id ? { ...v, ...partial } : v))
    }));
  };

  const removeVariable = (id) => {
    setAssembly((prev) => ({
      ...prev,
      variables: prev.variables.filter((v) => v.id !== id)
    }));
  };

  const handleFileUpload = async (event) => {
    const files = Array.from(event.target.files || []);
    if (!files.length) return;
    const reads = files.map((file) => new Promise((resolve) => {
      const reader = new FileReader();
      reader.onload = () => {
        const result = reader.result || "";
        const base64 = typeof result === "string" && result.includes(",") ? result.split(",", 2)[1] : result;
        resolve({
          id: `${Date.now()}_${Math.random().toString(36).slice(2, 8)}`,
          fileName: file.name,
          size: file.size,
          mimeType: file.type,
          data: base64
        });
      };
      reader.onerror = () => resolve(null);
      reader.readAsDataURL(file);
    }));
    const uploaded = (await Promise.all(reads)).filter(Boolean);
    if (uploaded.length) {
      setAssembly((prev) => ({ ...prev, files: [...prev.files, ...uploaded] }));
    }
    event.target.value = "";
  };

  const removeFile = (id) => {
    setAssembly((prev) => ({ ...prev, files: prev.files.filter((f) => f.id !== id) }));
  };

  const computeTargetPath = () => {
    if (currentPath) return currentPath;
    const baseName = sanitizeFileName(fileName || assembly.name || (isAnsible ? "playbook" : "assembly"));
    const folder = normalizeFolderPath(folderPath);
    return folder ? `${folder}/${baseName}` : baseName;
  };

  const saveAssembly = async () => {
    if (!assembly.name.trim()) {
      alert("Assembly Name is required.");
      return;
    }
    const payload = toServerDocument(assembly);
    payload.type = assembly.type;
    const targetPath = computeTargetPath();
    if (!targetPath) {
      alert("Unable to determine file path.");
      return;
    }
    setSaving(true);
    try {
      if (currentPath) {
        const resp = await fetch(`/api/assembly/edit`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ island, path: currentPath, content: payload })
        });
        const data = await resp.json().catch(() => ({}));
        if (!resp.ok) {
          throw new Error(data?.error || `HTTP ${resp.status}`);
        }
        if (data?.rel_path) {
          setCurrentPath(data.rel_path);
          setFolderPath(normalizeFolderPath(data.rel_path.split("/").slice(0, -1).join("/")));
          setFileName(data.rel_path.split("/").pop() || fileName);
        }
      } else {
        const resp = await fetch(`/api/assembly/create`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ island, kind: "file", path: targetPath, content: payload, type: assembly.type })
        });
        const data = await resp.json();
        if (!resp.ok) throw new Error(data?.error || `HTTP ${resp.status}`);
        if (data.rel_path) {
          setCurrentPath(data.rel_path);
          setFolderPath(data.rel_path.split("/").slice(0, -1).join("/"));
          setFileName(data.rel_path.split("/").pop() || "");
        } else {
          setCurrentPath(targetPath);
          setFileName(targetPath.split("/").pop() || "");
        }
      }
      onSaved && onSaved();
    } catch (err) {
      console.error("Failed to save assembly:", err);
      alert(err.message || "Failed to save assembly");
    } finally {
      setSaving(false);
    }
  };

  const saveRename = async () => {
    try {
      const nextName = sanitizeFileName(renameValue || fileName || assembly.name);
      const resp = await fetch(`/api/assembly/rename`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ island, kind: "file", path: currentPath, new_name: nextName, type: assembly.type })
      });
      const data = await resp.json();
      if (!resp.ok) throw new Error(data?.error || `HTTP ${resp.status}`);
      const rel = data.rel_path || currentPath;
      setCurrentPath(rel);
      setFolderPath(rel.split("/").slice(0, -1).join("/"));
      setFileName(rel.split("/").pop() || nextName);
      setRenameOpen(false);
    } catch (err) {
      console.error("Failed to rename assembly:", err);
      alert(err.message || "Failed to rename");
      setRenameOpen(false);
    }
  };

  const deleteAssembly = async () => {
    if (!currentPath) {
      setDeleteOpen(false);
      return;
    }
    try {
      const resp = await fetch(`/api/assembly/delete`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ island, kind: "file", path: currentPath })
      });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({}));
        throw new Error(data?.error || `HTTP ${resp.status}`);
      }
      setDeleteOpen(false);
      setAssembly(defaultAssembly(defaultType));
      setCurrentPath("");
      setFileName("");
      onSaved && onSaved();
    } catch (err) {
      console.error("Failed to delete assembly:", err);
      alert(err.message || "Failed to delete assembly");
      setDeleteOpen(false);
    }
  };

  const siteValuesText = (assembly.sites?.values || []).join("\n");

  return (
    <Box sx={{ display: "flex", flexDirection: "column", flex: 1, height: "100%", overflow: "hidden" }}>
      <Box sx={{ px: 2, pt: 2 }}>
        <Typography variant="h5" sx={{ color: "#58a6ff", fontWeight: 500, mb: 0.5 }}>
          Assembly Editor
        </Typography>
        <Typography variant="body2" sx={{ color: "#9ba3b4", mb: 2 }}>
          Create and edit variables, scripts, and other fields related to assemblies.
        </Typography>
      </Box>
      <Box sx={{ flex: 1, overflow: "auto", p: 2, pt: 0 }}>
        <Paper sx={{ p: 2, bgcolor: "#1e1e1e", borderRadius: 2, border: "1px solid #2a2a2a" }} elevation={2}>
          <Box sx={{ display: "flex", alignItems: "center", gap: 2, mb: 3 }}>
            <Box sx={{ flex: 1 }}>
              <Typography variant="subtitle2" sx={{ color: "#58a6ff" }}>
                Assembly Details
              </Typography>
            </Box>
            {currentPath ? (
              <Tooltip title="Rename File">
                <Button
                  size="small"
                  onClick={() => { setRenameValue(fileName); setRenameOpen(true); }}
                  sx={{ color: "#58a6ff", textTransform: "none" }}
                >
                  Rename
                </Button>
              </Tooltip>
            ) : null}
            {currentPath ? (
              <Tooltip title="Delete Assembly">
                <Button
                  size="small"
                  onClick={() => setDeleteOpen(true)}
                  sx={{ color: "#ff6b6b", textTransform: "none" }}
                >
                  Delete
                </Button>
              </Tooltip>
            ) : null}
            <Button
              variant="outlined"
              onClick={saveAssembly}
              disabled={saving}
              sx={{
                color: "#58a6ff",
                borderColor: "#58a6ff",
                textTransform: "none",
                backgroundColor: saving ? "rgba(88,166,255,0.08)" : "#1e1e1e",
                "&:hover": { borderColor: "#7db7ff" }
              }}
            >
              {saving ? "Saving..." : "Save Assembly"}
            </Button>
          </Box>

          <Grid container spacing={2}>
            <Grid item xs={12} md={6}>
              <TextField
                label="Assembly Name"
                value={assembly.name}
                onChange={(e) => updateAssembly({ name: e.target.value })}
                fullWidth
                variant="outlined"
                sx={{
                  mb: 2,
                  "& .MuiOutlinedInput-root": {
                    bgcolor: "#121212",
                    color: "#e6edf3",
                    "& fieldset": { borderColor: "#333" },
                    "&:hover fieldset": { borderColor: "#555" }
                  },
                  "& .MuiInputLabel-root": { color: "#aaa" }
                }}
              />
              <TextField
                label="Description"
                value={assembly.description}
                onChange={(e) => updateAssembly({ description: e.target.value })}
                multiline
                minRows={3}
                fullWidth
                variant="outlined"
                sx={{
                  "& .MuiOutlinedInput-root": {
                    bgcolor: "#121212",
                    color: "#e6edf3",
                    "& fieldset": { borderColor: "#333" },
                    "&:hover fieldset": { borderColor: "#555" }
                  },
                  "& .MuiInputLabel-root": { color: "#aaa" }
                }}
              />
            </Grid>
            <Grid item xs={12} md={6}>
              <FormControl fullWidth sx={{ mb: 2 }}>
                <InputLabel sx={{ color: "#aaa" }}>Category</InputLabel>
                <Select
                  value={assembly.category}
                  label="Category"
                  onChange={(e) => updateAssembly({ category: e.target.value })}
                  sx={{
                    bgcolor: "#121212",
                    color: "#e6edf3",
                    "& .MuiOutlinedInput-notchedOutline": { borderColor: "#333" },
                    "&:hover .MuiOutlinedInput-notchedOutline": { borderColor: "#555" }
                  }}
                >
                  {CATEGORY_OPTIONS.map((o) => (
                    <MenuItem key={o.key} value={o.key}>{o.label}</MenuItem>
                  ))}
                </Select>
              </FormControl>

              <FormControl fullWidth>
                <InputLabel sx={{ color: "#aaa" }}>Type</InputLabel>
                <Select
                  value={assembly.type}
                  label="Type"
                  onChange={(e) => updateAssembly({ type: e.target.value })}
                  sx={{
                    bgcolor: "#121212",
                    color: "#e6edf3",
                    "& .MuiOutlinedInput-notchedOutline": { borderColor: "#333" },
                    "&:hover .MuiOutlinedInput-notchedOutline": { borderColor: "#555" }
                  }}
                >
                  {TYPE_OPTIONS.map((o) => (
                    <MenuItem key={o.key} value={o.key}>{o.label}</MenuItem>
                  ))}
                </Select>
              </FormControl>
            </Grid>
          </Grid>

          <Box sx={{ mt: 3 }}>
            <Typography variant="subtitle2" sx={{ color: "#58a6ff", mb: 1 }}>
              Script Content
            </Typography>
            <Box sx={{ border: "1px solid #333", borderRadius: 1, background: "#121212" }}>
              <Editor
                value={assembly.script}
                onValueChange={(value) => updateAssembly({ script: value })}
                highlight={(src) => highlightedHtml(src, prismLanguage)}
                padding={12}
                placeholder={currentPath ? `Editing: ${currentPath}` : "Start typing your script..."}
                style={{
                  fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
                  fontSize: 14,
                  color: "#e6edf3",
                  background: "#121212",
                  outline: "none",
                  minHeight: 320,
                  lineHeight: 1.45,
                  caretColor: "#58a6ff"
                }}
              />
            </Box>
          </Box>

          <Grid container spacing={2} sx={{ mt: 3 }}>
            <Grid item xs={12} md={6}>
              <TextField
                label="Timeout (seconds)"
                type="number"
                value={assembly.timeoutSeconds}
                onChange={(e) => {
                  const val = Number(e.target.value);
                  updateAssembly({ timeoutSeconds: Number.isNaN(val) ? 0 : val });
                }}
                fullWidth
                variant="outlined"
                sx={{
                  "& .MuiOutlinedInput-root": {
                    bgcolor: "#121212",
                    color: "#e6edf3",
                    "& fieldset": { borderColor: "#333" },
                    "&:hover fieldset": { borderColor: "#555" }
                  },
                  "& .MuiInputLabel-root": { color: "#aaa" }
                }}
                helperText="Timeout this script if not completed within X seconds"
              />
            </Grid>
            <Grid item xs={12} md={6}>
              <Typography variant="subtitle2" sx={{ color: "#58a6ff", mb: 1 }}>
                Sites
              </Typography>
              <RadioGroup
                row
                value={assembly.sites.mode === "specific" ? "specific" : "all"}
                onChange={(e) => handleSitesChange(e.target.value, assembly.sites.values)}
                sx={{ color: "#e6edf3" }}
              >
                <FormControlLabel value="all" control={<Radio sx={{ color: "#58a6ff" }} />} label="All Sites" />
                <FormControlLabel value="specific" control={<Radio sx={{ color: "#58a6ff" }} />} label="Specific Sites" />
              </RadioGroup>
              {assembly.sites.mode === "specific" ? (
                <TextField
                  label="Allowed Sites (one per line)"
                  value={siteValuesText}
                  onChange={(e) => handleSitesChange("specific", e.target.value)}
                  multiline
                  minRows={3}
                  fullWidth
                  variant="outlined"
                  sx={{
                    mt: 1,
                    "& .MuiOutlinedInput-root": {
                      bgcolor: "#121212",
                      color: "#e6edf3",
                      "& fieldset": { borderColor: "#333" },
                      "&:hover fieldset": { borderColor: "#555" }
                    },
                    "& .MuiInputLabel-root": { color: "#aaa" }
                  }}
                />
              ) : null}
            </Grid>
          </Grid>

          <Box sx={{ mt: 4 }}>
            <Typography variant="subtitle2" sx={{ color: "#58a6ff", mb: 1 }}>
              Variables
            </Typography>
            <Typography variant="body2" sx={{ color: "#9ba3b4", mb: 2 }}>
              Variables are passed into the execution environment as environment variables at runtime.
            </Typography>
            {(assembly.variables || []).length ? (
              <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
                {assembly.variables.map((variable) => (
                  <Paper
                    key={variable.id}
                    sx={{ p: 2, bgcolor: "#171717", border: "1px solid #2a2a2a", borderRadius: 1 }}
                  >
                    <Grid container spacing={2} alignItems="center">
                      <Grid item xs={12} md={3}>
                        <TextField
                          label="Variable Name"
                          value={variable.name}
                          onChange={(e) => updateVariable(variable.id, { name: e.target.value })}
                          fullWidth
                          variant="outlined"
                          sx={{
                            "& .MuiOutlinedInput-root": {
                              bgcolor: "#121212",
                              color: "#e6edf3",
                              "& fieldset": { borderColor: "#333" },
                              "&:hover fieldset": { borderColor: "#555" }
                            },
                            "& .MuiInputLabel-root": { color: "#aaa" }
                          }}
                        />
                      </Grid>
                      <Grid item xs={12} md={3}>
                        <TextField
                          label="Display Label"
                          value={variable.label}
                          onChange={(e) => updateVariable(variable.id, { label: e.target.value })}
                          fullWidth
                          variant="outlined"
                          sx={{
                            "& .MuiOutlinedInput-root": {
                              bgcolor: "#121212",
                              color: "#e6edf3",
                              "& fieldset": { borderColor: "#333" },
                              "&:hover fieldset": { borderColor: "#555" }
                            },
                            "& .MuiInputLabel-root": { color: "#aaa" }
                          }}
                        />
                      </Grid>
                      <Grid item xs={12} md={2}>
                        <FormControl fullWidth>
                          <InputLabel sx={{ color: "#aaa" }}>Type</InputLabel>
                          <Select
                            value={variable.type}
                            label="Type"
                            onChange={(e) => updateVariable(variable.id, { type: e.target.value })}
                            sx={{
                              bgcolor: "#121212",
                              color: "#e6edf3",
                              "& .MuiOutlinedInput-notchedOutline": { borderColor: "#333" },
                              "&:hover .MuiOutlinedInput-notchedOutline": { borderColor: "#555" }
                            }}
                          >
                            {VARIABLE_TYPE_OPTIONS.map((opt) => (
                              <MenuItem key={opt.key} value={opt.key}>{opt.label}</MenuItem>
                            ))}
                          </Select>
                        </FormControl>
                      </Grid>
                      <Grid item xs={12} md={3}>
                        {variable.type === "boolean" ? (
                          <FormControlLabel
                            control={
                              <Checkbox
                                checked={Boolean(variable.defaultValue)}
                                onChange={(e) => updateVariable(variable.id, { defaultValue: e.target.checked })}
                                sx={{ color: "#58a6ff" }}
                              />
                            }
                            label="Default Value"
                          />
                        ) : (
                          <TextField
                            label="Default Value"
                            value={variable.defaultValue ?? ""}
                            onChange={(e) => updateVariable(variable.id, { defaultValue: e.target.value })}
                            fullWidth
                            variant="outlined"
                            sx={{
                              "& .MuiOutlinedInput-root": {
                                bgcolor: "#121212",
                                color: "#e6edf3",
                                "& fieldset": { borderColor: "#333" },
                                "&:hover fieldset": { borderColor: "#555" }
                              },
                              "& .MuiInputLabel-root": { color: "#aaa" }
                            }}
                          />
                        )}
                      </Grid>
                      <Grid item xs={12} md={1} sx={{ display: "flex", justifyContent: "center" }}>
                        <Tooltip title="Required">
                          <Checkbox
                            checked={Boolean(variable.required)}
                            onChange={(e) => updateVariable(variable.id, { required: e.target.checked })}
                            sx={{ color: "#58a6ff" }}
                          />
                        </Tooltip>
                      </Grid>
                      <Grid item xs={12}>
                        <TextField
                          label="Description"
                          value={variable.description}
                          onChange={(e) => updateVariable(variable.id, { description: e.target.value })}
                          fullWidth
                          multiline
                          minRows={2}
                          variant="outlined"
                          sx={{
                            "& .MuiOutlinedInput-root": {
                              bgcolor: "#121212",
                              color: "#e6edf3",
                              "& fieldset": { borderColor: "#333" },
                              "&:hover fieldset": { borderColor: "#555" }
                            },
                            "& .MuiInputLabel-root": { color: "#aaa" }
                          }}
                        />
                      </Grid>
                      <Grid item xs={12} sx={{ display: "flex", justifyContent: "flex-end" }}>
                        <IconButton onClick={() => removeVariable(variable.id)} sx={{ color: "#ff6b6b" }}>
                          <DeleteIcon />
                        </IconButton>
                      </Grid>
                    </Grid>
                  </Paper>
                ))}
              </Box>
            ) : (
              <Typography variant="body2" sx={{ color: "#787f8b", mb: 1 }}>
                No variables have been defined.
              </Typography>
            )}
            <Button
              startIcon={<AddIcon />}
              onClick={addVariable}
              sx={{ mt: 2, color: "#58a6ff", textTransform: "none" }}
            >
              Add Variable
            </Button>
          </Box>

          <Box sx={{ mt: 4 }}>
            <Typography variant="subtitle2" sx={{ color: "#58a6ff", mb: 1 }}>
              Files
            </Typography>
            <Typography variant="body2" sx={{ color: "#9ba3b4", mb: 2 }}>
              Upload supporting files. They will be embedded as Base64 and available to the assembly at runtime.
            </Typography>
            {(assembly.files || []).length ? (
              <Box sx={{ display: "flex", flexDirection: "column", gap: 1.5 }}>
                {assembly.files.map((file) => (
                  <Paper key={file.id} sx={{ p: 1.5, bgcolor: "#171717", border: "1px solid #2a2a2a", display: "flex", alignItems: "center", justifyContent: "space-between" }}>
                    <Box>
                      <Typography variant="body2" sx={{ color: "#e6edf3" }}>{file.fileName}</Typography>
                      <Typography variant="caption" sx={{ color: "#888" }}>{formatBytes(file.size)}{file.mimeType ? ` • ${file.mimeType}` : ""}</Typography>
                    </Box>
                    <IconButton onClick={() => removeFile(file.id)} sx={{ color: "#ff6b6b" }}>
                      <DeleteIcon />
                    </IconButton>
                  </Paper>
                ))}
              </Box>
            ) : (
              <Typography variant="body2" sx={{ color: "#787f8b", mb: 1 }}>
                No files uploaded yet.
              </Typography>
            )}
            <Button
              component="label"
              startIcon={<UploadFileIcon />}
              sx={{ mt: 2, color: "#58a6ff", textTransform: "none" }}
            >
              Upload File
              <input type="file" hidden multiple onChange={handleFileUpload} />
            </Button>
          </Box>
        </Paper>
      </Box>

      <RenameFileDialog
        open={renameOpen}
        value={renameValue}
        onChange={setRenameValue}
        onCancel={() => setRenameOpen(false)}
        onSave={saveRename}
      />
      <ConfirmDeleteDialog
        open={deleteOpen}
        message="Deleting this assembly cannot be undone. Continue?"
        onCancel={() => setDeleteOpen(false)}
        onConfirm={deleteAssembly}
      />
    </Box>
  );
}
