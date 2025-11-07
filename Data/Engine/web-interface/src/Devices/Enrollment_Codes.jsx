import React, { useCallback, useEffect, useMemo, useState, useRef } from "react";
import {
  Box,
  Paper,
  Typography,
  Button,
  Stack,
  Alert,
  FormControl,
  InputLabel,
  MenuItem,
  Select,
  CircularProgress,
  Tooltip
} from "@mui/material";
import {
  ContentCopy as CopyIcon,
  DeleteOutline as DeleteIcon,
  Refresh as RefreshIcon,
  Key as KeyIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
// IMPORTANT: Do NOT import global AG Grid CSS here to avoid overriding other pages.
// We rely on the project's existing CSS and themeQuartz class name like other MagicUI pages.
ModuleRegistry.registerModules([AllCommunityModule]);

// Match the palette used on other pages (see Site_List / Device_List)
const MAGIC_UI = {
  shellBg:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), #040711",
  panelBg:
    "linear-gradient(135deg, rgba(10, 16, 31, 0.98) 0%, rgba(6, 10, 24, 0.94) 60%, rgba(15, 6, 26, 0.96) 100%)",
  panelBorder: "rgba(148, 163, 184, 0.35)",
  textBright: "#e2e8f0",
  textMuted: "#94a3b8",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
};

// Generate a scoped Quartz theme class (same pattern as other pages)
const gridTheme = themeQuartz.withParams({
  accentColor: "#8b5cf6",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});
const themeClassName = gridTheme.themeName || "ag-theme-quartz";

const TTL_PRESETS = [
  { value: 1, label: "1 hour" },
  { value: 3, label: "3 hours" },
  { value: 6, label: "6 hours" },
  { value: 12, label: "12 hours" },
  { value: 24, label: "24 hours" },
];

const determineStatus = (record) => {
  if (!record) return "expired";
  const maxUses = Number.isFinite(record?.max_uses) ? record.max_uses : 1;
  const useCount = Number.isFinite(record?.use_count) ? record.use_count : 0;
  if (useCount >= Math.max(1, maxUses || 1)) return "used";
  if (!record.expires_at) return "expired";
  const expires = new Date(record.expires_at);
  if (Number.isNaN(expires.getTime())) return "expired";
  return expires.getTime() > Date.now() ? "active" : "expired";
};

const formatDateTime = (value) => {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
};

const maskCode = (code) => {
  if (!code) return "—";
  const parts = code.split("-");
  if (parts.length <= 1) {
    const prefix = code.slice(0, 4);
    return `${prefix}${"•".repeat(Math.max(0, code.length - prefix.length))}`;
  }
  return parts
    .map((part, idx) => (idx === 0 || idx === parts.length - 1 ? part : "•".repeat(part.length)))
    .join("-");
};

