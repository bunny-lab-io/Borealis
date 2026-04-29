import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useLoaderData, useNavigate, useSearchParams } from "react-router-dom";
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
  Tooltip,
  Typography,
} from "@mui/material";
import {
  AddRounded as AddIcon,
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
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../../DialogStyles.jsx";
import { useRoutePageChrome } from "../../app/hooks/useRoutePageChrome.js";
import {
  createRouteRequestPlan,
  fetchRouteJson,
  getRouteErrorMessage,
  requireAuthenticatedRequest,
  rethrowIfRouteRedirect,
} from "../../app/routes/routeData.js";
import { APP_PATHS } from "../../app/routes/paths.js";
import { CountSliderGroup } from "../../Automation/Watchdogs/shared.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const PAGE_TITLE = "Device Filters";
const PAGE_SUBTITLE =
  "Build reusable assembly-targeting device filters with explicit site scope, criteria, and scheduler-safe lifecycle controls.";
const PAGE_ICON = HeaderIcon;
const gridFontFamily = "'IBM Plex Sans','Helvetica Neue',Arial,sans-serif";
const iconFontFamily = "'Quartz Regular'";
const FILTER_TAB_OPTIONS = [
  { key: "active", label: "Active" },
  { key: "archived", label: "Archived" },
];

const gridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});

const AUTO_SIZE_COLUMNS = [
  "name",
  "description",
  "site_mode",
  "matching_device_count",
  "usage",
  "last_edited_by",
  "updated_at",
];

const GRID_WRAPPER_SX = {
  width: "100%",
  height: "100%",
  minHeight: 560,
  fontFamily: gridFontFamily,
  "--ag-font-family": gridFontFamily,
  "--ag-icon-font-family": iconFontFamily,
  "& .ag-root-wrapper": {
    minHeight: "100%",
    border: "none",
    borderRadius: 0,
    background: "transparent",
  },
  "& .ag-root, & .ag-header, & .ag-center-cols-container, & .ag-paging-panel": {
    fontFamily: gridFontFamily,
  },
  "& .ag-header": {
    backgroundColor: "rgba(15,23,42,0.9)",
    borderBottom: "1px solid rgba(148,163,184,0.25)",
  },
  "& .ag-header-cell-label": {
    color: "#e2e8f0",
    fontWeight: 600,
    letterSpacing: 0.3,
  },
  "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    textAlign: "left",
    padding: "8px 12px 8px 18px",
    color: "#e2e8f0",
    fontSize: "0.88rem",
  },
  "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
    width: "100%",
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    padding: 0,
  },
  "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
    paddingLeft: "12px",
    paddingRight: "9px",
  },
  "& .ag-row": {
    borderColor: "rgba(255,255,255,0.04)",
    transition: "background 0.2s ease",
  },
  "& .ag-row:nth-of-type(even)": {
    backgroundColor: "rgba(15,23,42,0.45)",
  },
  "& .ag-row-hover": {
    backgroundColor: "rgba(73,156,196,0.2) !important",
  },
  "& .ag-icon": {
    fontFamily: iconFontFamily,
  },
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

function ScopeText({ siteMode }) {
  const meta = SITE_MODE_META[String(siteMode || "global").toLowerCase()] || SITE_MODE_META.global;
  return (
    <Typography
      component="span"
      sx={{
        color: meta.color,
        fontWeight: 600,
        fontSize: "0.82rem",
        letterSpacing: 0.2,
      }}
    >
      {meta.label}
    </Typography>
  );
}

