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
import { LocationCity as SitesIcon } from "@mui/icons-material";

function NavigationSidebar({ currentPage, onNavigate }) {
  const [expandedNav, setExpandedNav] = useState({
    sites: true,
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
          color: active ? "#e6f2ff" : "#ccc",
          position: "relative",
          background: active
            ? "linear-gradient(90deg, rgba(88,166,255,0.10) 0%, rgba(88,166,255,0.03) 60%, rgba(88,166,255,0.00) 100%)"
            : "transparent",
          borderTopRightRadius: 8,
          borderBottomRightRadius: 8,
          boxShadow: active
            ? "inset 0 0 0 1px rgba(88,166,255,0.25)"
            : "none",
          transition: "background 160ms ease, box-shadow 160ms ease, color 160ms ease",
          "&:hover": {
            background: active
              ? "linear-gradient(90deg, rgba(88,166,255,0.14) 0%, rgba(88,166,255,0.06) 60%, rgba(88,166,255,0.00) 100%)"
              : "#2c2c2c"
          }
        }}
        selected={active}
      >
        <Box
          sx={{
            position: "absolute",
            left: 0,
            top: 6,
            bottom: 6,
            width: active ? 3 : 0,
            bgcolor: "#58a6ff",
            borderTopRightRadius: 2,
            borderBottomRightRadius: 2,
            boxShadow: active ? "0 0 6px rgba(88,166,255,0.35)" : "none",
            transition: "width 180ms ease, box-shadow 200ms ease"
          }}
        />
        {icon && (
          <Box
            sx={{
              mr: 1,
              display: "flex",
              alignItems: "center",
              color: active ? "#7db7ff" : "#58a6ff",
              transition: "color 160ms ease"
            }}
          >
            {icon}
          </Box>
        )}
        <ListItemText
          primary={label}
          primaryTypographyProps={{ fontSize: "0.95rem", fontWeight: active ? 600 : 400 }}
        />
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
        {/* Sites */}
        {(() => {
          const groupActive = currentPage === "sites";
          return (
        <Accordion
          expanded={expandedNav.sites}
          onChange={(_, e) => setExpandedNav((s) => ({ ...s, sites: e }))}
          square
          disableGutters
          sx={{ "&:before": { display: "none" }, margin: 0, border: 0 }}
        >
          <AccordionSummary
            expandIcon={<ExpandMoreIcon />}
            sx={{
              position: "relative",
              background: groupActive
                ? "linear-gradient(90deg, rgba(88,166,255,0.08) 0%, rgba(88,166,255,0.00) 100%)"
                : "#2c2c2c",
              minHeight: "36px",
              "& .MuiAccordionSummary-content": { margin: 0 },
              "&::before": {
                content: '""',
                position: "absolute",
                left: 0,
                top: 6,
                bottom: 6,
                width: groupActive ? 2 : 0,
                bgcolor: "#58a6ff",
                borderTopRightRadius: 2,
                borderBottomRightRadius: 2,
                transition: "width 160ms ease"
              }
            }}
          >
            <Typography sx={{ fontSize: "0.9rem", color: "#58a6ff" }}>
              <b>Sites</b>
            </Typography>
          </AccordionSummary>
          <AccordionDetails sx={{ p: 0, bgcolor: "#232323" }}>
            <NavItem icon={<SitesIcon fontSize="small" />} label="All Sites" pageKey="sites" />
          </AccordionDetails>
        </Accordion>
          );
        })()}
        {/* Devices */}
        {(() => {
          const groupActive = currentPage === "devices";
          return (
        <Accordion
          expanded={expandedNav.devices}
          onChange={(_, e) => setExpandedNav((s) => ({ ...s, devices: e }))}
          square
          disableGutters
          sx={{ "&:before": { display: "none" }, margin: 0, border: 0 }}
        >
          <AccordionSummary
            expandIcon={<ExpandMoreIcon />}
            sx={{
              position: "relative",
              background: groupActive
                ? "linear-gradient(90deg, rgba(88,166,255,0.08) 0%, rgba(88,166,255,0.00) 100%)"
                : "#2c2c2c",
              minHeight: "36px",
              "& .MuiAccordionSummary-content": { margin: 0 },
              "&::before": {
                content: '""',
                position: "absolute",
                left: 0,
                top: 6,
                bottom: 6,
                width: groupActive ? 2 : 0,
                bgcolor: "#58a6ff",
                borderTopRightRadius: 2,
                borderBottomRightRadius: 2,
                transition: "width 160ms ease"
              }
            }}
          >
            <Typography sx={{ fontSize: "0.9rem", color: "#58a6ff" }}>
              <b>Devices</b>
            </Typography>
          </AccordionSummary>
          <AccordionDetails sx={{ p: 0, bgcolor: "#232323" }}>
            <NavItem icon={<DevicesIcon fontSize="small" />} label="Devices" pageKey="devices" />
          </AccordionDetails>
        </Accordion>
          );
        })()}

        {/* Automation */}
        {(() => {
          const groupActive = ["jobs", "scripts", "workflows", "community"].includes(currentPage);
          return (
        <Accordion
          expanded={expandedNav.automation}
          onChange={(_, e) => setExpandedNav((s) => ({ ...s, automation: e }))}
          square
          disableGutters
          sx={{ "&:before": { display: "none" }, margin: 0, border: 0 }}
        >
          <AccordionSummary
            expandIcon={<ExpandMoreIcon />}
            sx={{
              position: "relative",
              background: groupActive
                ? "linear-gradient(90deg, rgba(88,166,255,0.08) 0%, rgba(88,166,255,0.00) 100%)"
                : "#2c2c2c",
              minHeight: "36px",
              "& .MuiAccordionSummary-content": { margin: 0 },
              "&::before": {
                content: '""',
                position: "absolute",
                left: 0,
                top: 6,
                bottom: 6,
                width: groupActive ? 2 : 0,
                bgcolor: "#58a6ff",
                borderTopRightRadius: 2,
                borderBottomRightRadius: 2,
                transition: "width 160ms ease"
              }
            }}
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
          );
        })()}

        {/* Filters & Groups */}
        {(() => {
          const groupActive = currentPage === "filters" || currentPage === "groups";
          return (
        <Accordion
          expanded={expandedNav.filters}
          onChange={(_, e) => setExpandedNav((s) => ({ ...s, filters: e }))}
          square
          disableGutters
          sx={{ "&:before": { display: "none" }, margin: 0, border: 0 }}
        >
          <AccordionSummary
            expandIcon={<ExpandMoreIcon />}
            sx={{
              position: "relative",
              background: groupActive
                ? "linear-gradient(90deg, rgba(88,166,255,0.08) 0%, rgba(88,166,255,0.00) 100%)"
                : "#2c2c2c",
              minHeight: "36px",
              "& .MuiAccordionSummary-content": { margin: 0 },
              "&::before": {
                content: '""',
                position: "absolute",
                left: 0,
                top: 6,
                bottom: 6,
                width: groupActive ? 2 : 0,
                bgcolor: "#58a6ff",
                borderTopRightRadius: 2,
                borderBottomRightRadius: 2,
                transition: "width 160ms ease"
              }
            }}
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
          );
        })()}
      </Box>
    </Box>
  );
}

export default React.memo(NavigationSidebar);
