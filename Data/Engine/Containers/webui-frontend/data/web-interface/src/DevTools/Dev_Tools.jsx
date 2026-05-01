import React, { useMemo } from "react";
import { Box, Tab, Tabs } from "@mui/material";
import {
  ConstructionRounded as ConstructionRoundedIcon,
  DashboardCustomizeRounded as DashboardCustomizeRoundedIcon,
  SpaceDashboardRounded as SpaceDashboardRoundedIcon,
  ViewSidebarRounded as ViewSidebarRoundedIcon,
} from "@mui/icons-material";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { useUrlTabState } from "../app/hooks/useUrlTabState.js";
import PageStyleTemplate, {
  CockpitLayoutTemplate,
  GridInspectorLayoutTemplate,
} from "./Page_Style_Template.jsx";

const MAGIC_UI = {
  panelBorder: "rgba(148, 163, 184, 0.32)",
};

const NAV_TAB_HEIGHT = 32;
const NAV_TAB_COLORS = {
  text: "#cbd5e1",
  textActive: "#e6f2ff",
  icon: "#8fbfff",
  iconActive: "#7db7ff",
  hover: "rgba(255,255,255,0.05)",
  activeBg:
    "linear-gradient(to top, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)",
};

const DEV_TOOL_TABS = Object.freeze([
  {
    key: "page_style_template",
    label: "Page Style Template",
    icon: DashboardCustomizeRoundedIcon,
  },
  {
    key: "grid_inspector_layout",
    label: "Grid Inspector Layout",
    icon: ViewSidebarRoundedIcon,
  },
  {
    key: "cockpit_layout",
    label: "Cockpit Layout",
    icon: SpaceDashboardRoundedIcon,
  },
]);

const DEV_TOOL_TAB_URL_BY_KEY = Object.freeze({
  page_style_template: "page_style_template",
  grid_inspector_layout: "grid_inspector_layout",
  cockpit_layout: "cockpit_layout",
});

const DEV_TOOL_TAB_KEY_BY_URL = Object.freeze({
  page_style_template: "page_style_template",
  page_template: "page_style_template",
  grid_inspector_layout: "grid_inspector_layout",
  cockpit_layout: "cockpit_layout",
});

const DEV_TOOL_TAB_KEYS = DEV_TOOL_TABS.map((tab) => tab.key);

function renderActiveTab(activeKey) {
  if (activeKey === "page_style_template") {
    return <PageStyleTemplate />;
  }
  if (activeKey === "grid_inspector_layout") {
    return <GridInspectorLayoutTemplate />;
  }
  if (activeKey === "cockpit_layout") {
    return <CockpitLayoutTemplate />;
  }
  return <PageStyleTemplate />;
}

export default function DevTools() {
  const { activeKey, setActiveKey } = useUrlTabState({
    param: "tab",
    defaultKey: "page_style_template",
    allowedKeys: DEV_TOOL_TAB_KEYS,
    keyByUrl: DEV_TOOL_TAB_KEY_BY_URL,
    urlByKey: DEV_TOOL_TAB_URL_BY_KEY,
  });

  const activeTabIndex = Math.max(
    0,
    DEV_TOOL_TABS.findIndex((tab) => tab.key === activeKey)
  );

  const headerActions = useMemo(() => [], []);

  useRoutePageChrome({
    title: "Dev Tools",
    subtitle: "Internal UI labs for page styling, table behavior, and interaction testing.",
    Icon: ConstructionRoundedIcon,
    actions: headerActions,
  });

  return (
    <PageBodyFrame
      variant="content_panel"
      contentSx={{
        p: 0,
        minHeight: 0,
        overflowY: "auto",
      }}
    >
      <Box
        sx={{
          px: 2,
          pt: 1.5,
          borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
        }}
      >
        <Tabs
          value={activeTabIndex}
          onChange={(_, nextIndex) => setActiveKey(DEV_TOOL_TABS[nextIndex]?.key || DEV_TOOL_TABS[0].key)}
          variant="scrollable"
          scrollButtons="auto"
          TabIndicatorProps={{
            style: {
              height: 3,
              borderRadius: 3,
              background: NAV_TAB_COLORS.iconActive,
            },
          }}
          sx={{
            minHeight: NAV_TAB_HEIGHT,
            height: NAV_TAB_HEIGHT,
            minWidth: 0,
            maxWidth: "100%",
            "& .MuiTabs-flexContainer": {
              minHeight: NAV_TAB_HEIGHT,
              height: NAV_TAB_HEIGHT,
              alignItems: "stretch",
            },
            "& .MuiTab-root": {
              color: NAV_TAB_COLORS.text,
              textTransform: "none",
              fontWeight: 400,
              fontFamily: "inherit",
              fontSize: "0.8rem",
              minHeight: NAV_TAB_HEIGHT,
              height: NAV_TAB_HEIGHT,
              opacity: 1,
              borderRadius: 1,
              py: 0.35,
              transition:
                "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
              "& .MuiTab-iconWrapper": {
                color: NAV_TAB_COLORS.icon,
              },
              "&:hover": {
                background: NAV_TAB_COLORS.hover,
              },
              "&:active": {
                transform: "translateY(0.5px)",
              },
            },
            "& .MuiTab-root.Mui-selected": {
              color: NAV_TAB_COLORS.textActive,
              fontWeight: 600,
              background: NAV_TAB_COLORS.activeBg,
              "& .MuiTab-iconWrapper": {
                color: NAV_TAB_COLORS.iconActive,
              },
              "&:hover": {
                background: NAV_TAB_COLORS.activeBg,
              },
            },
          }}
        >
          {DEV_TOOL_TABS.map((tab) => {
            const Icon = tab.icon;
            return (
              <Tab
                key={tab.key}
                label={tab.label}
                icon={<Icon fontSize="small" />}
                iconPosition="start"
              />
            );
          })}
        </Tabs>
      </Box>
      <Box sx={{ p: 2, minWidth: 0 }}>{renderActiveTab(activeKey)}</Box>
    </PageBodyFrame>
  );
}
