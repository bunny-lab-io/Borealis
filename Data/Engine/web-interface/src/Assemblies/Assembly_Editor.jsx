import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Typography,
  Button,
  TextField,
  Menu,
  MenuItem,
  Grid,
  Checkbox,
  IconButton,
  Tooltip,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  Tabs,
  Tab,
} from "@mui/material";
import {
  Add as AddIcon,
  Delete as DeleteIcon,
  UploadFile as UploadFileIcon,
  Code as CodeIcon,
  DatasetLinkedRounded as DatasetLinkedRoundedIcon,
  FolderZipRounded as FolderZipRoundedIcon,
  TerminalRounded as TerminalRoundedIcon,
} from "@mui/icons-material";
import Prism from "prismjs";
import "prismjs/components/prism-json";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-powershell";
import "prismjs/components/prism-batch";
import "prismjs/themes/prism-okaidia.css";
import Editor from "react-simple-code-editor";
import { ConfirmDeleteDialog } from "../Dialogs";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { DomainBadge, DirtyStatePill, DOMAIN_OPTIONS } from "./Assembly_Badges";
import {
  decodeBase64String,
  normalizeVariablesFromServer,
  normalizeFilesFromServer,
  parseAssemblyExport,
} from "./assemblyUtils";

const TYPE_OPTIONS_ALL = [
  { key: "ansible", label: "Ansible Playbook", prism: "yaml" },
  { key: "powershell", label: "PowerShell Script", prism: "powershell" },
  { key: "batch", label: "Batch Script", prism: "batch" },
  { key: "bash", label: "Bash Script", prism: "bash" },
];

const VARIABLE_TYPE_OPTIONS = [
  { key: "string", label: "String" },
  { key: "number", label: "Number" },
  { key: "boolean", label: "Boolean" },
  { key: "credential", label: "Credential" },
];

const MAGIC_UI = {
  panelBg: "rgba(7,10,24,0.84)",
  panelBgSoft: "rgba(6,10,24,0.74)",
  inputBg: "rgba(5,10,24,0.88)",
  panelBorder: "rgba(148, 163, 184, 0.28)",
  panelBorderStrong: "rgba(125, 211, 252, 0.24)",
  textMuted: "#94a3b8",
  textBright: "#e2e8f0",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
  accentC: "#34d399",
  warning: "#f8d47a",
  danger: "#ff8c98",
  glow: "0 14px 30px rgba(2,6,23,0.34)",
};

const INPUT_BASE_SX = {
  "& .MuiOutlinedInput-root": {
    background: MAGIC_UI.inputBg,
    color: MAGIC_UI.textBright,
    borderRadius: 2.5,
    minHeight: 42,
    transition: "border-color 160ms ease, box-shadow 160ms ease, background 160ms ease",
    "& fieldset": {
      borderColor: "rgba(148,163,184,0.28)",
    },
    "&:hover fieldset": {
      borderColor: "rgba(125,211,252,0.48)",
    },
    "&.Mui-focused fieldset": {
      borderColor: MAGIC_UI.accentB,
      boxShadow: "0 0 0 1px rgba(192,132,252,0.28)",
    },
  },
  "& .MuiOutlinedInput-input, & .MuiInputBase-input": {
    padding: "11px 13px",
    fontSize: "0.95rem",
    lineHeight: 1.4,
  },
  "& .MuiOutlinedInput-inputMultiline": {
    padding: "11px 13px",
  },
  "& .MuiInputLabel-root": {
    color: MAGIC_UI.textMuted,
  },
  "& .MuiInputLabel-root.Mui-focused": {
    color: MAGIC_UI.accentA,
  },
  "& .MuiFormHelperText-root": {
    color: MAGIC_UI.textMuted,
    mx: 0.5,
    mt: 0.8,
  },
  "& input[type=number]": { MozAppearance: "textfield" },
  "& input[type=number]::-webkit-outer-spin-button": { WebkitAppearance: "none", margin: 0 },
  "& input[type=number]::-webkit-inner-spin-button": { WebkitAppearance: "none", margin: 0 },
};

const SELECT_BASE_SX = {
  ...INPUT_BASE_SX,
  "& .MuiSelect-select": {
    minHeight: "42px",
    padding: "10px 13px !important",
    display: "flex",
    alignItems: "center",
  },
};

const SECTION_EYEBROW_SX = {
  color: MAGIC_UI.accentA,
  fontWeight: 700,
  fontSize: "0.72rem",
  letterSpacing: 0.8,
  textTransform: "uppercase",
};

const GLASS_PANEL_SX = {
  background: MAGIC_UI.panelBg,
  borderRadius: 3,
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  boxShadow: MAGIC_UI.glow,
  p: { xs: 2, md: 2.5 },
  display: "flex",
  flexDirection: "column",
  gap: 2,
  minWidth: 0,
};

const GLASS_PANEL_SOFT_SX = {
  ...GLASS_PANEL_SX,
  background: MAGIC_UI.panelBgSoft,
  boxShadow: "0 10px 20px rgba(2,6,23,0.22)",
};

const MINI_CARD_SX = {
  borderRadius: 2.25,
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  background: "rgba(4,9,22,0.56)",
  p: 1.5,
  minWidth: 0,
};

const INLINE_PRIMARY_BUTTON_SX = {
  alignSelf: "flex-start",
  borderRadius: 999,
  px: 2.1,
  py: 0.9,
  textTransform: "none",
  fontWeight: 600,
  color: "#06101d",
  backgroundImage: "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)",
  boxShadow: "0 16px 34px rgba(125, 211, 252, 0.16)",
  "&:hover": {
    backgroundImage: "linear-gradient(135deg, #91dcff 0%, #cfa0ff 100%)",
  },
};

const INLINE_SECONDARY_BUTTON_SX = {
  alignSelf: "flex-start",
  borderRadius: 999,
  px: 2,
  py: 0.85,
  textTransform: "none",
  fontWeight: 600,
  color: MAGIC_UI.textBright,
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  background: "rgba(5,10,24,0.84)",
  "&:hover": {
    borderColor: "rgba(125,211,252,0.5)",
    background: "rgba(8,13,30,0.94)",
  },
};

const DELETE_ICON_BUTTON_SX = {
  color: MAGIC_UI.danger,
  border: "1px solid rgba(244,63,94,0.22)",
  background: "rgba(44,8,22,0.38)",
  borderRadius: 999,
  "&:hover": {
    background: "rgba(58,10,28,0.68)",
    borderColor: "rgba(251,113,133,0.42)",
  },
};

const TAB_SECTION_SX = {
  display: "flex",
  flexDirection: "column",
  gap: 1.75,
};

