////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Navigation_Sidebar.jsx

import React, { useState } from "react";
import {
  Accordion,
  AccordionSummary,
  AccordionDetails,
  Typography,
  Box,
  ListItemButton,
  ListItemText
} from "@mui/material";
import {
  ExpandMore as ExpandMoreIcon,
  Devices as DevicesIcon,
  FilterAlt as FilterIcon,
  Groups as GroupsIcon,
  Work as JobsIcon,
  AutoAwesomeMosaic as WorkflowsIcon,
  Code as ScriptIcon,
  PeopleOutline as CommunityIcon
} from "@mui/icons-material";

function NavigationSidebar({ currentPage, onNavigate }) {
  const [expandedNav, setExpandedNav] = useState({
    devices: true,
    filters: true,
    automation: true
  });

  const NavItem = ({ icon, label, pageKey, indent = 0 }) => {
    const active = currentPage === pageKey;
    return (
      <ListItemButton
        onClick={() => onNavigate(pageKey)}
        sx={{
          pl: indent ? 4 : 2,
          py: 1,
          color: "#ccc",
          position: "relative",
          bgcolor: active ? "#2a2a2a" : "transparent",
          "&:hover": { bgcolor: "#2c2c2c" }
        }}
      >
        <Box
          sx={{
            position: "absolute",
            left: 0,
            top: 0,
            bottom: 0,
            width: active ? 3 : 0,
            bgcolor: "#58a6ff",
            transition: "width 0.15s ease"
          }}
        />
        {icon && <Box sx={{ mr: 1, display: "flex", alignItems: "center" }}>{icon}</Box>}
        <ListItemText primary={label} primaryTypographyProps={{ fontSize: "0.95rem" }} />
      </ListItemButton>
    );
  };

  return (
    <Box
      sx={{
        width: 260,
        bgcolor: "#121212",
        borderRight: "1px solid #333",
        display: "flex",
        flexDirection: "column",
        overflow: "hidden"
      }}
    >
      <Box sx={{ flex: 1, overflowY: "auto" }}>
        {/* Devices */}
        <Accordion
          expanded={expandedNav.devices}
          onChange={(_, e) => setExpandedNav((s) => ({ ...s, devices: e }))}
          square
          disableGutters
          sx={{ "&:before": { display: "none" }, margin: 0, border: 0 }}
        >
          <AccordionSummary
            expandIcon={<ExpandMoreIcon />}
            sx={{ backgroundColor: "#2c2c2c", minHeight: "36px", "& .MuiAccordionSummary-content": { margin: 0 } }}
          >
            <Typography sx={{ fontSize: "0.9rem", color: "#58a6ff" }}>
              <b>Devices</b>
            </Typography>
          </AccordionSummary>
          <AccordionDetails sx={{ p: 0, bgcolor: "#232323" }}>
            <NavItem icon={<DevicesIcon fontSize="small" />} label="Devices" pageKey="devices" />
          </AccordionDetails>
        </Accordion>

        {/* Automation */}
        <Accordion
          expanded={expandedNav.automation}
          onChange={(_, e) => setExpandedNav((s) => ({ ...s, automation: e }))}
          square
          disableGutters
          sx={{ "&:before": { display: "none" }, margin: 0, border: 0 }}
        >
          <AccordionSummary
            expandIcon={<ExpandMoreIcon />}
            sx={{ backgroundColor: "#2c2c2c", minHeight: "36px", "& .MuiAccordionSummary-content": { margin: 0 } }}
          >
            <Typography sx={{ fontSize: "0.9rem", color: "#58a6ff" }}>
              <b>Automation</b>
            </Typography>
          </AccordionSummary>
          <AccordionDetails sx={{ p: 0, bgcolor: "#232323" }}>
            <NavItem icon={<JobsIcon fontSize="small" />} label="Scheduled Jobs" pageKey="jobs" />
            <NavItem icon={<ScriptIcon fontSize="small" />} label="Scripts" pageKey="scripts" />
            <NavItem icon={<WorkflowsIcon fontSize="small" />} label="Workflows" pageKey="workflows" />
            <NavItem icon={<CommunityIcon fontSize="small" />} label="Community Content" pageKey="community" />
          </AccordionDetails>
        </Accordion>

        {/* Filters & Groups */}
        <Accordion
          expanded={expandedNav.filters}
          onChange={(_, e) => setExpandedNav((s) => ({ ...s, filters: e }))}
          square
          disableGutters
          sx={{ "&:before": { display: "none" }, margin: 0, border: 0 }}
        >
          <AccordionSummary
            expandIcon={<ExpandMoreIcon />}
            sx={{ backgroundColor: "#2c2c2c", minHeight: "36px", "& .MuiAccordionSummary-content": { margin: 0 } }}
          >
            <Typography sx={{ fontSize: "0.9rem", color: "#58a6ff" }}>
              <b>Filters & Groups</b>
            </Typography>
          </AccordionSummary>
          <AccordionDetails sx={{ p: 0, bgcolor: "#232323" }}>
            <NavItem icon={<FilterIcon fontSize="small" />} label="Filters" pageKey="filters" />
            <NavItem icon={<GroupsIcon fontSize="small" />} label="Groups" pageKey="groups" />
          </AccordionDetails>
        </Accordion>
      </Box>
    </Box>
  );
}

export default React.memo(NavigationSidebar);