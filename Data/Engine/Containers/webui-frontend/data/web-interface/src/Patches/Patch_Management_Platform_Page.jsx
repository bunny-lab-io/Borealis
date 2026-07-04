import React, { useCallback, useMemo, useState } from "react";
import {
  Alert,
  Box,
  Checkbox,
  Menu,
  MenuItem,
  Stack,
  Tab,
  Tabs,
  Typography,
} from "@mui/material";
import AddRoundedIcon from "@mui/icons-material/AddRounded";
import SystemUpdateAltRoundedIcon from "@mui/icons-material/SystemUpdateAltRounded";
import { AgGridReact } from "ag-grid-react";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { BOREALIS_BLUE, CountSliderGroup, buildNavTabsSx } from "../Automation/Watchdogs/shared.jsx";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import {
  DEFAULT_GRID_COL_DEF,
  DEVICE_DETAILS_GRID_THEME,
  GridShell,
  MAGIC_UI,
} from "../Devices/Tabs/Shared.jsx";
import { PATCH_PAGE_TABS } from "./Patch_Management.jsx";

const STATE_FILTER_OPTIONS = [
  { key: "pending", label: "Pending" },
  { key: "installed", label: "Installed" },
];

const SEVERITY_FILTER_OPTIONS = [
  { key: "critical", label: "Critical" },
  { key: "important", label: "Important" },
  { key: "moderate", label: "Moderate" },
  { key: "low", label: "Low" },
  { key: "unspecified", label: "Unspecified" },
];

const FILTER_LABEL_SX = {
  color: BOREALIS_BLUE,
  fontSize: 11,
  fontWeight: 600,
  lineHeight: 1.1,
  pl: 1,
};

function platformCopy(platform) {
  const normalized = String(platform || "").toLowerCase();
  if (normalized === "macos") {
    return {
      title: "MacOS Patch Management",
      subtitle: "macOS patch inventory and policies are planned for a future Borealis release.",
      label: "MacOS",
    };
  }
  return {
    title: "Linux Patch Management",
    subtitle: "Linux patch inventory and policies are planned for a future Borealis release.",
    label: "Linux",
  };
}

function FilterSliderBlock({ label = "", children }) {
  return (
    <Box sx={{ display: "flex", flexDirection: "column", alignItems: "flex-start", gap: "8px" }}>
      <Typography component="span" sx={FILTER_LABEL_SX}>
        {label}
      </Typography>
      {children}
    </Box>
  );
}

