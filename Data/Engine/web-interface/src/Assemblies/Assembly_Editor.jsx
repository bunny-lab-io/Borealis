import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Paper,
  Typography,
  Button,
  TextField,
  Menu, MenuItem,
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
import {
  Add as AddIcon,
  Delete as DeleteIcon,
  UploadFile as UploadFileIcon,
  Code as CodeIcon,
} from "@mui/icons-material";
import Prism from "prismjs";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-powershell";
import "prismjs/components/prism-batch";
import "prismjs/themes/prism-okaidia.css";
import Editor from "react-simple-code-editor";
import { ConfirmDeleteDialog } from "../Dialogs";
import { DomainBadge, DirtyStatePill, DOMAIN_OPTIONS } from "./Assembly_Badges";
import {
  decodeBase64String,
  normalizeVariablesFromServer,
  normalizeFilesFromServer
} from "./assemblyUtils";

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

const PAGE_ICON = CodeIcon;
const PAGE_TITLE_SCRIPT = "Assembly Editor";
const PAGE_TITLE_ANSIBLE = "Ansible Assembly Editor";
const PAGE_SUBTITLE_SCRIPT = "Edit Borealis script assemblies, variables, and payloads before scheduling.";
const PAGE_SUBTITLE_ANSIBLE = "Author Ansible playbooks with Borealis variables, inventory, and credential bindings.";

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

