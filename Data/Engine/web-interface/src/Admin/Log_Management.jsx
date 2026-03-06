import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Paper,
  Typography,
  Stack,
  Button,
  IconButton,
  Tooltip,
  FormControl,
  InputLabel,
  Select,
  MenuItem,
  TextField,
  Divider,
  Chip,
  CircularProgress,
  ToggleButtonGroup,
  ToggleButton,
  Alert,
  InputAdornment,
} from "@mui/material";
import {
  ReceiptLong as LogsIcon,
  Refresh as RefreshIcon,
  DeleteOutline as DeleteIcon,
  Save as SaveIcon,
  Visibility as VisibilityIcon,
  VisibilityOff as VisibilityOffIcon,
  History as HistoryIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import Prism from "prismjs";
import "prismjs/components/prism-bash";
import "prismjs/themes/prism-okaidia.css";
import Editor from "react-simple-code-editor";

ModuleRegistry.registerModules([AllCommunityModule]);

const AURORA_SHELL = {
  background:
    "radial-gradient(120% 80% at 0% 0%, rgba(77, 172, 255, 0.22) 0%, rgba(4, 7, 17, 0.0) 60%)," +
    "radial-gradient(100% 80% at 100% 0%, rgba(214, 130, 255, 0.25) 0%, rgba(6, 9, 20, 0.0) 62%)," +
    "linear-gradient(135deg, rgba(3,6,15,1) 0%, rgba(8,12,25,0.97) 50%, rgba(13,7,20,0.95) 100%)",
  panel:
    "linear-gradient(135deg, rgba(10, 16, 31, 0.98) 0%, rgba(6, 10, 24, 0.95) 60%, rgba(15, 6, 26, 0.97) 100%)",
  border: "rgba(148, 163, 184, 0.28)",
  text: "#e2e8f0",
  muted: "#94a3b8",
  accent: "#7db7ff",
};

const gradientButtonSx = {
  textTransform: "none",
  fontWeight: 600,
  color: "#041125",
  borderRadius: 999,
  backgroundImage: "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)",
  boxShadow: "0 10px 25px rgba(124, 58, 237, 0.4)",
  "&:hover": {
    backgroundImage: "linear-gradient(135deg, #8be9ff 0%, #d5a8ff 100%)",
    boxShadow: "0 14px 32px rgba(124, 58, 237, 0.45)",
  },
};

const quartzTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#05070f",
  foregroundColor: "#f8fafc",
  headerBackgroundColor: "#0b1527",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  headerFontSize: 14,
  borderColor: "rgba(148,163,184,0.28)",
});
const quartzThemeClass = quartzTheme.themeName || "ag-theme-quartz";
const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const gridThemeStyle = {
  width: "100%",
  height: "100%",
  "--ag-foreground-color": "#f1f5f9",
  "--ag-background-color": "#050b16",
  "--ag-header-background-color": "#0f1a2e",
  "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
  "--ag-row-hover-color": "rgba(73,156,196,0.2)",
  "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
  "--ag-border-color": "rgba(148,163,184,0.28)",
  "--ag-font-family": gridFontFamily,
};

function formatBytes(bytes) {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const idx = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / Math.pow(1024, idx);
  return `${value.toFixed(value >= 10 || idx === 0 ? 0 : 1)} ${units[idx]}`;
}

function formatTimestamp(ts) {
  if (!ts) return "n/a";
  try {
    const d = new Date(ts);
    if (!Number.isNaN(d.getTime())) {
      return d.toLocaleString();
    }
  } catch {
    /* noop */
  }
  return ts;
}

