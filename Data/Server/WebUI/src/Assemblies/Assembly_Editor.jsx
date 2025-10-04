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

const BACKGROUND_COLORS = {
  field: "#1C1C1C", /* Shared surface color for text fields, dropdown inputs, and script editors */
  sectionCard: "#2E2E2E", /* Background for section container cards */
  menuSelected: "rgba(88,166,255,0.16)", /* Background for selected dropdown items */
  menuSelectedHover: "rgba(88,166,255,0.24)", /* Background for hovered selected dropdown items */
  primaryActionSaving: "rgba(88,166,255,0.12)", /* Background for primary action button while saving */
  primaryActionHover: "rgba(88,166,255,0.18)", /* Background for primary action button hover state */
  dialog: "#1a1f27" /* Background for modal dialogs */
};

const INPUT_BASE_SX = {
  "& .MuiOutlinedInput-root": {
    bgcolor: BACKGROUND_COLORS.field,
    color: "#e6edf3", /* Text Color */
    borderRadius: 1, /* Roundness of UI Elements */
    minHeight: 40,
    "& fieldset": { borderColor: "#2b3544" },
    "&:hover fieldset": { borderColor: "#3a4657" },
    "&.Mui-focused fieldset": { borderColor: "#58a6ff" }
  },

  "& .MuiOutlinedInput-input": {
    padding: "9px 12px",
    fontSize: "0.95rem",
    lineHeight: 1.4
  },

  "& .MuiOutlinedInput-inputMultiline": {
    padding: "9px 12px"
  },

  "& .MuiInputLabel-root": {
    color: "#9ba3b4",
    transform: "translate(12px, 11px) scale(0.8)"        // label at rest (inside field)
  },
  "& .MuiInputLabel-root.Mui-focused": { color: "#58a6ff" },
  "& .MuiInputLabel-root.MuiInputLabel-shrink": {
    transform: "translate(12px, -6px) scale(0.75)"      // floated label position
  },

  "& input[type=number]": { MozAppearance: "textfield" },
  "& input[type=number]::-webkit-outer-spin-button": { WebkitAppearance: "none", margin: 0 },
  "& input[type=number]::-webkit-inner-spin-button": { WebkitAppearance: "none", margin: 0 }
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
  fontWeight: 400,
  fontSize: "14px",
  letterSpacing: 0.2
};

const SECTION_CARD_SX = {
  bgcolor: BACKGROUND_COLORS.sectionCard,
  borderRadius: 2,
  border: "1px solid #262f3d",
};

const MENU_PROPS = {
  PaperProps: {
    sx: {
      bgcolor: BACKGROUND_COLORS.field,
      color: "#e6edf3",
      border: "1px solid #2b3544",
      "& .MuiMenuItem-root.Mui-selected": {
        bgcolor: BACKGROUND_COLORS.menuSelected
      },
      "& .MuiMenuItem-root.Mui-selected:hover": {
        bgcolor: BACKGROUND_COLORS.menuSelectedHover
      }
    }
  }
};

function keyBy(arr) {
  return Object.fromEntries(arr.map((o) => [o.key, o]));
}

const TYPE_MAP = keyBy(TYPE_OPTIONS_ALL);

const PAGE_BACKGROUND = "#0d1117"; /* Color of Void Space Between Sidebar and Page */

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

function decodeBase64String(data = "") {
  if (typeof data !== "string") {
    return { success: false, value: "" };
  }
  if (!data.trim()) {
    return { success: true, value: "" };
  }
  try {
    if (typeof window !== "undefined" && typeof window.atob === "function") {
      const binary = window.atob(data);
      if (typeof TextDecoder !== "undefined") {
        const decoder = new TextDecoder("utf-8", { fatal: false });
        return { success: true, value: decoder.decode(Uint8Array.from(binary, (c) => c.charCodeAt(0))) };
      }
      return { success: true, value: binary };
    }
  } catch (err) {
    // fall through to Buffer fallback
  }
  try {
    if (typeof Buffer !== "undefined") {
      return { success: true, value: Buffer.from(data, "base64").toString("utf-8") };
    }
  } catch (err) {
    // ignore
  }
  return { success: false, value: "" };
}

