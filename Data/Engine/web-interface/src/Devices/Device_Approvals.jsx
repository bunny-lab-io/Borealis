import React, { useCallback, useEffect, useMemo, useState, useRef } from "react";
import {
  Alert,
  Box,
  Button,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  FormControl,
  InputLabel,
  MenuItem,
  Paper,
  Select,
  Stack,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import {
  CheckCircleOutline as ApproveIcon,
  HighlightOff as DenyIcon,
  Refresh as RefreshIcon,
  Security as SecurityIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import { PAGE_HEADER_CONTROL_SX, PageHeaderActionRail } from "../Page_Header_Actions.jsx";
import PageBodyFrame from "../PageBodyFrame.jsx";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BODY_TEXT_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";
// NOTE: Do NOT import global AG Grid CSS to avoid affecting other pages.
// We rely on the Quartz theme class name + scoped CSS vars like the rest of MagicUI.
ModuleRegistry.registerModules([AllCommunityModule]);

// MagicUI palette to match Enrollment Codes / Site List
const MAGIC_UI = {
  shellBg:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), #040711",
  panelBorder: "rgba(148, 163, 184, 0.35)",
  textBright: "#e2e8f0",
  accentA: "#7dd3fc",
};

// Quartz theme instance (same params used across MagicUI pages)
const gridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});
const themeClassName = gridTheme.themeName || "ag-theme-quartz";

const STATUS_OPTIONS = [
  { value: "all", label: "All" },
  { value: "pending", label: "Pending" },
  { value: "approved", label: "Approved" },
  { value: "completed", label: "Completed" },
  { value: "denied", label: "Denied" },
  { value: "expired", label: "Expired" },
];

const statusChipColor = {
  pending: "warning",
  approved: "info",
  completed: "success",
  denied: "default",
  expired: "default",
};

const formatDateTime = (value) => {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
};

const normalizeStatus = (status) => {
  if (!status) return "pending";
  if (status === "completed") return "completed";
  return status.toLowerCase();
};

const PAGE_TITLE = "Device Approval Queue";
const PAGE_SUBTITLE = "Review pending device enrollments and resolve conflicts with existing records.";