function JobsDialog({ open, onClose, jobs, onOpenJob, title }) {
  return (
    <Dialog open={open} onClose={onClose} maxWidth="sm" fullWidth PaperProps={{ sx: DIALOG_PAPER_SX }}>
      <DialogTitle sx={DIALOG_TITLE_SX}>
        <DialogHeaderBlock
          title={title || "Jobs Referencing this Filter"}
          subtitle="Review the scheduled jobs that currently reference this filter."
        />
      </DialogTitle>
      <DialogContent sx={{ ...DIALOG_CONTENT_SX, pt: 1.5 }}>
        {!jobs?.length ? (
          <Typography sx={{ color: "#cbd5e1" }}>No scheduled jobs reference this filter.</Typography>
        ) : (
          <Stack spacing={1.2}>
            {jobs.map((job) => (
              <Box
                key={job.id}
                sx={{
                  border: "1px solid rgba(148,163,184,0.24)",
                  background: "rgba(15,23,42,0.66)",
                  borderRadius: 2.5,
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
                  variant="outlined"
                  size="small"
                  endIcon={<LaunchIcon fontSize="small" />}
                  onClick={() => {
                    onClose?.();
                    onOpenJob?.(job);
                  }}
                  sx={{
                    textTransform: "none",
                    borderColor: "rgba(148,163,184,0.32)",
                    color: "#e2e8f0",
                    background: "rgba(5,10,24,0.8)",
                    "&:hover": {
                      borderColor: "rgba(125,211,252,0.5)",
                      background: "rgba(8,14,30,0.92)",
                    },
                  }}
                >
                  Open Job
                </Button>
              </Box>
            ))}
          </Stack>
        )}
      </DialogContent>
      <DialogActions sx={DIALOG_ACTIONS_SX}>
        <Button
          onClick={onClose}
          sx={DIALOG_BUTTON_SX}
        >
          Close
        </Button>
      </DialogActions>
    </Dialog>
  );
}

export async function loadDeviceFilterListPageData(request) {
  const progress = createRouteRequestPlan(request, 4);
  try {
    await requireAuthenticatedRequest(request, progress);
    const pageUrl = new URL(request.url);
    const selectedSiteId = String(pageUrl.searchParams.get("site") || "").trim();
    const siteQuery = selectedSiteId ? `&site=${encodeURIComponent(selectedSiteId)}` : "";
    const [activePayload, archivedPayload] = await Promise.all([
      progress.fetchJson(`/api/device_filters?archived=0${siteQuery}`),
      progress.fetchJson(`/api/device_filters?archived=1${siteQuery}`),
    ]);

    return {
      selectedSiteId,
      filterCollections: {
        active: Array.isArray(activePayload?.filters) ? activePayload.filters : [],
        archived: Array.isArray(archivedPayload?.filters) ? archivedPayload.filters : [],
      },
      initialError: "",
    };
  } catch (error) {
    rethrowIfRouteRedirect(error);
    return {
      filterCollections: { active: [], archived: [] },
      initialError: getRouteErrorMessage(error, "Unable to load filters."),
    };
  } finally {
    progress.finalize();
  }
}

export default function DeviceFilterList({ refreshToken }) {
  const loaderData = useLoaderData();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const gridRef = useRef(null);
  const [tab, setTab] = useState("active");
  const [filterCollections, setFilterCollections] = useState(
    () => loaderData?.filterCollections || { active: [], archived: [] }
  );
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(() => String(loaderData?.initialError || ""));
  const [actionError, setActionError] = useState("");
  const [jobsDialog, setJobsDialog] = useState({ open: false, jobs: [], title: "" });
  const selectedSiteId = useMemo(
    () => String(searchParams.get("site") || "").trim(),
    [searchParams]
  );
  const filters = useMemo(() => {
    if (tab === "archived") return filterCollections.archived || [];
    if (tab === "active") return filterCollections.active || [];
    return [...(filterCollections.active || []), ...(filterCollections.archived || [])];
  }, [filterCollections, tab]);
  const filterCounts = useMemo(
    () => ({
      active: Array.isArray(filterCollections.active) ? filterCollections.active.length : 0,
      archived: Array.isArray(filterCollections.archived) ? filterCollections.archived.length : 0,
    }),
    [filterCollections]
  );

  const loadFilters = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const siteQuery = selectedSiteId ? `&site=${encodeURIComponent(selectedSiteId)}` : "";
      const loadBucket = async (archived) => {
        const response = await fetch(`/api/device_filters?archived=${archived ? "1" : "0"}${siteQuery}`);
        const data = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(data?.message || data?.error || `Failed to load filters (${response.status})`);
        }
        return Array.isArray(data?.filters) ? data.filters : [];
      };
      const [activeFilters, archivedFilters] = await Promise.all([loadBucket(false), loadBucket(true)]);
      setFilterCollections({
        active: activeFilters,
        archived: archivedFilters,
      });
    } catch (err) {
      setFilterCollections({ active: [], archived: [] });
      setError(err?.message || "Unable to load filters.");
    } finally {
      setLoading(false);
    }
  }, [selectedSiteId]);

  useEffect(() => {
    if (!loaderData || String(loaderData?.selectedSiteId || "") !== selectedSiteId) {
      loadFilters();
      return;
    }
    setFilterCollections(loaderData?.filterCollections || { active: [], archived: [] });
    setError(String(loaderData?.initialError || ""));
  }, [loadFilters, loaderData, refreshToken, selectedSiteId]);

  const handleViewDevices = useCallback(
    (filter) => {
      try {
        localStorage.setItem(
          "device_list_saved_filter_preview",
          JSON.stringify({ filter_id: filter?.id, name: filter?.name || "Saved Filter" })
        );
      } catch {
        /* noop */
      }
      navigate(APP_PATHS.devices);
    },
    [navigate]
  );

  const handleOpenJob = useCallback(
    (job) => {
      const jobId = Number(job?.id);
      if (!Number.isInteger(jobId) || jobId <= 0) return;
      navigate(APP_PATHS.job(jobId), {
        state: job ? { initialJob: job } : undefined,
      });
    },
    [navigate]
  );

  const handleCreateFilter = useCallback(() => {
    navigate(APP_PATHS.filterNew);
  }, [navigate]);

  const handleEditFilter = useCallback(
    (filter) => {
      const filterId = Number(filter?.id);
      if (!Number.isInteger(filterId) || filterId <= 0) return;
      navigate(APP_PATHS.filter(filterId), {
        state: filter ? { initialFilter: filter } : undefined,
      });
    },
    [navigate]
  );

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "device-filters-refresh",
        label: "Refresh",
        icon: <RefreshIcon />,
        tone: "secondary",
        loading,
        onClick: loadFilters,
      },
      {
        id: "device-filters-create",
        label: "Create Filter",
        icon: <AddIcon />,
        tone: "primary",
        onClick: handleCreateFilter,
      },
    ],
    [handleCreateFilter, loadFilters, loading]
  );

  useRoutePageChrome({
    title: PAGE_TITLE,
    subtitle: PAGE_SUBTITLE,
    Icon: PAGE_ICON,
    actions: pageHeaderActions,
  });

  const autoSize = useCallback(() => {
    const api = gridRef.current;
    if (!api || !filters.length) return;
    requestAnimationFrame(() => {
      try {
        api.autoSizeColumns(AUTO_SIZE_COLUMNS, true);
      } catch {
        /* ignore */
      }
    });
  }, [filters.length]);

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
        headerName: "Filter Name",
        minWidth: 350,
        flex: 1.4,
        cellRenderer: (params) => (
          <Box
            component="a"
            href="#"
            onClick={(event) => {
              event.preventDefault();
              handleEditFilter(params.data);
            }}
            sx={{
              display: "inline-flex",
              alignItems: "center",
              fontWeight: 700,
              color: "#7dd3fc",
              textDecoration: "none",
              cursor: "pointer",
              "&:hover": {
                color: "#bae6fd",
              },
            }}
          >
            {params.value || "Unnamed Filter"}
          </Box>
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
        minWidth: 120,
        flex: 1,
        cellRenderer: (params) => (
          <Tooltip title={scopeSummary(params.data)}>
            <Box sx={{ display: "inline-flex", alignItems: "center" }}>
              <ScopeText siteMode={params.value} />
            </Box>
          </Tooltip>
        ),
      },
      {
        field: "matching_device_count",
        headerName: "Devices",
        minWidth: 110,
        flex: 0.8,
        valueFormatter: (params) =>
          typeof params.value === "number" ? params.value.toLocaleString() : "0",
      },
      {
        field: "usage",
        headerName: "Jobs Using this Filter",
        minWidth: 170,
        flex: 1,
        sortable: false,
        filter: false,
        cellRenderer: (params) => {
          const count = Number(params.data?.usage?.job_count || 0);
          if (!count) return <Typography sx={{ color: "#64748b" }}>None</Typography>;
          return (
            <Box
              component="a"
              href="#"
              onClick={(event) => {
                event.preventDefault();
                openJobs(params.data);
              }}
              sx={{
                color: "#7dd3fc",
                textDecoration: "none",
                cursor: "pointer",
                fontWeight: 500,
                "&:hover": {
                  color: "#bae6fd",
                },
              }}
            >
              {`Scheduled Jobs (${count})`}
            </Box>
          );
        },
      },
      {
        field: "last_edited_by",
        headerName: "Last Edited By",
        minWidth: 150,
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
        minWidth: 150,
        flex: 1.2,
        sortable: false,
        filter: false,
        cellRenderer: (params) => {
          const record = params.data;
          return (
            <Stack direction="row" spacing={0.5} sx={{ width: "100%", justifyContent: "flex-end" }}>
              <Tooltip title="View Devices">
                <IconButton size="small" onClick={() => handleViewDevices(record)}>
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
    [handleArchiveToggle, handleClone, handleDelete, handleEditFilter, handleViewDevices, openJobs]
  );

  const defaultColDef = useMemo(
    () => ({
      sortable: true,
      resizable: true,
      flex: 1,
      filter: "agTextColumnFilter",
      cellClass: "auto-col-tight",
    }),
    []
  );

  const stack = (
    <Stack spacing={1.5}>
      <Box
        sx={{
          display: "flex",
          flexWrap: "wrap",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 1.5,
        }}
      >
        <CountSliderGroup
          options={FILTER_TAB_OPTIONS}
          activeKey={tab}
          counts={filterCounts}
          onChange={setTab}
        />
        <Typography variant="body2" sx={{ color: "rgba(155, 163, 180, 0.96)" }}>
          {tab
            ? `Showing ${filters.length} ${tab === "archived" ? "archived" : "active"} filter${filters.length === 1 ? "" : "s"}`
            : `Showing all ${filters.length} filter${filters.length === 1 ? "" : "s"}`}
        </Typography>
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
          <Box sx={GRID_WRAPPER_SX}>
            <AgGridReact
              rowData={filters}
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
        onOpenJob={handleOpenJob}
      />
    </>
  );
}
