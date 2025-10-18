////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Server/WebUI/src/Admin/Device_Approvals.jsx

import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  Alert,
  Box,
  Button,
  Chip,
  CircularProgress,
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
  { value: "pending", label: "Pending" },
  { value: "approved", label: "Approved" },
  { value: "completed", label: "Completed" },
  { value: "denied", label: "Denied" },
  { value: "expired", label: "Expired" },
  { value: "all", label: "All" },
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
  const [statusFilter, setStatusFilter] = useState("pending");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [feedback, setFeedback] = useState(null);
  const [guidInputs, setGuidInputs] = useState({});
  const [actioningId, setActioningId] = useState(null);

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

  const handleApprove = useCallback(
    async (record) => {
      if (!record?.id) return;
      setActioningId(record.id);
      setFeedback(null);
      setError("");
      try {
        const guid = (guidInputs[record.id] || "").trim();
        const resp = await fetch(`/api/admin/device-approvals/${encodeURIComponent(record.id)}/approve`, {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(guid ? { guid } : {}),
        });
        if (!resp.ok) {
          const body = await resp.json().catch(() => ({}));
          throw new Error(body.error || `Approval failed (${resp.status})`);
        }
        setFeedback({ type: "success", message: "Enrollment approved" });
        await loadApprovals();
      } catch (err) {
        setFeedback({ type: "error", message: err.message || "Unable to approve request" });
      } finally {
        setActioningId(null);
      }
    },
    [guidInputs, loadApprovals]
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
                      <TableCell>{record.approved_by_user_id || "—"}</TableCell>
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
                                    onClick={() => handleApprove(record)}
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
    </Box>
  );
}

export default React.memo(DeviceApprovals);
