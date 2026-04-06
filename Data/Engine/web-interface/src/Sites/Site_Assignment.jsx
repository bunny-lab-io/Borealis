import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  Alert,
  Box,
  Chip,
  Paper,
  Stack,
  Typography,
} from "@mui/material";
import {
  ArrowBack as ArrowBackIcon,
  AssignmentTurnedIn as AssignIcon,
  LocationCity as LocationCityIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { APP_PATHS } from "../app/routes/paths.js";

ModuleRegistry.registerModules([AllCommunityModule]);

const PAGE_TITLE = "Site Assignment";
const PAGE_SUBTITLE = "Manage the sites that operators can access and manage.";
const AUTO_SIZE_COLUMNS = ["name", "device_count", "enrollment_code"];

const gridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});
const themeClassName = gridTheme.themeName || "ag-theme-quartz";
const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';

export default function SiteAssignment() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [users, setUsers] = useState([]);
  const [sites, setSites] = useState([]);
  const [existingAssignments, setExistingAssignments] = useState({});
  const [selectedSiteIds, setSelectedSiteIds] = useState(() => new Set());
  const [warning, setWarning] = useState("");
  const [loading, setLoading] = useState(false);
  const [assigning, setAssigning] = useState(false);
  const [error, setError] = useState("");
  const gridRef = useRef(null);
  const gridApiRef = useRef(null);

  const normalizedUsernames = useMemo(
    () => searchParams.getAll("user").map((value) => String(value || "").trim()).filter(Boolean),
    [searchParams]
  );

  const autoSizeColumns = useCallback(() => {
    const api = gridApiRef.current || gridRef.current?.api;
    if (!api || !sites.length) return;
    const doSize = () => {
      try {
        api.autoSizeColumns(AUTO_SIZE_COLUMNS, true);
      } catch {}
    };
    if (typeof requestAnimationFrame === "function") {
      requestAnimationFrame(doSize);
    } else {
      setTimeout(doSize, 0);
    }
  }, [sites.length]);

  const loadSelection = useCallback(async () => {
    if (!normalizedUsernames.length) {
      setUsers([]);
      setSites([]);
      setExistingAssignments({});
      setSelectedSiteIds(new Set());
      setWarning("");
      return;
    }
    setLoading(true);
    setError("");
    try {
      const resp = await fetch("/api/user_site_assignments/selection", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ usernames: normalizedUsernames }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.message || data?.error || "Unable to load site assignment state.");
      }
      setUsers(Array.isArray(data.users) ? data.users : []);
      setSites(Array.isArray(data.sites) ? data.sites : []);
      setExistingAssignments(data.existing_assignments && typeof data.existing_assignments === "object" ? data.existing_assignments : {});
      setWarning(data.warning || "");
      setSelectedSiteIds(
        new Set(
          Array.isArray(data.selected_site_ids)
            ? data.selected_site_ids.map((value) => Number(value)).filter((value) => Number.isFinite(value))
            : []
        )
      );
    } catch (err) {
      setError(err?.message || "Unable to load site assignment state.");
      setUsers([]);
      setSites([]);
      setExistingAssignments({});
      setSelectedSiteIds(new Set());
      setWarning("");
    } finally {
      setLoading(false);
    }
  }, [normalizedUsernames]);

  useEffect(() => {
    loadSelection();
  }, [loadSelection]);

  useEffect(() => {
    autoSizeColumns();
  }, [sites, autoSizeColumns]);

  useEffect(() => {
    const api = gridApiRef.current || gridRef.current?.api;
    if (!api) return;
    api.forEachNode((node) => {
      const id = Number(node.data?.id);
      node.setSelected(Number.isFinite(id) && selectedSiteIds.has(id));
    });
  }, [sites, selectedSiteIds]);

  const sendNotification = useAppNotifications({
    title: PAGE_TITLE,
    icon: "locationcity",
    variant: "info",
  });

  const handleAssign = useCallback(async () => {
    if (!users.length) return;
    setAssigning(true);
    setError("");
    try {
      const resp = await fetch("/api/user_site_assignments/assign", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          usernames: users.map((user) => user.username),
          site_ids: Array.from(selectedSiteIds),
        }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.message || data?.error || "Unable to assign sites.");
      }
      const label =
        users.length === 1
          ? `Site assignments updated for ${users[0].username}`
          : `Site assignments updated for ${users.length} users`;
      sendNotification(label);
      navigate(APP_PATHS.users);
    } catch (err) {
      setError(err?.message || "Unable to assign sites.");
    } finally {
      setAssigning(false);
    }
  }, [navigate, selectedSiteIds, sendNotification, users]);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "site-assignment-back",
        label: "Back to Users",
        icon: <ArrowBackIcon />,
        tone: "secondary",
        onClick: () => navigate(APP_PATHS.users),
      },
      {
        id: "site-assignment-assign",
        label: "Assign",
        icon: <AssignIcon />,
        tone: "primary",
        disabled: !users.length || loading || assigning,
        loading: assigning,
        onClick: handleAssign,
      },
    ],
    [assigning, handleAssign, loading, navigate, users.length]
  );

  useRoutePageChrome({
    title: PAGE_TITLE,
    subtitle: PAGE_SUBTITLE,
    Icon: LocationCityIcon,
    actions: pageHeaderActions,
  });

  const rowSelection = useMemo(
    () => ({
      mode: "multiRow",
      checkboxes: true,
      headerCheckbox: true,
      enableSelectionWithoutKeys: true,
      enableClickSelection: false,
    }),
    []
  );

  const selectionColumnDef = useMemo(
    () => ({
      headerName: "",
      minWidth: 52,
      width: 52,
      maxWidth: 52,
      pinned: "left",
      filter: false,
      sortable: false,
      resizable: false,
      suppressHeaderMenuButton: true,
      suppressHeaderContextMenu: true,
      suppressMovable: true,
      lockPinned: true,
      lockPosition: true,
    }),
    []
  );

  const columnDefs = useMemo(
    () => [
      {
        headerName: "Name",
        field: "name",
        minWidth: 240,
        flex: 1,
        cellClass: "auto-col-tight",
      },
      {
        headerName: "Description",
        field: "description",
        minWidth: 260,
        flex: 1,
      },
      {
        headerName: "Devices",
        field: "device_count",
        minWidth: 140,
        cellClass: "auto-col-tight",
      },
      {
        headerName: "Agent Enrollment Code",
        field: "enrollment_code",
        minWidth: 240,
        cellClass: "auto-col-tight",
      },
    ],
    []
  );

  const defaultColDef = useMemo(
    () => ({
      sortable: true,
      filter: "agTextColumnFilter",
      resizable: true,
      minWidth: 140,
    }),
    []
  );

  const selectedCount = selectedSiteIds.size;

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
      <PageBodyFrame
        variant="grid_with_stack"
        stack={
          <Stack spacing={1.5}>
            {warning ? <Alert severity="warning" variant="outlined">{warning}</Alert> : null}
            {error ? <Alert severity="error" variant="outlined">{error}</Alert> : null}
            <Box
              sx={{
                borderRadius: 2.5,
                border: "1px solid rgba(148,163,184,0.26)",
                background: "rgba(8,12,24,0.72)",
                px: 2,
                py: 1.75,
              }}
            >
              <Typography sx={{ color: "#e2e8f0", fontWeight: 700, fontSize: "0.95rem" }}>
                Selected Users
              </Typography>
              <Stack spacing={1.1} sx={{ mt: 1.25 }}>
                {users.length ? (
                  users.map((user) => {
                    const assignedSites = Array.isArray(existingAssignments?.[user.username]) ? existingAssignments[user.username] : [];
                    return (
                      <Box
                        key={user.username}
                        sx={{
                          borderRadius: 2,
                          border: "1px solid rgba(148,163,184,0.22)",
                          background: "rgba(15,23,42,0.48)",
                          px: 1.4,
                          py: 1.1,
                        }}
                      >
                        <Typography sx={{ color: "#f4f7ff", fontWeight: 600, fontSize: "0.92rem" }}>
                          {user.display_name || user.username}
                        </Typography>
                        <Typography sx={{ color: "#94a3b8", fontSize: "0.82rem", mt: 0.2 }}>
                          {user.username}
                        </Typography>
                        <Box sx={{ mt: 0.9, display: "flex", flexWrap: "wrap", gap: 0.8 }}>
                          {assignedSites.length ? (
                            assignedSites.map((site) => (
                              <Chip
                                key={`${user.username}-${site.id}`}
                                label={site.name}
                                size="small"
                                sx={{
                                  color: "#dbe8ff",
                                  borderColor: "rgba(125,211,252,0.32)",
                                  background: "rgba(16,37,58,0.7)",
                                }}
                                variant="outlined"
                              />
                            ))
                          ) : (
                            <Typography sx={{ color: "#7c8aa5", fontSize: "0.82rem" }}>
                              No sites assigned
                            </Typography>
                          )}
                        </Box>
                      </Box>
                    );
                  })
                ) : (
                  <Typography sx={{ color: "#94a3b8", fontSize: "0.88rem" }}>
                    Select one or more users from User Management to assign sites.
                  </Typography>
                )}
              </Stack>
            </Box>
          </Stack>
        }
      >
        <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, minHeight: 0 }}>
          {selectedCount > 0 ? (
            <Box sx={{ display: "flex", justifyContent: "flex-end", mb: 1.25, px: 3, pt: 1.5 }}>
              <Typography variant="body2" sx={{ color: "#94a3b8", fontWeight: 600 }}>
                {selectedCount} sites selected
              </Typography>
            </Box>
          ) : null}
          <Box
            className={themeClassName}
            sx={{
              flexGrow: 1,
              minHeight: 0,
              "--ag-font-family": gridFontFamily,
              "--ag-icon-font-family": iconFontFamily,
              "--ag-checkbox-border-radius": "3px",
              "& .ag-root-wrapper": {
                minHeight: "100%",
                border: "none",
                borderRadius: 0,
                background: "transparent",
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
              "& .ag-row-selected": {
                backgroundColor: "rgba(125,211,252,0.2) !important",
                boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
              },
            }}
          >
            <AgGridReact
              ref={gridRef}
              rowData={sites}
              columnDefs={columnDefs}
              defaultColDef={defaultColDef}
              rowSelection={rowSelection}
              selectionColumnDef={selectionColumnDef}
              suppressCellFocus
              pagination
              paginationPageSize={20}
              paginationPageSizeSelector={[20, 50, 100]}
              animateRows
              getRowId={(params) => String(params.data?.id || "")}
              onGridReady={(params) => {
                gridApiRef.current = params.api;
                autoSizeColumns();
              }}
              onSelectionChanged={() => {
                const api = gridApiRef.current || gridRef.current?.api;
                if (!api) return;
                const selected = api
                  .getSelectedNodes()
                  .map((node) => Number(node.data?.id))
                  .filter((value) => Number.isFinite(value));
                setSelectedSiteIds(new Set(selected));
              }}
              theme={gridTheme}
            />
          </Box>
        </Box>
      </PageBodyFrame>
    </Paper>
  );
}