export default function EnrollmentCodes() {
  const [codes, setCodes] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [feedback, setFeedback] = useState(null);
  const [statusFilter, setStatusFilter] = useState("all");
  const [ttlHours, setTtlHours] = useState(6);
  const [generating, setGenerating] = useState(false);
  const [maxUses, setMaxUses] = useState(2);
  const gridRef = useRef(null);

  const fetchCodes = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const query = statusFilter === "all" ? "" : `?status=${encodeURIComponent(statusFilter)}`;
      const resp = await fetch(`/api/admin/enrollment-codes${query}`, { credentials: "include" });
      if (!resp.ok) {
        const body = await resp.json().catch(() => ({}));
        throw new Error(body.error || `Request failed (${resp.status})`);
      }
      const data = await resp.json();
      setCodes(Array.isArray(data.codes) ? data.codes : []);
    } catch (err) {
      setError(err.message || "Unable to load codes");
    } finally {
      setLoading(false);
    }
  }, [statusFilter]);

  useEffect(() => { fetchCodes(); }, [fetchCodes]);

  const handleGenerate = useCallback(async () => {
    setGenerating(true);
    try {
      const resp = await fetch("/api/admin/enrollment-codes", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ttl_hours: ttlHours, max_uses: maxUses }),
      });
      if (!resp.ok) {
        const body = await resp.json().catch(() => ({}));
        throw new Error(body.error || `Request failed (${resp.status})`);
      }
      await fetchCodes();
      setFeedback({ type: "success", message: "New installer code created" });
    } catch (err) {
      setFeedback({ type: "error", message: err.message });
    } finally {
      setGenerating(false);
    }
  }, [ttlHours, maxUses, fetchCodes]);

  const handleCopy = (code) => {
    if (!code) return;
    try {
      if (navigator.clipboard?.writeText) {
        navigator.clipboard.writeText(code);
        setFeedback({ type: "success", message: "Code copied to clipboard" });
      }
    } catch (_) {}
  };

  const handleDelete = async (id) => {
    if (!id) return;
    if (!window.confirm("Delete this installer code?")) return;
    try {
      const resp = await fetch(`/api/admin/enrollment-codes/${id}`, {
        method: "DELETE",
        credentials: "include",
      });
      if (!resp.ok) {
        const body = await resp.json().catch(() => ({}));
        throw new Error(body.error || `Request failed (${resp.status})`);
      }
      await fetchCodes();
      setFeedback({ type: "success", message: "Code deleted" });
    } catch (err) {
      setFeedback({ type: "error", message: err.message });
    }
  };

  const columns = useMemo(() => [
    {
      headerName: "Status",
      field: "status",
      cellRenderer: (params) => {
        const status = determineStatus(params.data);
        const color =
          status === "active" ? "#34d399" :
          status === "used" ? "#7dd3fc" :
          "#fbbf24";
        return <span style={{ color, fontWeight: 600 }}>{status}</span>;
      },
      width: 120
    },
    {
      headerName: "Installer Code",
      field: "code",
      cellRenderer: (params) => (
        <span style={{ fontFamily: "monospace", color: "#7dd3fc" }}>{maskCode(params.value)}</span>
      ),
      minWidth: 200
    },
    { headerName: "Expires At", field: "expires_at", valueFormatter: p => formatDateTime(p.value) },
    { headerName: "Created By", field: "created_by_user_id" },
    {
      headerName: "Usage",
      valueGetter: (p) => `${p.data.use_count || 0} / ${p.data.max_uses || 1}`,
      cellStyle: { fontFamily: "monospace" },
      width: 120
    },
    { headerName: "Last Used", field: "last_used_at", valueFormatter: p => formatDateTime(p.value) },
    { headerName: "Used By GUID", field: "used_by_guid" },
    {
      headerName: "Actions",
      cellRenderer: (params) => {
        const record = params.data;
        const disableDelete = (record.use_count || 0) !== 0;
        return (
          <Stack direction="row" spacing={1} justifyContent="flex-end">
            <Tooltip title="Copy code">
              <span>
                <Button size="small" onClick={() => handleCopy(record.code)}>
                  <CopyIcon fontSize="small" />
                </Button>
              </span>
            </Tooltip>
            <Tooltip title={disableDelete ? "Only unused codes can be deleted" : "Delete code"}>
              <span>
                <Button size="small" disabled={disableDelete} onClick={() => handleDelete(record.id)}>
                  <DeleteIcon fontSize="small" />
                </Button>
              </span>
            </Tooltip>
          </Stack>
        );
      },
      width: 160
    }
  ], []);

  const defaultColDef = useMemo(() => ({
    sortable: true,
    filter: true,
    resizable: true,
    flex: 1,
    minWidth: 140,
  }), []);

  return (
    <Paper
      sx={{
        m: 0,
        p: 0,
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minWidth: 0,
        height: "100%",
        borderRadius: 0,
        border: `1px solid ${MAGIC_UI.panelBorder}`,
        background: MAGIC_UI.shellBg,
        boxShadow: "0 25px 80px rgba(6, 12, 30, 0.8)",
        overflow: "hidden",
      }}
      elevation={0}
    >
      {/* Hero header */}
      <Box sx={{ p: 3 }}>
        <Stack direction="row" spacing={2} alignItems="center" justifyContent="space-between">
          <Stack direction="row" spacing={1} alignItems="center">
            <KeyIcon sx={{ color: MAGIC_UI.accentA }} />
            <Typography variant="h6" sx={{ color: MAGIC_UI.textBright, fontWeight: 700 }}>
              Enrollment Installer Codes
            </Typography>
          </Stack>
          <Stack direction="row" spacing={1}>
            <Button
              variant="contained"
              disabled={generating}
              startIcon={generating ? <CircularProgress size={16} color="inherit" /> : null}
              onClick={handleGenerate}
              sx={{ background: "linear-gradient(135deg,#7dd3fc,#c084fc)", borderRadius: 999 }}
            >
              {generating ? "Generating…" : "Generate Code"}
            </Button>
            <Button variant="outlined" startIcon={<RefreshIcon />} onClick={fetchCodes} disabled={loading}>
              Refresh
            </Button>
          </Stack>
        </Stack>
      </Box>

      {/* Controls */}
      <Box sx={{ p: 2, display: "flex", gap: 2, alignItems: "center", flexWrap: "wrap" }}>
        <FormControl size="small" sx={{ minWidth: 140 }}>
          <InputLabel>Status</InputLabel>
          <Select value={statusFilter} label="Status" onChange={(e) => setStatusFilter(e.target.value)}>
            <MenuItem value="all">All</MenuItem>
            <MenuItem value="active">Active</MenuItem>
            <MenuItem value="used">Used</MenuItem>
            <MenuItem value="expired">Expired</MenuItem>
          </Select>
        </FormControl>

        <FormControl size="small" sx={{ minWidth: 160 }}>
          <InputLabel>Duration</InputLabel>
          <Select value={ttlHours} label="Duration" onChange={(e) => setTtlHours(Number(e.target.value))}>
            {TTL_PRESETS.map((p) => (
              <MenuItem key={p.value} value={p.value}>
                {p.label}
              </MenuItem>
            ))}
          </Select>
        </FormControl>

        <FormControl size="small" sx={{ minWidth: 160 }}>
          <InputLabel>Allowed Uses</InputLabel>
          <Select value={maxUses} label="Allowed Uses" onChange={(e) => setMaxUses(Number(e.target.value))}>
            {[1, 2, 3, 5].map((n) => (
              <MenuItem key={n} value={n}>
                {n === 1 ? "Single use" : `${n} uses`}
              </MenuItem>
            ))}
          </Select>
        </FormControl>
      </Box>

      {feedback && (
        <Box sx={{ px: 3 }}>
          <Alert severity={feedback.type} onClose={() => setFeedback(null)}>
            {feedback.message}
          </Alert>
        </Box>
      )}
      {error && (
        <Box sx={{ px: 3 }}>
          <Alert severity="error">{error}</Alert>
        </Box>
      )}

      {/* Grid wrapper — all overrides are SCOPED to this instance via inline CSS vars */}
      <Box
        className={themeClassName}
        sx={{
          flex: 1,
          p: 2,
          overflow: "hidden",
        }}
        // Inline style ensures the CSS variables only affect THIS grid instance
        style={{
          "--ag-background-color": "#070b1a",
          "--ag-foreground-color": "#f4f7ff",
          "--ag-header-background-color": "#0f172a",
          "--ag-header-foreground-color": "#cfe0ff",
          "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
          "--ag-row-hover-color": "rgba(125,183,255,0.08)",
          "--ag-selected-row-background-color": "rgba(64,164,255,0.18)",
          "--ag-font-family": "'IBM Plex Sans', 'Helvetica Neue', Arial, sans-serif",
          "--ag-border-color": "rgba(125,183,255,0.18)",
          "--ag-row-border-color": "rgba(125,183,255,0.14)",
          "--ag-border-radius": "8px",
        }}
      >
        <AgGridReact
          ref={gridRef}
          rowData={codes}
          columnDefs={columns}
          defaultColDef={defaultColDef}
          animateRows
          pagination
          paginationPageSize={20}
        />
      </Box>
    </Paper>
  );
}