const SUMMARY_FIELDS_GRID_SX = {
  display: "grid",
  gridTemplateColumns: {
    xs: "1fr",
    sm: "repeat(2, minmax(0, 1fr))",
    lg: "repeat(5, minmax(0, 1fr))",
  },
  gap: 1.5,
  alignItems: "start",
};

const VARIABLE_FIELDS_GRID_SX = {
  display: "grid",
  gridTemplateColumns: {
    xs: "1fr",
    sm: "repeat(2, minmax(0, 1fr))",
    lg: "repeat(6, minmax(0, 1fr))",
  },
  gap: 1.5,
  alignItems: "start",
};

const INLINE_CHECKBOX_FIELD_SX = {
  borderRadius: 2.5,
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  background: MAGIC_UI.inputBg,
  minHeight: 56,
  px: 1.5,
  display: "flex",
  alignItems: "center",
  justifyContent: "space-between",
  gap: 1,
};

const VARIABLE_CARD_SX = {
  ...GLASS_PANEL_SOFT_SX,
  p: { xs: 1.75, md: 2 },
  display: "grid",
  gridTemplateColumns: {
    xs: "1fr",
    lg: "minmax(0, 1fr) auto",
  },
  alignItems: "stretch",
  gap: 1.5,
};

const FILE_ROW_SX = {
  borderRadius: 2.25,
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  background: "rgba(5,10,24,0.74)",
  px: { xs: 1.4, md: 1.8 },
  py: 1.35,
  display: "flex",
  alignItems: "center",
  justifyContent: "space-between",
  gap: 1.5,
  flexWrap: "wrap",
};

const EMPTY_STATE_SX = {
  borderRadius: 2.5,
  border: `1px dashed ${MAGIC_UI.panelBorderStrong}`,
  background: "rgba(5,10,24,0.46)",
  p: { xs: 2, md: 2.5 },
};

const PAGE_ICON = CodeIcon;
const PAGE_TITLE_SCRIPT = "Assembly Editor";
const PAGE_TITLE_ANSIBLE = "Ansible Assembly Editor";
const PAGE_SUBTITLE_SCRIPT = "Edit Borealis script assemblies, variables, and payloads before scheduling.";
const PAGE_SUBTITLE_ANSIBLE = "Author Ansible playbooks with Borealis variables, inventory, and credential bindings.";

const NAV_TAB_HEIGHT = 32;
const NAV_TAB_COLORS = {
  text: "#cbd5e1",
  textActive: "#e6f2ff",
  icon: "#8fbfff",
  iconActive: "#7db7ff",
  hover: "rgba(255,255,255,0.05)",
  activeBg:
    "linear-gradient(to top, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)",
};

const ASSEMBLY_EDITOR_TAB_URL_BY_KEY = Object.freeze({
  summary: "summary",
  script: "script",
  variables: "variables",
  files: "files",
});

const ASSEMBLY_EDITOR_TAB_KEY_BY_URL = Object.freeze({
  summary: "summary",
  overview: "summary",
  details: "summary",
  script: "script",
  source: "script",
  variables: "variables",
  environment_variables: "variables",
  files: "files",
  payloads: "files",
});

const MENU_PROPS = {
  PaperProps: {
    sx: {
      background: MAGIC_UI.panelBg,
      color: MAGIC_UI.textBright,
      borderRadius: 2.5,
      border: `1px solid ${MAGIC_UI.panelBorder}`,
      boxShadow: MAGIC_UI.glow,
      "& .MuiMenuItem-root": {
        fontSize: "0.92rem",
      },
      "& .MuiMenuItem-root.Mui-selected": {
        bgcolor: "rgba(125,211,252,0.14)",
      },
      "& .MuiMenuItem-root.Mui-selected:hover": {
        bgcolor: "rgba(125,211,252,0.22)",
      },
      "& .MuiMenuItem-root:hover": {
        bgcolor: "rgba(255,255,255,0.04)",
      },
    },
  },
};

function keyBy(arr) {
  return Object.fromEntries(arr.map((o) => [o.key, o]));
}

const TYPE_MAP = keyBy(TYPE_OPTIONS_ALL);
const VARIABLE_REFERENCE_EXAMPLE_BY_TYPE = Object.freeze({
  ansible: "{{ variableName }}",
  powershell: "$env:variableName",
  batch: "%variableName%",
  bash: "$variableName",
});

const CODE_EDITOR_FONT_FAMILY = 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace';

const CODE_EDITOR_STYLE = {
  fontFamily: CODE_EDITOR_FONT_FAMILY,
  fontSize: 14,
  color: MAGIC_UI.textBright,
  background: "transparent",
  outline: "none",
  minHeight: 320,
  lineHeight: 1.45,
  caretColor: MAGIC_UI.accentA,
};

const CODE_EDITOR_CONTAINER_SX = {
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  borderRadius: 2.5,
  background: "rgba(3,7,18,0.9)",
  overflow: "hidden",
  boxShadow: "inset 0 1px 0 rgba(255,255,255,0.02)",
};

const DIALOG_PAPER_SX = {
  background: MAGIC_UI.panelBg,
  color: MAGIC_UI.textBright,
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  boxShadow: MAGIC_UI.glow,
};

const DIALOG_BUTTON_SX = {
  borderRadius: 999,
  textTransform: "none",
  px: 2,
  fontWeight: 600,
  color: MAGIC_UI.textBright,
};

const DIALOG_PRIMARY_BUTTON_SX = {
  ...DIALOG_BUTTON_SX,
  color: "#06101d",
  backgroundImage: "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)",
  "&:hover": {
    backgroundImage: "linear-gradient(135deg, #91dcff 0%, #cfa0ff 100%)",
  },
};

function buildNavTabsSx(minHeight = NAV_TAB_HEIGHT) {
  return {
    borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
    minHeight,
    height: minHeight,
    "& .MuiTabs-flexContainer": {
      minHeight,
      height: minHeight,
      alignItems: "stretch",
    },
    "& .MuiTab-root": {
      color: NAV_TAB_COLORS.text,
      textTransform: "none",
      fontWeight: 400,
      fontFamily: "inherit",
      fontSize: "0.8rem",
      minHeight,
      height: minHeight,
      opacity: 1,
      borderRadius: 1,
      py: 0.35,
      transition: "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
      "& .MuiTab-iconWrapper": {
        color: NAV_TAB_COLORS.icon,
      },
      "&:hover": {
        background: NAV_TAB_COLORS.hover,
      },
      "&:active": {
        transform: "translateY(0.5px)",
      },
    },
    "& .MuiTab-root.Mui-selected": {
      color: NAV_TAB_COLORS.textActive,
      fontWeight: 600,
      background: NAV_TAB_COLORS.activeBg,
      "& .MuiTab-iconWrapper": {
        color: NAV_TAB_COLORS.iconActive,
      },
      "&:hover": {
        background: NAV_TAB_COLORS.activeBg,
      },
    },
  };
}

