import React, { useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Paper,
  Typography,
  Button,
  TextField,
  MenuItem,
  Grid,
  FormControlLabel,
  Checkbox,
  IconButton,
  Tooltip,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  ListItemText
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

const INPUT_BASE_SX = {
  "& .MuiOutlinedInput-root": {
    bgcolor: "#1f2329",
    color: "#e6edf3",
    borderRadius: 1,
    minHeight: 42,
    "& fieldset": { borderColor: "#2d333b" },
    "&:hover fieldset": { borderColor: "#3b4a5c" },
    "&.Mui-focused fieldset": { borderColor: "#58a6ff" }
  },
  "& .MuiOutlinedInput-input": {
    padding: "10px 12px",
    fontSize: "0.95rem"
  },
  "& .MuiOutlinedInput-inputMultiline": {
    padding: "10px 12px"
  },
  "& .MuiInputLabel-root": { color: "#9ba3b4" },
  "& .MuiInputLabel-root.Mui-focused": { color: "#58a6ff" }
};

const SELECT_BASE_SX = {
  ...INPUT_BASE_SX,
  "& .MuiSelect-select": {
    padding: "10px 12px !important",
    display: "flex",
    alignItems: "center"
  }
};

const SECTION_TITLE_SX = {
  color: "#58a6ff",
  fontWeight: 500,
  fontSize: "14px",
  letterSpacing: 0.2
};

const SECTION_CARD_SX = {
  bgcolor: "#161b22",
  borderRadius: 2,
  border: "1px solid #1f2a37"
};

const MENU_PROPS = {
  PaperProps: {
    sx: {
      bgcolor: "#1f2329",
      color: "#e6edf3",
      border: "1px solid #2d333b",
      "& .MuiMenuItem-root.Mui-selected": {
        bgcolor: "rgba(88,166,255,0.16)"
      },
      "& .MuiMenuItem-root.Mui-selected:hover": {
        bgcolor: "rgba(88,166,255,0.24)"
      }
    }
  }
};

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
    timeoutSeconds: 3600,
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
    if (Array.isArray(doc.script_lines)) {
      assembly.script = doc.script_lines
        .map((line) => (line == null ? "" : String(line)))
        .join("\n");
    } else {
      assembly.script = doc.script ?? doc.content ?? "";
    }
    const timeout = doc.timeout_seconds ?? doc.timeout ?? assembly.timeoutSeconds;
    assembly.timeoutSeconds = Number.isFinite(Number(timeout))
      ? Number(timeout)
      : assembly.timeoutSeconds;
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
  const normalizedScript = typeof assembly.script === "string"
    ? assembly.script.replace(/\r\n/g, "\n")
    : "";
  const scriptLines = normalizedScript ? normalizedScript.split("\n") : [];
  const timeoutNumeric = Number(assembly.timeoutSeconds);
  const timeoutSeconds = Number.isFinite(timeoutNumeric) ? Math.max(0, Math.round(timeoutNumeric)) : 3600;
  return {
    version: 1,
    name: assembly.name?.trim() || "",
    description: assembly.description || "",
    category: assembly.category || "script",
    type: assembly.type || "powershell",
    script: normalizedScript,
    script_lines: scriptLines,
    timeout_seconds: timeoutSeconds,
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
    <Dialog open={open} onClose={onCancel} PaperProps={{ sx: { bgcolor: "#1a1f27", color: "#fff" } }}>
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
          sx={INPUT_BASE_SX}
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
  const [siteOptions, setSiteOptions] = useState([]);
  const [siteLoading, setSiteLoading] = useState(false);
  const contextNonceRef = useRef(null);

  const TYPE_OPTIONS = useMemo(
    () => (isAnsible ? TYPE_OPTIONS_ALL.filter((o) => o.key === "ansible") : TYPE_OPTIONS_ALL.filter((o) => o.key !== "ansible")),
    [isAnsible]
  );

  const siteOptionMap = useMemo(() => {
    const map = new Map();
    siteOptions.forEach((site) => {
      if (!site) return;
      const id = site.id != null ? String(site.id) : "";
      if (!id) return;
      map.set(id, site);
    });
    return map;
  }, [siteOptions]);

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

  useEffect(() => {
    let canceled = false;
    const loadSites = async () => {
      try {
        setSiteLoading(true);
        const resp = await fetch("/api/sites");
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        const data = await resp.json();
        if (canceled) return;
        const items = Array.isArray(data?.sites) ? data.sites : [];
        setSiteOptions(items.map((s) => ({ ...s, id: s?.id != null ? String(s.id) : "" })).filter((s) => s.id));
      } catch (err) {
        if (!canceled) {
          console.error("Failed to load sites:", err);
          setSiteOptions([]);
        }
      } finally {
        if (!canceled) setSiteLoading(false);
      }
    };
    loadSites();
    return () => {
      canceled = true;
    };
  }, []);

  const prismLanguage = TYPE_MAP[assembly.type]?.prism || "powershell";

  const updateAssembly = (partial) => {
    setAssembly((prev) => ({ ...prev, ...partial }));
  };

  const updateSitesMode = (modeValue) => {
    setAssembly((prev) => ({
      ...prev,
      sites: {
        mode: modeValue,
        values: modeValue === "specific" ? prev.sites.values || [] : []
      }
    }));
  };

  const updateSelectedSites = (values) => {
    const arr = Array.isArray(values)
      ? values
      : typeof values === "string"
        ? values.split(",").map((v) => v.trim()).filter(Boolean)
        : [];
    setAssembly((prev) => ({
      ...prev,
      sites: {
        mode: "specific",
        values: arr.map((v) => String(v))
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

  const siteScopeValue = assembly.sites?.mode === "specific" ? "specific" : "all";
  const selectedSiteValues = Array.isArray(assembly.sites?.values)
    ? assembly.sites.values.map((v) => String(v))
    : [];

  return (
    <Box sx={{ display: "flex", flexDirection: "column", flex: 1, height: "100%", overflow: "hidden" }}>
      <Box sx={{ flex: 1, overflow: "auto", p: 2 }}>
        <Paper sx={{ p: 3, ...SECTION_CARD_SX, minHeight: "100%" }} elevation={0}>
          <Box sx={{ mb: 3 }}>
            <Typography variant="h5" sx={{ color: "#58a6ff", fontWeight: 500, mb: 0.5 }}>
              Assembly Editor
            </Typography>
            <Typography variant="body2" sx={{ color: "#9ba3b4" }}>
              Create and edit variables, scripts, and other fields related to assemblies.
            </Typography>
          </Box>

          <Box sx={{ display: "flex", alignItems: "center", gap: 2, mb: 3 }}>
            <Box sx={{ flex: 1 }}>
              <Typography variant="caption" sx={SECTION_TITLE_SX}>
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
                backgroundColor: saving ? "rgba(88,166,255,0.12)" : "#1f2329",
                "&:hover": {
                  borderColor: "#7db7ff",
                  backgroundColor: "rgba(88,166,255,0.18)"
                },
                "&.Mui-disabled": {
                  color: "#3c4452",
                  borderColor: "#2d333b"
                }
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
                sx={{ ...INPUT_BASE_SX, mb: 2 }}
              />
              <TextField
                label="Description"
                value={assembly.description}
                onChange={(e) => updateAssembly({ description: e.target.value })}
                multiline
                minRows={3}
                fullWidth
                variant="outlined"
                sx={INPUT_BASE_SX}
              />
            </Grid>
            <Grid item xs={12} md={6}>
              <TextField
                select
                fullWidth
                label="Category"
                value={assembly.category}
                onChange={(e) => updateAssembly({ category: e.target.value })}
                sx={{ ...SELECT_BASE_SX, mb: 2 }}
                SelectProps={{ MenuProps: MENU_PROPS }}
              >
                {CATEGORY_OPTIONS.map((o) => (
                  <MenuItem key={o.key} value={o.key}>{o.label}</MenuItem>
                ))}
              </TextField>

              <TextField
                select
                fullWidth
                label="Type"
                value={assembly.type}
                onChange={(e) => updateAssembly({ type: e.target.value })}
                sx={SELECT_BASE_SX}
                SelectProps={{ MenuProps: MENU_PROPS }}
              >
                {TYPE_OPTIONS.map((o) => (
                  <MenuItem key={o.key} value={o.key}>{o.label}</MenuItem>
                ))}
              </TextField>
            </Grid>
          </Grid>

          <Box sx={{ mt: 3 }}>
            <Typography variant="caption" sx={{ ...SECTION_TITLE_SX, mb: 1 }}>
              Script Content
            </Typography>
            <Box sx={{ border: "1px solid #2d333b", borderRadius: 1, background: "#1f2329" }}>
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
                  background: "#1f2329",
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
                sx={INPUT_BASE_SX}
                helperText="Timeout this script if not completed within X seconds"
              />
            </Grid>
            <Grid item xs={12} md={6}>
              <Typography variant="caption" sx={{ ...SECTION_TITLE_SX, mb: 1 }}>
                Sites
              </Typography>
              <TextField
                select
                fullWidth
                label="Site Scope"
                value={siteScopeValue}
                onChange={(e) => updateSitesMode(e.target.value)}
                sx={{ ...SELECT_BASE_SX, mb: siteScopeValue === "specific" ? 2 : 0 }}
                SelectProps={{ MenuProps: MENU_PROPS }}
              >
                <MenuItem value="all">All Sites</MenuItem>
                <MenuItem value="specific">Specific Sites</MenuItem>
              </TextField>
              {siteScopeValue === "specific" ? (
                <TextField
                  select
                  fullWidth
                  label="Allowed Sites"
                  value={selectedSiteValues}
                  onChange={(e) => updateSelectedSites(Array.isArray(e.target.value) ? e.target.value : [])}
                  sx={SELECT_BASE_SX}
                  SelectProps={{
                    multiple: true,
                    renderValue: (selected) => {
                      if (!selected || selected.length === 0) {
                        return <Typography sx={{ color: "#6b7687" }}>Select sites</Typography>;
                      }
                      const names = selected.map((val) => siteOptionMap.get(String(val))?.name || String(val));
                      return names.join(", ");
                    },
                    MenuProps: MENU_PROPS
                  }}
                >
                  {siteLoading ? (
                    <MenuItem disabled>
                      <ListItemText primary="Loading sites..." />
                    </MenuItem>
                  ) : siteOptions.length ? (
                    siteOptions.map((site) => {
                      const value = String(site.id);
                      const checked = selectedSiteValues.includes(value);
                      return (
                        <MenuItem key={value} value={value} sx={{ display: "flex", alignItems: "flex-start", gap: 1 }}>
                          <Checkbox checked={checked} sx={{ color: "#58a6ff", mr: 1 }} />
                          <ListItemText
                            primary={site.name}
                            secondary={site.description ? site.description : undefined}
                            primaryTypographyProps={{ sx: { color: "#e6edf3" } }}
                            secondaryTypographyProps={{ sx: { color: "#7f8794" } }}
                          />
                        </MenuItem>
                      );
                    })
                  ) : (
                    <MenuItem disabled>
                      <ListItemText primary="No sites available" />
                    </MenuItem>
                  )}
                </TextField>
              ) : null}
            </Grid>
          </Grid>

          <Box sx={{ mt: 4 }}>
            <Typography variant="caption" sx={{ ...SECTION_TITLE_SX, mb: 1 }}>
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
                    sx={{ p: 2, bgcolor: "#1f2329", border: "1px solid #2d333b", borderRadius: 1 }}
                  >
                    <Grid container spacing={2} alignItems="center">
                      <Grid item xs={12} md={3}>
                        <TextField
                          label="Variable Name"
                          value={variable.name}
                          onChange={(e) => updateVariable(variable.id, { name: e.target.value })}
                          fullWidth
                          variant="outlined"
                          sx={INPUT_BASE_SX}
                        />
                      </Grid>
                      <Grid item xs={12} md={3}>
                        <TextField
                          label="Display Label"
                          value={variable.label}
                          onChange={(e) => updateVariable(variable.id, { label: e.target.value })}
                          fullWidth
                          variant="outlined"
                          sx={INPUT_BASE_SX}
                        />
                      </Grid>
                      <Grid item xs={12} md={2}>
                        <TextField
                          select
                          fullWidth
                          label="Type"
                          value={variable.type}
                          onChange={(e) => updateVariable(variable.id, { type: e.target.value })}
                          sx={SELECT_BASE_SX}
                          SelectProps={{ MenuProps: MENU_PROPS }}
                        >
                          {VARIABLE_TYPE_OPTIONS.map((opt) => (
                            <MenuItem key={opt.key} value={opt.key}>{opt.label}</MenuItem>
                          ))}
                        </TextField>
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
                            sx={INPUT_BASE_SX}
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
                          sx={INPUT_BASE_SX}
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
            <Typography variant="caption" sx={{ ...SECTION_TITLE_SX, mb: 1 }}>
              Files
            </Typography>
            <Typography variant="body2" sx={{ color: "#9ba3b4", mb: 2 }}>
              Upload supporting files. They will be embedded as Base64 and available to the assembly at runtime.
            </Typography>
            {(assembly.files || []).length ? (
              <Box sx={{ display: "flex", flexDirection: "column", gap: 1.5 }}>
                {assembly.files.map((file) => (
                  <Paper
                    key={file.id}
                    sx={{
                      p: 1.5,
                      bgcolor: "#1f2329",
                      border: "1px solid #2d333b",
                      display: "flex",
                      alignItems: "center",
                      justifyContent: "space-between"
                    }}
                  >
                    <Box>
                      <Typography variant="body2" sx={{ color: "#e6edf3" }}>{file.fileName}</Typography>
                      <Typography variant="caption" sx={{ color: "#7f8794" }}>{formatBytes(file.size)}{file.mimeType ? ` • ${file.mimeType}` : ""}</Typography>
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
