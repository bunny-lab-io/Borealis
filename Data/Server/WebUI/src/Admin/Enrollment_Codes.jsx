////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Server/WebUI/src/Admin/Enrollment_Codes.jsx

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
  Tooltip,
  Typography,
} from "@mui/material";
import {
  ContentCopy as CopyIcon,
  DeleteOutline as DeleteIcon,
  Refresh as RefreshIcon,
  Key as KeyIcon,
} from "@mui/icons-material";

const TTL_PRESETS = [
  { value: 1, label: "1 hour" },
  { value: 3, label: "3 hours" },
  { value: 6, label: "6 hours" },
  { value: 12, label: "12 hours" },
  { value: 24, label: "24 hours" },
];

const statusColor = {
  active: "success",
  used: "default",
  expired: "warning",
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

const formatDateTime = (value) => {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
};

const determineStatus = (record) => {
  if (!record) return "expired";
  if (record.used_at) return "used";
  if (!record.expires_at) return "expired";
  const expires = new Date(record.expires_at);
  if (Number.isNaN(expires.getTime())) return "expired";
  return expires.getTime() > Date.now() ? "active" : "expired";
};

function EnrollmentCodes() {
  const [codes, setCodes] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [feedback, setFeedback] = useState(null);
  const [statusFilter, setStatusFilter] = useState("all");
  const [ttlHours, setTtlHours] = useState(6);
  const [generating, setGenerating] = useState(false);

  const filteredCodes = useMemo(() => {
    if (statusFilter === "all") return codes;
    return codes.filter((code) => determineStatus(code) === statusFilter);
  }, [codes, statusFilter]);

  const fetchCodes = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const query = statusFilter === "all" ? "" : `?status=${encodeURIComponent(statusFilter)}`;
      const resp = await fetch(`/api/admin/enrollment-codes${query}`, {
        credentials: "include",
      });
      if (!resp.ok) {
        const body = await resp.json().catch(() => ({}));
        throw new Error(body.error || `Request failed (${resp.status})`);
      }
      const data = await resp.json();
      setCodes(Array.isArray(data.codes) ? data.codes : []);
    } catch (err) {
      setError(err.message || "Unable to load enrollment codes");
    } finally {
      setLoading(false);
    }
  }, [statusFilter]);

  useEffect(() => {
    fetchCodes();
  }, [fetchCodes]);

  const handleGenerate = useCallback(async () => {
    setGenerating(true);
    setError("");
    try {
      const resp = await fetch("/api/admin/enrollment-codes", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ttl_hours: ttlHours }),
      });
      if (!resp.ok) {
        const body = await resp.json().catch(() => ({}));
        throw new Error(body.error || `Request failed (${resp.status})`);
      }
      const created = await resp.json();
      setFeedback({ type: "success", message: `Installer code ${created.code} created` });
      await fetchCodes();
    } catch (err) {
      setFeedback({ type: "error", message: err.message || "Failed to create code" });
    } finally {
      setGenerating(false);
    }
  }, [fetchCodes, ttlHours]);

  const handleDelete = useCallback(
    async (id) => {
      if (!id) return;
      const confirmDelete = window.confirm("Delete this unused installer code?");
      if (!confirmDelete) return;
      setError("");
      try {
        const resp = await fetch(`/api/admin/enrollment-codes/${encodeURIComponent(id)}`, {
          method: "DELETE",
          credentials: "include",
        });
        if (!resp.ok) {
          const body = await resp.json().catch(() => ({}));
          throw new Error(body.error || `Request failed (${resp.status})`);
        }
        setFeedback({ type: "success", message: "Installer code deleted" });
        await fetchCodes();
      } catch (err) {
        setFeedback({ type: "error", message: err.message || "Failed to delete code" });
      }
    },
    [fetchCodes]
  );

  const handleCopy = useCallback((code) => {
    if (!code) return;
    try {
      if (navigator.clipboard?.writeText) {
        navigator.clipboard.writeText(code);
        setFeedback({ type: "success", message: "Code copied to clipboard" });
      } else {
        const textArea = document.createElement("textarea");
        textArea.value = code;
        textArea.style.position = "fixed";
        textArea.style.opacity = "0";
        document.body.appendChild(textArea);
        textArea.select();
        document.execCommand("copy");
        document.body.removeChild(textArea);
        setFeedback({ type: "success", message: "Code copied to clipboard" });
      }
    } catch (err) {
      setFeedback({ type: "error", message: err.message || "Unable to copy code" });
    }
  }, []);

  const renderStatusChip = (record) => {
    const status = determineStatus(record);
    return <Chip size="small" label={status} color={statusColor[status] || "default"} variant="outlined" />;
  };

  return (
    <Box sx={{ p: 3, display: "flex", flexDirection: "column", gap: 3 }}>
      <Stack direction="row" alignItems="center" spacing={2}>
        <KeyIcon color="primary" />
        <Typography variant="h5">Enrollment Installer Codes</Typography>
      </Stack>

      <Paper sx={{ p: 2, display: "flex", flexDirection: "column", gap: 2 }}>
        <Stack direction={{ xs: "column", sm: "row" }} spacing={2} alignItems={{ xs: "stretch", sm: "center" }}>
          <FormControl size="small" sx={{ minWidth: 160 }}>
            <InputLabel id="status-filter-label">Filter</InputLabel>
            <Select
              labelId="status-filter-label"
              label="Filter"
              value={statusFilter}
              onChange={(event) => setStatusFilter(event.target.value)}
            >
              <MenuItem value="all">All</MenuItem>
              <MenuItem value="active">Active</MenuItem>
              <MenuItem value="used">Used</MenuItem>
              <MenuItem value="expired">Expired</MenuItem>
            </Select>
          </FormControl>

          <FormControl size="small" sx={{ minWidth: 180 }}>
            <InputLabel id="ttl-select-label">Duration</InputLabel>
            <Select
              labelId="ttl-select-label"
              label="Duration"
              value={ttlHours}
              onChange={(event) => setTtlHours(event.target.value)}
            >
              {TTL_PRESETS.map((preset) => (
                <MenuItem key={preset.value} value={preset.value}>
                  {preset.label}
                </MenuItem>
              ))}
            </Select>
          </FormControl>

          <Button
            variant="contained"
            color="primary"
            onClick={handleGenerate}
            disabled={generating}
            startIcon={generating ? <CircularProgress size={16} color="inherit" /> : null}
          >
            {generating ? "Generating…" : "Generate Code"}
          </Button>

          <Button
            variant="outlined"
            startIcon={<RefreshIcon />}
            onClick={fetchCodes}
            disabled={loading}
          >
            Refresh
          </Button>
        </Stack>

        {feedback ? (
          <Alert
            severity={feedback.type}
            onClose={() => setFeedback(null)}
            variant="outlined"
          >
            {feedback.message}
          </Alert>
        ) : null}

        {error ? (
          <Alert severity="error" variant="outlined">
            {error}
          </Alert>
        ) : null}

        <TableContainer component={Paper} variant="outlined" sx={{ maxHeight: 420 }}>
          <Table size="small" stickyHeader>
            <TableHead>
              <TableRow>
                <TableCell>Status</TableCell>
                <TableCell>Installer Code</TableCell>
                <TableCell>Expires At</TableCell>
                <TableCell>Created By</TableCell>
                <TableCell>Used At</TableCell>
                <TableCell>Used By GUID</TableCell>
                <TableCell align="right">Actions</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {loading ? (
                <TableRow>
                  <TableCell colSpan={7} align="center">
                    <Stack direction="row" spacing={1} alignItems="center" justifyContent="center">
                      <CircularProgress size={20} />
                      <Typography variant="body2">Loading installer codes…</Typography>
                    </Stack>
                  </TableCell>
                </TableRow>
              ) : filteredCodes.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={7} align="center">
                    <Typography variant="body2" color="text.secondary">
                      No installer codes match this filter.
                    </Typography>
                  </TableCell>
                </TableRow>
              ) : (
                filteredCodes.map((record) => {
                  const status = determineStatus(record);
                  const disableDelete = status !== "active";
                  return (
                    <TableRow hover key={record.id}>
                      <TableCell>{renderStatusChip(record)}</TableCell>
                      <TableCell sx={{ fontFamily: "monospace" }}>{maskCode(record.code)}</TableCell>
                      <TableCell>{formatDateTime(record.expires_at)}</TableCell>
                      <TableCell>{record.created_by_user_id || "—"}</TableCell>
                      <TableCell>{formatDateTime(record.used_at)}</TableCell>
                      <TableCell sx={{ fontFamily: "monospace" }}>
                        {record.used_by_guid || "—"}
                      </TableCell>
                      <TableCell align="right">
                        <Tooltip title="Copy code">
                          <span>
                            <IconButton
                              size="small"
                              onClick={() => handleCopy(record.code)}
                              disabled={!record.code}
                            >
                              <CopyIcon fontSize="small" />
                            </IconButton>
                          </span>
                        </Tooltip>
                        <Tooltip title={disableDelete ? "Only unused codes can be deleted" : "Delete code"}>
                          <span>
                            <IconButton
                              size="small"
                              onClick={() => handleDelete(record.id)}
                              disabled={disableDelete}
                            >
                              <DeleteIcon fontSize="small" />
                            </IconButton>
                          </span>
                        </Tooltip>
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

export default React.memo(EnrollmentCodes);