export default function LogManagement({ isAdmin = false, onPageMetaChange }) {
  const [logs, setLogs] = useState([]);
  const [defaultRetention, setDefaultRetention] = useState(30);
  const [selectedDomain, setSelectedDomain] = useState(null);
  const [selectedFile, setSelectedFile] = useState(null);
  const [retentionDraft, setRetentionDraft] = useState("");
  const [listLoading, setListLoading] = useState(false);
  const [entryLoading, setEntryLoading] = useState(false);
  const [entries, setEntries] = useState([]);
  const [entriesMeta, setEntriesMeta] = useState(null);
  const [gridMode, setGridMode] = useState("structured");
  const [error, setError] = useState(null);
  const [actionMessage, setActionMessage] = useState(null);
  const [quickFilter, setQuickFilter] = useState("");
  const gridRef = useRef(null);
  const useGlobalHeader = Boolean(onPageMetaChange);
  const sendNotification = useCallback(async (message) => {
    if (!message) return;
    try {
      await fetch("/api/notifications/notify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          title: "Log Management",
          message,
          icon: "logs",
          variant: "info",
        }),
      });
    } catch {
      /* notifications are best-effort */
    }
  }, []);

  const logMap = useMemo(() => {
    const map = new Map();
    logs.forEach((log) => map.set(log.file, log));
    return map;
  }, [logs]);

  const rawContent = useMemo(() => entries.map((entry) => entry.raw || "").join("\n"), [entries]);

  const columnDefs = useMemo(
    () => [
      { headerName: "Timestamp", field: "timestamp", minWidth: 200 },
      { headerName: "Level", field: "level", width: 110, valueFormatter: (p) => (p.value || "").toUpperCase() },
      { headerName: "Scope", field: "scope", minWidth: 120 },
      { headerName: "Service", field: "service", minWidth: 160 },
      { headerName: "Message", field: "message", flex: 1, minWidth: 300 },
    ],
    []
  );

