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
  Polyline as WorkflowsIcon,
  Code as ScriptIcon,
  PeopleOutline as CommunityIcon,
  Apps as AssembliesIcon
} from "@mui/icons-material";
import { LocationCity as SitesIcon } from "@mui/icons-material";
import { Dns as ServerInfoIcon, VpnKey as CredentialIcon, PersonOutline as UserIcon } from "@mui/icons-material";

function NavigationSidebar({ currentPage, onNavigate, isAdmin = false }) {
  const [expandedNav, setExpandedNav] = useState({
    sites: true,
    devices: true,
    automation: true,
    filters: true,
    access: true,
    admin: true
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
          borderTopRightRadius: 0,
          borderBottomRightRadius: 0,
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
            top: 0,
            bottom: 0,
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
          primaryTypographyProps={{ fontSize: "0.75rem", fontWeight: active ? 600 : 400 }}
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
                top: 0,
                bottom: 0,
                width: groupActive ? 3 : 0,
                bgcolor: "#58a6ff",
                borderTopRightRadius: 2,
                borderBottomRightRadius: 2,
                transition: "width 160ms ease"
              }
            }}
          >
            <Typography sx={{ fontSize: "0.85rem", color: "#58a6ff" }}>
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
                top: 0,
                bottom: 0,
                width: groupActive ? 3 : 0,
                bgcolor: "#58a6ff",
                borderTopRightRadius: 2,
                borderBottomRightRadius: 2,
                transition: "width 160ms ease"
              }
            }}
          >
            <Typography sx={{ fontSize: "0.85rem", color: "#58a6ff" }}>
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
          const groupActive = ["jobs", "assemblies", "community"].includes(currentPage);
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
                top: 0,
                bottom: 0,
                width: groupActive ? 3 : 0,
                bgcolor: "#58a6ff",
                borderTopRightRadius: 2,
                borderBottomRightRadius: 2,
                transition: "width 160ms ease"
              }
            }}
          >
            <Typography sx={{ fontSize: "0.85rem", color: "#58a6ff" }}>
              <b>Automation</b>
            </Typography>
          </AccordionSummary>
          <AccordionDetails sx={{ p: 0, bgcolor: "#232323" }}>
            <NavItem icon={<AssembliesIcon fontSize="small" />} label="Assemblies" pageKey="assemblies" />
            <NavItem icon={<CommunityIcon fontSize="small" />} label="Community Content" pageKey="community" />
            <NavItem icon={<JobsIcon fontSize="small" />} label="Scheduled Jobs" pageKey="jobs" />
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
                top: 0,
                bottom: 0,
                width: groupActive ? 3 : 0,
                bgcolor: "#58a6ff",
                borderTopRightRadius: 2,
                borderBottomRightRadius: 2,
                transition: "width 160ms ease"
              }
            }}
          >
            <Typography sx={{ fontSize: "0.85rem", color: "#58a6ff" }}>
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

        {/* Access Management */}
        {(() => {
          if (!isAdmin) return null;
          const groupActive = currentPage === "access_credentials" || currentPage === "access_users";
          return (
        <Accordion
          expanded={expandedNav.access}
          onChange={(_, e) => setExpandedNav((s) => ({ ...s, access: e }))}
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
                top: 0,
                bottom: 0,
                width: groupActive ? 3 : 0,
                bgcolor: "#58a6ff",
                borderTopRightRadius: 2,
                borderBottomRightRadius: 2,
                transition: "width 160ms ease"
              }
            }}
          >
            <Typography sx={{ fontSize: "0.85rem", color: "#58a6ff" }}>
              <b>Access Management</b>
            </Typography>
          </AccordionSummary>
          <AccordionDetails sx={{ p: 0, bgcolor: "#232323" }}>
            <NavItem icon={<CredentialIcon fontSize="small" />} label="Credentials" pageKey="access_credentials" />
            <NavItem icon={<UserIcon fontSize="small" />} label="Users" pageKey="access_users" />
          </AccordionDetails>
        </Accordion>
          );
        })()}

        {/* Admin */}
        {(() => {
          if (!isAdmin) return null;
          const groupActive = currentPage === "server_info";
          return (
        <Accordion
          expanded={expandedNav.admin}
          onChange={(_, e) => setExpandedNav((s) => ({ ...s, admin: e }))}
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
                top: 0,
                bottom: 0,
                width: groupActive ? 3 : 0,
                bgcolor: "#58a6ff",
                borderTopRightRadius: 2,
                borderBottomRightRadius: 2,
                transition: "width 160ms ease"
              }
            }}
          >
            <Typography sx={{ fontSize: "0.85rem", color: "#58a6ff" }}>
              <b>Admin Settings</b>
            </Typography>
          </AccordionSummary>
          <AccordionDetails sx={{ p: 0, bgcolor: "#232323" }}>
            <NavItem icon={<ServerInfoIcon fontSize="small" />} label="Server Info" pageKey="server_info" />
          </AccordionDetails>
        </Accordion>
          );
        })()}
      </Box>
    </Box>
  );
}

export default React.memo(NavigationSidebar);
