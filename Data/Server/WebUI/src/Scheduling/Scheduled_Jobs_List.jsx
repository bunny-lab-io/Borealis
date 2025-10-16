////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Scheduled_Jobs_List.jsx

import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState
} from "react";
import {
  Paper,
  Box,
  Typography,
  Button,
  Switch,
  Dialog,
  DialogTitle,
  DialogActions,
  CircularProgress
} from "@mui/material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";

ModuleRegistry.registerModules([AllCommunityModule]);

const myTheme = themeQuartz.withParams({
  accentColor: "#FFA6FF",
  backgroundColor: "#1f2836",
  browserColorScheme: "dark",
  chromeBackgroundColor: {
    ref: "foregroundColor",
    mix: 0.07,
    onto: "backgroundColor"
  },
  fontFamily: {
    googleFont: "IBM Plex Sans"
  },
  foregroundColor: "#FFF",
  headerFontSize: 14
});

const themeClassName = myTheme.themeName || "ag-theme-quartz";
const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';

function ResultsBar({ counts }) {
  const total = Math.max(1, Number(counts?.total_targets || 0));
  const sections = [
    { key: "success", color: "#00d18c" },
    { key: "running", color: "#58a6ff" },
    { key: "failed", color: "#ff4f4f" },
    { key: "timed_out", color: "#b36ae2" },
    { key: "expired", color: "#777777" },
    { key: "pending", color: "#999999" }
  ];
  const labelFor = (key) =>
    key === "pending"
      ? "Scheduled"
      : key
          .replace(/_/g, " ")
          .replace(/^./, (c) => c.toUpperCase());

  const hasNonPending = sections
    .filter((section) => section.key !== "pending")
    .some((section) => Number(counts?.[section.key] || 0) > 0);

  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 0.5 }}>
      <Box
        sx={{
          display: "flex",
          borderRadius: 2,
          overflow: "hidden",
          width: 260,
          height: 8
        }}
      >
        {sections.map((section) => {
          const value = Number(counts?.[section.key] || 0);
          if (!value) return null;
          const width = `${Math.round((value / total) * 100)}%`;
          return (
            <Box
              key={section.key}
              component="span"
              sx={{ display: "block", height: "100%", width, backgroundColor: section.color }}
            />
          );
        })}
      </Box>
      <Box
        sx={{
          display: "flex",
          flexWrap: "wrap",
          gap: 1,
          color: "#aaa",
          fontSize: 12,
          fontFamily: gridFontFamily
        }}
      >
        {(() => {
          if (!hasNonPending && Number(counts?.pending || 0) > 0) {
            return <Box component="span">Scheduled</Box>;
          }
          return sections
            .filter((section) => Number(counts?.[section.key] || 0) > 0)
            .map((section) => (
              <Box
                key={section.key}
                component="span"
                sx={{ display: "inline-flex", alignItems: "center", gap: 0.75 }}
              >
                <Box
                  component="span"
                  sx={{
                    width: 8,
                    height: 8,
                    borderRadius: 1,
                    backgroundColor: section.color
                  }}
                />
                {counts?.[section.key]} {labelFor(section.key)}
              </Box>
            ));
        })()}
      </Box>
    </Box>
  );
}

