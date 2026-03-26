import React, { useCallback, useEffect, useMemo, useState } from "react";
import { Box, Button, Dialog, DialogActions, DialogContent, DialogTitle } from "@mui/material";
import Prism from "prismjs";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-powershell";
import "prismjs/components/prism-batch";
import "prismjs/themes/prism-okaidia.css";
import Editor from "react-simple-code-editor";
import { AgGridReact } from "ag-grid-react";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../../DialogStyles.jsx";
import {
  DEFAULT_GRID_COL_DEF,
  DEVICE_DETAILS_GRID_THEME,
  DEVICE_GRID_STYLE,
  GridShell,
  MAGIC_UI,
  gridFontFamily,
} from "./Shared.jsx";

const HISTORY_STATUS_THEME = {
  running: {
    text: "#58a6ff",
    background: "rgba(88,166,255,0.15)",
    border: "1px solid rgba(88,166,255,0.4)",
    dot: "#58a6ff",
  },
  success: {
    text: "#00d18c",
    background: "rgba(0,209,140,0.16)",
    border: "1px solid rgba(0,209,140,0.35)",
    dot: "#00d18c",
  },
  failed: {
    text: "#ff7b89",
    background: "rgba(255,123,137,0.16)",
    border: "1px solid rgba(255,123,137,0.35)",
    dot: "#ff7b89",
  },
  default: {
    text: "#e2e8f0",
    background: "rgba(226,232,240,0.12)",
    border: "1px solid rgba(226,232,240,0.25)",
    dot: "#e2e8f0",
  },
};

const StatusPillCell = React.memo(function StatusPillCell(props) {
  const value = String(props?.value || "");
  if (!value) return null;
  const theme = HISTORY_STATUS_THEME[value.toLowerCase()] || HISTORY_STATUS_THEME.default;
  return (
    <Box
      component="span"
      sx={{
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        minWidth: 76,
        px: 1.5,
        py: 0.4,
        borderRadius: 999,
        backgroundColor: theme.background,
        border: theme.border,
        color: theme.text,
        fontWeight: 600,
        fontSize: "13px",
        lineHeight: 1,
        fontFamily: gridFontFamily,
        textTransform: "capitalize",
        gap: 0.75,
      }}
    >
      <Box
        component="span"
        sx={{
          width: 8,
          height: 8,
          borderRadius: "50%",
          backgroundColor: theme.dot,
          boxShadow: "0 0 0 2px rgba(0, 0, 0, 0.22)",
        }}
      />
      {value}
    </Box>
  );
});

const HistoryActionsCell = React.memo(function HistoryActionsCell(props) {
  const row = props.data || {};
  const onViewOutput = props.context?.onViewOutput;

  return (
    <Box sx={{ display: "flex", gap: 1, alignItems: "center" }}>
      {row.has_stdout ? (
        <Button
          size="small"
          sx={{ color: MAGIC_UI.accentA, textTransform: "none", minWidth: 0, p: 0 }}
          onClick={() => onViewOutput && onViewOutput(row, "stdout")}
        >
          StdOut
        </Button>
      ) : null}
      {row.has_stderr ? (
        <Button
          size="small"
          sx={{ color: "#ff7b89", textTransform: "none", minWidth: 0, p: 0 }}
          onClick={() => onViewOutput && onViewOutput(row, "stderr")}
        >
          StdErr
        </Button>
      ) : null}
    </Box>
  );
});

const GRID_COMPONENTS = {
  StatusPillCell,
  HistoryActionsCell,
};

