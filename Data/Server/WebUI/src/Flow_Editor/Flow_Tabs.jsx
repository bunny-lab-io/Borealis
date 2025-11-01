////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Flow_Tabs.jsx

import React from "react";
import { Box, Tabs, Tab, Tooltip } from "@mui/material";
import { Add as AddIcon } from "@mui/icons-material";

/**
 * Renders the tab bar (including the "add tab" button).
 * 
 * Props:
 * - tabs (array of {id, tab_name, nodes, edges})
 * - activeTabId (string)
 * - onTabChange(newActiveTabId: string)
 * - onAddTab()
 * - onTabRightClick(evt: MouseEvent, tabId: string)
 */
export default function FlowTabs({
  tabs,
  activeTabId,
  onTabChange,
  onAddTab,
  onTabRightClick
}) {
  // Determine the currently active tab index
  const activeIndex = (() => {
    const idx = tabs.findIndex((t) => t.id === activeTabId);
    return idx >= 0 ? idx : 0;
  })();

  // Handle tab clicks
  const handleChange = (event, newValue) => {
    if (newValue === "__addtab__") {
      // The "plus" tab
      onAddTab();
    } else {
      // normal tab index
      const newTab = tabs[newValue];
      if (newTab) {
        onTabChange(newTab.id);
      }
    }
  };

  return (
    <Box
      sx={{
        display: "flex",
        alignItems: "center",
        backgroundColor: "#232323",
        borderBottom: "1px solid #333",
        height: "36px"
      }}
    >
      <Tabs
        value={activeIndex}
        onChange={handleChange}
        variant="scrollable"
        scrollButtons="auto"
        textColor="inherit"
        TabIndicatorProps={{
          style: { backgroundColor: "#58a6ff" }
        }}
        sx={{
          minHeight: "36px",
          height: "36px",
          flexGrow: 1
        }}
      >
        {tabs.map((tab, index) => (
          <Tab
            key={tab.id}
            label={tab.tab_name}
            value={index}
            onContextMenu={(evt) => onTabRightClick(evt, tab.id)}
            sx={{
              minHeight: "36px",
              height: "36px",
              textTransform: "none",
              backgroundColor: tab.id === activeTabId ? "#2C2C2C" : "transparent",
              color: "#58a6ff"
            }}
          />
        ))}
        {/* The "plus" tab has a special value */}
        <Tooltip title="Create a New Concurrent Tab" arrow>
          <Tab
            icon={<AddIcon />}
            value="__addtab__"
            sx={{
              minHeight: "36px",
              height: "36px",
              color: "#58a6ff",
              textTransform: "none"
            }}
          />
        </Tooltip>
      </Tabs>
    </Box>
  );
}