function encodeBase64String(text = "") {
  if (typeof text !== "string") {
    text = text == null ? "" : String(text);
  }
  if (!text) return "";
  try {
    if (typeof TextEncoder !== "undefined" && typeof window !== "undefined" && typeof window.btoa === "function") {
      const encoder = new TextEncoder();
      const bytes = encoder.encode(text);
      let binary = "";
      bytes.forEach((b) => { binary += String.fromCharCode(b); });
      return window.btoa(binary);
    }
  } catch (err) {
    // fall through to Buffer fallback
  }
  try {
    if (typeof Buffer !== "undefined") {
      return Buffer.from(text, "utf-8").toString("base64");
    }
  } catch (err) {
    // ignore
  }
  return "";
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
    const legacyScript = Array.isArray(doc.script_lines)
      ? doc.script_lines.map((line) => (line == null ? "" : String(line))).join("\n")
      : "";
    const script = doc.script ?? doc.content ?? legacyScript;
    if (typeof script === "string") {
      const encoding = (doc.script_encoding || doc.scriptEncoding || "").toLowerCase();
      if (["base64", "b64", "base-64"].includes(encoding)) {
        const decoded = decodeBase64String(script);
        assembly.script = decoded.success ? decoded.value : "";
      } else if (!encoding) {
        const decoded = decodeBase64String(script);
        assembly.script = decoded.success ? decoded.value : script;
      } else {
        assembly.script = script;
      }
    } else {
      assembly.script = legacyScript;
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
  const timeoutNumeric = Number(assembly.timeoutSeconds);
  const timeoutSeconds = Number.isFinite(timeoutNumeric) ? Math.max(0, Math.round(timeoutNumeric)) : 3600;
  const encodedScript = encodeBase64String(normalizedScript);
  return {
    version: 1,
    name: assembly.name?.trim() || "",
    description: assembly.description || "",
    category: assembly.category || "script",
    type: assembly.type || "powershell",
    script: encodedScript,
    script_encoding: "base64",
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
    <Dialog open={open} onClose={onCancel} PaperProps={{ sx: { bgcolor: BACKGROUND_COLORS.dialog, color: "#fff" } }}>
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
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        flex: 1,
        height: "100%",
        overflow: "hidden",
        ml: -0.95,
        mt: -0.95,
        bgcolor: PAGE_BACKGROUND
      }}
    >
      <Box sx={{ flex: 1, overflow: "auto", p: { xs: 2, md: 3 } }}>
        <Paper sx={{ p: { xs: 2.5, md: 3 }, ...SECTION_CARD_SX, minHeight: "100%" }} elevation={0}>
          
      <Grid container alignItems="center" justifyContent="space-between" sx={{ mb: 3 }}>
        {/* Left half */}
        <Grid item xs={12} sm={6}>
          <Box>
            <Typography variant="h6" sx={{ color: '#58a6ff', mb: 0 }}>Assembly Editor</Typography>
            <Typography variant="body2" sx={{ color: '#aaa' }}>Create and edit variables, scripts, and other fields related to assemblies.</Typography>
          </Box>
        </Grid>

        {/* Right half */}
        <Grid item xs={12} sm={6}>
          <Box
            sx={{
              display: "flex",
              alignItems: "center",
              gap: 1.5,
              flexWrap: "wrap",
              justifyContent: { xs: "flex-start", sm: "flex-end" },
              mt: { xs: 2, sm: 0 }
            }}
          >
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
                backgroundColor: saving
                  ? BACKGROUND_COLORS.primaryActionSaving
                  : BACKGROUND_COLORS.field,
                "&:hover": {
                  borderColor: "#7db7ff",
                  backgroundColor: BACKGROUND_COLORS.primaryActionHover
                },
                "&.Mui-disabled": {
                  color: "#3c4452",
                  borderColor: "#2b3544"
                }
              }}
            >
              {saving ? "Saving..." : "Save Assembly"}
            </Button>
          </Box>
        </Grid>
      </Grid>


          <Box
            sx={{
              display: "flex",
              alignItems: "flex-start",
              justifyContent: "space-between",
              gap: 2,
              mb: 0,
              flexWrap: "wrap"
            }}
          >
            <Box sx={{ flex: "1 1 auto", minWidth: 120 }}>
              <Typography variant="caption" sx={SECTION_TITLE_SX}>
                Overview
              </Typography>
            </Box>
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
                minRows={2}
                maxRows={8}
                fullWidth
                variant="outlined"
                sx={{
                  ...INPUT_BASE_SX,
                  "& .MuiOutlinedInput-inputMultiline": {
                    padding: "6px 12px",
                    lineHeight: 1.4
                  }
                }}
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
            <Box sx={{ border: "1px solid #2b3544", borderRadius: 1, background: BACKGROUND_COLORS.field }}>
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
                  background: BACKGROUND_COLORS.field, /* Color of Script Box */
                  outline: "none",
                  minHeight: 320,
                  lineHeight: 1.45,
                  caretColor: "#58a6ff"
                }}
              />
            </Box>
          </Box>

          <Grid container spacing={2} sx={{ mt: 4 }}>
            <Grid item xs={12} md={6}>
              <TextField
                label="Timeout (seconds)"
                type="text"
                inputMode="numeric"
                value={assembly.timeoutSeconds}
                onChange={(e) => {
                  const nextValue = e.target.value.replace(/[^0-9]/g, "");
                  updateAssembly({ timeoutSeconds: nextValue ? Number(nextValue) : 0 });
                }}
                fullWidth
                variant="outlined"
                sx={INPUT_BASE_SX}
                helperText="Timeout this script if not completed within X seconds"
              />
            </Grid>
            <Grid item xs={12} md={6}>
              <Box
                sx={{
                  display: "flex",
                  flexDirection: { xs: "column", sm: "row" },
                  gap: 2,
                  alignItems: "flex-start"
                }}
              >
                <TextField
                  select
                  fullWidth
                  label="Site Scope"
                  value={siteScopeValue}
                  onChange={(e) => updateSitesMode(e.target.value)}
                  sx={{
                    ...SELECT_BASE_SX,
                    width: { xs: "100%", sm: 320, lg: 360 }
                  }}
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
                    sx={{
                      ...SELECT_BASE_SX,
                      width: { xs: "100%", sm: 360, lg: 420 }
                    }}
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
              </Box>
            </Grid>
          </Grid>

          <Box sx={{ mt: 3 }}>
            <Typography variant="caption" sx={{ ...SECTION_TITLE_SX, mb: 1 }}>
              Environment Variables
            </Typography>
            <Typography variant="body2" sx={{ color: "#9ba3b4", mb: 2 }}>
              Variables are dynamically passed into the script as environment variables at runtime.  They are written like <b>$env:variableName</b> in the script editor.
            </Typography>
            {(assembly.variables || []).length ? (
              <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
                {assembly.variables.map((variable) => (
                  <Paper
                    key={variable.id}
                    sx={{ p: 2, bgcolor: BACKGROUND_COLORS.field, border: "1px solid #2b3544", borderRadius: 1 }}
                  >
                    <Box sx={{ display: "flex", flexDirection: "column", gap: { xs: 2, lg: 1.5 } }}>
                      <Box
                        sx={{
                          display: "flex",
                          flexWrap: "wrap",
                          alignItems: "center",
                          gap: { xs: 2, lg: 1.5 },
                          pt: 0.5
                        }}
                      >
                        <Box sx={{ flex: { xs: "1 1 100%", lg: "0 1 28%" }, minWidth: { lg: 220 } }}>
                          <Tooltip
                            title="This is the name of the variable you will be referencing inside of the script via $env:<variable>."
                            arrow
                            placement="top-start"
                          >
                            <TextField
                              label="Variable"
                              value={variable.name}
                              onChange={(e) => updateVariable(variable.id, { name: e.target.value })}
                              fullWidth
                              variant="outlined"
                              sx={INPUT_BASE_SX}
                            />
                          </Tooltip>
                        </Box>
                        <Box sx={{ flex: { xs: "1 1 100%", lg: "0 1 22%" }, minWidth: { lg: 180 } }}>
                          <Tooltip
                            title="This is the name that will be given to the variable and seen by Borealis server operators."
                            arrow
                            placement="top-start"
                          >
                            <TextField
                              label="Display Label"
                              value={variable.label}
                              onChange={(e) => updateVariable(variable.id, { label: e.target.value })}
                              fullWidth
                              variant="outlined"
                              sx={INPUT_BASE_SX}
                            />
                          </Tooltip>
                        </Box>

                        <Box sx={{ flex: { xs: "1 1 100%", lg: "0 1 18%" }, minWidth: { lg: 160 } }}>
                          <Tooltip
                            title="This defines the type of variable data the script should expect."
                            arrow
                            placement="top-start"
                          >
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
                          </Tooltip>
                        </Box>
                        <Box
                          sx={{
                            flex: { xs: "1 1 100%", lg: "0 1 24%" },
                            minWidth: { lg: 220 },
                            display: "flex",
                            alignItems: "center"
                          }}
                        >
                          {variable.type === "boolean" ? (
                            <Tooltip
                              title="This is the value that will be pre-populated in the assembly when ran.  Use a sensible default value."
                              arrow
                              placement="top-start"
                            >
                              <FormControlLabel
                                control={
                                  <Checkbox
                                    checked={Boolean(variable.defaultValue)}
                                    onChange={(e) => updateVariable(variable.id, { defaultValue: e.target.checked })}
                                    sx={{ color: "#58a6ff" }}
                                  />
                                }
                                label="Default Value"
                                sx={{
                                  color: "#9ba3b4",
                                  m: 0,
                                  "& .MuiFormControlLabel-label": { fontSize: "0.95rem" }
                                }}
                              />
                            </Tooltip>
                          ) : (
                            <Tooltip
                              title="This is the value that will be pre-populated in the assembly when ran.  Use a sensible default value."
                              arrow
                              placement="top-start"
                            >
                              <TextField
                                label="Default Value"
                                value={variable.defaultValue ?? ""}
                                onChange={(e) => updateVariable(variable.id, { defaultValue: e.target.value })}
                                fullWidth
                                variant="outlined"
                                sx={INPUT_BASE_SX}
                              />
                            </Tooltip>
                          )}
                        </Box>
                        <Box sx={{ flex: { xs: "1 1 100%", lg: "0 1 28%" }, minWidth: { lg: 220 } }}>
                          <Tooltip
                            title="Instruct the operator in why this variable exists and how to set it appropriately."
                            arrow
                            placement="top-start"
                          >
                            <TextField
                              label="Description"
                              value={variable.description}
                              onChange={(e) => updateVariable(variable.id, { description: e.target.value })}
                              fullWidth
                              variant="outlined"
                              sx={INPUT_BASE_SX}
                            />
                          </Tooltip>
                        </Box>
                        <Box
                          sx={{
                            flex: { xs: "1 1 100%", lg: "0 0 auto" },
                            display: "flex",
                            justifyContent: "flex-end",
                            alignItems: "center",
                            ml: { lg: "auto" }
                          }}
                        >
                          <Tooltip title="Remove this Variable." arrow>
                            <IconButton onClick={() => removeVariable(variable.id)} sx={{ color: "#ff6b6b" }}>
                              <DeleteIcon />
                            </IconButton>
                          </Tooltip>
                        </Box>
                      </Box>
                      <Box
                        sx={{
                          display: "flex",
                          flexWrap: "wrap",
                          gap: { xs: 2, lg: 1.5 }
                        }}
                      >
                      <Box
                        sx={{
                          flex: { xs: "1 1 100%", lg: "0 1 50%" },
                          minWidth: { lg: 220 },
                          display: "flex",
                          alignItems: "center",
                          gap: 1.5
                        }}
                      >
                        <Typography
                          variant="caption"
                          sx={{ color: "#9ba3b4", fontSize: "0.75rem", ml: 0.5}} /* Left-Hand Spacing for the "Required" label to the left of the checkbox */
                        >
                          Required
                        </Typography>
                        <Checkbox
                          checked={Boolean(variable.required)}
                          onChange={(e) =>
                            updateVariable(variable.id, { required: e.target.checked })
                          }
                          sx={{
                            color: "#58a6ff",
                            p: 0.5,
                          }}
                          inputProps={{ "aria-label": "Required" }}
                        />
                      </Box>
                      </Box>
                    </Box>
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
                      bgcolor: BACKGROUND_COLORS.field,
                      border: "1px solid #2b3544",
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