export default function DeviceApprovals({ onPageMetaChange }) {
  const [approvals, setApprovals] = useState([]);
  const [statusFilter, setStatusFilter] = useState("pending");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [feedback, setFeedback] = useState(null);
  const [guidInputs, setGuidInputs] = useState({});
  const [actioningId, setActioningId] = useState(null);
  const [conflictPrompt, setConflictPrompt] = useState(null);
  const gridRef = useRef(null);
  const useGlobalHeader = Boolean(onPageMetaChange);

  const loadApprovals = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const query = statusFilter === "all" ? "" : `?status=${encodeURIComponent(statusFilter)}`;
      const resp = await fetch(`/api/admin/device-approvals${query}`, { credentials: "include" });
      if (!resp.ok) {
        const body = await resp.json().catch(() => ({}));
        throw new Error(body.error || `Request failed (${resp.status})`);
      }
      const data = await resp.json();
      setApprovals(Array.isArray(data.approvals) ? data.approvals : []);
    } catch (err) {
      setError(err.message || "Unable to load device approvals");
    } finally {
      setLoading(false);
    }
  }, [statusFilter]);

  useEffect(() => { loadApprovals(); }, [loadApprovals]);

  const dedupedApprovals = useMemo(() => {
    const normalized = approvals
      .map((record) => ({ ...record, status: normalizeStatus(record.status) }))
      .sort((a, b) => {
        const left = new Date(a.created_at || 0).getTime();
        const right = new Date(b.created_at || 0).getTime();
        return left - right;
      });
    if (statusFilter !== "pending") {
      return normalized;
    }
    const seen = new Set();
    const unique = [];
    for (const record of normalized) {
      const key = record.ssl_key_fingerprint_claimed || record.hostname_claimed || record.id;
      if (seen.has(key)) continue;
      seen.add(key);
      unique.push(record);
    }
    return unique;
  }, [approvals, statusFilter]);

  const handleGuidChange = useCallback((id, value) => {
    setGuidInputs((prev) => ({ ...prev, [id]: value }));
  }, []);

  const submitApproval = useCallback(
    async (record, overrides = {}) => {
      if (!record?.id) return;
      setActioningId(record.id);
      setFeedback(null);
      setError("");
      try {
        const manualGuid = (guidInputs[record.id] || "").trim();
        const payload = {};
        const overrideGuidRaw = overrides.guid;
        let overrideGuid = "";
        if (typeof overrideGuidRaw === "string") {
          overrideGuid = overrideGuidRaw.trim();
        } else if (overrideGuidRaw != null) {
          overrideGuid = String(overrideGuidRaw).trim();
        }
        if (overrideGuid) {
          payload.guid = overrideGuid;
        } else if (manualGuid) {
          payload.guid = manualGuid;
        }
        const resolutionRaw = overrides.conflictResolution || overrides.resolution;
        if (typeof resolutionRaw === "string" && resolutionRaw.trim()) {
          payload.conflict_resolution = resolutionRaw.trim().toLowerCase();
        }
        const resp = await fetch(`/api/admin/device-approvals/${encodeURIComponent(record.id)}/approve`, {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(Object.keys(payload).length ? payload : {}),
        });
        const body = await resp.json().catch(() => ({}));
        if (!resp.ok) {
          if (resp.status === 409 && body.error === "conflict_resolution_required") {
            const conflict = record.hostname_conflict;
            const fallbackAlternate =
              record.alternate_hostname ||
              (record.hostname_claimed ? `${record.hostname_claimed}-1` : "");
            if (conflict) {
              setConflictPrompt({
                record,
                conflict,
                alternate: fallbackAlternate || "",
              });
            }
            return;
          }
          throw new Error(body.error || `Approval failed (${resp.status})`);
        }
        const appliedResolution = (body.conflict_resolution || payload.conflict_resolution || "").toLowerCase();
        let successMessage = "Enrollment approved";
        if (appliedResolution === "overwrite") successMessage = "Enrollment approved; existing device overwritten";
        else if (appliedResolution === "coexist") successMessage = "Enrollment approved; devices will co-exist";
        else if (appliedResolution === "auto_merge_fingerprint") successMessage = "Enrollment approved; device reconnected with its existing identity";
        setFeedback({ type: "success", message: successMessage });
        await loadApprovals();
      } catch (err) {
        setFeedback({ type: "error", message: err.message || "Unable to approve request" });
      } finally {
        setActioningId(null);
      }
    },
    [guidInputs, loadApprovals]
  );

  const startApprove = useCallback(
    (record) => {
      if (!record?.id) return;
      const status = normalizeStatus(record.status);
      if (status !== "pending") return;
      const manualGuid = (guidInputs[record.id] || "").trim();
      const conflict = record.hostname_conflict;
      const requiresPrompt = Boolean(conflict?.requires_prompt ?? record.conflict_requires_prompt);
      if (requiresPrompt && !manualGuid) {
        const fallbackAlternate =
          record.alternate_hostname ||
          (record.hostname_claimed ? `${record.hostname_claimed}-1` : "");
        setConflictPrompt({ record, conflict, alternate: fallbackAlternate || "" });
        return;
      }
      submitApproval(record);
    },
    [guidInputs, submitApproval]
  );

  const handleDeny = useCallback(
    async (record) => {
      if (!record?.id) return;
      const confirmDeny = window.confirm("Deny this enrollment request?");
      if (!confirmDeny) return;
      setActioningId(record.id);
      setFeedback(null);
      setError("");
      try {
        const resp = await fetch(`/api/admin/device-approvals/${encodeURIComponent(record.id)}/deny`, {
          method: "POST",
          credentials: "include",
        });
        if (!resp.ok) {
          const body = await resp.json().catch(() => ({}));
          throw new Error(body.error || `Deny failed (${resp.status})`);
        }
        setFeedback({ type: "success", message: "Enrollment denied" });
        await loadApprovals();
      } catch (err) {
        setFeedback({ type: "error", message: err.message || "Unable to deny request" });
      } finally {
        setActioningId(null);
      }
    },
    [loadApprovals]
  );

  const pageHeaderControls = useMemo(
    () => [
      <FormControl key="approval-status-filter" size="small" sx={{ minWidth: 200, ...PAGE_HEADER_CONTROL_SX }}>
        <InputLabel id="approval-status-filter-label">Status</InputLabel>
        <Select
          labelId="approval-status-filter-label"
          label="Status"
          value={statusFilter}
          onChange={(event) => setStatusFilter(event.target.value)}
        >
          {STATUS_OPTIONS.map((option) => (
            <MenuItem key={option.value} value={option.value}>
              {option.label}
            </MenuItem>
          ))}
        </Select>
      </FormControl>,
    ],
    [statusFilter]
  );

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "device-approvals-refresh",
        label: "Refresh",
        icon: <RefreshIcon />,
        tone: "secondary",
        loading,
        onClick: loadApprovals,
      },
    ],
    [loadApprovals, loading]
  );

  useEffect(() => {
    onPageMetaChange?.({
      page_title: PAGE_TITLE,
      page_subtitle: PAGE_SUBTITLE,
      page_icon: SecurityIcon,
      page_header_actions: pageHeaderActions,
      page_header_controls: pageHeaderControls,
    });
    return () => onPageMetaChange?.(null);
  }, [onPageMetaChange, pageHeaderActions, pageHeaderControls]);

  const columns = useMemo(() => [
    {
      headerName: "Status",
      field: "status",
      valueGetter: (p) => normalizeStatus(p.data?.status),
      cellRenderer: (params) => {
        const status = params.value || "pending";
        // mimic MUI Chip coloring via text hues
        const color = status === "completed" ? "#34d399"
          : status === "approved" ? "#60a5fa"
          : status === "denied" || status === "expired" ? "#9aa0a6"
          : "#fbbf24";
        return <span style={{ color, fontWeight: 600 }}>{status}</span>;
      },
      minWidth: 110,
      width: 110,
    },
    { headerName: "Hostname", field: "hostname_claimed", minWidth: 180 },
    {
      headerName: "Site",
      field: "site_name",
      valueGetter: (p) => p.data?.site_name || (p.data?.site_id ? `Site ${p.data.site_id}` : "—"),
      minWidth: 160,
    },
    {
      headerName: "Date of Enrollment Request",
      field: "created_at",
      valueFormatter: (p) => formatDateTime(p.value),
      minWidth: 200,
    },
    {
      headerName: "Date of Approval",
      field: "updated_at",
      valueFormatter: (p) => formatDateTime(p.value),
      minWidth: 180,
    },
    {
      headerName: "Approved By",
      valueGetter: (p) => p.data?.approved_by_username || p.data?.approved_by_user_id || "—",
      minWidth: 100,
      Width: 100,
    },
    {
      headerName: "Actions",
      cellRenderer: (params) => {
        const record = params.data || {};
        const status = normalizeStatus(record.status);
        const showActions = status === "pending";
        const guidValue = params.context.guidInputs[record.id] || "";
        const { startApprove, handleDeny, handleGuidChange, actioningId } = params.context;
        if (!showActions) {
          return (
            <Box sx={{ display: "flex", alignItems: "center", height: "100%" }}>
              <Typography variant="body2" sx={{ color: "#9aa0a6" }}>
                No actions available
              </Typography>
            </Box>
          );
        }
        const isBusy = actioningId === record.id;
        return (
          <Box sx={{ display: "flex", alignItems: "center", height: "100%" }}>
            <Stack direction="row" spacing={8} alignItems="center">
              <TextField
                size="small"
                label="Optional GUID"
                placeholder="Leave empty to auto-generate"
                value={guidValue}
                onChange={(e) => handleGuidChange(record.id, e.target.value)}
                sx={{ minWidth: 220 }}
              />
              <Stack direction="row" spacing={1}>
                <Tooltip title="Approve enrollment">
                  <span>
                    <Button
                      color="success"
                      variant="text"
                      onClick={() => startApprove(record)}
                      disabled={isBusy}
                      startIcon={isBusy ? <CircularProgress size={16} color="success" /> : <ApproveIcon fontSize="small" />}
                    >
                      Approve
                    </Button>
                  </span>
                </Tooltip>
                <Tooltip title="Deny enrollment">
                  <span>
                    <Button
                      color="error"
                      variant="text"
                      onClick={() => handleDeny(record)}
                      disabled={isBusy}
                      startIcon={<DenyIcon fontSize="small" />}
                    >
                      Deny
                    </Button>
                  </span>
                </Tooltip>
              </Stack>
            </Stack>
          </Box>
        );
      },
      minWidth: 480,
      flex: 1,
    },
  ], []);

  const defaultColDef = useMemo(() => ({
    sortable: true,
    filter: true,
    resizable: true,
    minWidth: 140,
  }), []);

  // Dialog helpers
  const conflictRecord = conflictPrompt?.record;
  const conflictInfo = conflictPrompt?.conflict;
  const conflictHostname = conflictRecord?.hostname_claimed || conflictRecord?.hostname || "";
  const conflictSiteName = conflictInfo?.site_name || "";
  const conflictSiteDescriptor = conflictInfo
    ? conflictSiteName
      ? `under site ${conflictSiteName}`
      : "under site (not assigned)"
    : "under site (not assigned)";
  const conflictAlternate =
    conflictPrompt?.alternate ||
    (conflictHostname ? `${conflictHostname}-1` : "hostname-1");
  const conflictGuidDisplay = conflictInfo?.guid || "";

  const handleConflictCancel = useCallback(() => setConflictPrompt(null), []);
  const handleConflictOverwrite = useCallback(() => {
    if (!conflictPrompt?.record) { setConflictPrompt(null); return; }
    const { record, conflict } = conflictPrompt;
    setConflictPrompt(null);
    const conflictGuid = conflict?.guid != null ? String(conflict.guid).trim() : "";
    submitApproval(record, { guid: conflictGuid, conflictResolution: "overwrite" });
  }, [conflictPrompt, submitApproval]);
  const handleConflictCoexist = useCallback(() => {
    if (!conflictPrompt?.record) { setConflictPrompt(null); return; }
    const { record } = conflictPrompt;
    setConflictPrompt(null);
    submitApproval(record, { conflictResolution: "coexist" });
  }, [conflictPrompt, submitApproval]);

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
        border: "none",
        background: "transparent",
        boxShadow: "none",
      overflow: "hidden",
    }}
    elevation={0}
  >
      {!useGlobalHeader && (
        <Box sx={{ px: 3, pt: 3, pb: 1.5 }}>
          <Box
            sx={{
              display: "flex",
              flexDirection: { xs: "column", xl: "row" },
              alignItems: { xs: "stretch", xl: "flex-start" },
              justifyContent: "space-between",
              gap: 2,
            }}
          >
            <Box sx={{ minWidth: 0 }}>
              <Stack direction="row" spacing={1} alignItems="center">
                <SecurityIcon sx={{ color: MAGIC_UI.accentA }} />
                <Typography variant="h6" sx={{ color: MAGIC_UI.textBright, fontWeight: 700 }}>
                  {PAGE_TITLE}
                </Typography>
              </Stack>
              <Typography variant="body2" sx={{ color: "#aaa", mt: 0.5 }}>
                {PAGE_SUBTITLE}
              </Typography>
            </Box>
            <PageHeaderActionRail actions={pageHeaderActions} controls={pageHeaderControls} />
          </Box>
        </Box>
      )}

      {/* Feedback */}
      <PageBodyFrame variant="grid">
        <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, minHeight: 0 }}>
          {feedback && (
            <Box sx={{ mb: 2 }}>
              <Alert severity={feedback.type} variant="outlined" onClose={() => setFeedback(null)}>
                {feedback.message}
              </Alert>
            </Box>
          )}
          {error && (
            <Box sx={{ mb: 2 }}>
              <Alert severity="error" variant="outlined">
                {error}
              </Alert>
            </Box>
          )}

          <Box
            className={themeClassName}
            sx={{
              flex: 1,
              minHeight: 0,
              width: "100%",
              overflow: "hidden",
              "& .ag-root-wrapper": {
                minHeight: "100%",
                border: "none",
                background: "transparent",
              },
              "& .ag-row-selected": {
                backgroundColor: "rgba(125,211,252,0.2) !important",
                boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
              },
            }}
            style={{
              "--ag-background-color": "#070b1a",
              "--ag-foreground-color": "#f4f7ff",
              "--ag-header-background-color": "#0f172a",
              "--ag-header-foreground-color": "#cfe0ff",
              "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
              "--ag-row-hover-color": "rgba(73,156,196,0.2)",
              "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
              "--ag-font-family": "'IBM Plex Sans', 'Helvetica Neue', Arial, sans-serif",
              "--ag-border-color": "rgba(125,183,255,0.18)",
              "--ag-row-border-color": "rgba(125,183,255,0.14)",
              "--ag-border-radius": "8px",
            }}
          >
            <AgGridReact
              ref={gridRef}
              rowData={dedupedApprovals}
              columnDefs={columns}
              defaultColDef={defaultColDef}
              suppressCellFocus
              animateRows
              pagination
              paginationPageSize={20}
              context={{ startApprove, handleDeny, handleGuidChange, actioningId, guidInputs }}
            />
          </Box>
        </Box>
      </PageBodyFrame>

      {/* Conflict Dialog (unchanged logic) */}
      <Dialog open={Boolean(conflictPrompt)} onClose={handleConflictCancel} maxWidth="sm" fullWidth PaperProps={{ sx: DIALOG_PAPER_SX }}>
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Hostname Conflict"
            subtitle="Choose whether this incoming device should overwrite the existing record or coexist beside it."
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Stack spacing={2}>
            <DialogContentText sx={DIALOG_BODY_TEXT_SX}>
              {conflictHostname
                ? `Device ${conflictHostname} already exists in the database ${conflictSiteDescriptor}.`
                : `A device with this hostname already exists in the database ${conflictSiteDescriptor}.`}
            </DialogContentText>
            <DialogContentText sx={DIALOG_BODY_TEXT_SX}>
              Do you want this device to overwrite the existing device, or allow both to co-exist?
            </DialogContentText>
            <DialogContentText sx={DIALOG_BODY_TEXT_SX}>
              {`Device will be renamed ${conflictAlternate} if you choose to allow both to co-exist.`}
            </DialogContentText>
            {conflictGuidDisplay ? (
              <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
                Existing device GUID: {conflictGuidDisplay}
              </Typography>
            ) : null}
          </Stack>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={handleConflictCancel} sx={DIALOG_BUTTON_SX}>Cancel</Button>
          <Button onClick={handleConflictCoexist} sx={DIALOG_BUTTON_SX}>
            Allow Both
          </Button>
          <Button onClick={handleConflictOverwrite} sx={DIALOG_PRIMARY_BUTTON_SX} disabled={!conflictGuidDisplay}>
            Overwrite Existing
          </Button>
        </DialogActions>
      </Dialog>
    </Paper>
  );
}