export default function ScheduledJobsList({ onCreateJob, onEditJob, refreshToken }) {
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [bulkDeleteOpen, setBulkDeleteOpen] = useState(false);
  const [selectedIds, setSelectedIds] = useState(() => new Set());
  const gridApiRef = useRef(null);

  const loadJobs = useCallback(
    async ({ showLoading = false } = {}) => {
      if (showLoading) {
        setLoading(true);
        setError("");
      }
      try {
        const resp = await fetch("/api/scheduled_jobs");
        const data = await resp.json().catch(() => ({}));
        if (!resp.ok) {
          throw new Error(data?.error || `HTTP ${resp.status}`);
        }
        const pretty = (st) => {
          const s = String(st || "").toLowerCase();
          const map = {
            immediately: "Immediately",
            once: "Once",
            every_5_minutes: "Every 5 Minutes",
            every_10_minutes: "Every 10 Minutes",
            every_15_minutes: "Every 15 Minutes",
            every_30_minutes: "Every 30 Minutes",
            every_hour: "Every Hour",
            daily: "Daily",
            weekly: "Weekly",
            monthly: "Monthly",
            yearly: "Yearly"
          };
          if (map[s]) return map[s];
          try {
            return s.replace(/_/g, " ").replace(/^./, (c) => c.toUpperCase());
          } catch {
            return String(st || "");
          }
        };
        const fmt = (ts) => {
          if (!ts) return "";
          try {
            const d = new Date(Number(ts) * 1000);
            if (Number.isNaN(d?.getTime())) return "";
            return d.toLocaleString(undefined, {
              year: "numeric",
              month: "2-digit",
              day: "2-digit",
              hour: "numeric",
              minute: "2-digit"
            });
          } catch {
            return "";
          }
        };
        const mappedRows = (data?.jobs || []).map((j) => {
          const compName = (Array.isArray(j.components) && j.components[0]?.name) || "Demonstration Component";
          const targetText = Array.isArray(j.targets)
            ? `${j.targets.length} device${j.targets.length !== 1 ? "s" : ""}`
            : "";
          const occurrence = pretty(j.schedule_type || "immediately");
          const resultsCounts = {
            total_targets: Array.isArray(j.targets) ? j.targets.length : 0,
            pending: Array.isArray(j.targets) ? j.targets.length : 0,
            ...(j.result_counts || {})
          };
          if (resultsCounts && resultsCounts.total_targets == null) {
            resultsCounts.total_targets = Array.isArray(j.targets) ? j.targets.length : 0;
          }
          return {
            id: j.id,
            name: j.name,
            scriptWorkflow: compName,
            target: targetText,
            occurrence,
            lastRun: fmt(j.last_run_ts),
            nextRun: fmt(j.next_run_ts || j.start_ts),
            result: j.last_status || (j.next_run_ts ? "Scheduled" : ""),
            resultsCounts,
            enabled: Boolean(j.enabled),
            raw: j
          };
        });
        setRows(mappedRows);
        setError("");
        setSelectedIds((prev) => {
          if (!prev.size) return prev;
          const valid = new Set(
            mappedRows.map((row, index) => row.id ?? row.name ?? String(index))
          );
          let changed = false;
          const next = new Set();
          prev.forEach((value) => {
            if (valid.has(value)) {
              next.add(value);
            } else {
              changed = true;
            }
          });
          return changed ? next : prev;
        });
      } catch (err) {
        setRows([]);
        setSelectedIds(() => new Set());
        setError(String(err?.message || err || "Failed to load scheduled jobs"));
      } finally {
        if (showLoading) {
          setLoading(false);
        }
      }
    },
    []
  );

  useEffect(() => {
    let timer;
    let isMounted = true;
    (async () => {
      if (!isMounted) return;
      await loadJobs({ showLoading: true });
    })();
    timer = setInterval(() => {
      loadJobs();
    }, 5000);
    return () => {
      isMounted = false;
      if (timer) clearInterval(timer);
    };
  }, [loadJobs, refreshToken]);

  const handleGridReady = useCallback((params) => {
    gridApiRef.current = params.api;
  }, []);

  useEffect(() => {
    const api = gridApiRef.current;
    if (!api) return;
    if (loading) {
      api.showLoadingOverlay();
    } else if (!rows.length) {
      api.showNoRowsOverlay();
    } else {
      api.hideOverlay();
    }
  }, [loading, rows]);

  useEffect(() => {
    const api = gridApiRef.current;
    if (!api) return;
    api.forEachNode((node) => {
      const shouldSelect = selectedIds.has(node.id);
      if (node.isSelected() !== shouldSelect) {
        node.setSelected(shouldSelect);
      }
    });
  }, [rows, selectedIds]);

  const anySelected = selectedIds.size > 0;

  const handleSelectionChanged = useCallback(() => {
    const api = gridApiRef.current;
    if (!api) return;
    const selectedNodes = api.getSelectedNodes();
    const next = new Set();
    selectedNodes.forEach((node) => {
      if (node?.id != null) {
        next.add(String(node.id));
      }
    });
    setSelectedIds(next);
  }, []);

  const getRowId = useCallback((params) => {
    return (
      params?.data?.id ??
      params?.data?.name ??
      String(params?.rowIndex ?? "")
    );
  }, []);

  const nameCellRenderer = useCallback(
    (params) => {
      const row = params.data;
      if (!row) return null;
      const handleClick = (event) => {
        event.preventDefault();
        event.stopPropagation();
        if (typeof onEditJob === "function") {
          onEditJob(row.raw);
        }
      };
      return (
        <Button
          onClick={handleClick}
          sx={{
            color: "#58a6ff",
            textTransform: "none",
            p: 0,
            minWidth: 0,
            fontFamily: gridFontFamily
          }}
        >
          {row.name || "-"}
        </Button>
      );
    },
    [onEditJob]
  );

  const resultsCellRenderer = useCallback((params) => {
    return <ResultsBar counts={params?.data?.resultsCounts} />;
  }, []);

  const enabledCellRenderer = useCallback(
    (params) => {
      const row = params.data;
      if (!row) return null;
      const handleToggle = async (event) => {
        event.stopPropagation();
        const nextEnabled = event.target.checked;
        try {
          await fetch(`/api/scheduled_jobs/${row.id}/toggle`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ enabled: nextEnabled })
          });
        } catch {
          // ignore network errors for toggle
        }
        setRows((prev) =>
          prev.map((job) => {
            if ((job.id ?? job.name) === (row.id ?? row.name)) {
              const updatedRaw = { ...(job.raw || {}), enabled: nextEnabled };
              return { ...job, enabled: nextEnabled, raw: updatedRaw };
            }
            return job;
          })
        );
      };
      return (
        <Switch
          size="small"
          checked={Boolean(row.enabled)}
          onChange={handleToggle}
          onClick={(event) => event.stopPropagation()}
          sx={{
            "& .MuiSwitch-switchBase.Mui-checked": {
              color: "#58a6ff"
            },
            "& .MuiSwitch-switchBase.Mui-checked + .MuiSwitch-track": {
              bgcolor: "#58a6ff"
            }
          }}
        />
      );
    },
    []
  );

  const columnDefs = useMemo(
    () => [
      {
        headerName: "",
        field: "__checkbox__",
        checkboxSelection: true,
        headerCheckboxSelection: true,
        maxWidth: 60,
        minWidth: 60,
        sortable: false,
        filter: false,
        resizable: false,
        suppressMenu: true,
        pinned: false
      },
      {
        headerName: "Name",
        field: "name",
        cellRenderer: nameCellRenderer,
        sort: "asc"
      },
      {
        headerName: "Assembly(s)",
        field: "scriptWorkflow",
        valueGetter: (params) => params.data?.scriptWorkflow || "Demonstration Component"
      },
      {
        headerName: "Target",
        field: "target"
      },
      {
        headerName: "Recurrence",
        field: "occurrence"
      },
      {
        headerName: "Last Run",
        field: "lastRun"
      },
      {
        headerName: "Next Run",
        field: "nextRun"
      },
      {
        headerName: "Results",
        field: "resultsCounts",
        minWidth: 280,
        cellRenderer: resultsCellRenderer,
        sortable: false,
        filter: false
      },
      {
        headerName: "Enabled",
        field: "enabled",
        minWidth: 140,
        maxWidth: 160,
        cellRenderer: enabledCellRenderer,
        sortable: false,
        filter: false,
        resizable: false,
        suppressMenu: true
      }
    ],
    [enabledCellRenderer, nameCellRenderer, resultsCellRenderer]
  );

  const defaultColDef = useMemo(
    () => ({
      sortable: true,
      filter: "agTextColumnFilter",
      resizable: true,
      flex: 1,
      minWidth: 140,
      cellStyle: {
        display: "flex",
        alignItems: "center",
        color: "#f5f7fa",
        fontFamily: gridFontFamily,
        fontSize: "13px"
      },
      headerClass: "scheduled-jobs-grid-header"
    }),
    []
  );

  return (
    <Paper
      sx={{
        m: 2,
        p: 0,
        bgcolor: "#1e1e1e",
        color: "#f5f7fa",
        fontFamily: gridFontFamily,
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minWidth: 0,
        minHeight: 420
      }}
      elevation={2}
    >
      <Box
        sx={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          p: 2,
          borderBottom: "1px solid #2a2a2a"
        }}
      >
        <Box>
          <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0.3 }}>
            Scheduled Jobs
          </Typography>
          <Typography variant="body2" sx={{ color: "#aaa" }}>
            List of automation jobs with schedules, results, and actions.
          </Typography>
        </Box>
        <Box sx={{ display: "flex", gap: 1, alignItems: "center" }}>
          <Button
            variant="outlined"
            size="small"
            disabled={!anySelected}
            sx={{
              color: anySelected ? "#ff8080" : "#666",
              borderColor: anySelected ? "#ff8080" : "#333",
              textTransform: "none",
              fontFamily: gridFontFamily,
              "&:hover": {
                borderColor: anySelected ? "#ff8080" : "#333"
              }
            }}
            onClick={() => setBulkDeleteOpen(true)}
          >
            Delete Job
          </Button>
          <Button
            variant="contained"
            size="small"
            sx={{
              bgcolor: "#58a6ff",
              color: "#0b0f19",
              textTransform: "none",
              fontFamily: gridFontFamily,
              "&:hover": {
                bgcolor: "#7db7ff"
              }
            }}
            onClick={() => onCreateJob && onCreateJob()}
          >
            Create Job
          </Button>
        </Box>
      </Box>

      {loading && (
        <Box
          sx={{
            display: "flex",
            alignItems: "center",
            gap: 1,
            color: "#7db7ff",
            px: 2,
            py: 1.5,
            borderBottom: "1px solid #2a2a2a"
          }}
        >
          <CircularProgress size={18} sx={{ color: "#58a6ff" }} />
          <Typography variant="body2">Loading scheduled jobs…</Typography>
        </Box>
      )}

      {error && (
        <Box sx={{ px: 2, py: 1.5, color: "#ff8080", borderBottom: "1px solid #2a2a2a" }}>
          <Typography variant="body2">{error}</Typography>
        </Box>
      )}

      <Box
        sx={{
          flexGrow: 1,
          minHeight: 0,
          display: "flex",
          flexDirection: "column",
          mt: "10px",
          px: 2,
          pb: 2
        }}
      >
        <Box
          className={themeClassName}
          sx={{
            width: "100%",
            height: "100%",
            flexGrow: 1,
            fontFamily: gridFontFamily,
            "--ag-font-family": gridFontFamily,
            "--ag-icon-font-family": iconFontFamily,
            "--ag-row-border-style": "solid",
            "--ag-row-border-color": "#2a2a2a",
            "--ag-row-border-width": "1px",
            "& .ag-root-wrapper": {
              borderRadius: 1,
              minHeight: 320
            },
            "& .ag-root, & .ag-header, & .ag-center-cols-container, & .ag-paging-panel": {
              fontFamily: gridFontFamily
            },
            "& .ag-icon": {
              fontFamily: iconFontFamily
            },
            "& .scheduled-jobs-grid-header": {
              fontFamily: gridFontFamily,
              fontWeight: 600,
              color: "#f5f7fa"
            }
          }}
        >
          <AgGridReact
            rowData={rows}
            columnDefs={columnDefs}
            defaultColDef={defaultColDef}
            animateRows
            rowHeight={46}
            headerHeight={44}
            suppressCellFocus
            rowSelection="multiple"
            rowMultiSelectWithClick
            suppressRowClickSelection
            getRowId={getRowId}
            overlayNoRowsTemplate="<span class='ag-overlay-no-rows-center'>No scheduled jobs found.</span>"
            onGridReady={handleGridReady}
            onSelectionChanged={handleSelectionChanged}
            theme={myTheme}
            style={{
              width: "100%",
              height: "100%",
              fontFamily: gridFontFamily,
              "--ag-icon-font-family": iconFontFamily
            }}
          />
        </Box>
      </Box>

      <Dialog
        open={bulkDeleteOpen}
        onClose={() => setBulkDeleteOpen(false)}
        PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}
      >
        <DialogTitle>Are you sure you want to delete this job(s)?</DialogTitle>
        <DialogActions>
          <Button onClick={() => setBulkDeleteOpen(false)} sx={{ color: "#58a6ff" }}>
            Cancel
          </Button>
          <Button
            onClick={async () => {
              try {
                const ids = Array.from(selectedIds);
                const idSet = new Set(ids);
                await Promise.allSettled(
                  ids.map((id) => fetch(`/api/scheduled_jobs/${id}`, { method: "DELETE" }))
                );
                setRows((prev) =>
                  prev.filter((job, index) => {
                    const key = getRowId({ data: job, rowIndex: index });
                    return !idSet.has(key);
                  })
                );
                setSelectedIds(() => new Set());
              } catch {
                // ignore delete errors here; a fresh load will surface them
              }
              setBulkDeleteOpen(false);
              await loadJobs({ showLoading: true });
            }}
            variant="outlined"
            sx={{ color: "#58a6ff", borderColor: "#58a6ff" }}
          >
            Confirm
          </Button>
        </DialogActions>
      </Dialog>
    </Paper>
  );
}