function downloadJsonFile(fileName, data) {
  const safeName = fileName && fileName.trim() ? fileName.trim() : "assembly.json";
  const content = JSON.stringify(data, null, 2);
  const blob = new Blob([content], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = safeName.endsWith(".json") ? safeName : `${safeName}.json`;
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  URL.revokeObjectURL(url);
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
        assembly.script = decoded.success ? decoded.value : script;
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
    assembly_type: (assembly.type || "").toLowerCase() === "ansible" ? "ansible" : "script",
    assembly_subtype: assembly.type || "powershell",
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
      <DialogTitle>Rename Assembly</DialogTitle>
      <DialogContent>
        <TextField
          autoFocus
          margin="dense"
          label="Assembly Name"
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
  mode = "script",
  initialAssembly = null,
  onConsumeInitialData,
  onSaved,
  userRole = "User",
  onPageMetaChange,
}) {
  const normalizedMode = mode === "ansible" ? "ansible" : "script";
  const isAnsible = normalizedMode === "ansible";
  const defaultType = isAnsible ? "ansible" : "powershell";
  const [assembly, setAssembly] = useState(() => defaultAssembly(defaultType));
  const [assemblyGuid, setAssemblyGuid] = useState(initialAssembly?.row?.assemblyGuid || null);
  const [domain, setDomain] = useState(() => (initialAssembly?.row?.domain || "user").toLowerCase());
  const [fileName, setFileName] = useState(() => sanitizeFileName(initialAssembly?.row?.name || ""));
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [siteOptions, setSiteOptions] = useState([]);
  const [siteLoading, setSiteLoading] = useState(false);
  const [queueInfo, setQueueInfo] = useState(initialAssembly?.row?.queueEntry || null);
  const [isDirtyQueue, setIsDirtyQueue] = useState(Boolean(initialAssembly?.row?.isDirty));
  const [devModeEnabled, setDevModeEnabled] = useState(false);
  const [devModeBusy, setDevModeBusy] = useState(false);
  const importInputRef = useRef(null);
  const [menuAnchorEl, setMenuAnchorEl] = useState(null);
  const [errorMessage, setErrorMessage] = useState("");
  const isAdmin = (userRole || "").toLowerCase() === "admin";

  const pageTitle = useMemo(
    () => (isAnsible ? PAGE_TITLE_ANSIBLE : PAGE_TITLE_SCRIPT),
    [isAnsible]
  );
  const pageSubtitle = useMemo(
    () => (isAnsible ? PAGE_SUBTITLE_ANSIBLE : PAGE_SUBTITLE_SCRIPT),
    [isAnsible]
  );
  const sendNotification = useCallback(
    async ({ message, variant = "info" }) => {
      if (!message) return;
      try {
        await fetch("/api/notifications/notify", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          credentials: "include",
          body: JSON.stringify({
            title: pageTitle,
            message,
            icon: "code",
            variant,
          }),
        });
      } catch {
        /* notifications are best-effort */
      }
    },
    [pageTitle]
  );

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

  useEffect(() => {
    let canceled = false;

    const hydrateFromDocument = (document) => {
      const doc = fromServerDocument(document || {}, defaultType);
      setAssembly(doc);
      setFileName((prev) => prev || sanitizeFileName(doc.name || ""));
    };

    const hydrateNewContext = (ctx) => {
      const doc = defaultAssembly(ctx?.defaultType || defaultType);
      if (ctx?.name) doc.name = ctx.name;
      if (ctx?.description) doc.description = ctx.description;
      if (ctx?.category) doc.category = ctx.category;
      if (ctx?.type) doc.type = ctx.type;
      hydrateFromDocument(doc);
      setAssemblyGuid(null);
      setDomain((ctx?.domain || initialAssembly?.row?.domain || "user").toLowerCase());
      setQueueInfo(null);
      setIsDirtyQueue(false);
      const suggested = ctx?.suggestedFileName || ctx?.name || doc.name || "";
      setFileName(sanitizeFileName(suggested));
    };

    const hydrateExisting = async (guid, row) => {
      try {
        setLoading(true);
        const resp = await fetch(`/api/assemblies/${encodeURIComponent(guid)}/export`);
        if (!resp.ok) {
          const problem = await resp.text();
          throw new Error(problem || `Failed to load assembly (HTTP ${resp.status})`);
        }
        const data = await resp.json();
        if (canceled) return;
        const payload = data?.payload && typeof data.payload === "object" ? data.payload : {};
        const enrichedDoc = { ...payload };
        const fallbackName =
          data?.display_name || row?.name || assembly.name || "";
        enrichedDoc.name = enrichedDoc.name || fallbackName;
        enrichedDoc.display_name = enrichedDoc.display_name || fallbackName;
        enrichedDoc.description =
          enrichedDoc.description ||
          data?.summary ||
          row?.description ||
          "";
        enrichedDoc.category = enrichedDoc.category || row?.category || "";
        enrichedDoc.type =
          enrichedDoc.type ||
          data?.assembly_subtype ||
          row?.assembly_subtype ||
          row?.assemblyType ||
          defaultType;
        hydrateFromDocument({ ...enrichedDoc });
        setAssemblyGuid(data?.assembly_guid || guid);
        setDomain((data?.source || data?.domain || row?.domain || "user").toLowerCase());
        setQueueInfo({
          dirty_since: data?.dirty_since || row?.queueEntry?.dirty_since || null,
          last_persisted: data?.last_persisted || row?.queueEntry?.last_persisted || null,
        });
        setIsDirtyQueue(Boolean(data?.is_dirty));
        const exportName = sanitizeFileName(
          data?.display_name || row?.name || guid
        );
        setFileName(exportName);
      } catch (err) {
        console.error("Failed to load assembly:", err);
        if (!canceled) {
          setErrorMessage(err?.message || "Failed to load assembly data.");
        }
      } finally {
        if (!canceled) {
          setLoading(false);
          onConsumeInitialData?.();
        }
      }
    };

    const row = initialAssembly?.row;
    const context = row?.createContext || initialAssembly?.createContext;

    if (row?.assemblyGuid) {
      hydrateExisting(row.assemblyGuid, row);
      return () => {
        canceled = true;
      };
    }

    if (context) {
      hydrateNewContext(context);
      onConsumeInitialData?.();
      return () => {
        canceled = true;
      };
    }

    return () => {
      canceled = true;
    };
  }, [initialAssembly, defaultType, onConsumeInitialData]);

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

  const canWriteToDomain = domain === "user" || (isAdmin && devModeEnabled);

  const handleSaveAssembly = async () => {
    if (!assembly.name.trim()) {
      alert("Assembly Name is required.");
      return;
    }
    const document = toServerDocument(assembly);
    setSaving(true);
    setErrorMessage("");
    try {
      const resp = await fetch("/api/assemblies/import", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          document,
          domain,
          assembly_guid: assemblyGuid || undefined,
        }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.error || data?.message || `HTTP ${resp.status}`);
      }
      const nextGuid = data?.assembly_guid || assemblyGuid;
      setAssemblyGuid(nextGuid || null);
      const nextDomain = (data?.source || data?.domain || domain || "user").toLowerCase();
      setDomain(nextDomain);
      setQueueInfo({
        dirty_since: data?.dirty_since || null,
        last_persisted: data?.last_persisted || null,
      });
      setIsDirtyQueue(Boolean(data?.is_dirty));
      if (data?.display_name) {
        setAssembly((prev) => ({ ...prev, name: data.display_name }));
        setFileName(sanitizeFileName(data.display_name));
      } else {
        setFileName((prev) => prev || sanitizeFileName(assembly.name));
      }
      onSaved?.();
    } catch (err) {
      console.error("Failed to save assembly:", err);
      const message = err?.message || "Failed to save assembly.";
      setErrorMessage(message);
      alert(message);
    } finally {
      setSaving(false);
    }
  };

  const handleRenameConfirm = () => {
    const trimmed = (renameValue || assembly.name || "").trim();
    if (!trimmed) {
      setRenameOpen(false);
      return;
    }
    setAssembly((prev) => ({ ...prev, name: trimmed }));
    setFileName(sanitizeFileName(trimmed));
    setRenameOpen(false);
  };

  const handleDeleteAssembly = async () => {
    if (!assemblyGuid) {
      setDeleteOpen(false);
      return;
    }
    setSaving(true);
    setErrorMessage("");
    try {
      const resp = await fetch(`/api/assemblies/${encodeURIComponent(assemblyGuid)}`, {
        method: "DELETE",
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.error || data?.message || `HTTP ${resp.status}`);
      }
      setDeleteOpen(false);
      onSaved?.();
    } catch (err) {
      console.error("Failed to delete assembly:", err);
      const message = err?.message || "Failed to delete assembly.";
      setErrorMessage(message);
      alert(message);
    } finally {
      setSaving(false);
    }
  };

  const handleDevModeToggle = async (enabled) => {
    setDevModeBusy(true);
    setErrorMessage("");
    try {
      const resp = await fetch("/api/assemblies/dev-mode/switch", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.error || data?.message || `HTTP ${resp.status}`);
      }
      const nextDevMode = Boolean(data?.dev_mode);
      setDevModeEnabled(nextDevMode);
      sendNotification({
        variant: nextDevMode ? "warning" : "info",
        message: nextDevMode ? "Developer Mode Enabled" : "Developer Mode Disabled",
      });
    } catch (err) {
      console.error("Failed to toggle Dev Mode:", err);
      const message = err?.message || "Failed to update Dev Mode.";
      setErrorMessage(message);
      alert(message);
    } finally {
      setDevModeBusy(false);
    }
  };

  const handleFlushQueue = async () => {
    setDevModeBusy(true);
    setErrorMessage("");
    try {
      const resp = await fetch("/api/assemblies/dev-mode/write", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.error || data?.message || `HTTP ${resp.status}`);
      }
      setIsDirtyQueue(false);
      setQueueInfo((prev) => ({
        ...(prev || {}),
        dirty_since: null,
        last_persisted: new Date().toISOString(),
      }));
    } catch (err) {
      console.error("Failed to flush assembly queue:", err);
      const message = err?.message || "Failed to flush queued writes.";
      setErrorMessage(message);
      alert(message);
    } finally {
      setDevModeBusy(false);
    }
  };

  const handleExportAssembly = async () => {
    handleMenuClose();
    setErrorMessage("");
    try {
      if (assemblyGuid) {
        const resp = await fetch(`/api/assemblies/${encodeURIComponent(assemblyGuid)}/export`);
        const data = await resp.json().catch(() => ({}));
        if (!resp.ok) {
          throw new Error(data?.error || data?.message || `HTTP ${resp.status}`);
        }
        const exportDoc = { ...data };
        delete exportDoc.queue;
        const exportName = sanitizeFileName(fileName || data?.display_name || assembly.name || assemblyGuid);
        downloadJsonFile(exportName, exportDoc);
      } else {
        const document = toServerDocument(assembly);
        const exportDoc = {
          assembly_guid: assemblyGuid,
          domain,
          assembly_type: isAnsible ? "ansible" : "script",
          assembly_subtype: assembly.type,
          display_name: assembly.name,
          summary: assembly.description,
          category: assembly.category,
          payload: document,
        };
        const exportName = sanitizeFileName(fileName || assembly.name || "assembly");
        downloadJsonFile(exportName, exportDoc);
      }
    } catch (err) {
      console.error("Failed to export assembly:", err);
      const message = err?.message || "Failed to export assembly.";
      setErrorMessage(message);
      alert(message);
    }
  };

  const handleImportAssembly = async (event) => {
    const file = event.target.files && event.target.files[0];
    if (!file) return;
    setErrorMessage("");
    try {
      const text = await file.text();
      const parsed = JSON.parse(text);
      const payload = parsed?.payload || parsed;
      const doc = fromServerDocument(payload || {}, defaultType);
      setAssembly(doc);
      setAssemblyGuid(parsed?.assembly_guid || null);
      setDomain("user");
      setQueueInfo(null);
      setIsDirtyQueue(false);
      const baseName = parsed?.display_name || parsed?.name || file.name.replace(/\.[^.]+$/, "") || "assembly";
      setFileName(sanitizeFileName(baseName));
      alert("Assembly imported. Review details before saving.");
    } catch (err) {
      console.error("Failed to import assembly:", err);
      const message = err?.message || "Failed to import assembly JSON.";
      setErrorMessage(message);
      alert(message);
    } finally {
      event.target.value = "";
    }
  };

  const handleMenuOpen = (event) => {
    setMenuAnchorEl(event.currentTarget);
  };

  const handleMenuClose = () => {
    setMenuAnchorEl(null);
  };

  const triggerImport = () => {
    handleMenuClose();
    importInputRef.current?.click();
  };

  const triggerExport = () => {
    handleExportAssembly();
  };

  const triggerFlushQueue = () => {
    handleMenuClose();
    handleFlushQueue();
  };

  const saveDisabled = saving || loading || !canWriteToDomain;
  const deleteDisabled = !assemblyGuid || saving || loading;
  const renameDisabled = saving || loading;
  const dirtyPillVisible = Boolean(isDirtyQueue);
  const lastPersistedDisplay = queueInfo?.last_persisted
    ? new Date(queueInfo.last_persisted).toLocaleString()
    : null;
  const dirtySinceDisplay = queueInfo?.dirty_since
    ? new Date(queueInfo.dirty_since).toLocaleString()
    : null;

  const pageHeaderBadges = useMemo(
    () =>
      [
        <DomainBadge key="assembly-domain" domain={domain} size="small" />,
        dirtyPillVisible ? <DirtyStatePill key="assembly-dirty" compact /> : null,
      ].filter(Boolean),
    [dirtyPillVisible, domain]
  );

  const pageHeaderActions = useMemo(() => {
    const actions = [];
    if (isAdmin && devModeEnabled) {
      actions.push({
        id: "assembly-flush-queue",
        label: "Flush Queue",
        tone: "warning",
        disabled: devModeBusy || !dirtyPillVisible,
        onClick: triggerFlushQueue,
      });
    }
    if (isAdmin) {
      actions.push({
        id: "assembly-dev-mode",
        label: devModeEnabled ? "Disable Dev Mode" : "Enable Dev Mode",
        tone: "warning",
        disabled: devModeBusy,
        onClick: () => handleDevModeToggle(!devModeEnabled),
      });
    }
    if (assemblyGuid) {
      actions.push({
        id: "assembly-delete",
        label: "Delete",
        icon: <DeleteIcon />,
        tone: "danger",
        disabled: deleteDisabled,
        onClick: () => setDeleteOpen(true),
      });
    }
    actions.push(
      {
        id: "assembly-import-export",
        label: "Import / Export",
        icon: <UploadFileIcon />,
        tone: "secondary",
        onClick: handleMenuOpen,
      },
      {
        id: "assembly-rename",
        label: "Rename",
        tone: "secondary",
        disabled: renameDisabled,
        onClick: () => {
          setRenameValue(assembly.name || "");
          setRenameOpen(true);
        },
      },
      {
        id: "assembly-save",
        label: "Save Assembly",
        tone: "primary",
        disabled: saveDisabled,
        loading: saving,
        onClick: handleSaveAssembly,
      }
    );
    return actions;
  }, [
    assembly.name,
    assemblyGuid,
    deleteDisabled,
    devModeBusy,
    devModeEnabled,
    dirtyPillVisible,
    handleMenuOpen,
    handleSaveAssembly,
    isAdmin,
    renameDisabled,
    saveDisabled,
    saving,
    triggerFlushQueue,
  ]);

  useEffect(() => {
    onPageMetaChange?.({
      page_title: pageTitle,
      page_subtitle: pageSubtitle,
      page_icon: PAGE_ICON,
      page_header_actions: pageHeaderActions,
      page_header_badges: pageHeaderBadges,
    });
    return () => onPageMetaChange?.(null);
  }, [onPageMetaChange, pageHeaderActions, pageHeaderBadges, pageSubtitle, pageTitle]);

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
      <Menu
        anchorEl={menuAnchorEl}
        open={Boolean(menuAnchorEl)}
        onClose={handleMenuClose}
        PaperProps={{ sx: { bgcolor: BACKGROUND_COLORS.dialog, color: "#fff" } }}
      >
        <MenuItem onClick={triggerExport}>Export JSON</MenuItem>
        <MenuItem onClick={triggerImport}>Import JSON</MenuItem>
      </Menu>
      <input
        ref={importInputRef}
        type="file"
        accept="application/json"
        style={{ display: "none" }}
        onChange={handleImportAssembly}
      />

      <Box
        sx={{
          mt: 2,
          mb: 2,
          display: "flex",
          alignItems: "center",
          gap: 2,
          flexWrap: "wrap",
        }}
      >
        <TextField
          select
          label="Domain"
          value={domain}
          onChange={(e) => setDomain(String(e.target.value || "").toLowerCase())}
          disabled={loading}
          sx={{ ...SELECT_BASE_SX, width: 220 }}
          SelectProps={{ MenuProps: MENU_PROPS }}
        >
          {DOMAIN_OPTIONS.map((option) => (
            <MenuItem key={option.value} value={option.value}>
              {option.label}
            </MenuItem>
          ))}
        </TextField>
        {dirtySinceDisplay ? (
          <Typography variant="caption" sx={{ color: "#9ba3b4" }}>
            Dirty since: {dirtySinceDisplay}
          </Typography>
        ) : null}
        {lastPersistedDisplay ? (
          <Typography variant="caption" sx={{ color: "#9ba3b4" }}>
            Last persisted: {lastPersistedDisplay}
          </Typography>
        ) : null}
      </Box>

      {!canWriteToDomain ? (
        <Box
          sx={{
            mb: 2,
            p: 1.5,
            borderRadius: 1,
            border: "1px solid rgba(248, 212, 122, 0.4)",
            backgroundColor: "rgba(248, 212, 122, 0.12)",
          }}
        >
          <Typography variant="body2" sx={{ color: "#f8d47a" }}>
            This domain is read-only. Enable Dev Mode as an administrator to edit or switch to the User domain.
          </Typography>
        </Box>
      ) : null}

      {errorMessage ? (
        <Box
          sx={{
            mb: 2,
            p: 1.5,
            borderRadius: 1,
            border: "1px solid rgba(255, 138, 138, 0.4)",
            backgroundColor: "rgba(255, 138, 138, 0.12)",
          }}
        >
          <Typography variant="body2" sx={{ color: "#ff8a8a" }}>{errorMessage}</Typography>
        </Box>
      ) : null}


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
                placeholder={assemblyGuid ? `Editing assembly: ${assemblyGuid}` : "Start typing your script..."}
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
        onSave={handleRenameConfirm}
      />
      <ConfirmDeleteDialog
        open={deleteOpen}
        message="Deleting this assembly cannot be undone. Continue?"
        onCancel={() => setDeleteOpen(false)}
        onConfirm={handleDeleteAssembly}
      />
    </Box>
  );
}
