import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  Chip,
  LinearProgress,
  Stack,
  Switch,
  Tab,
  Tabs,
  Typography,
} from "@mui/material";
import {
  AddRounded as AddIcon,
  CachedRounded as RefreshIcon,
  DeleteRounded as DeleteIcon,
  Policy as HeaderIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { AllCommunityModule, ModuleRegistry } from "ag-grid-community";

import PageBodyFrame from "../../PageBodyFrame.jsx";
import { useRoutePageChrome } from "../../app/hooks/useRoutePageChrome.js";
import { APP_PATHS } from "../../app/routes/paths.js";
import {
  GRID_WRAPPER_SX,
  buildNavTabsSx,
  formatTimestamp,
  gridTheme,
  severityColor,
  siteModeSummary,
} from "./shared.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const PAGE_TITLE = "Watchdogs";
const PAGE_SUBTITLE =
  "Design device-targeted watchdog policies that evaluate Borealis inventory, explain why they matched, and launch native remediation.";

export default function WatchdogList() {
  const navigate = useNavigate();
  const gridRef = useRef(null);
  const [items, setItems] = useState([]);
  const [archivedItems, setArchivedItems] = useState([]);
  const [activeTab, setActiveTab] = useState("active");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [selectedIds, setSelectedIds] = useState([]);

  const loadItems = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [activeResp, archivedResp] = await Promise.all([
        fetch("/api/watchdogs?archived=0", { credentials: "include" }),
        fetch("/api/watchdogs?archived=1", { credentials: "include" }),
      ]);
      const activeData = await activeResp.json().catch(() => ({}));
      const archivedData = await archivedResp.json().catch(() => ({}));
      if (!activeResp.ok) {
        throw new Error(activeData?.errors?.[0] || activeData?.message || `HTTP ${activeResp.status}`);
      }
      if (!archivedResp.ok) {
        throw new Error(archivedData?.errors?.[0] || archivedData?.message || `HTTP ${archivedResp.status}`);
      }
      setItems(Array.isArray(activeData?.items) ? activeData.items : []);
      setArchivedItems(Array.isArray(archivedData?.items) ? archivedData.items : []);
    } catch (err) {
      setError(String(err?.message || err || "Failed to load watchdogs."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadItems();
  }, [loadItems]);

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    if (!socket || typeof socket.on !== "function") return undefined;
    const handler = () => loadItems();
    socket.on("watchdog_incidents_changed", handler);
    return () => {
      try {
        socket.off("watchdog_incidents_changed", handler);
      } catch {
        /* noop */
      }
    };
  }, [loadItems]);

  const rows = activeTab === "archived" ? archivedItems : items;

  const toggleEnabled = useCallback(
    async (record) => {
      if (!record?.id) return;
      try {
        const resp = await fetch(`/api/watchdogs/${encodeURIComponent(record.id)}`, {
          method: "PUT",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            ...record,
            enabled: !record.enabled,
          }),
        });
        const data = await resp.json().catch(() => ({}));
        if (!resp.ok) {
          throw new Error(data?.errors?.[0] || `HTTP ${resp.status}`);
        }
        loadItems();
      } catch (err) {
        setError(String(err?.message || err || "Failed to update watchdog."));
      }
    },
    [loadItems]
  );

  const deleteSelected = useCallback(async () => {
    if (!selectedIds.length) return;
    const confirmed = window.confirm(
      `Delete ${selectedIds.length} watchdog${selectedIds.length === 1 ? "" : "s"}?`
    );
    if (!confirmed) return;
    try {
      await Promise.all(
        selectedIds.map((watchdogId) =>
          fetch(`/api/watchdogs/${encodeURIComponent(watchdogId)}`, {
            method: "DELETE",
            credentials: "include",
          })
        )
      );
      setSelectedIds([]);
      loadItems();
    } catch (err) {
      setError(String(err?.message || err || "Failed to delete watchdogs."));
    }
  }, [loadItems, selectedIds]);

  const columnDefs = useMemo(
    () => [
      {
        checkboxSelection: true,
        headerCheckboxSelection: true,
        width: 54,
        pinned: "left",
        sortable: false,
        filter: false,
      },
      {
        field: "name",
        headerName: "Watchdog",
        minWidth: 220,
        flex: 1.2,
        cellRenderer: (params) => (
          <Button
            color="inherit"
            onClick={() => navigate(APP_PATHS.watchdog(params?.data?.id))}
            sx={{ justifyContent: "flex-start", px: 0, textTransform: "none" }}
          >
            {params.value}
          </Button>
        ),
      },
      {
        field: "description",
        headerName: "Description",
        minWidth: 220,
        flex: 1.3,
      },
      {
        field: "scope",
        headerName: "Scope",
        minWidth: 220,
        flex: 1.2,
        valueGetter: (params) => siteModeSummary(params.data),
      },
      {
        field: "target_device_count",
        headerName: "Targets",
        width: 110,
        valueFormatter: (params) => `${Number(params.value || 0)}`,
      },
      {
        field: "open_incident_count",
        headerName: "Open Alerts",
        width: 120,
        valueFormatter: (params) => `${Number(params.value || 0)}`,
      },
      {
        field: "action_summaries",
        headerName: "Actions",
        minWidth: 220,
        flex: 1.2,
        valueGetter: (params) =>
          Array.isArray(params?.data?.action_summaries) ? params.data.action_summaries.join(", ") : "",
      },
      {
        field: "last_edited_by",
        headerName: "Last Edited By",
        minWidth: 160,
        flex: 0.7,
      },
      {
        field: "updated_at",
        headerName: "Updated",
        minWidth: 170,
        valueFormatter: (params) => formatTimestamp(params.value),
      },
      {
        field: "severity",
        headerName: "Severity",
        width: 120,
        cellRenderer: (params) => (
          <Chip
            label={String(params.value || "warning").replace(/^./, (char) => char.toUpperCase())}
            size="small"
            sx={{
              color: severityColor(params.value),
              borderColor: severityColor(params.value),
              backgroundColor: "rgba(15,23,42,0.5)",
            }}
            variant="outlined"
          />
        ),
      },
      {
        field: "enabled",
        headerName: "Enabled",
        width: 110,
        sortable: false,
        filter: false,
        cellRenderer: (params) => (
          <Switch
            checked={Boolean(params.value)}
            onChange={() => toggleEnabled(params.data)}
            size="small"
          />
        ),
      },
    ],
    [navigate, toggleEnabled]
  );

  useRoutePageChrome({
    title: PAGE_TITLE,
    subtitle: PAGE_SUBTITLE,
    Icon: HeaderIcon,
  });

  return (
    <PageBodyFrame
      variant="grid_with_stack"
      stack={
        <Stack spacing={1.5}>
          <Stack direction={{ xs: "column", md: "row" }} spacing={1.5} justifyContent="space-between">
            <Tabs
              value={activeTab}
              onChange={(_event, nextValue) => setActiveTab(nextValue)}
              sx={buildNavTabsSx()}
            >
              <Tab value="active" label={`Active (${items.length})`} />
              <Tab value="archived" label={`Archived (${archivedItems.length})`} />
            </Tabs>
            <Stack direction="row" spacing={1}>
              <Button startIcon={<RefreshIcon />} onClick={loadItems} disabled={loading}>
                Refresh
              </Button>
              <Button
                color="error"
                startIcon={<DeleteIcon />}
                onClick={deleteSelected}
                disabled={!selectedIds.length}
              >
                Delete
              </Button>
              <Button
                variant="contained"
                startIcon={<AddIcon />}
                onClick={() => navigate(APP_PATHS.watchdogNew)}
              >
                New Watchdog
              </Button>
            </Stack>
          </Stack>
          {loading ? <LinearProgress /> : null}
          {error ? <Alert severity="error">{error}</Alert> : null}
          <Typography variant="body2" sx={{ color: "rgba(203, 213, 225, 0.82)" }}>
            Watchdogs live under Automation because they are policy authoring and remediation tools. Active incidents are handled separately from the dedicated Alerts queue.
          </Typography>
        </Stack>
      }
      main={
        <Box sx={GRID_WRAPPER_SX}>
          <AgGridReact
            ref={gridRef}
            theme={gridTheme}
            rowData={rows}
            columnDefs={columnDefs}
            rowSelection={{ mode: "multiRow" }}
            suppressRowClickSelection
            onSelectionChanged={(event) => {
              const selected = event.api.getSelectedRows() || [];
              setSelectedIds(selected.map((item) => item.id).filter(Boolean));
            }}
            defaultColDef={{
              sortable: true,
              filter: true,
              resizable: true,
            }}
            animateRows
            domLayout="autoHeight"
          />
        </Box>
      }
    />
  );
}