const defaultColDef = useMemo(
    () => ({
      sortable: true,
      filter: true,
      resizable: true,
      wrapText: false,
      cellStyle: { fontFamily: gridFontFamily, fontSize: "0.9rem", color: "#e2e8f0" },
    }),
    []
  );

  const applyQuickFilter = useCallback(
    (value) => {
      setQuickFilter(value);
      const api = gridRef.current?.api;
      if (api) api.setGridOption("quickFilterText", value);
    },
    [setQuickFilter]
  );

  const fetchLogs = useCallback(async () => {
    if (!isAdmin) return;
    setListLoading(true);
    setError(null);
    setActionMessage(null);
    try {
      const resp = await fetch("/api/server/logs", { credentials: "include" });
      if (!resp.ok) throw new Error(`Failed to load logs (HTTP ${resp.status})`);
      const data = await resp.json();
      setLogs(Array.isArray(data?.logs) ? data.logs : []);
      setDefaultRetention(Number.isFinite(data?.default_retention_days) ? data.default_retention_days : 30);
      if (Array.isArray(data?.retention_deleted) && data.retention_deleted.length > 0) {
        setActionMessage(`Removed ${data.retention_deleted.length} expired log files automatically.`);
      }
    } catch (err) {
      setError(String(err));
    } finally {
      setListLoading(false);
    }
  }, [isAdmin]);

  const fetchEntries = useCallback(
    async (file) => {
      if (!file) return;
      setEntryLoading(true);
      setError(null);
      try {
        const resp = await fetch(`/api/server/logs/${encodeURIComponent(file)}/entries?limit=1000`, {
          credentials: "include",
        });
        if (!resp.ok) throw new Error(`Failed to load log entries (HTTP ${resp.status})`);
        const data = await resp.json();
        setEntries(Array.isArray(data?.entries) ? data.entries : []);
        setEntriesMeta(data);
        applyQuickFilter("");
      } catch (err) {
        setEntries([]);
        setEntriesMeta(null);
        setError(String(err));
      } finally {
        setEntryLoading(false);
      }
    },
    [applyQuickFilter]
  );

  useEffect(() => {
    if (isAdmin) fetchLogs();
  }, [fetchLogs, isAdmin]);

  useEffect(() => {
    onPageMetaChange?.({
      page_title: "Log Management",
      page_subtitle: "Analyze engine logs and adjust retention across services.",
      page_icon: LogsIcon,
    });
    return () => onPageMetaChange?.(null);
  }, [onPageMetaChange]);

  useEffect(() => {
    if (!logs.length) {
      setSelectedDomain(null);
      return;
    }
    setSelectedDomain((prev) => (prev && logMap.has(prev) ? prev : logs[0].file));
  }, [logs, logMap]);

  useEffect(() => {
    if (!selectedDomain) {
      setSelectedFile(null);
      return;
    }
    const domain = logMap.get(selectedDomain);
    if (!domain) return;
    const firstVersion = domain.versions?.[0]?.file || domain.file;
    setSelectedFile((prev) => {
      if (domain.versions?.some((v) => v.file === prev)) return prev;
      return firstVersion;
    });
    setRetentionDraft(String(domain.retention_days ?? defaultRetention));
  }, [defaultRetention, logMap, selectedDomain]);

  useEffect(() => {
    if (selectedFile) fetchEntries(selectedFile);
  }, [fetchEntries, selectedFile]);

  const selectedDomainData = selectedDomain ? logMap.get(selectedDomain) : null;

  const handleRetentionSave = useCallback(async () => {
    if (!selectedDomainData) return;
    const trimmed = String(retentionDraft || "").trim();
    const payloadValue = trimmed ? parseInt(trimmed, 10) : null;
    setActionMessage(null);
    setError(null);
    try {
      const resp = await fetch("/api/server/logs/retention", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          retention: {
            [selectedDomainData.file]: Number.isFinite(payloadValue) ? payloadValue : null,
          },
        }),
      });
      if (!resp.ok) throw new Error(`Failed to update retention (HTTP ${resp.status})`);
      const data = await resp.json();
      setLogs(Array.isArray(data?.logs) ? data.logs : logs);
      if (Array.isArray(data?.retention_deleted) && data.retention_deleted.length > 0) {
        setActionMessage(`Retention applied. Removed ${data.retention_deleted.length} expired files.`);
      } else {
        setActionMessage("Retention policy saved.");
      }
    } catch (err) {
      setError(String(err));
    }
  }, [logs, retentionDraft, selectedDomainData]);

  const handleDelete = useCallback(
    async (scope) => {
      if (!selectedFile) return;
      const confirmMessage =
        scope === "family"
          ? "Delete this log domain and all rotated files?"
          : "Delete the currently selected log file?";
      if (!window.confirm(confirmMessage)) return;
      setActionMessage(null);
      setError(null);
      try {
        const target = scope === "family" ? selectedDomainData?.file || selectedFile : selectedFile;
        const resp = await fetch(`/api/server/logs/${encodeURIComponent(target)}?scope=${scope}`, {
          method: "DELETE",
          credentials: "include",
        });
        if (!resp.ok) throw new Error(`Failed to delete log (HTTP ${resp.status})`);
        const data = await resp.json();
        setLogs(Array.isArray(data?.logs) ? data.logs : logs);
        setEntries([]);
        setEntriesMeta(null);
        if (target) {
          sendNotification(`Log "${target}" Deleted Successfully`);
        }
      } catch (err) {
        setError(String(err));
      }
    },
    [logs, selectedDomainData, selectedFile, sendNotification]
  );

  const disableRetentionSave =
    !selectedDomainData ||
    (String(selectedDomainData.retention_days ?? defaultRetention) === retentionDraft.trim() &&
      retentionDraft.trim() !== "");

  if (!isAdmin) {
    return (
      <Paper sx={{ m: 3, p: 4, bgcolor: AURORA_SHELL.panel }}>
        <Typography color="error">Administrator permissions are required to view log management.</Typography>
      </Paper>
    );
  }

  return (
    <Paper
      elevation={0}
      sx={{
        m: 0,
        p: 0,
        minHeight: "100%",
        backgroundImage: "none",
        backgroundColor: "transparent",
        borderRadius: 0,
        display: "flex",
        flexDirection: "column",
      }}
    >
      <Box
        sx={{
          position: "fixed",
          top: { xs: 72, md: 88 },
          right: { xs: 12, md: 20 },
          zIndex: 1400,
          pointerEvents: "none",
        }}
      >
        <Stack direction="row" spacing={1.25} sx={{ pointerEvents: "auto" }}>
          <Button
            variant="contained"
            startIcon={<RefreshIcon />}
            onClick={() => {
              fetchLogs();
              if (selectedFile) fetchEntries(selectedFile);
            }}
            sx={{ ...gradientButtonSx, minWidth: 112 }}
          >
            Refresh
          </Button>
        </Stack>
      </Box>

      {!useGlobalHeader && (
        <Box sx={{ px: 3, pt: 3, pb: 1 }}>
          <Stack direction="row" spacing={1.25} alignItems="center">
            <LogsIcon sx={{ fontSize: 22, color: AURORA_SHELL.accent, mt: 0.25 }} />
            <Typography
              variant="h6"
              sx={{
                fontWeight: 700,
                letterSpacing: 0.5,
                color: AURORA_SHELL.text,
              }}
            >
              Log Management
            </Typography>
          </Stack>
          <Typography variant="body2" sx={{ color: AURORA_SHELL.muted, mt: 0.75, mb: 2.5 }}>
            Analyze engine logs and adjust log retention periods for different engine services.
          </Typography>
        </Box>
      )}

      {error && (
        <Box sx={{ px: 3, pt: 2 }}>
          <Alert severity="error">{error}</Alert>
        </Box>
      )}
      {actionMessage && (
        <Box sx={{ px: 3, pt: 2 }}>
          <Alert severity="success">{actionMessage}</Alert>
        </Box>
      )}

      <Box sx={{ display: "flex", flexGrow: 1, minHeight: 0 }}>
        <Box
          sx={{
            width: 360,
            p: 3,
            borderRight: "none",
            bgcolor: "transparent",
            display: "flex",
            flexDirection: "column",
            gap: 2,
          }}
        >
          <FormControl fullWidth variant="outlined" size="small">
            <InputLabel id="log-select-label">Log Domain</InputLabel>
            <Select
              labelId="log-select-label"
              label="Log Domain"
              value={selectedDomain || ""}
              onChange={(e) => setSelectedDomain(e.target.value)}
              sx={{
                "& .MuiOutlinedInput-notchedOutline": { borderColor: "rgba(148,163,184,0.35)" },
                "&:hover .MuiOutlinedInput-notchedOutline": { borderColor: "rgba(148,163,184,0.6)" },
                "&.Mui-focused .MuiOutlinedInput-notchedOutline": { borderColor: AURORA_SHELL.accent },
                "& .MuiSelect-select": {
                  bgcolor: "rgba(8,15,32,0.85)",
                  color: AURORA_SHELL.text,
                  borderRadius: 1,
                },
                "& svg": { color: AURORA_SHELL.accent },
              }}
              MenuProps={{
                PaperProps: {
                  sx: {
                    bgcolor: "rgba(12,18,36,0.98)",
                    border: `1px solid rgba(148,163,184,0.35)`,
                    mt: 1,
                    boxShadow: "0 12px 32px rgba(0,0,0,0.45)",
                    "& .MuiMenuItem-root": {
                      color: AURORA_SHELL.text,
                      "&.Mui-selected": {
                        bgcolor: "rgba(125,183,255,0.18)",
                      },
                      "&.Mui-selected:hover": {
                        bgcolor: "rgba(125,183,255,0.25)",
                      },
                    },
                  },
                },
              }}
            >
              {logs.map((log) => (
                <MenuItem key={log.file} value={log.file}>
                  <Stack direction="row" spacing={1} alignItems="center" sx={{ width: "100%" }}>
                    <Typography sx={{ flexGrow: 1 }}>{log.display_name || log.file}</Typography>
                    <Chip
                      label={formatBytes(log.size_bytes || 0)}
                      size="small"
                      sx={{
                        bgcolor: "rgba(96,165,250,0.16)",
                        color: AURORA_SHELL.text,
                        borderRadius: 999,
                        border: "1px solid rgba(96,165,250,0.45)",
                      }}
                    />
                  </Stack>
                </MenuItem>
              ))}
            </Select>
          </FormControl>

          {selectedDomainData && (
            <>
              <FormControl fullWidth size="small">
                <InputLabel id="version-select-label">Log File</InputLabel>
                <Select
                  labelId="version-select-label"
                  label="Log File"
                  value={selectedFile || ""}
                  onChange={(e) => setSelectedFile(e.target.value)}
                  sx={{
                    "& .MuiOutlinedInput-notchedOutline": { borderColor: "rgba(148,163,184,0.35)" },
                    "&:hover .MuiOutlinedInput-notchedOutline": { borderColor: "rgba(148,163,184,0.6)" },
                    "&.Mui-focused .MuiOutlinedInput-notchedOutline": { borderColor: AURORA_SHELL.accent },
                    "& .MuiSelect-select": {
                      bgcolor: "rgba(8,15,32,0.85)",
                      color: AURORA_SHELL.text,
                      borderRadius: 1,
                    },
                    "& svg": { color: AURORA_SHELL.accent },
                  }}
                  MenuProps={{
                    PaperProps: {
                      sx: {
                        bgcolor: "rgba(12,18,36,0.98)",
                        border: `1px solid rgba(148,163,184,0.35)`,
                        mt: 1,
                        boxShadow: "0 12px 32px rgba(0,0,0,0.45)",
                        "& .MuiMenuItem-root": {
                          color: AURORA_SHELL.text,
                          "&.Mui-selected": { bgcolor: "rgba(125,183,255,0.18)" },
                          "&.Mui-selected:hover": { bgcolor: "rgba(125,183,255,0.25)" },
                        },
                      },
                    },
                  }}
                >
                  {(selectedDomainData.versions || [{ file: selectedDomainData.file, label: "Active" }]).map(
                    (ver) => (
                      <MenuItem key={ver.file} value={ver.file}>
                        {ver.label}{" "}
                        <Typography component="span" sx={{ ml: 1, color: AURORA_SHELL.muted, fontSize: "0.8rem" }}>
                          {formatTimestamp(ver.modified)} · {formatBytes(ver.size_bytes || 0)}
                        </Typography>
                      </MenuItem>
                    )
                  )}
                </Select>
              </FormControl>

              <Box
                sx={{
                  bgcolor: "rgba(255,255,255,0.03)",
                  borderRadius: 2,
                  border: `1px solid ${AURORA_SHELL.border}`,
                  p: 2,
                }}
              >
                <Typography sx={{ color: AURORA_SHELL.muted, fontSize: "0.85rem", mb: 1 }}>Overview</Typography>
                <Stack spacing={1}>
                  <Stack direction="row" justifyContent="space-between">
                    <Typography sx={{ color: AURORA_SHELL.muted }}>Size</Typography>
                    <Typography sx={{ color: AURORA_SHELL.text, fontWeight: 600 }}>
                      {formatBytes(selectedDomainData.size_bytes || 0)}
                    </Typography>
                  </Stack>
                  <Stack direction="row" justifyContent="space-between">
                    <Typography sx={{ color: AURORA_SHELL.muted }}>Last Updated</Typography>
                    <Typography sx={{ color: AURORA_SHELL.text }}>{formatTimestamp(selectedDomainData.modified)}</Typography>
                  </Stack>
                  <Stack direction="row" justifyContent="space-between">
                    <Typography sx={{ color: AURORA_SHELL.muted }}>Rotations</Typography>
                    <Typography sx={{ color: AURORA_SHELL.text }}>{selectedDomainData.rotation_count || 0}</Typography>
                  </Stack>
                  <Stack direction="row" justifyContent="space-between">
                    <Typography sx={{ color: AURORA_SHELL.muted }}>Retention</Typography>
                    <Typography sx={{ color: AURORA_SHELL.text }}>
                      {(selectedDomainData.retention_days ?? defaultRetention) || defaultRetention} days
                    </Typography>
                  </Stack>
                </Stack>
              </Box>

              <Divider light sx={{ borderColor: AURORA_SHELL.border }} />

              <Stack spacing={1}>
                <Typography sx={{ color: AURORA_SHELL.muted, fontSize: "0.85rem" }}>Retention Policy</Typography>
                <TextField
                  label="Retention Days"
                  size="small"
                  value={retentionDraft}
                  onChange={(e) => setRetentionDraft(e.target.value)}
                  helperText={`Leave blank to inherit default (${defaultRetention} days).`}
                  InputProps={{ inputProps: { min: 0 } }}
                />
                <Button
                  variant="contained"
                  startIcon={<SaveIcon />}
                  onClick={handleRetentionSave}
                  disabled={!selectedDomainData || disableRetentionSave}
                  sx={{
                    ...gradientButtonSx,
                    opacity: !selectedDomainData || disableRetentionSave ? 0.5 : 1,
                  }}
                >
                  Save Retention
                </Button>
              </Stack>

              <Divider light sx={{ borderColor: AURORA_SHELL.border }} />

              <Stack direction="row" spacing={1}>
                <Button
                  variant="outlined"
                  startIcon={<DeleteIcon />}
                  onClick={() => handleDelete("file")}
                  sx={{
                    color: "#f97316",
                    borderColor: "rgba(249,115,22,0.6)",
                    textTransform: "none",
                    fontWeight: 600,
                  }}
                >
                  Delete File
                </Button>
                <Button
                  variant="outlined"
                  startIcon={<HistoryIcon />}
                  onClick={() => handleDelete("family")}
                  sx={{
                    color: "#f43f5e",
                    borderColor: "rgba(244,63,94,0.6)",
                    textTransform: "none",
                    fontWeight: 600,
                  }}
                >
                  Purge Domain
                </Button>
              </Stack>
            </>
          )}

          {listLoading && (
            <Stack alignItems="center" justifyContent="center" sx={{ flexGrow: 1 }}>
              <CircularProgress size={32} />
            </Stack>
          )}
        </Box>

        <Box sx={{ flexGrow: 1, display: "flex", flexDirection: "column", minWidth: 0, p: 3, gap: 2 }}>
          <Stack direction={{ xs: "column", md: "row" }} spacing={2} alignItems="center">
            <ToggleButtonGroup
              exclusive
              size="small"
              value={gridMode}
              onChange={(_, val) => val && setGridMode(val)}
              sx={{
                background: "rgba(9,14,25,0.9)",
                borderRadius: 1,
                border: `1px solid ${AURORA_SHELL.border}`,
                boxShadow: "0 14px 32px rgba(2,6,23,0.55)",
                overflow: "hidden",
                "& .MuiToggleButton-root": {
                  textTransform: "none",
                  color: "#dce7f5",
                  border: "none",
                  px: 2.8,
                  py: 1,
                  fontWeight: 700,
                  fontSize: 13,
                  letterSpacing: 0.1,
                  transition: "all 0.18s ease",
                  backgroundColor: "#0f1627",
                  "&:hover": { backgroundColor: "rgba(148,163,184,0.14)" },
                  "&.Mui-selected": {
                    color: "#0c1224",
                    backgroundImage: "linear-gradient(135deg,#7fc9ff 0%,#b195ff 100%)",
                    boxShadow: "0 10px 24px rgba(124,58,237,0.4)",
                  },
                  "&.Mui-selected:hover": {
                    backgroundImage: "linear-gradient(135deg,#8bd8ff 0%,#c0a8ff 100%)",
                  },
                },
              }}
            >
              <ToggleButton value="structured" sx={{ color: AURORA_SHELL.text, textTransform: "none" }}>
                <VisibilityIcon fontSize="small" sx={{ mr: 1 }} /> Structured
              </ToggleButton>
              <ToggleButton value="raw" sx={{ color: AURORA_SHELL.text, textTransform: "none" }}>
                <VisibilityOffIcon fontSize="small" sx={{ mr: 1 }} /> Raw
              </ToggleButton>
            </ToggleButtonGroup>
            <TextField
              placeholder="Quick filter"
              size="small"
              value={quickFilter}
              onChange={(e) => applyQuickFilter(e.target.value)}
              sx={{
                minWidth: 220,
                flexGrow: 1,
                "& .MuiOutlinedInput-root": {
                  bgcolor: "rgba(255,255,255,0.05)",
                  borderRadius: 999,
                  color: AURORA_SHELL.text,
                },
                "& .MuiInputBase-input": {
                  color: AURORA_SHELL.text,
                },
              }}
              InputProps={{
                startAdornment: (
                  <InputAdornment position="start">
                    <LogsIcon fontSize="small" sx={{ color: AURORA_SHELL.muted }} />
                  </InputAdornment>
                ),
              }}
            />
            <Tooltip title="Reload entries">
              <span>
                <IconButton
                  onClick={() => selectedFile && fetchEntries(selectedFile)}
                  disabled={!selectedFile || entryLoading}
                  sx={{ color: AURORA_SHELL.accent }}
                >
                  <RefreshIcon />
                </IconButton>
              </span>
            </Tooltip>
          </Stack>

          {gridMode === "structured" ? (
            <Box
              sx={{
                flexGrow: 1,
                minHeight: 0,
                borderRadius: 2,
                border: "none",
                bgcolor: "transparent",
                overflow: "hidden",
              }}
            >
              {entryLoading ? (
                <Stack alignItems="center" justifyContent="center" sx={{ height: "100%" }}>
                  <CircularProgress />
                </Stack>
              ) : (
                <Box
                  className={quartzThemeClass}
                  sx={{
                    "& .ag-row-selected": {
                      backgroundColor: "rgba(125,211,252,0.2) !important",
                      boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
                    },
                  }}
                  style={gridThemeStyle}
                >
                  <AgGridReact
                    ref={gridRef}
                    rowData={entries}
                    columnDefs={columnDefs}
                    defaultColDef={defaultColDef}
                    rowHeight={42}
                    animateRows
                    pagination
                    paginationPageSize={50}
                    suppressCellFocus
                    overlayNoRowsTemplate="<span style='color:#94a3b8'>No log entries to display.</span>"
                  />
                </Box>
              )}
            </Box>
          ) : (
            <Box
              sx={{
                flexGrow: 1,
                borderRadius: 2,
                border: `1px solid ${AURORA_SHELL.border}`,
                bgcolor: "#050d1f",
                overflow: "hidden",
                display: "flex",
                flexDirection: "column",
              }}
            >
              {entryLoading ? (
                <Stack alignItems="center" justifyContent="center" sx={{ height: "100%" }}>
                  <CircularProgress />
                </Stack>
              ) : (
                <Box sx={{ height: "100%", overflow: "auto" }}>
                  <Editor
                    value={rawContent}
                    onValueChange={() => {}}
                    highlight={(code) => Prism.highlight(code, Prism.languages.bash, "bash")}
                    padding={16}
                    textareaId="raw-log-viewer"
                    textareaProps={{ readOnly: true }}
                    style={{
                      fontFamily:
                        '"IBM Plex Mono", "SFMono-Regular", Consolas, "Liberation Mono", Menlo, Courier, monospace',
                      fontSize: 13,
                      minHeight: "100%",
                      height: "100%",
                      color: "#e2e8f0",
                      background: "transparent",
                      overflow: "auto",
                    }}
                    readOnly
                  />
                </Box>
              )}
            </Box>
          )}

          {entriesMeta && (
            <Typography sx={{ fontSize: "0.85rem", color: AURORA_SHELL.muted }}>
              Showing {entriesMeta.returned_lines} of {entriesMeta.total_lines} lines from {entriesMeta.file}
              {entriesMeta.truncated ? " (truncated to the most recent lines)" : ""}.
            </Typography>
          )}
        </Box>
      </Box>
    </Paper>
  );
}