export default function PatchManagementPlatformPage({ platform = "linux" }) {
  const copy = platformCopy(platform);
  const notifyOperator = useAppNotifications({
    title: copy.title,
    icon: "pendingactions",
    variant: "info",
  });
  const [activeTab, setActiveTab] = useState("patch_list");
  const [stateFilter, setStateFilter] = useState("pending");
  const [severityFilter, setSeverityFilter] = useState("");
  const [newPolicyMenuAnchor, setNewPolicyMenuAnchor] = useState(null);

  const notifyNotImplemented = useCallback(() => {
    void notifyOperator("Feature not implemented yet.");
  }, [notifyOperator]);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: `${platform}-patch-bulk-install`,
        label: "Bulk Install",
        icon: <SystemUpdateAltRoundedIcon />,
        tone: "secondary",
        onClick: notifyNotImplemented,
      },
      {
        id: `${platform}-patch-new-policy`,
        label: "New Policy",
        icon: <AddRoundedIcon />,
        tone: "primary",
        onClick: (event) => {
          setNewPolicyMenuAnchor(event.currentTarget);
          notifyNotImplemented();
        },
      },
    ],
    [notifyNotImplemented, platform]
  );

  useRoutePageChrome({
    title: copy.title,
    subtitle: copy.subtitle,
    Icon: SystemUpdateAltRoundedIcon,
    actions: pageHeaderActions,
  });

  const patchColumnDefs = useMemo(
    () => [
      {
        colId: "select",
        headerName: "",
        width: 52,
        minWidth: 52,
        maxWidth: 52,
        sortable: false,
        filter: false,
        resizable: false,
        cellRenderer: () => (
          <Checkbox
            size="small"
            disabled
            sx={{
              p: 0.2,
              color: "rgba(148,163,184,0.65)",
              "&.Mui-disabled": { color: "rgba(71,85,105,0.5)" },
            }}
          />
        ),
      },
      { field: "kb", headerName: "KB", width: 120, minWidth: 120 },
      { field: "title", headerName: "Title", flex: 1.6, minWidth: 320 },
      { field: "state", headerName: "State", width: 135, minWidth: 135 },
      { field: "install", headerName: "Install", width: 240, minWidth: 240, sortable: false, filter: false },
      { field: "severity", headerName: "Severity", width: 150, minWidth: 135 },
      { field: "classification", headerName: "Classification", width: 190, minWidth: 170 },
      { field: "source", headerName: "Source", width: 150, minWidth: 135 },
      { field: "device_count", headerName: "Devices", width: 145, minWidth: 130 },
      { field: "captured_at", headerName: "Updated", width: 215, minWidth: 190 },
    ],
    []
  );

  const policyColumnDefs = useMemo(
    () => [
      { field: "name", headerName: "Policy", flex: 1.2, minWidth: 220 },
      { field: "enabled", headerName: "Enabled", width: 120 },
      { field: "role_scope", headerName: "Role", width: 135 },
      { field: "target_count", headerName: "Covered", width: 120 },
      { field: "deferral_days", headerName: "Deferral", width: 120 },
      { field: "install_schedule_type", headerName: "Install", width: 130 },
      { field: "install_start_ts", headerName: "Start", width: 190 },
      { field: "managed_update_mode", headerName: "Managed WU", width: 135 },
      { field: "reboot_after_install", headerName: "Reboot", width: 120 },
      { field: "actions", headerName: "Actions", width: 300, sortable: false, filter: false },
    ],
    []
  );

  const activeColumnDefs = activeTab === "patch_list" ? patchColumnDefs : policyColumnDefs;
  const zeroCounts = useMemo(
    () => ({
      pending: 0,
      installed: 0,
      critical: 0,
      important: 0,
      moderate: 0,
      low: 0,
      unspecified: 0,
    }),
    []
  );

  return (
    <>
      <Menu
        anchorEl={newPolicyMenuAnchor}
        open={Boolean(newPolicyMenuAnchor)}
        onClose={() => setNewPolicyMenuAnchor(null)}
        PaperProps={{
          sx: {
            mt: 1,
            borderRadius: 2,
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            background: "rgba(8,13,30,0.98)",
            color: MAGIC_UI.textBright,
            boxShadow: "0 24px 60px rgba(2,8,23,0.7)",
            "& .MuiMenuItem-root": {
              fontSize: "0.88rem",
              color: MAGIC_UI.textBright,
              minHeight: 34,
            },
            "& .MuiMenuItem-root:hover": {
              background: "rgba(125, 183, 255, 0.12)",
            },
          },
        }}
      >
        <MenuItem
          onClick={() => {
            setNewPolicyMenuAnchor(null);
            notifyNotImplemented();
          }}
        >
          New Site Policy
        </MenuItem>
        <MenuItem
          onClick={() => {
            setNewPolicyMenuAnchor(null);
            notifyNotImplemented();
          }}
        >
          New Device / Filter Policy
        </MenuItem>
      </Menu>
      <PageBodyFrame
        variant="grid_with_stack"
        stack={
          <Stack spacing={1.2}>
            <Tabs
              value={activeTab}
              onChange={(_, value) => {
                setActiveTab(value);
                notifyNotImplemented();
              }}
              sx={buildNavTabsSx()}
              TabIndicatorProps={{ sx: { background: "linear-gradient(90deg, #7dd3fc, #c084fc)" } }}
            >
              {PATCH_PAGE_TABS.map((tab) => (
                <Tab key={tab.key} value={tab.key} label={tab.label} />
              ))}
            </Tabs>
            <Box sx={{ display: "flex", alignItems: "flex-start", columnGap: 1, rowGap: 1, flexWrap: "wrap" }}>
              <FilterSliderBlock label="State">
                <CountSliderGroup
                  options={STATE_FILTER_OPTIONS}
                  activeKey={stateFilter}
                  counts={zeroCounts}
                  onChange={(value) => {
                    setStateFilter(value);
                    notifyNotImplemented();
                  }}
                />
              </FilterSliderBlock>
              <FilterSliderBlock label="Severity">
                <CountSliderGroup
                  options={SEVERITY_FILTER_OPTIONS}
                  activeKey={severityFilter}
                  counts={zeroCounts}
                  onChange={(value) => {
                    setSeverityFilter(value);
                    notifyNotImplemented();
                  }}
                />
              </FilterSliderBlock>
            </Box>
            <Alert
              severity="info"
              sx={{
                background: "rgba(8, 47, 73, 0.48)",
                color: MAGIC_UI.textBright,
                border: "1px solid rgba(125, 211, 252, 0.28)",
                "& .MuiAlert-icon": { color: BOREALIS_BLUE },
              }}
            >
              Coming Soon
            </Alert>
          </Stack>
        }
      >
        <GridShell
          sx={{
            flexGrow: 1,
            minHeight: 520,
            borderRadius: 0,
            border: "none",
          }}
        >
          <AgGridReact
            rowData={[]}
            columnDefs={activeColumnDefs}
            defaultColDef={DEFAULT_GRID_COL_DEF}
            rowSelection={{ mode: "singleRow", checkboxes: false, headerCheckbox: false, enableClickSelection: true }}
            suppressCellFocus
            pagination
            paginationPageSize={100}
            paginationPageSizeSelector={[20, 50, 100]}
            overlayNoRowsTemplate={`<span class="ag-overlay-no-rows-center">${copy.label} Patch Management Coming Soon</span>`}
            theme={DEVICE_DETAILS_GRID_THEME}
          />
        </GridShell>
      </PageBodyFrame>
    </>
  );
}
