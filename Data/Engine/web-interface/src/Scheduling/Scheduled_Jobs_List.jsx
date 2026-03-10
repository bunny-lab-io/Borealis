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
  CircularProgress,
} from "@mui/material";
import {
  Schedule as HeaderIcon,
  Cached as CachedIcon,
  Add as AddIcon
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import { DomainBadge, resolveDomainMeta } from "../Assemblies/Assembly_Badges";
import {
  buildAssemblyIndex,
  parseAssembliesCollectionPayload,
  resolveAssemblyForComponent
} from "../Assemblies/assemblyUtils";
import { logScheduledJobNav } from "./jobNavDebug.js";

// -----------------------------------------------------------------------------
//  Register AG Grid community modules
// -----------------------------------------------------------------------------
ModuleRegistry.registerModules([AllCommunityModule]);

// -----------------------------------------------------------------------------
//  MagicUI x Quartz Theme (parity with Page_Template)
// -----------------------------------------------------------------------------
const gridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});
const themeClassName = gridTheme.themeName || "ag-theme-quartz";

// Typography
const gridFontFamily = "'IBM Plex Sans','Helvetica Neue',Arial,sans-serif";
const iconFontFamily = "'Quartz Regular'";

// Aurora gradient shell colors
const AURORA_SHELL = {
  background:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), #040711",
  text: "#e2e8f0",
  subtext: "#9ba3b4",
  accent: "#7dd3fc",
};

const PAGE_TITLE = "Scheduled Jobs";
const PAGE_SUBTITLE = "Monitor scheduled, recurring, and completed Borealis jobs with live status.";
const PAGE_ICON = HeaderIcon;

const FILTER_OPTIONS = [
  { key: "all", label: "All" },
  { key: "immediate", label: "Immediate" },
  { key: "scheduled", label: "Scheduled" },
  { key: "recurring", label: "Recurring" },
  { key: "completed", label: "Completed" },
];
const AUTO_SIZE_COLUMNS = [
  "name",
  "componentsMeta",
  "target",
  "occurrence",
  "lastRun",
  "nextRun",
  "resultsCounts",
];

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
    <Box sx={{ display: "flex", flexDirection: "column", gap: 0.25, lineHeight: 1.7, fontFamily: gridFontFamily }}>
      <Box sx={{ display: "flex", borderRadius: 1, overflow: "hidden", width: 220, height: 6 }}>
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
          columnGap: 0.75,
          rowGap: 0.25,
          color: "#aaa",
          fontSize: 11,
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
              <Box key={section.key} component="span" sx={{ display: "inline-flex", alignItems: "center", gap: 0.5 }}>
                <Box component="span" sx={{ width: 6, height: 6, borderRadius: 1, backgroundColor: section.color }} />
                {counts?.[section.key]} {labelFor(section.key)}
              </Box>
            ));
        })()}
      </Box>
    </Box>
  );
}

