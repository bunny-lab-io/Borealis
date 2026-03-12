import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  LinearProgress,
  Stack,
  Tab,
  Tabs,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import {
  ArchiveRounded as ArchiveIcon,
  CachedRounded as RefreshIcon,
  ContentCopyRounded as CloneIcon,
  DeleteRounded as DeleteIcon,
  FilterAlt as HeaderIcon,
  LaunchRounded as LaunchIcon,
  UnarchiveRounded as UnarchiveIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";

import PageBodyFrame from "../../PageBodyFrame.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const PAGE_TITLE = "Device Filters";
const PAGE_SUBTITLE =
  "Build reusable automation targeting rules with explicit site scope, previewable criteria, and scheduler-safe lifecycle controls.";
const PAGE_ICON = HeaderIcon;
const gridFontFamily = "'IBM Plex Sans','Helvetica Neue',Arial,sans-serif";
const iconFontFamily = "'Quartz Regular'";

const gridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});

const TAB_LABELS = {
  active: "Active",
  archived: "Archived",
};

const SITE_MODE_META = {
  global: {
    label: "Global",
    color: "#86efac",
    border: "rgba(74, 222, 128, 0.35)",
    background: "rgba(74, 222, 128, 0.12)",
  },
  specific_sites: {
    label: "Specific Sites",
    color: "#93c5fd",
    border: "rgba(147, 197, 253, 0.35)",
    background: "rgba(147, 197, 253, 0.12)",
  },
  global_exclusions: {
    label: "Global w/ Exclusions",
    color: "#fcd34d",
    border: "rgba(252, 211, 77, 0.35)",
    background: "rgba(252, 211, 77, 0.12)",
  },
};

const criteriaModeLabel = (value) => {
  const normalized = String(value || "basic").trim().toLowerCase();
  return normalized === "advanced" ? "Advanced" : "Basic";
};

const formatTimestamp = (value) => {
  if (!value) return "";
  const numeric = Number(value);
  const direct = Number.isFinite(numeric) && numeric > 0 ? new Date(numeric * 1000) : new Date(value);
  if (Number.isNaN(direct.getTime())) return "";
  return direct.toLocaleString();
};

const scopeSummary = (record) => {
  const siteNames = Array.isArray(record?.site_names) ? record.site_names.filter(Boolean) : [];
  const meta = SITE_MODE_META[String(record?.site_mode || "global").toLowerCase()] || SITE_MODE_META.global;
  if (!siteNames.length) return meta.label;
  return `${meta.label}: ${siteNames.join(", ")}`;
};

