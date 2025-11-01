////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Server/WebUI/src/Admin/Device_Approvals.jsx

import React, { useCallback, useEffect, useMemo, useState } from "react";
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
  IconButton,
  InputLabel,
  MenuItem,
  Paper,
  Select,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
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

const formatFingerprint = (fp) => {
  if (!fp) return "—";
  const normalized = fp.replace(/[^a-f0-9]/gi, "").toLowerCase();
  if (!normalized) return fp;
  return normalized.match(/.{1,4}/g)?.join(" ") ?? normalized;
};

const normalizeStatus = (status) => {
  if (!status) return "pending";
  if (status === "completed") return "completed";
  return status.toLowerCase();
};

function DeviceApprovals() {
  const [approvals, setApprovals] = useState([]);
  const [statusFilter, setStatusFilter] = useState("all");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [feedback, setFeedback] = useState(null);
  const [guidInputs, setGuidInputs] = useState({});
  const [actioningId, setActioningId] = useState(null);
  const [conflictPrompt, setConflictPrompt] = useState(null);

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

  useEffect(() => {
    loadApprovals();
  }, [loadApprovals]);

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
        if (appliedResolution === "overwrite") {
          successMessage = "Enrollment approved; existing device overwritten";
        } else if (appliedResolution === "coexist") {
          successMessage = "Enrollment approved; devices will co-exist";
        } else if (appliedResolution === "auto_merge_fingerprint") {
          successMessage = "Enrollment approved; device reconnected with its existing identity";
        }
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
        setConflictPrompt({
          record,
          conflict,
          alternate: fallbackAlternate || "",
        });
        return;
      }
      submitApproval(record);
    },
    [guidInputs, submitApproval]
  );

  const handleConflictCancel = useCallback(() => {
    setConflictPrompt(null);
  }, []);

  const handleConflictOverwrite = useCallback(() => {
    if (!conflictPrompt?.record) {
      setConflictPrompt(null);
      return;
    }
    const { record, conflict } = conflictPrompt;
    setConflictPrompt(null);
    const conflictGuid = conflict?.guid != null ? String(conflict.guid).trim() : "";
    submitApproval(record, {
      guid: conflictGuid,
      conflictResolution: "overwrite",
    });
  }, [conflictPrompt, submitApproval]);

  const handleConflictCoexist = useCallback(() => {
    if (!conflictPrompt?.record) {
      setConflictPrompt(null);
      return;
    }
    const { record } = conflictPrompt;
    setConflictPrompt(null);
    submitApproval(record, {
      conflictResolution: "coexist",
    });
  }, [conflictPrompt, submitApproval]);

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

  return (
    <Box sx={{ p: 3, display: "flex", flexDirection: "column", gap: 3 }}>
      <Stack direction="row" alignItems="center" spacing={2}>
        <SecurityIcon color="primary" />
        <Typography variant="h5">Device Approval Queue</Typography>
      </Stack>

      <Paper sx={{ p: 2, display: "flex", flexDirection: "column", gap: 2 }}>
        <Stack direction={{ xs: "column", sm: "row" }} spacing={2} alignItems={{ xs: "stretch", sm: "center" }}>
          <FormControl size="small" sx={{ minWidth: 200 }}>
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
          </FormControl>

          <Button
            variant="outlined"
            startIcon={<RefreshIcon />}
            onClick={loadApprovals}
            disabled={loading}
          >
            Refresh
          </Button>
        </Stack>

        {feedback ? (
          <Alert severity={feedback.type} variant="outlined" onClose={() => setFeedback(null)}>
            {feedback.message}
          </Alert>
        ) : null}

        {error ? (
          <Alert severity="error" variant="outlined">
            {error}
          </Alert>
        ) : null}

        <TableContainer component={Paper} variant="outlined" sx={{ maxHeight: 480 }}>
          <Table size="small" stickyHeader>
            <TableHead>
              <TableRow>
                <TableCell>Status</TableCell>
                <TableCell>Hostname</TableCell>
                <TableCell>Fingerprint</TableCell>
                <TableCell>Enrollment Code</TableCell>
                <TableCell>Created</TableCell>
                <TableCell>Updated</TableCell>
                <TableCell>Approved By</TableCell>
                <TableCell align="right">Actions</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {loading ? (
                <TableRow>
                  <TableCell colSpan={8} align="center">
                    <Stack direction="row" spacing={1} alignItems="center" justifyContent="center">
                      <CircularProgress size={20} />
                      <Typography variant="body2">Loading approvals…</Typography>
                    </Stack>
                  </TableCell>
                </TableRow>
              ) : dedupedApprovals.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={8} align="center">
                    <Typography variant="body2" color="text.secondary">
                      No enrollment requests match this filter.
                    </Typography>
                  </TableCell>
                </TableRow>
              ) : (
                dedupedApprovals.map((record) => {
                  const status = normalizeStatus(record.status);
                  const showActions = status === "pending";
                  const guidValue = guidInputs[record.id] || "";
                  const approverDisplay = record.approved_by_username || record.approved_by_user_id;
                  return (
                    <TableRow hover key={record.id}>
                      <TableCell>
                        <Chip
                          size="small"
                          label={status}
                          color={statusChipColor[status] || "default"}
                          variant="outlined"
                        />
                      </TableCell>
                      <TableCell>{record.hostname_claimed || "—"}</TableCell>
                      <TableCell sx={{ fontFamily: "monospace", whiteSpace: "nowrap" }}>
                        {formatFingerprint(record.ssl_key_fingerprint_claimed)}
                      </TableCell>
                      <TableCell sx={{ fontFamily: "monospace" }}>
                        {record.enrollment_code_id || "—"}
                      </TableCell>
                      <TableCell>{formatDateTime(record.created_at)}</TableCell>
                      <TableCell>{formatDateTime(record.updated_at)}</TableCell>
                      <TableCell>{approverDisplay || "—"}</TableCell>
                      <TableCell align="right">
                        {showActions ? (
                          <Stack direction={{ xs: "column", sm: "row" }} spacing={1} alignItems="center">
                            <TextField
                              size="small"
                              label="Optional GUID"
                              placeholder="Leave empty to auto-generate"
                              value={guidValue}
                              onChange={(event) => handleGuidChange(record.id, event.target.value)}
                              sx={{ minWidth: 200 }}
                            />
                            <Stack direction="row" spacing={1}>
                              <Tooltip title="Approve enrollment">
                                <span>
                                  <IconButton
                                    color="success"
                                    onClick={() => startApprove(record)}
                                    disabled={actioningId === record.id}
                                  >
                                    {actioningId === record.id ? (
                                      <CircularProgress color="success" size={20} />
                                    ) : (
                                      <ApproveIcon fontSize="small" />
                                    )}
                                  </IconButton>
                                </span>
                              </Tooltip>
                              <Tooltip title="Deny enrollment">
                                <span>
                                  <IconButton
                                    color="error"
                                    onClick={() => handleDeny(record)}
                                    disabled={actioningId === record.id}
                                  >
                                    <DenyIcon fontSize="small" />
                                  </IconButton>
                                </span>
                              </Tooltip>
                            </Stack>
                          </Stack>
                        ) : (
                          <Typography variant="body2" color="text.secondary">
                            No actions available
                          </Typography>
                        )}
                      </TableCell>
                    </TableRow>
                  );
                })
              )}
            </TableBody>
          </Table>
        </TableContainer>
      </Paper>
      <Dialog
        open={Boolean(conflictPrompt)}
        onClose={handleConflictCancel}
        maxWidth="sm"
        fullWidth
      >
        <DialogTitle>Hostname Conflict</DialogTitle>
        <DialogContent dividers>
          <Stack spacing={2}>
            <DialogContentText>
              {conflictHostname
                ? `Device ${conflictHostname} already exists in the database ${conflictSiteDescriptor}.`
                : `A device with this hostname already exists in the database ${conflictSiteDescriptor}.`}
            </DialogContentText>
            <DialogContentText>
              Do you want this device to overwrite the existing device, or allow both to co-exist?
            </DialogContentText>
            <DialogContentText>
              {`Device will be renamed ${conflictAlternate} if you choose to allow both to co-exist.`}
            </DialogContentText>
            {conflictGuidDisplay ? (
              <Typography variant="body2" color="text.secondary">
                Existing device GUID: {conflictGuidDisplay}
              </Typography>
            ) : null}
          </Stack>
        </DialogContent>
        <DialogActions>
          <Button onClick={handleConflictCancel}>Cancel</Button>
          <Button onClick={handleConflictCoexist} color="info" variant="outlined">
            Allow Both
          </Button>
          <Button
            onClick={handleConflictOverwrite}
            color="primary"
            variant="contained"
            disabled={!conflictGuidDisplay}
          >
            Overwrite Existing
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  );
}

export default React.memo(DeviceApprovals);