export default function ScheduledJobsList({ onCreateJob, onEditJob, refreshToken, onPageMetaChange }) {
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [bulkDeleteOpen, setBulkDeleteOpen] = useState(false);
  const [selectedIds, setSelectedIds] = useState(() => new Set());
  const [jobFilterMode, setJobFilterMode] = useState("all");
  const [assembliesPayload, setAssembliesPayload] = useState({ items: [], queue: [] });
  const [assembliesLoading, setAssembliesLoading] = useState(false);
  const [assembliesError, setAssembliesError] = useState("");
  const gridApiRef = useRef(null);
  const sendNotification = useCallback(async (message) => {
    if (!message) return;
    try {
      await fetch("/api/notifications/notify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          title: PAGE_TITLE,
          message,
          icon: "schedule",
          variant: "info",
        }),
      });
    } catch {
      /* best-effort notification */
    }
  }, []);

  const autoSizeTrackedColumns = useCallback(() => {
    const api = gridApiRef.current;
    if (!api) return;
    const run = () => {
      try {
        api.autoSizeColumns(AUTO_SIZE_COLUMNS, true);
      } catch {
        /* ignore auto-size errors triggered during async refresh */
      }
    };
    if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
      window.requestAnimationFrame(run);
    } else {
      setTimeout(run, 0);
    }
  }, []);

  const deriveRowKey = useCallback((row, index = "") => {
    if (row && row.id != null && row.id !== "") {
      return String(row.id);
    }
    if (row && row.name) {
      return String(row.name);
    }
    if (index !== undefined && index !== null && index !== "") {
      return `__row_${index}`;
    }
    return "";
  }, []);

  const assembliesCellRenderer = useCallback((params) => {
    const list = params?.data?.componentsMeta || [];
    if (!list.length) {
      return <Typography variant="body2" sx={{ color: "#888" }}>No assemblies</Typography>;
    }
    return (
      <Box sx={{ display: "flex", flexWrap: "wrap", gap: 1 }}>
        {list.map((item) => (
          <Box key={item.key} sx={{ display: "flex", alignItems: "center", gap: 0.75 }}>
            <DomainBadge domain={item.domain} size="small" />
            <Typography variant="body2" sx={{ color: "#f5f7fa" }}>{item.label}</Typography>
          </Box>
        ))}
      </Box>
    );
  }, []);

  const loadAssemblies = useCallback(async () => {
    setAssembliesLoading(true);
    setAssembliesError("");
    try {
      const resp = await fetch("/api/assemblies");
      if (!resp.ok) {
        const detail = await resp.text();
        throw new Error(detail || `HTTP ${resp.status}`);
      }
      const data = await resp.json();
      const normalized = parseAssembliesCollectionPayload(data);
      setAssembliesPayload({
        items: normalized.items,
        queue: normalized.queue
      });
    } catch (err) {
      console.error("Failed to load assemblies:", err);
      setAssembliesPayload({ items: [], queue: [] });
      setAssembliesError(err?.message || "Failed to load assemblies");
    } finally {
      setAssembliesLoading(false);
    }
  }, []);

  const assemblyIndex = useMemo(
    () => buildAssemblyIndex(assembliesPayload.items, assembliesPayload.queue),
    [assembliesPayload.items, assembliesPayload.queue]
  );

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
          const components = Array.isArray(j.components) ? j.components : [];
          const normalizedComponents = components.map((component) => {
            const record = resolveAssemblyForComponent(assemblyIndex, component);
            const displayName =
              record?.displayName ||
              component.name ||
              component.component_name ||
              component.script_name ||
              component.script_path ||
              component.path ||
              "Assembly";
            const domainValue = (record?.domain || component.domain || "user").toLowerCase();
            const domainMeta = resolveDomainMeta(domainValue);
            const assemblyGuid =
              component.assembly_guid ||
              component.assemblyGuid ||
              record?.assemblyGuid ||
              null;
            return {
              ...component,
              assembly_guid: assemblyGuid,
              name: displayName,
              domain: domainValue,
              domainLabel: domainMeta.label,
              path: record?.path || component.path || component.script_path || component.playbook_path || ""
            };
          });
          const componentSummaries = normalizedComponents.map((comp, idx) => ({
            key: `${comp.assembly_guid || comp.path || idx}-${idx}`,
            label: comp.name,
            domain: comp.domain
          }));
          const compName =
            componentSummaries.length === 1
              ? componentSummaries[0].label
              : componentSummaries.length > 1
                ? `${componentSummaries.length} Assemblies`
                : "No Assemblies";
          const targetText = Array.isArray(j.targets)
            ? `${j.targets.length} device${j.targets.length !== 1 ? "s" : ""}`
            : "";
          const occurrence = pretty(j.schedule_type || "immediately");
          const fallbackTargetCount = Array.isArray(j.targets) ? j.targets.length : 0;
          const resultsCounts = {
            total_targets: fallbackTargetCount,
            pending: fallbackTargetCount,
            ...(j.result_counts || {})
          };
          if (resultsCounts.total_targets == null || Number.isNaN(Number(resultsCounts.total_targets))) {
            resultsCounts.total_targets = fallbackTargetCount;
          }
          const normalizeCount = (value) => {
            const num = Number(value);
            return Number.isFinite(num) ? num : 0;
          };
          const totalTargets = normalizeCount(resultsCounts.total_targets);
          const pendingCount = normalizeCount(resultsCounts.pending);
          const runningCount = normalizeCount(resultsCounts.running);
          const successCount = normalizeCount(resultsCounts.success);
          const failedCount = normalizeCount(resultsCounts.failed);
          const expiredCount = normalizeCount(resultsCounts.expired);
          const timedOutCount = normalizeCount(resultsCounts.timed_out || resultsCounts.timedOut);
          const totalFinished = successCount + failedCount + expiredCount + timedOutCount;
          const allTargetsEvaluated =
            totalTargets > 0
              ? totalFinished >= totalTargets && pendingCount === 0 && runningCount === 0
              : pendingCount === 0 && runningCount === 0;
          const everyTargetSuccessful =
            totalTargets > 0
              ? successCount >= totalTargets && pendingCount === 0 && runningCount === 0
              : pendingCount === 0 &&
                runningCount === 0 &&
                failedCount === 0 &&
                expiredCount === 0 &&
                timedOutCount === 0;
          const jobExpiredFlag =
            expiredCount > 0 || String(j.last_status || "").toLowerCase() === "expired";
          const scheduleRaw = String(j.schedule_type || "").toLowerCase();
          const isImmediateType = scheduleRaw === "immediately";
          const isScheduledType = scheduleRaw === "once";
          const showImmediate = isImmediateType && !allTargetsEvaluated;
          const showScheduled = isScheduledType && !allTargetsEvaluated;
          const canComplete = isImmediateType || isScheduledType;
          const showCompleted = canComplete && (jobExpiredFlag || everyTargetSuccessful);
          const categoryFlags = {
            immediate: showImmediate,
            scheduled: showScheduled,
            recurring: !isImmediateType && !isScheduledType,
            completed: showCompleted
          };
          return {
            id: j.id,
            name: j.name,
            scriptWorkflow: compName,
            componentsMeta: componentSummaries,
            target: targetText,
            occurrence,
            lastRun: fmt(j.last_run_ts),
            nextRun: fmt(j.next_run_ts || j.start_ts),
            result: j.last_status || (j.next_run_ts ? "Scheduled" : ""),
            resultsCounts,
            enabled: Boolean(j.enabled),
            categoryFlags,
            raw: { ...j, components: normalizedComponents }
          };
        });
        setRows(mappedRows);
        setError("");
        setSelectedIds((prev) => {
          if (!prev.size) return prev;
          const valid = new Set(
            mappedRows.map((row, index) => deriveRowKey(row, index))
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
    [assemblyIndex, deriveRowKey]
  );

  useEffect(() => {
    loadAssemblies();
  }, [loadAssemblies, refreshToken]);

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

  const handleGridReady = useCallback(
    (params) => {
      gridApiRef.current = params.api;
      autoSizeTrackedColumns();
    },
    [autoSizeTrackedColumns]
  );

  const filterCounts = useMemo(() => {
    const totals = { all: rows.length, immediate: 0, scheduled: 0, recurring: 0, completed: 0 };
    rows.forEach((row) => {
      if (row?.categoryFlags?.immediate) totals.immediate += 1;
      if (row?.categoryFlags?.scheduled) totals.scheduled += 1;
      if (row?.categoryFlags?.recurring) totals.recurring += 1;
      if (row?.categoryFlags?.completed) totals.completed += 1;
    });
    return totals;
  }, [rows]);

  const filteredRows = useMemo(() => {
    if (jobFilterMode === "all") return rows;
    return rows.filter((row) => row?.categoryFlags?.[jobFilterMode]);
  }, [rows, jobFilterMode]);
  const activeFilterLabel = useMemo(() => {
    const match = FILTER_OPTIONS.find((option) => option.key === jobFilterMode);
    return match ? match.label : jobFilterMode;
  }, [jobFilterMode]);

  useEffect(() => {
    const api = gridApiRef.current;
    if (!api) return;
    if (loading) {
      api.showLoadingOverlay();
    } else if (!filteredRows.length) {
      api.showNoRowsOverlay();
    } else {
      api.hideOverlay();
    }
  }, [loading, filteredRows]);

  useEffect(() => {
    const api = gridApiRef.current;
    if (!api) return;
    api.forEachNode((node) => {
      const nodeKey = deriveRowKey(node?.data, node?.rowIndex);
      const shouldSelect = nodeKey && selectedIds.has(nodeKey);
      if (node.isSelected() !== shouldSelect) {
        node.setSelected(shouldSelect);
      }
    });
  }, [deriveRowKey, filteredRows, selectedIds]);

  const anySelected = selectedIds.size > 0;
  useEffect(() => {
    if (!filteredRows.length || loading) return;
    autoSizeTrackedColumns();
  }, [filteredRows, loading, autoSizeTrackedColumns]);

  const handleSelectionChanged = useCallback(() => {
    const api = gridApiRef.current;
    if (!api) return;
    const selectedNodes = api.getSelectedNodes();
    const next = new Set();
    selectedNodes.forEach((node) => {
      const nodeKey = deriveRowKey(node?.data, node?.rowIndex);
      if (nodeKey) {
        next.add(nodeKey);
      }
    });
    setSelectedIds(next);
  }, [deriveRowKey]);

  const getRowId = useCallback(
    (params) => deriveRowKey(params?.data, params?.rowIndex),
    [deriveRowKey]
  );

  const nameCellRenderer = useCallback(
    (params) => {
      const row = params.data;
      if (!row) return null;
      const handleClick = (event) => {
        event.preventDefault();
        event.stopPropagation();
        logScheduledJobNav("ScheduledJobsList", "name_click", {
          rowId: row?.id ?? null,
          rawJobId: row?.raw?.id ?? null,
          jobName: row?.name || "",
          eventType: event?.type || "",
        });
        if (typeof onEditJob === "function") {
          onEditJob(row.raw);
          logScheduledJobNav("ScheduledJobsList", "name_click_dispatched", {
            rawJobId: row?.raw?.id ?? null,
            jobName: row?.name || "",
          });
        } else {
          logScheduledJobNav("ScheduledJobsList", "name_click_missing_handler", {
            rawJobId: row?.raw?.id ?? null,
            jobName: row?.name || "",
          });
        }
      };
      return (
        <a
          href="#"
          onClick={handleClick}
          title={row.name || ""}
          style={{
            color: "#58a6ff",
            textDecoration: "none",
            fontWeight: 500,
            cursor: "pointer",
            fontFamily: gridFontFamily
          }}
        >
          {row.name || "-"}
        </a>
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
            "& .MuiSwitch-switchBase.Mui-checked": { color: "#58a6ff" },
            "& .MuiSwitch-switchBase.Mui-checked + .MuiSwitch-track": { bgcolor: "#58a6ff" }
          }}
        />
      );
    },
    []
  );

  // Selection column parity (square, centered, pinned left)
  const selectionCol = {
    headerName: "",
    field: "__select__",
    width: 52,
    maxWidth: 52,
    checkboxSelection: true,
    headerCheckboxSelection: true,
    resizable: false,
    sortable: false,
    suppressMenu: true,
    filter: false,
    pinned: "left",
    lockPosition: true,
  };

  const columnDefs = useMemo(
    () => [
      selectionCol,
      {
        headerName: "Name",
        field: "name",
        cellRenderer: nameCellRenderer,
        sort: "asc",
        minWidth: 150,
        cellClass: "auto-col-tight",
      },
      {
        headerName: "Assembly(s)",
        field: "componentsMeta",
        minWidth: 180,
        cellRenderer: assembliesCellRenderer,
        cellClass: "auto-col-tight",
      },
      { headerName: "Target", field: "target", minWidth: 140, cellClass: "auto-col-tight" },
      { headerName: "Recurrence", field: "occurrence", minWidth: 150, cellClass: "auto-col-tight" },
      { headerName: "Last Run", field: "lastRun", minWidth: 150, cellClass: "auto-col-tight" },
      { headerName: "Next Run", field: "nextRun", minWidth: 150, cellClass: "auto-col-tight" },
      {
        headerName: "Results",
        field: "resultsCounts",
        minWidth: 280,
        cellRenderer: resultsCellRenderer,
        cellClass: "auto-col-tight",
        sortable: false,
        filter: false
      },
      {
        headerName: "Enabled",
        field: "enabled",
        minWidth: 140,
        flex: 1,
        cellRenderer: enabledCellRenderer,
        cellClass: "auto-col-tight",
        sortable: false,
        filter: false,
        resizable: false,
        suppressMenu: true
      }
    ],
    [enabledCellRenderer, nameCellRenderer, resultsCellRenderer, assembliesCellRenderer]
  );

  const defaultColDef = useMemo(
    () => ({
      sortable: true,
      filter: "agTextColumnFilter",
      resizable: true,
      minWidth: 140,
      cellStyle: {
        display: "flex",
        alignItems: "center",
        justifyContent: "flex-start",
        color: "#f5f7fa",
        fontFamily: gridFontFamily,
        fontSize: "13px",
        textAlign: "left",
        paddingLeft: "8px",
        paddingRight: "6px",
      },
    }),
    []
  );

  const handleRefreshClick = useCallback(async () => {
    await loadJobs({ showLoading: true });
  }, [loadJobs]);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "scheduled-jobs-refresh",
        label: "Refresh",
        icon: <CachedIcon />,
        tone: "secondary",
        loading,
        onClick: handleRefreshClick,
      },
      {
        id: "scheduled-jobs-create",
        label: "Create Job",
        icon: <AddIcon />,
        tone: "primary",
        onClick: () => onCreateJob && onCreateJob(),
      },
    ],
    [handleRefreshClick, loading, onCreateJob]
  );

  useEffect(() => {
    onPageMetaChange?.({
      page_title: PAGE_TITLE,
      page_subtitle: PAGE_SUBTITLE,
      page_icon: PAGE_ICON,
      page_header_actions: pageHeaderActions,
    });
    return () => onPageMetaChange?.(null);
  }, [onPageMetaChange, pageHeaderActions]);

  return (
    <Paper
      sx={{
        m: 0,
        p: 0,
        background: "transparent",
        border: "none",
        boxShadow: "none",
        borderRadius: 0,
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minWidth: 0,
        minHeight: 420,
        height: "100%",
        color: AURORA_SHELL.text,
        fontFamily: gridFontFamily,
      }}
      elevation={0}
    >
      <Box sx={{ mt: 2, px: 2, pb: 2, flexGrow: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
        <Box sx={{ display: "flex", flexWrap: "wrap", alignItems: "center", justifyContent: "space-between", gap: 1.5, mb: 2, px: 0.5 }}>
          <Box
            sx={{
              display: "inline-flex",
              alignItems: "center",
              gap: 0.75,
              background: "linear-gradient(120deg, rgba(8,12,24,0.92), rgba(4,7,17,0.85))",
              borderRadius: 999,
              border: "1px solid rgba(148,163,184,0.35)",
              boxShadow: "0 18px 48px rgba(2,8,23,0.45)",
              padding: "4px"
            }}
          >
            {FILTER_OPTIONS.map((option) => {
              const active = jobFilterMode === option.key;
              return (
                <Box
                  key={option.key}
                  component="button"
                  type="button"
                  onClick={() => setJobFilterMode(option.key)}
                  sx={{
                    border: "none",
                    outline: "none",
                    background: active ? "linear-gradient(135deg,#7dd3fc,#c084fc)" : "transparent",
                    color: active ? "#041224" : "#cbd5e1",
                    fontWeight: 600,
                    fontSize: 13,
                    px: 2,
                    py: 0.5,
                    borderRadius: 999,
                    cursor: "pointer",
                    display: "inline-flex",
                    alignItems: "center",
                    gap: 0.6,
                    boxShadow: active ? "0 0 18px rgba(125,211,252,0.35)" : "none",
                    transition: "all 0.2s ease",
                  }}
                >
                  <Box component="span" sx={{ userSelect: "none" }}>{option.label}</Box>
                  <Box
                    component="span"
                    sx={{
                      minWidth: 28,
                      textAlign: "center",
                      borderRadius: 999,
                      fontSize: 12,
                      fontWeight: 600,
                      px: 0.75,
                      py: 0.1,
                      color: active ? "#041224" : "#94a3b8",
                      backgroundColor: active ? "rgba(4,18,36,0.2)" : "rgba(15,23,42,0.65)",
                      border: active ? "1px solid rgba(4,18,36,0.3)" : "1px solid rgba(148,163,184,0.3)"
                    }}
                  >
                    {filterCounts[option.key] ?? 0}
                  </Box>
                </Box>
              );
            })}
          </Box>
          <Typography variant="body2" sx={{ color: AURORA_SHELL.subtext }}>
            {jobFilterMode === "all"
              ? `Showing ${filterCounts.all || 0} jobs`
              : `Showing ${filterCounts[jobFilterMode] || 0} ${activeFilterLabel} job${(filterCounts[jobFilterMode] || 0) === 1 ? "" : "s"}`}
          </Typography>
        </Box>

        <Box
          className={themeClassName}
          sx={{
            background: "transparent",
            border: "none",
            p: 0,
            width: "100%",
            flexGrow: 1,
            minHeight: 0,
            height: "100%",
            position: "relative",
            fontFamily: gridFontFamily,
            "--ag-font-family": gridFontFamily,
            "--ag-icon-font-family": iconFontFamily,

            "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
              display: "flex",
              alignItems: "center",
              justifyContent: "flex-start",
              textAlign: "left",
              paddingTop: "8px",
              paddingBottom: "8px",
              paddingLeft: "18px",
              paddingRight: "12px",
              gap: 0,
            },
            "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
              width: "100%",
              display: "flex",
              alignItems: "center",
              justifyContent: "flex-start",
              gap: 0,
              paddingTop: 0,
              paddingBottom: 0,
            },
            "& .ag-center-cols-container .ag-cell .ag-cell-value, & .ag-pinned-left-cols-container .ag-cell .ag-cell-value, & .ag-pinned-right-cols-container .ag-cell .ag-cell-value": {
              width: "100%",
              display: "flex",
              alignItems: "center",
              justifyContent: "flex-start",
              textAlign: "left",
            },

            /* Center the selection column (header + body) */
            "& .ag-header .ag-header-select-all, & .ag-header .ag-checkbox-input-wrapper": {
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
            },
            "& .ag-cell.ag-selection-centered": {
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              paddingLeft: 0,
              paddingRight: 0,
            },
            "& .ag-cell.ag-selection-centered .ag-cell-wrapper": {
              gap: "0 !important",
              width: "100%",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              paddingTop: 0,
              paddingBottom: 0,
            },
            "& .ag-cell.ag-selection-centered .ag-selection-checkbox, & .ag-cell.ag-selection-centered .ag-checkbox-input-wrapper": {
              margin: "0 auto",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
            },
            "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
              paddingLeft: "12px",
              paddingRight: "9px",
              justifyContent: "flex-start !important",
              textAlign: "left !important",
            },
            "& .ag-center-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper": {
              justifyContent: "flex-start !important",
            },
            "& .ag-center-cols-container .ag-cell.auto-col-tight .ag-cell-value, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight .ag-cell-value, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight .ag-cell-value": {
              textAlign: "left !important",
              justifyContent: "flex-start !important",
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
            "--ag-border-color": "rgba(125,183,255,0.18)",
            "--ag-row-border-color": "rgba(125,183,255,0.14)",
            "--ag-border-radius": "8px",
            "--ag-checkbox-border-radius": "3px",
            "--ag-checkbox-background-color": "rgba(255,255,255,0.06)",
            "--ag-checkbox-border-color": "rgba(180,200,220,0.6)",
            "--ag-checkbox-checked-color": "#7dd3fc",
            "--ag-cell-horizontal-padding": "18px",
          }}
        >
          {/* Action bar for bulk delete stays above grid when needed */}
          {anySelected && (
            <Box sx={{ display: "flex", alignItems: "center", gap: 1, color: "#7db7ff", px: 1.5, py: 1 }}>
              <Button
                variant="outlined"
                size="small"
                sx={{
                  color: "#ff8080",
                  borderColor: "rgba(255,128,128,0.5)",
                  textTransform: "none",
                  borderRadius: 999,
                  "&:hover": { borderColor: "#ff8080" },
                }}
                onClick={() => setBulkDeleteOpen(true)}
              >
                Delete Job
              </Button>
            </Box>
          )}

          <AgGridReact
            rowData={filteredRows}
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
            theme={gridTheme}
            style={{ width: "100%", height: "100%", fontFamily: gridFontFamily, "--ag-icon-font-family": iconFontFamily }}
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
                const selectedJobs = rows.filter((job, index) => {
                  const key = getRowId({ data: job, rowIndex: index });
                  return idSet.has(key);
                });
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
                selectedJobs
                  .map((job) => job?.name)
                  .filter(Boolean)
                  .forEach((name) => sendNotification(`Job ${name} Deleted Successfully`));
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