function readAssemblyEditorTabFromUrl() {
  if (typeof window === "undefined") {
    return "summary";
  }
  const params = new URLSearchParams(window.location.search || "");
  const rawTab = String(params.get("tab") || "").toLowerCase();
  return ASSEMBLY_EDITOR_TAB_KEY_BY_URL[rawTab] || "summary";
}

function writeAssemblyEditorTabToUrl(tabKey) {
  if (typeof window === "undefined") {
    return;
  }
  const params = new URLSearchParams(window.location.search || "");
  params.set("tab", ASSEMBLY_EDITOR_TAB_URL_BY_KEY[tabKey] || ASSEMBLY_EDITOR_TAB_URL_BY_KEY.summary);
  const search = params.toString();
  const nextLocation = `${window.location.pathname}${search ? `?${search}` : ""}${window.location.hash || ""}`;
  window.history.replaceState(window.history.state, "", nextLocation);
}

function GlassPanel({ children, sx }) {
  return <Box sx={[GLASS_PANEL_SX, sx]}>{children}</Box>;
}

function SectionHeader({ eyebrow, title, detail, action }) {
  return (
    <Box
      sx={{
        display: "flex",
        alignItems: "flex-start",
        justifyContent: "space-between",
        gap: 1.5,
        flexWrap: "wrap",
      }}
    >
      <Box sx={{ minWidth: 0, flex: "1 1 320px" }}>
        {eyebrow ? (
          <Typography variant="caption" sx={SECTION_EYEBROW_SX}>
            {eyebrow}
          </Typography>
        ) : null}
        <Typography
          variant="h6"
          sx={{
            color: MAGIC_UI.textBright,
            fontWeight: 600,
            fontSize: "1.02rem",
            mt: eyebrow ? 0.45 : 0,
          }}
        >
          {title}
        </Typography>
        {detail ? (
          <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted, mt: 0.7, maxWidth: 760 }}>
            {detail}
          </Typography>
        ) : null}
      </Box>
      {action ? <Box sx={{ flexShrink: 0 }}>{action}</Box> : null}
    </Box>
  );
}

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

function generateAssemblyGuid() {
  try {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
      return crypto.randomUUID().toUpperCase();
    }
  } catch {
    // fall through to manual GUID generation
  }

  const template = "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx";
  return template.replace(/[xy]/g, (ch) => {
    const random = Math.floor(Math.random() * 16);
    const value = ch === "x" ? random : ((random & 0x3) | 0x8);
    return value.toString(16);
  }).toUpperCase();
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
    type: defaultType,
    script: "",
    timeoutSeconds: 3600,
    variables: [],
    files: [],
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
  } catch {
    // fall through to Buffer fallback
  }
  try {
    if (typeof Buffer !== "undefined") {
      return Buffer.from(text, "utf-8").toString("base64");
    }
  } catch {
    // ignore
  }
  return "";
}

function fromServerDocument(doc = {}, defaultType = "powershell") {
  const assembly = defaultAssembly(defaultType);
  if (doc && typeof doc === "object") {
    assembly.name = doc.name || doc.display_name || assembly.name;
    assembly.description = doc.description || doc.summary || "";
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
    assembly.variables = normalizeVariablesFromServer(doc.variables);
    assembly.files = normalizeFilesFromServer(doc.files);
  }
  return assembly;
}

function toServerDocument(assembly, assemblyGuid = null) {
  const normalizedScript = typeof assembly.script === "string"
    ? assembly.script.replace(/\r\n/g, "\n")
    : "";
  const timeoutNumeric = Number(assembly.timeoutSeconds);
  const timeoutSeconds = Number.isFinite(timeoutNumeric) ? Math.max(0, Math.round(timeoutNumeric)) : 3600;
  const encodedScript = encodeBase64String(normalizedScript);
  const resolvedGuid = typeof assemblyGuid === "string" && assemblyGuid.trim()
    ? assemblyGuid.trim()
    : "";
  return {
    assembly_guid: resolvedGuid,
    name: assembly.name?.trim() || "",
    description: assembly.description || "",
    type: assembly.type || "powershell",
    script: encodedScript,
    timeout_seconds: timeoutSeconds,
    variables: (assembly.variables || []).map((v) => ({
      name: v.name?.trim() || "",
      label: v.label || "",
      type: v.type || "string",
      default: v.defaultValue ?? "",
      required: Boolean(v.required),
      description: v.description || "",
    })),
    files: (assembly.files || []).map((f) => ({
      file_name: f.fileName || "file.bin",
      size: f.size || 0,
      mime_type: f.mimeType || "",
      data: f.data || "",
    })),
  };
}