export default function ActivityHistoryTab({ hostname = "", refreshToken = 0 }) {
  const [historyRows, setHistoryRows] = useState([]);
  const [outputOpen, setOutputOpen] = useState(false);
  const [outputTitle, setOutputTitle] = useState("");
  const [outputContent, setOutputContent] = useState("");
  const [outputLang, setOutputLang] = useState("powershell");
  const [assemblyNameMap, setAssemblyNameMap] = useState({});

  const normalizedHostname = useMemo(() => String(hostname || "").trim(), [hostname]);

  const formatTimestamp = useCallback((epochSec) => {
    const ts = Number(epochSec || 0);
    if (!ts) return "unknown";
    const date = new Date(ts * 1000);
    const mm = String(date.getMonth() + 1).padStart(2, "0");
    const dd = String(date.getDate()).padStart(2, "0");
    const yyyy = date.getFullYear();
    let hh = date.getHours();
    const ampm = hh >= 12 ? "PM" : "AM";
    hh = hh % 12 || 12;
    const min = String(date.getMinutes()).padStart(2, "0");
    return `${mm}/${dd}/${yyyy} @ ${hh}:${min} ${ampm}`;
  }, []);

  const formatScriptType = useCallback((raw) => {
    const value = String(raw || "").toLowerCase();
    if (value === "ansible") return "Ansible Playbook";
    if (value === "reverse_tunnel" || value === "vpn_tunnel") return "Reverse VPN Tunnel";
    return "Script";
  }, []);

  useEffect(() => {
    let canceled = false;

    const loadAssemblyNames = async () => {
      const next = {};
      const storeName = (rawPath, rawName) => {
        const name = typeof rawName === "string" ? rawName.trim() : "";
        if (!name) return;
        const normalizedPath = String(rawPath || "")
          .replace(/\\/g, "/")
          .replace(/^\/+/, "")
          .trim();
        if (!normalizedPath) return;
        if (!next[normalizedPath]) next[normalizedPath] = name;
        const base = normalizedPath.split("/").pop() || "";
        if (base && !next[base]) next[base] = name;
        const dot = base.lastIndexOf(".");
        if (dot > 0) {
          const baseNoExt = base.slice(0, dot);
          if (baseNoExt && !next[baseNoExt]) next[baseNoExt] = name;
        }
      };

      try {
        const resp = await fetch("/api/assemblies");
        if (!resp.ok) return;
        const data = await resp.json();
        const items = Array.isArray(data?.items) ? data.items : [];
        items.forEach((item) => {
          if (!item || typeof item !== "object") return;
          const displayName = (item.display_name || "").trim() || item.assembly_guid || "";
          if (!displayName) return;
          storeName(item.virtual_path || item.path || "", displayName);
          if (item.assembly_guid && !next[item.assembly_guid]) {
            next[item.assembly_guid] = displayName;
          }
          if (item.payload_guid && !next[item.payload_guid]) {
            next[item.payload_guid] = displayName;
          }
        });
      } catch {
        // Ignore enrichment failures and fall back to raw names.
      }

      if (!canceled) {
        setAssemblyNameMap(next);
      }
    };

    loadAssemblyNames();

    return () => {
      canceled = true;
    };
  }, []);

  const resolveAssemblyName = useCallback(
    (scriptName, scriptPath) => {
      const normalized = String(scriptPath || "").replace(/\\/g, "/").trim();
      const base = normalized ? normalized.split("/").pop() || "" : "";
      const baseNoExt = base && base.includes(".") ? base.slice(0, base.lastIndexOf(".")) : base;
      return (
        assemblyNameMap[normalized] ||
        (base ? assemblyNameMap[base] : "") ||
        (baseNoExt ? assemblyNameMap[baseNoExt] : "") ||
        scriptName ||
        base ||
        scriptPath ||
        ""
      );
    },
    [assemblyNameMap]
  );

  const loadHistory = useCallback(async () => {
    if (!normalizedHostname) {
      setHistoryRows([]);
      return;
    }
    try {
      const resp = await fetch(`/api/device/activity/${encodeURIComponent(normalizedHostname)}`);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      setHistoryRows(data.history || []);
    } catch (error) {
      console.warn("Failed to load activity history", error);
      setHistoryRows([]);
    }
  }, [normalizedHostname]);

  useEffect(() => {
    loadHistory();
  }, [loadHistory, refreshToken]);

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    if (!socket || !normalizedHostname) return undefined;

    let refreshTimer = null;
    const expectedHost = normalizedHostname.toLowerCase();
    const scheduleRefresh = (delay = 200) => {
      if (refreshTimer) clearTimeout(refreshTimer);
      refreshTimer = setTimeout(() => {
        refreshTimer = null;
        loadHistory();
      }, delay);
    };

    const handleActivityChanged = (payload = {}) => {
      const payloadHost = String(payload?.hostname || "").trim().toLowerCase();
      if (!payloadHost || payloadHost !== expectedHost) return;
      scheduleRefresh(payload?.change === "updated" ? 150 : 0);
    };

    socket.on("device_activity_changed", handleActivityChanged);

    return () => {
      if (refreshTimer) clearTimeout(refreshTimer);
      socket.off("device_activity_changed", handleActivityChanged);
    };
  }, [loadHistory, normalizedHostname]);

  const historyColumnDefs = useMemo(
    () => [
      {
        headerName: "Activity",
        field: "script_type",
        minWidth: 180,
        valueGetter: (params) => formatScriptType(params.data?.script_type),
      },
      {
        headerName: "Task",
        field: "script_display_name",
        flex: 1.2,
        minWidth: 240,
        filter: "agTextColumnFilter",
      },
      {
        headerName: "Ran On",
        field: "ran_at",
        width: 210,
        valueFormatter: (params) => formatTimestamp(params.value),
        sort: "desc",
        comparator: (a, b) => (a || 0) - (b || 0),
      },
      {
        headerName: "Job Status",
        field: "status",
        width: 160,
        cellRenderer: "StatusPillCell",
      },
      {
        headerName: "StdOut / StdErr",
        colId: "stdout",
        width: 220,
        sortable: false,
        filter: false,
        cellRenderer: "HistoryActionsCell",
      },
    ],
    [formatScriptType, formatTimestamp]
  );

  const highlightCode = useCallback((code, lang) => {
    try {
      return Prism.highlight(code ?? "", Prism.languages[lang] || Prism.languages.markup, lang);
    } catch {
      return String(code || "");
    }
  }, []);

  const handleViewOutput = useCallback(
    async (row, which) => {
      if (!row || !row.id) return;
      try {
        const resp = await fetch(`/api/device/activity/job/${row.id}`);
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        const data = await resp.json();
        const scriptPath = String(data.script_path || "").toLowerCase();
        const lang = scriptPath.endsWith(".ps1")
          ? "powershell"
          : scriptPath.endsWith(".bat")
          ? "batch"
          : scriptPath.endsWith(".sh")
          ? "bash"
          : scriptPath.endsWith(".yml")
          ? "yaml"
          : "powershell";
        const friendly = resolveAssemblyName(data.script_name, data.script_path);
        setOutputLang(lang);
        setOutputTitle(`${which === "stderr" ? "StdErr" : "StdOut"} - ${friendly}`);
        setOutputContent(which === "stderr" ? data.stderr || "" : data.stdout || "");
        setOutputOpen(true);
      } catch (error) {
        console.warn("Failed to load output", error);
      }
    },
    [resolveAssemblyName]
  );

  const historyDisplayRows = useMemo(
    () =>
      (historyRows || []).map((row) => ({
        ...row,
        script_display_name: resolveAssemblyName(row.script_name, row.script_path),
      })),
    [historyRows, resolveAssemblyName]
  );

  const getHistoryRowId = useCallback((params) => String(params.data?.id || params.rowIndex), []);

  const historyGridContext = useMemo(
    () => ({
      onViewOutput: handleViewOutput,
    }),
    [handleViewOutput]
  );

  return (
    <>
      <GridShell sx={{ flexGrow: 1, minHeight: 360 }}>
        <AgGridReact
          rowData={historyDisplayRows}
          columnDefs={historyColumnDefs}
          defaultColDef={DEFAULT_GRID_COL_DEF}
          pagination
          paginationPageSize={20}
          paginationPageSizeSelector={[20, 50, 100]}
          animateRows
          components={GRID_COMPONENTS}
          context={historyGridContext}
          getRowId={getHistoryRowId}
          suppressCellFocus
          theme={DEVICE_DETAILS_GRID_THEME}
          style={DEVICE_GRID_STYLE}
        />
      </GridShell>

      <Dialog
        open={outputOpen}
        onClose={() => setOutputOpen(false)}
        fullWidth
        maxWidth="md"
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock title={outputTitle} subtitle="Review command output and captured response data." />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Box
            sx={{
              border: `1px solid ${MAGIC_UI.panelBorder}`,
              borderRadius: 2,
              bgcolor: "rgba(4,7,17,0.65)",
              maxHeight: "56vh",
              overflowY: "auto",
              overflowX: "auto",
              overscrollBehavior: "contain",
              scrollbarGutter: "stable both-edges",
              "&::-webkit-scrollbar": {
                width: 10,
                height: 10,
              },
              "&::-webkit-scrollbar-track": {
                background: "rgba(15,23,42,0.45)",
                borderRadius: 999,
              },
              "&::-webkit-scrollbar-thumb": {
                background: "rgba(125,183,255,0.35)",
                borderRadius: 999,
                border: "2px solid rgba(15,23,42,0.45)",
              },
            }}
          >
            <Editor
              value={outputContent}
              onValueChange={() => {}}
              highlight={(code) => highlightCode(code, outputLang)}
              padding={12}
              style={{
                fontFamily:
                  'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
                fontSize: 12,
                color: "#e6edf3",
                minHeight: 200,
                whiteSpace: "pre",
                overflowWrap: "normal",
                wordBreak: "normal",
              }}
              textareaProps={{ readOnly: true, wrap: "off", spellCheck: false }}
            />
          </Box>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={() => setOutputOpen(false)} sx={DIALOG_BUTTON_SX}>
            Close
          </Button>
        </DialogActions>
      </Dialog>
    </>
  );
}