const matchesSearch = (record, query) => {
  if (!query) return true;
  const haystack = [
    record?.name,
    record?.description,
    scopeSummary(record),
    record?.last_edited_by,
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
  return haystack.includes(query);
};

function ScopePill({ siteMode }) {
  const meta = SITE_MODE_META[String(siteMode || "global").toLowerCase()] || SITE_MODE_META.global;
  return (
    <Box
      component="span"
      sx={{
        display: "inline-flex",
        alignItems: "center",
        px: 1.1,
        py: 0.25,
        borderRadius: 999,
        border: `1px solid ${meta.border}`,
        background: meta.background,
        color: meta.color,
        fontWeight: 700,
        fontSize: "0.82rem",
        letterSpacing: 0.2,
      }}
    >
      {meta.label}
    </Box>
  );
}

function JobsDialog({ open, onClose, jobs, onOpenJob, title }) {
  return (
    <Dialog open={open} onClose={onClose} maxWidth="sm" fullWidth>
      <DialogTitle>{title || "Jobs Referencing this Filter"}</DialogTitle>
      <DialogContent dividers>
        {!jobs?.length ? (
          <Typography sx={{ color: "#cbd5e1" }}>No scheduled jobs reference this filter.</Typography>
        ) : (
          <Stack spacing={1.2}>
            {jobs.map((job) => (
              <Box
                key={job.id}
                sx={{
                  border: "1px solid rgba(148,163,184,0.24)",
                  borderRadius: 2,
                  px: 1.5,
                  py: 1.2,
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  gap: 1.5,
                }}
              >
                <Box sx={{ minWidth: 0 }}>
                  <Typography sx={{ color: "#f8fafc", fontWeight: 600 }} noWrap>
                    {job.name || `Job ${job.id}`}
                  </Typography>
                  <Typography variant="body2" sx={{ color: "#94a3b8" }}>
                    Job ID: {job.id}
                  </Typography>
                </Box>
                <Button
                  size="small"
                  endIcon={<LaunchIcon fontSize="small" />}
                  onClick={() => {
                    onClose?.();
                    onOpenJob?.(job);
                  }}
                  sx={{ textTransform: "none" }}
                >
                  Open Job
                </Button>
              </Box>
            ))}
          </Stack>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>Close</Button>
      </DialogActions>
    </Dialog>
  );
}

export default function DeviceFilterList({
  onCreateFilter,
  onEditFilter,
  onViewDevices,
  onOpenJob,
  refreshToken,
  onPageMetaChange,
}) {
  const gridRef = useRef(null);
  const [tab, setTab] = useState("active");
  const [search, setSearch] = useState("");
  const [filters, setFilters] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [actionError, setActionError] = useState("");
  const [jobsDialog, setJobsDialog] = useState({ open: false, jobs: [], title: "" });

  const loadFilters = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const archived = tab === "archived" ? "1" : "0";
      const response = await fetch(`/api/device_filters?archived=${archived}`);
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(data?.message || data?.error || `Failed to load filters (${response.status})`);
      }
      setFilters(Array.isArray(data?.filters) ? data.filters : []);
    } catch (err) {
      setFilters([]);
      setError(err?.message || "Unable to load filters.");
    } finally {
      setLoading(false);
    }
  }, [tab]);

  useEffect(() => {
    onPageMetaChange?.({
      title: PAGE_TITLE,
      subtitle: PAGE_SUBTITLE,
      Icon: PAGE_ICON,
    });
  }, [onPageMetaChange]);

  useEffect(() => {
    loadFilters();
  }, [loadFilters, refreshToken]);

  const filteredRows = useMemo(() => {
    const query = search.trim().toLowerCase();
    return filters.filter((record) => matchesSearch(record, query));
  }, [filters, search]);

  const autoSize = useCallback(() => {
    const api = gridRef.current;
    if (!api || !filteredRows.length) return;
    requestAnimationFrame(() => {
      try {
        api.autoSizeColumns(["name", "description", "site_mode", "criteria_mode", "matching_device_count", "last_edited_by", "updated_at"], true);
      } catch {
        /* ignore */
      }
    });
  }, [filteredRows.length]);

  useEffect(() => {
    autoSize();
  }, [autoSize]);

  const performAction = useCallback(
    async (path, options = {}) => {
      setActionError("");
      const response = await fetch(path, {
        method: options.method || "POST",
        headers: { "Content-Type": "application/json" },
        body: options.body ? JSON.stringify(options.body) : undefined,
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        if (response.status === 409 && Array.isArray(data?.jobs)) {
          setJobsDialog({
            open: true,
            jobs: data.jobs,
            title: data?.message || "This filter is referenced by scheduled jobs.",
          });
        } else {
          setActionError(data?.message || data?.error || `Request failed (${response.status})`);
        }
        return null;
      }
      return data;
    },
    []
  );

  const handleClone = useCallback(
    async (record) => {
      const data = await performAction(`/api/device_filters/${record.id}/clone`);
      if (data?.filter) {
        loadFilters();
      }
    },
    [loadFilters, performAction]
  );

  const handleArchiveToggle = useCallback(
    async (record) => {
      const route = record?.archived ? "unarchive" : "archive";
      const data = await performAction(`/api/device_filters/${record.id}/${route}`);
      if (data?.filter) {
        loadFilters();
      }
    },
    [loadFilters, performAction]
  );

  const handleDelete = useCallback(
    async (record) => {
      if (!window.confirm(`Delete filter "${record?.name || "Unnamed Filter"}"?`)) return;
      const data = await performAction(`/api/device_filters/${record.id}`, { method: "DELETE" });
      if (data?.status === "ok") {
        loadFilters();
      }
    },
    [loadFilters, performAction]
  );

  const openJobs = useCallback((record) => {
    const jobs = Array.isArray(record?.usage?.jobs) ? record.usage.jobs : [];
    setJobsDialog({
      open: true,
      jobs,
      title: jobs.length ? "Jobs Referencing this Filter" : "No Jobs Reference this Filter",
    });
  }, []);

  const columnDefs = useMemo(
    () => [
      {
        field: "name",
        headerName: "Filter",
        minWidth: 250,
        flex: 1.4,
        cellRenderer: (params) => (
          <Button
            variant="text"
            size="small"
            onClick={() => onEditFilter?.(params.data)}
            sx={{
              px: 0,
              minWidth: "unset",
              textTransform: "none",
              fontWeight: 700,
              color: "#7dd3fc",
            }}
          >
            {params.value || "Unnamed Filter"}
          </Button>
        ),
      },
      {
        field: "description",
        headerName: "Description",
        minWidth: 220,
        flex: 1.2,
        valueFormatter: (params) => params.value || "—",
      },
      {
        field: "site_mode",
        headerName: "Scope",
        minWidth: 210,
        flex: 1,
        cellRenderer: (params) => (
          <Tooltip title={scopeSummary(params.data)}>
            <Box sx={{ display: "inline-flex", alignItems: "center" }}>
              <ScopePill siteMode={params.value} />
            </Box>
          </Tooltip>
        ),
      },
      {
        field: "criteria_mode",
        headerName: "Mode",
        minWidth: 120,
        flex: 0.7,
        valueFormatter: (params) => criteriaModeLabel(params.value),
      },
      {
        field: "matching_device_count",
        headerName: "Matched Devices",
        minWidth: 150,
        flex: 0.8,
        valueFormatter: (params) =>
          typeof params.value === "number" ? params.value.toLocaleString() : "0",
      },
      {
        field: "usage",
        headerName: "Scheduler Usage",
        minWidth: 200,
        flex: 1,
        sortable: false,
        filter: false,
        cellRenderer: (params) => {
          const count = Number(params.data?.usage?.job_count || 0);
          if (!count) return <Typography sx={{ color: "#64748b" }}>None</Typography>;
          return (
            <Button
              variant="text"
              size="small"
              onClick={() => openJobs(params.data)}
              sx={{ px: 0, minWidth: "unset", textTransform: "none" }}
            >
              Jobs Referencing this Filter ({count})
            </Button>
          );
        },
      },
      {
        field: "last_edited_by",
        headerName: "Last Edited By",
        minWidth: 160,
        flex: 0.9,
        valueFormatter: (params) => params.value || "Unknown",
      },
      {
        field: "updated_at",
        headerName: "Last Updated",
        minWidth: 180,
        flex: 1,
        valueFormatter: (params) => formatTimestamp(params.value),
      },
      {
        field: "actions",
        headerName: "",
        minWidth: 220,
        flex: 1.2,
        sortable: false,
        filter: false,
        cellRenderer: (params) => {
          const record = params.data;
          return (
            <Stack direction="row" spacing={0.5} sx={{ width: "100%", justifyContent: "flex-end" }}>
              <Tooltip title="View Devices">
                <IconButton size="small" onClick={() => onViewDevices?.(record)}>
                  <LaunchIcon fontSize="small" />
                </IconButton>
              </Tooltip>
              <Tooltip title="Clone">
                <IconButton size="small" onClick={() => handleClone(record)}>
                  <CloneIcon fontSize="small" />
                </IconButton>
              </Tooltip>
              <Tooltip title={record?.archived ? "Unarchive" : "Archive"}>
                <IconButton size="small" onClick={() => handleArchiveToggle(record)}>
                  {record?.archived ? <UnarchiveIcon fontSize="small" /> : <ArchiveIcon fontSize="small" />}
                </IconButton>
              </Tooltip>
              <Tooltip title="Delete">
                <IconButton size="small" onClick={() => handleDelete(record)} sx={{ color: "#fb7185" }}>
                  <DeleteIcon fontSize="small" />
                </IconButton>
              </Tooltip>
            </Stack>
          );
        },
      },
    ],
    [handleArchiveToggle, handleClone, handleDelete, onEditFilter, onViewDevices, openJobs]
  );

  const defaultColDef = useMemo(
    () => ({
      sortable: true,
      resizable: true,
      flex: 1,
      filter: "agTextColumnFilter",
      cellStyle: {
        display: "flex",
        alignItems: "center",
      },
    }),
    []
  );

  const stack = (
    <Stack spacing={1.5}>
      <Box sx={{ display: "flex", alignItems: { xs: "stretch", md: "center" }, gap: 1.25, flexWrap: "wrap" }}>
        <Tabs
          value={tab}
          onChange={(_, next) => setTab(next)}
          sx={{
            minHeight: 38,
            "& .MuiTabs-indicator": { backgroundColor: "#7dd3fc" },
          }}
        >
          <Tab value="active" label={TAB_LABELS.active} sx={{ minHeight: 38, textTransform: "none" }} />
          <Tab value="archived" label={TAB_LABELS.archived} sx={{ minHeight: 38, textTransform: "none" }} />
        </Tabs>
        <TextField
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          placeholder="Search filters..."
          size="small"
          sx={{ minWidth: { xs: "100%", md: 280 }, ml: { md: "auto" } }}
        />
        <Button
          variant="outlined"
          startIcon={<RefreshIcon />}
          onClick={loadFilters}
          sx={{ textTransform: "none" }}
        >
          Refresh
        </Button>
        <Button variant="contained" onClick={() => onCreateFilter?.()} sx={{ textTransform: "none" }}>
          Create Filter
        </Button>
      </Box>
      {loading ? <LinearProgress /> : null}
      {error ? <Alert severity="error">{error}</Alert> : null}
      {actionError ? <Alert severity="warning">{actionError}</Alert> : null}
    </Stack>
  );

  return (
    <>
      <PageBodyFrame
        variant="grid_with_stack"
        stack={stack}
        main={
          <Box
            sx={{
              width: "100%",
              height: "100%",
              minHeight: 560,
              "& .ag-root-wrapper": { borderRadius: 0 },
            }}
          >
            <AgGridReact
              rowData={filteredRows}
              columnDefs={columnDefs}
              defaultColDef={defaultColDef}
              suppressCellFocus
              animateRows
              pagination
              paginationPageSize={20}
              paginationPageSizeSelector={[20, 50, 100]}
              onGridReady={(params) => {
                gridRef.current = params.api;
                autoSize();
              }}
              getRowId={(params) => String(params.data?.id || "")}
              theme={gridTheme}
              style={{
                width: "100%",
                height: "100%",
                fontFamily: gridFontFamily,
                "--ag-icon-font-family": iconFontFamily,
              }}
            />
          </Box>
        }
      />
      <JobsDialog
        open={jobsDialog.open}
        jobs={jobsDialog.jobs}
        title={jobsDialog.title}
        onClose={() => setJobsDialog({ open: false, jobs: [], title: "" })}
        onOpenJob={onOpenJob}
      />
    </>
  );
}