function RenameFileDialog({ open, value, onChange, onCancel, onSave }) {
  return (
    <Dialog open={open} onClose={onCancel} PaperProps={{ sx: DIALOG_PAPER_SX }}>
      <DialogTitle sx={{ borderBottom: `1px solid ${MAGIC_UI.panelBorder}` }}>Rename Assembly</DialogTitle>
      <DialogContent sx={{ pt: 2.5 }}>
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
      <DialogActions sx={{ px: 3, py: 2, borderTop: `1px solid ${MAGIC_UI.panelBorder}` }}>
        <Button onClick={onCancel} sx={DIALOG_BUTTON_SX}>Cancel</Button>
        <Button onClick={onSave} sx={DIALOG_PRIMARY_BUTTON_SX}>Save</Button>
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
  const initialRow = initialAssembly?.row || null;
  const initialAssemblyGuid = initialRow?.assemblyGuid || initialAssembly?.assemblyGuid || null;
  const initialDomain = (initialRow?.domain || initialAssembly?.domain || "user").toLowerCase();
  const initialFileNameSource = initialRow?.name || initialAssembly?.name || "";
  const [assembly, setAssembly] = useState(() => defaultAssembly(defaultType));
  const [assemblyGuid, setAssemblyGuid] = useState(initialAssemblyGuid);
  const [domain, setDomain] = useState(() => initialDomain);
  const [fileName, setFileName] = useState(() => sanitizeFileName(initialFileNameSource));
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [queueInfo, setQueueInfo] = useState(initialRow?.queueEntry || null);
  const [isDirtyQueue, setIsDirtyQueue] = useState(Boolean(initialRow?.isDirty));
  const [devModeEnabled, setDevModeEnabled] = useState(false);
  const [devModeBusy, setDevModeBusy] = useState(false);
  const importInputRef = useRef(null);
  const [menuAnchorEl, setMenuAnchorEl] = useState(null);
  const [errorMessage, setErrorMessage] = useState("");
  const [jsonPreviewOpen, setJsonPreviewOpen] = useState(false);
  const [jsonPreviewText, setJsonPreviewText] = useState("");
  const [activeTab, setActiveTab] = useState(() => readAssemblyEditorTabFromUrl());
  const isAdmin = (userRole || "").toLowerCase() === "admin";
  const consumeInitialDataRef = useRef(onConsumeInitialData);

  useEffect(() => {
    consumeInitialDataRef.current = onConsumeInitialData;
  }, [onConsumeInitialData]);

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
        // notifications are best-effort
      }
    },
    [pageTitle]
  );

  const TYPE_OPTIONS = useMemo(
    () => (
      isAnsible
        ? TYPE_OPTIONS_ALL.filter((option) => option.key === "ansible")
        : TYPE_OPTIONS_ALL.filter((option) => option.key !== "ansible")
    ),
    [isAnsible]
  );

  useEffect(() => {
    let canceled = false;

    const normalizeAssemblyPath = (value = "") =>
      String(value || "")
        .replace(/\\/g, "/")
        .replace(/^\/+/, "")
        .trim()
        .toLowerCase();

    const hydrateFromDocument = (document) => {
      const doc = fromServerDocument(document || {}, defaultType);
      setAssembly(doc);
      setFileName((prev) => prev || sanitizeFileName(doc.name || ""));
    };

    const hydrateFromApiPayload = (data, row, guid) => {
      const parsed = parseAssemblyExport(data);
      const fallbackName =
        parsed?.metadata?.display_name ||
        data?.display_name ||
        row?.name ||
        "";

      const enrichedDoc = {
        ...(parsed?.payload && typeof parsed.payload === "object" ? parsed.payload : {}),
        name: fallbackName,
        display_name: fallbackName,
        description:
          parsed?.payload?.description ||
          parsed?.payload?.summary ||
          parsed?.metadata?.summary ||
          data?.summary ||
          row?.description ||
          "",
        type:
          parsed?.type ||
          data?.assembly_subtype ||
          row?.assembly_subtype ||
          row?.assemblyType ||
          defaultType,
        script: parsed?.script || "",
        timeout_seconds:
          parsed?.timeoutSeconds ||
          parsed?.payload?.timeout_seconds ||
          parsed?.payload?.timeout ||
          3600,
        variables: Array.isArray(parsed?.rawVariables) ? parsed.rawVariables : [],
        files: Array.isArray(parsed?.rawFiles) ? parsed.rawFiles : [],
      };

      const hasMeaningfulContent =
        Boolean(fallbackName) ||
        Boolean(enrichedDoc.description) ||
        Boolean(enrichedDoc.script) ||
        (Array.isArray(enrichedDoc.variables) && enrichedDoc.variables.length > 0) ||
        (Array.isArray(enrichedDoc.files) && enrichedDoc.files.length > 0) ||
        (parsed?.payload && typeof parsed.payload === "object" && Object.keys(parsed.payload).length > 0);

      if (!hasMeaningfulContent) {
        throw new Error(`Assembly payload was empty for ${guid}`);
      }

      hydrateFromDocument(enrichedDoc);
      setAssemblyGuid(data?.assembly_guid || parsed?.metadata?.assembly_guid || guid);
      setDomain((data?.source || data?.domain || row?.domain || "user").toLowerCase());
      setQueueInfo({
        dirty_since: data?.dirty_since || row?.queueEntry?.dirty_since || null,
        last_persisted: data?.last_persisted || row?.queueEntry?.last_persisted || null,
      });
      setIsDirtyQueue(Boolean(data?.is_dirty));
      setFileName(sanitizeFileName(fallbackName || guid));
      setErrorMessage("");
      return true;
    };

    const hydrateNewContext = (ctx) => {
      const doc = defaultAssembly(ctx?.defaultType || defaultType);
      if (ctx?.name) doc.name = ctx.name;
      if (ctx?.description) doc.description = ctx.description;
      if (ctx?.type) doc.type = ctx.type;
      hydrateFromDocument(doc);
      setAssemblyGuid(null);
      setDomain((ctx?.domain || initialAssembly?.row?.domain || "user").toLowerCase());
      setQueueInfo(null);
      setIsDirtyQueue(false);
      const suggested = ctx?.suggestedFileName || ctx?.name || doc.name || "";
      setFileName(sanitizeFileName(suggested));
    };

    const resolveGuidFromPath = async (path) => {
      const normalizedPath = normalizeAssemblyPath(path);
      if (!normalizedPath) {
        return null;
      }

      const resp = await fetch("/api/assemblies");
      if (!resp.ok) {
        const problem = await resp.text().catch(() => "");
        throw new Error(problem || `Failed to resolve assembly path (HTTP ${resp.status})`);
      }

      const data = await resp.json();
      const items = Array.isArray(data?.items) ? data.items : [];
      const match = items.find((item) => {
        const candidates = [
          item?.virtual_path,
          item?.path,
        ]
          .map((candidate) => normalizeAssemblyPath(candidate))
          .filter(Boolean);
        return candidates.includes(normalizedPath);
      });

      if (!match) {
        return null;
      }

      return {
        guid: match?.assembly_guid || match?.assembly_id || null,
        row: {
          assemblyGuid: match?.assembly_guid || match?.assembly_id || null,
          name: match?.name || match?.display_name || "",
          description: match?.description || match?.summary || "",
          domain: (match?.source || match?.domain || "user").toLowerCase(),
          assemblyType: match?.assembly_subtype || defaultType,
          sourcePath: match?.virtual_path || match?.path || path,
        },
      };
    };

    const hydrateExisting = async (guid, row) => {
      try {
        setLoading(true);
        setErrorMessage("");

        const detailResp = await fetch(`/api/assemblies/${encodeURIComponent(guid)}`);
        if (detailResp.ok) {
          const detailData = await detailResp.json();
          if (!canceled) {
            try {
              hydrateFromApiPayload(detailData, row, guid);
              return;
            } catch {
              // fall back to legacy export response
            }
          }
        }

        const exportResp = await fetch(`/api/assemblies/${encodeURIComponent(guid)}/export`);
        if (!exportResp.ok) {
          const detailProblem = detailResp.ok ? "" : await detailResp.text().catch(() => "");
          const exportProblem = await exportResp.text().catch(() => "");
          throw new Error(
            exportProblem ||
              detailProblem ||
              `Failed to load assembly (detail HTTP ${detailResp.status}, export HTTP ${exportResp.status})`
          );
        }

        const exportData = await exportResp.json();
        if (canceled) return;
        hydrateFromApiPayload(exportData, row, guid);
      } catch (err) {
        console.error("Failed to load assembly:", err);
        if (!canceled) {
          setErrorMessage(err?.message || "Failed to load assembly data.");
        }
      } finally {
        if (!canceled) {
          setLoading(false);
          consumeInitialDataRef.current?.();
        }
      }
    };

    const row = initialAssembly?.row || null;
    const context = row?.createContext || initialAssembly?.createContext;
    const routeGuid = initialAssembly?.assemblyGuid || null;
    const routePath = initialAssembly?.path || row?.sourcePath || row?.relPath || row?.path || "";

    if (row?.assemblyGuid || routeGuid) {
      hydrateExisting(row?.assemblyGuid || routeGuid, row);
      return () => {
        canceled = true;
      };
    }

    if (routePath) {
      (async () => {
        try {
          const resolved = await resolveGuidFromPath(routePath);
          if (canceled) return;
          if (!resolved?.guid) {
            throw new Error(`Assembly not found for path "${routePath}"`);
          }
          await hydrateExisting(resolved.guid, resolved.row || row);
        } catch (err) {
          console.error("Failed to resolve assembly by path:", err);
          if (!canceled) {
            setErrorMessage(err?.message || "Failed to resolve assembly path.");
            consumeInitialDataRef.current?.();
          }
        }
      })();
      return () => {
        canceled = true;
      };
    }

    if (context) {
      hydrateNewContext(context);
      consumeInitialDataRef.current?.();
      return () => {
        canceled = true;
      };
    }

    return () => {
      canceled = true;
    };
  }, [initialAssembly, defaultType]);

  useEffect(() => {
    writeAssemblyEditorTabToUrl(activeTab);
  }, [activeTab]);

  const prismLanguage = TYPE_MAP[assembly.type]?.prism || "powershell";

  const updateAssembly = useCallback((partial) => {
    setAssembly((prev) => ({ ...prev, ...partial }));
  }, []);

  const buildCurrentExportDocument = useCallback(() => {
    const resolvedGuid = assemblyGuid || generateAssemblyGuid();
    if (!assemblyGuid) {
      setAssemblyGuid(resolvedGuid);
    }
    return toServerDocument(assembly, resolvedGuid);
  }, [assembly, assemblyGuid]);

  const addVariable = useCallback(() => {
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
          description: "",
        },
      ],
    }));
  }, []);

  const updateVariable = useCallback((id, partial) => {
    setAssembly((prev) => ({
      ...prev,
      variables: prev.variables.map((variable) => (variable.id === id ? { ...variable, ...partial } : variable)),
    }));
  }, []);

  const removeVariable = useCallback((id) => {
    setAssembly((prev) => ({
      ...prev,
      variables: prev.variables.filter((variable) => variable.id !== id),
    }));
  }, []);

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
          data: base64,
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

  const removeFile = useCallback((id) => {
    setAssembly((prev) => ({ ...prev, files: prev.files.filter((file) => file.id !== id) }));
  }, []);

  const canWriteToDomain = domain === "user" || (isAdmin && devModeEnabled);

  const handleSaveAssembly = useCallback(async () => {
    if (!assembly.name.trim()) {
      alert("Assembly Name is required.");
      return;
    }

    const localGuid = assemblyGuid || generateAssemblyGuid();
    const document = toServerDocument(assembly, localGuid);
    setSaving(true);
    setErrorMessage("");

    try {
      const resp = await fetch("/api/assemblies/import", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          document,
          domain,
          assembly_guid: localGuid,
        }),
      });

      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.error || data?.message || `HTTP ${resp.status}`);
      }

      const nextGuid = data?.assembly_guid || localGuid;
      setAssemblyGuid(nextGuid || null);
      const nextDomain = (data?.source || data?.domain || domain || "user").toLowerCase();
      setDomain(nextDomain);
      setQueueInfo({
        dirty_since: data?.dirty_since || null,
        last_persisted: data?.last_persisted || null,
      });
      setIsDirtyQueue(Boolean(data?.is_dirty));

      if (data?.name || data?.display_name) {
        const resolvedName = data?.name || data?.display_name;
        setAssembly((prev) => ({ ...prev, name: resolvedName }));
        setFileName(sanitizeFileName(resolvedName));
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
  }, [assembly, assemblyGuid, domain, onSaved]);

  const handleRenameConfirm = useCallback(() => {
    const trimmed = (renameValue || assembly.name || "").trim();
    if (!trimmed) {
      setRenameOpen(false);
      return;
    }
    setAssembly((prev) => ({ ...prev, name: trimmed }));
    setFileName(sanitizeFileName(trimmed));
    setRenameOpen(false);
  }, [assembly.name, renameValue]);

  const handleDeleteAssembly = useCallback(async () => {
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
  }, [assemblyGuid, onSaved]);

  const handleDevModeToggle = useCallback(async (enabled) => {
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
  }, [sendNotification]);

  const handleFlushQueue = useCallback(async () => {
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
  }, []);

  const handleExportAssembly = useCallback(() => {
    setMenuAnchorEl(null);
    setErrorMessage("");
    try {
      const exportDoc = buildCurrentExportDocument();
      const exportName = sanitizeFileName(fileName || assembly.name || exportDoc.name || "assembly");
      downloadJsonFile(exportName, exportDoc);
    } catch (err) {
      console.error("Failed to export assembly:", err);
      const message = err?.message || "Failed to export assembly.";
      setErrorMessage(message);
      alert(message);
    }
  }, [assembly.name, buildCurrentExportDocument, fileName]);

  const handleViewJson = useCallback(() => {
    setMenuAnchorEl(null);
    setErrorMessage("");
    try {
      const exportDoc = buildCurrentExportDocument();
      setJsonPreviewText(JSON.stringify(exportDoc, null, 2));
      setJsonPreviewOpen(true);
    } catch (err) {
      console.error("Failed to render assembly JSON preview:", err);
      const message = err?.message || "Failed to render assembly JSON preview.";
      setErrorMessage(message);
      alert(message);
    }
  }, [buildCurrentExportDocument]);

  const handleCopyJson = useCallback(async () => {
    if (!jsonPreviewText) return;
    try {
      if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(jsonPreviewText);
        sendNotification({ message: "Assembly JSON copied to clipboard.", variant: "info" });
        return;
      }
      throw new Error("Clipboard API unavailable");
    } catch (err) {
      console.error("Failed to copy assembly JSON:", err);
      const message = "Failed to copy assembly JSON to clipboard.";
      setErrorMessage(message);
      alert(message);
    }
  }, [jsonPreviewText, sendNotification]);

  const handleImportAssembly = useCallback(async (event) => {
    const file = event.target.files && event.target.files[0];
    if (!file) return;

    setErrorMessage("");
    try {
      const text = await file.text();
      const parsed = JSON.parse(text);
      const payload = parsed?.payload || parsed;
      const doc = fromServerDocument(payload || {}, defaultType);
      setAssembly(doc);
      setAssemblyGuid(parsed?.assembly_guid || payload?.assembly_guid || null);
      setDomain("user");
      setQueueInfo(null);
      setIsDirtyQueue(false);
      const baseName = parsed?.name || parsed?.display_name || file.name.replace(/\.[^.]+$/, "") || "assembly";
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
  }, [defaultType]);

  const handleMenuOpen = useCallback((event) => {
    setMenuAnchorEl(event.currentTarget);
  }, []);

  const handleMenuClose = useCallback(() => {
    setMenuAnchorEl(null);
  }, []);

  const triggerImport = useCallback(() => {
    handleMenuClose();
    importInputRef.current?.click();
  }, [handleMenuClose]);

  const triggerFlushQueue = useCallback(() => {
    handleMenuClose();
    handleFlushQueue();
  }, [handleFlushQueue, handleMenuClose]);

  const saveDisabled = saving || loading || !canWriteToDomain;
  const deleteDisabled = !assemblyGuid || saving || loading;
  const renameDisabled = saving || loading;
  const dirtyPillVisible = Boolean(isDirtyQueue);
  const dirtySinceDisplay = queueInfo?.dirty_since
    ? new Date(queueInfo.dirty_since).toLocaleString()
    : null;
  const variableCount = assembly.variables?.length || 0;
  const fileCount = assembly.files?.length || 0;

  const totalFileBytes = useMemo(
    () => (assembly.files || []).reduce((sum, file) => sum + Number(file?.size || 0), 0),
    [assembly.files]
  );

  const editorEyebrow = isAnsible ? "Playbook Editor" : "Script Editor";
  const editorDetail = isAnsible
    ? "Write the playbook in the text editor below."
    : "Write the script in the text editor below.";
  const variableReferenceExample = VARIABLE_REFERENCE_EXAMPLE_BY_TYPE[assembly.type] || "$variableName";
  const tabDefs = useMemo(
    () => [
      { key: "summary", label: "Summary" },
      { key: "script", label: isAnsible ? "Playbook" : "Script", Icon: TerminalRoundedIcon },
      { key: "variables", label: "Variables", Icon: DatasetLinkedRoundedIcon },
      { key: "files", label: "Files", Icon: FolderZipRoundedIcon },
    ],
    [isAnsible]
  );

  useEffect(() => {
    if (!tabDefs.some((tab) => tab.key === activeTab)) {
      setActiveTab("summary");
    }
  }, [activeTab, tabDefs]);

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
    handleDevModeToggle,
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
    });
    return () => onPageMetaChange?.(null);
  }, [onPageMetaChange, pageHeaderActions, pageSubtitle, pageTitle]);

  return (
    <>
      <Menu
        anchorEl={menuAnchorEl}
        open={Boolean(menuAnchorEl)}
        onClose={handleMenuClose}
        PaperProps={MENU_PROPS.PaperProps}
      >
        <MenuItem onClick={handleExportAssembly}>Export JSON</MenuItem>
        {devModeEnabled ? <MenuItem onClick={handleViewJson}>View JSON</MenuItem> : null}
        <MenuItem onClick={triggerImport}>Import JSON</MenuItem>
      </Menu>

      <input
        ref={importInputRef}
        type="file"
        accept="application/json"
        style={{ display: "none" }}
        onChange={handleImportAssembly}
      />

      <PageBodyFrame variant="grid">
        <Box
          sx={{
            display: "flex",
            flexDirection: "column",
            minHeight: 0,
            height: "100%",
          }}
        >
          {!canWriteToDomain || errorMessage ? (
            <Box
              sx={{
                px: { xs: 2, md: 2.5 },
                pt: { xs: 2, md: 2.25 },
                pb: 1.5,
                display: "flex",
                flexDirection: "column",
                gap: 1.25,
              }}
            >
              {!canWriteToDomain ? (
                <Box
                  sx={{
                    borderRadius: 2.5,
                    border: "1px solid rgba(248, 212, 122, 0.34)",
                    background: "rgba(53, 39, 10, 0.35)",
                    px: 1.6,
                    py: 1.3,
                  }}
                >
                  <Typography variant="body2" sx={{ color: MAGIC_UI.warning }}>
                    This domain is read-only. Enable Dev Mode as an administrator to edit here, or switch the assembly back to the User domain.
                  </Typography>
                </Box>
              ) : null}

              {errorMessage ? (
                <Box
                  sx={{
                    borderRadius: 2.5,
                    border: "1px solid rgba(244, 63, 94, 0.28)",
                    background: "rgba(52, 10, 20, 0.42)",
                    px: 1.6,
                    py: 1.3,
                  }}
                >
                  <Typography variant="body2" sx={{ color: MAGIC_UI.danger }}>
                    {errorMessage}
                  </Typography>
                </Box>
              ) : null}
            </Box>
          ) : null}

          <Tabs
            value={activeTab}
            onChange={(_, nextTab) => setActiveTab(nextTab)}
            variant="scrollable"
            scrollButtons="auto"
            aria-label="Assembly editor sections"
            TabIndicatorProps={{
              style: {
                height: 3,
                borderRadius: 3,
                background: NAV_TAB_COLORS.iconActive,
              },
            }}
            sx={buildNavTabsSx()}
          >
            {tabDefs.map((tabDef) => {
              const TabIcon = tabDef.Icon;
              return (
                <Tab
                  key={tabDef.key}
                  value={tabDef.key}
                  label={tabDef.label}
                  icon={TabIcon ? <TabIcon sx={{ fontSize: 18 }} /> : undefined}
                  iconPosition="start"
                />
              );
            })}
          </Tabs>

          <Box
            sx={{
              flexGrow: 1,
              minHeight: 0,
              overflow: "auto",
              px: { xs: 2, md: 2.5 },
              pt: { xs: 2, md: 2.25 },
              pb: { xs: 2, md: 2.5 },
              display: "flex",
              flexDirection: "column",
              gap: 2.25,
            }}
          >
          {activeTab === "summary" ? (
            <Box sx={TAB_SECTION_SX}>
              <SectionHeader
                eyebrow="Summary"
                title="Assembly details"
                detail="Configure the core identity and runtime defaults here, then use the other tabs to edit source, variables, and files."
              />

              <Box sx={SUMMARY_FIELDS_GRID_SX}>
                <Box>
                  <TextField
                    label="Assembly Name"
                    value={assembly.name}
                    onChange={(e) => updateAssembly({ name: e.target.value })}
                    fullWidth
                    variant="outlined"
                    sx={INPUT_BASE_SX}
                  />
                  <DomainBadge domain={domain} size="small" sx={{ mt: 1 }} />
                </Box>

                <TextField
                  select
                  label="Domain"
                  value={domain}
                  onChange={(e) => setDomain(String(e.target.value || "").toLowerCase())}
                  disabled={loading}
                  fullWidth
                  sx={SELECT_BASE_SX}
                  SelectProps={{ MenuProps: MENU_PROPS }}
                >
                  {DOMAIN_OPTIONS.map((option) => (
                    <MenuItem key={option.value} value={option.value}>
                      {option.label}
                    </MenuItem>
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
                  {TYPE_OPTIONS.map((option) => (
                    <MenuItem key={option.key} value={option.key}>
                      {option.label}
                    </MenuItem>
                  ))}
                </TextField>

                <Box>
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
                  />
                  <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted, mt: 0.8, display: "block", px: 0.5 }}>
                    How long to run the assembly before assuming it failed.
                  </Typography>
                </Box>

                <TextField
                  label="Description"
                  value={assembly.description}
                  onChange={(e) => updateAssembly({ description: e.target.value })}
                  fullWidth
                  variant="outlined"
                  sx={INPUT_BASE_SX}
                />
              </Box>

              {dirtyPillVisible || loading || dirtySinceDisplay ? (
                <Box sx={{ display: "flex", alignItems: "center", gap: 1, flexWrap: "wrap" }}>
                  {dirtyPillVisible ? <DirtyStatePill compact /> : null}
                  {loading ? (
                    <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted }}>
                      Loading assembly data...
                    </Typography>
                  ) : null}
                  {dirtySinceDisplay ? (
                    <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted }}>
                      Dirty since: {dirtySinceDisplay}
                    </Typography>
                  ) : null}
                </Box>
              ) : null}
            </Box>
          ) : null}

          {activeTab === "script" ? (
            <Box sx={TAB_SECTION_SX}>
              <Box>
                <Typography variant="caption" sx={SECTION_EYEBROW_SX}>
                  {editorEyebrow}
                </Typography>
                <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted, mt: 0.45 }}>
                  {editorDetail}
                </Typography>
              </Box>

              <Box sx={CODE_EDITOR_CONTAINER_SX}>
                <Editor
                  value={assembly.script}
                  onValueChange={(value) => updateAssembly({ script: value })}
                  highlight={(src) => highlightedHtml(src, prismLanguage)}
                  padding={16}
                  placeholder={isAnsible ? "Start authoring your playbook..." : "Start typing your script..."}
                  style={{
                    ...CODE_EDITOR_STYLE,
                    minHeight: 620,
                  }}
                />
              </Box>
            </Box>
          ) : null}

          {activeTab === "variables" ? (
            <Box sx={TAB_SECTION_SX}>
              <SectionHeader
                eyebrow="Runtime inputs"
                title="Environment variables"
                detail="Define the inputs that operators will see when this assembly is launched. Keep the names script-friendly and the labels human-friendly."
                action={
                  <Button startIcon={<AddIcon />} onClick={addVariable} sx={INLINE_PRIMARY_BUTTON_SX}>
                    Add Variable
                  </Button>
                }
              />

              {variableCount ? (
                <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
                  {(assembly.variables || []).map((variable, index) => (
                    <Box key={variable.id} sx={VARIABLE_CARD_SX}>
                      <Box sx={{ display: "flex", flexDirection: "column", gap: 1.5, minWidth: 0 }}>
                        <Box sx={{ minWidth: 0 }}>
                          <Typography variant="body1" sx={{ color: MAGIC_UI.textBright, fontWeight: 600 }}>
                            {variable.label || variable.name || `Variable ${index + 1}`}
                          </Typography>
                          <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted, mt: 0.35, display: "block" }}>
                            {(VARIABLE_TYPE_OPTIONS.find((option) => option.key === variable.type)?.label || variable.type || "String")}
                            {variable.required ? " • Required at launch" : " • Optional input"}
                          </Typography>
                        </Box>

                        <Box sx={VARIABLE_FIELDS_GRID_SX}>
                          <Tooltip
                            title={`This name is used inside the assembly when the value is referenced at runtime. (e.g. ${variableReferenceExample})`}
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

                          <Tooltip
                            title="This is the operator-facing label shown in Borealis when the assembly is launched."
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

                          <Tooltip
                            title="Choose the kind of value Borealis should expect for this runtime input."
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
                              {VARIABLE_TYPE_OPTIONS.map((option) => (
                                <MenuItem key={option.key} value={option.key}>
                                  {option.label}
                                </MenuItem>
                              ))}
                            </TextField>
                          </Tooltip>

                          {variable.type === "boolean" ? (
                            <Tooltip
                              title="Sets the initial checked state operators will see when launching the assembly."
                              arrow
                              placement="top-start"
                            >
                              <Box sx={INLINE_CHECKBOX_FIELD_SX}>
                                <Typography variant="body2" sx={{ color: MAGIC_UI.textBright, fontWeight: 500 }}>
                                  Default Checked
                                </Typography>
                                <Checkbox
                                  checked={Boolean(variable.defaultValue)}
                                  onChange={(e) => updateVariable(variable.id, { defaultValue: e.target.checked })}
                                  sx={{
                                    color: MAGIC_UI.accentA,
                                    "&.Mui-checked": {
                                      color: MAGIC_UI.accentA,
                                    },
                                  }}
                                />
                              </Box>
                            </Tooltip>
                          ) : (
                            <Tooltip
                              title="Provide a sensible starter value for operators when they open the assembly."
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

                          <Tooltip
                            title="Explain why this variable exists so operators know what to enter."
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

                          <Box sx={INLINE_CHECKBOX_FIELD_SX}>
                            <Typography variant="body2" sx={{ color: MAGIC_UI.textBright, fontWeight: 500 }}>
                              Required
                            </Typography>
                            <Checkbox
                              checked={Boolean(variable.required)}
                              onChange={(e) => updateVariable(variable.id, { required: e.target.checked })}
                              sx={{
                                color: MAGIC_UI.accentA,
                                "&.Mui-checked": {
                                  color: MAGIC_UI.accentA,
                                },
                              }}
                              inputProps={{ "aria-label": "Required at launch" }}
                            />
                          </Box>
                        </Box>
                      </Box>

                      <Box sx={{ display: "flex", alignItems: "center", justifyContent: "center" }}>
                        <Tooltip title="Remove this variable." arrow>
                          <IconButton
                            aria-label="Remove variable"
                            onClick={() => removeVariable(variable.id)}
                            sx={DELETE_ICON_BUTTON_SX}
                          >
                            <DeleteIcon fontSize="small" />
                          </IconButton>
                        </Tooltip>
                      </Box>
                    </Box>
                  ))}
                </Box>
              ) : (
                <Box sx={EMPTY_STATE_SX}>
                  <Typography variant="body1" sx={{ color: MAGIC_UI.textBright, fontWeight: 600 }}>
                    No variables defined yet
                  </Typography>
                  <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted, mt: 0.8, maxWidth: 620 }}>
                    Add variables when the assembly needs operator-supplied values, credentials, toggles, or runtime hints.
                  </Typography>
                  <Button startIcon={<AddIcon />} onClick={addVariable} sx={{ ...INLINE_SECONDARY_BUTTON_SX, mt: 1.5 }}>
                    Create First Variable
                  </Button>
                </Box>
              )}
            </Box>
          ) : null}

          {activeTab === "files" ? (
            <Grid container spacing={2.25}>
              <Grid item xs={12} lg={4}>
                <GlassPanel sx={{ height: "100%" }}>
                  <SectionHeader
                    eyebrow="Payloads"
                    title="Support files"
                    detail="Attach binaries, or other supporting files that should travel with the assembly to targeted devices."
                  />

                  <Box sx={{ display: "grid", gridTemplateColumns: "repeat(2, minmax(0, 1fr))", gap: 1 }}>
                    <Box sx={MINI_CARD_SX}>
                      <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted, textTransform: "uppercase", letterSpacing: 0.65 }}>
                        Files
                      </Typography>
                      <Typography variant="body2" sx={{ color: MAGIC_UI.textBright, mt: 0.55, fontWeight: 600 }}>
                        {fileCount}
                      </Typography>
                    </Box>
                    <Box sx={MINI_CARD_SX}>
                      <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted, textTransform: "uppercase", letterSpacing: 0.65 }}>
                        Total size
                      </Typography>
                      <Typography variant="body2" sx={{ color: MAGIC_UI.textBright, mt: 0.55, fontWeight: 600 }}>
                        {formatBytes(totalFileBytes)}
                      </Typography>
                    </Box>
                  </Box>

                  <Button component="label" startIcon={<UploadFileIcon />} sx={INLINE_PRIMARY_BUTTON_SX}>
                    Upload Files
                    <input type="file" hidden multiple onChange={handleFileUpload} />
                  </Button>
                </GlassPanel>
              </Grid>

              <Grid item xs={12} lg={8}>
                <GlassPanel sx={{ height: "100%" }}>
                  <SectionHeader
                    eyebrow="Attached files"
                    title="Assembly Runtime Bundle"
                    detail="Review every payload that will be embedded into the assembly so the exported package stays clear and predictable."
                  />

                  {fileCount ? (
                    <Box sx={{ display: "flex", flexDirection: "column", gap: 1.2 }}>
                      {(assembly.files || []).map((file) => (
                        <Box key={file.id} sx={FILE_ROW_SX}>
                          <Box sx={{ display: "flex", alignItems: "center", gap: 1.3, minWidth: 0, flex: "1 1 280px" }}>
                            <Box
                              sx={{
                                width: 38,
                                height: 38,
                                borderRadius: 2,
                                display: "grid",
                                placeItems: "center",
                                background: "rgba(125,211,252,0.12)",
                                border: "1px solid rgba(125,211,252,0.22)",
                                flexShrink: 0,
                              }}
                            >
                              <UploadFileIcon sx={{ fontSize: 18, color: MAGIC_UI.accentA }} />
                            </Box>
                            <Box sx={{ minWidth: 0 }}>
                              <Typography
                                variant="body2"
                                sx={{
                                  color: MAGIC_UI.textBright,
                                  fontWeight: 600,
                                  whiteSpace: "nowrap",
                                  overflow: "hidden",
                                  textOverflow: "ellipsis",
                                }}
                              >
                                {file.fileName}
                              </Typography>
                              <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted }}>
                                {formatBytes(file.size)}
                                {file.mimeType ? ` • ${file.mimeType}` : ""}
                              </Typography>
                            </Box>
                          </Box>
                          <Tooltip title="Remove this file." arrow>
                            <IconButton
                              aria-label={`Remove ${file.fileName}`}
                              onClick={() => removeFile(file.id)}
                              sx={DELETE_ICON_BUTTON_SX}
                            >
                              <DeleteIcon fontSize="small" />
                            </IconButton>
                          </Tooltip>
                        </Box>
                      ))}
                    </Box>
                  ) : (
                    <Box sx={EMPTY_STATE_SX}>
                      <Typography variant="body1" sx={{ color: MAGIC_UI.textBright, fontWeight: 600 }}>
                        No files uploaded yet
                      </Typography>
                      <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted, mt: 0.8, maxWidth: 620 }}>
                        Attach a file when the assembly needs bundled templates, archives, installers, or other runtime payloads.
                      </Typography>
                    </Box>
                  )}
                </GlassPanel>
              </Grid>
            </Grid>
          ) : null}
        </Box>
      </Box>
      </PageBodyFrame>

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

      <Dialog
        open={jsonPreviewOpen}
        onClose={() => setJsonPreviewOpen(false)}
        maxWidth="lg"
        fullWidth
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={{ borderBottom: `1px solid ${MAGIC_UI.panelBorder}` }}>Assembly JSON Preview</DialogTitle>
        <DialogContent dividers sx={{ borderColor: MAGIC_UI.panelBorder }}>
          <Box sx={CODE_EDITOR_CONTAINER_SX}>
            <Editor
              value={jsonPreviewText}
              onValueChange={() => {}}
              highlight={(src) => highlightedHtml(src, "json")}
              padding={12}
              readOnly
              textareaId="assembly-json-preview"
              style={{
                ...CODE_EDITOR_STYLE,
                minHeight: 440,
                whiteSpace: "pre",
              }}
            />
          </Box>
        </DialogContent>
        <DialogActions sx={{ px: 3, py: 2, borderTop: `1px solid ${MAGIC_UI.panelBorder}` }}>
          <Button onClick={handleCopyJson} sx={DIALOG_PRIMARY_BUTTON_SX}>
            Copy JSON
          </Button>
          <Button onClick={() => setJsonPreviewOpen(false)} sx={DIALOG_BUTTON_SX}>
            Close
          </Button>
        </DialogActions>
      </Dialog>
    </>
  );
}
