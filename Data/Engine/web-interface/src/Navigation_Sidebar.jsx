////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Engine/web-interface/src/Navigation_Sidebar.jsx

import React, { useMemo, useState } from "react";
import {
  Accordion,
  AccordionSummary,
  AccordionDetails,
  Typography,
  Box,
  ListItemButton,
  ListItemText,
  Divider,
} from "@mui/material";
import {
  ExpandMore as ExpandMoreIcon,
  ChevronLeft as ChevronLeftIcon,
  ChevronRight as ChevronRightIcon,
  Devices as DevicesIcon,
  FilterAlt as FilterIcon,
  Schedule as ScheduleIcon,
  PeopleOutline as CommunityIcon,
  Apps as AssembliesIcon,
  LocationCity as SitesIcon,
  Dns as ServerInfoIcon,
  VpnKey as CredentialIcon,
  PersonOutline as UserIcon,
  GitHub as GitHubIcon,
  Dashboard as PageTemplateIcon,
  AdminPanelSettings as AdminPanelSettingsIcon,
  ReceiptLong as LogsIcon,
} from "@mui/icons-material";

const COLORS = {
  cyan: "#7db7ff",
  violet: "#c084fc",
  text: "#cbd5e1",
  textActive: "#e6f2ff",
  matte: "#0f141c",
  line: "rgba(125,183,255,0.14)",
  hover: "rgba(255,255,255,0.05)",
  itemActiveBg:
    "linear-gradient(90deg, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)",
};

const NAV_ITEM_ALIASES = {
  devices: ["device_details"],
  assemblies: ["scripts", "ansible_editor", "workflow-editor", "workflows"],
  jobs: ["create_job"],
  filters: ["filter_editor"],
};

function NavigationSidebar({ currentPage, onNavigate, isAdmin = false }) {
  const [collapsed, setCollapsed] = useState(false);
  const [expandedNav, setExpandedNav] = useState({
    sites: true,
    devices: true,
    automation: true,
    filters: true,
    access: true,
    admin: true,
    developer: true,
  });

  const effectivePage = useMemo(() => {
    for (const [rootKey, aliases] of Object.entries(NAV_ITEM_ALIASES)) {
      if (aliases.includes(currentPage)) {
        return rootKey;
      }
    }
    return currentPage;
  }, [currentPage]);

  const groupActive = useMemo(
    () => ({
      sites: currentPage === "sites",
      devices: [
        "devices",
        "ssh_devices",
        "winrm_devices",
        "agent_devices",
        "admin_device_approvals",
      ].includes(currentPage),
      automation: ["jobs", "assemblies", "community"].includes(currentPage),
      filters: ["filters", "filter_editor"].includes(currentPage),
      access: [
        "access_credentials",
        "access_users",
        "access_github_token",
      ].includes(currentPage),
      admin: ["server_info", "log_management"].includes(currentPage),
      developer: ["page_template"].includes(currentPage),
    }),
    [currentPage]
  );

  const Section = ({ title, k, children }) => (
    <Accordion
      expanded={collapsed ? true : expandedNav[k]}
      onChange={(_, e) => {
        if (collapsed) return;
        setExpandedNav((s) => ({ ...s, [k]: e }));
      }}
      square
      disableGutters
      sx={{
        "&:before": { display: "none" },
        m: 0,
        bgcolor: "transparent",
        border: 0,
      }}
    >
      <AccordionSummary
        expandIcon={<ExpandMoreIcon sx={{ color: COLORS.cyan }} />}
        sx={{
          minHeight: 38,
          "& .MuiAccordionSummary-content": {
            m: 0,
            py: 0.5,
            display: collapsed ? "none" : "flex",
          },
          display: collapsed ? "none" : "flex",
          backgroundColor: "rgba(255,255,255,0.02)",
          borderTopRightRadius: 8,
          borderBottomRightRadius: 8,
          px: collapsed ? 1 : 1.5,
        }}
        title={collapsed ? title : undefined}
      >
        {collapsed ? null : (
          <Typography
            sx={{
              fontSize: "0.85rem",
              color: COLORS.cyan,
              fontWeight: 700,
              letterSpacing: 0.3,
            }}
          >
            {title}
          </Typography>
        )}
      </AccordionSummary>
      <AccordionDetails sx={{ p: 0 }}>{children}</AccordionDetails>
    </Accordion>
  );

  const NavItem = ({ icon, label, pageKey, indent = 0 }) => {
    const active = effectivePage === pageKey;
    return (
      <ListItemButton
        onClick={() => onNavigate(pageKey)}
        sx={{
          pl: collapsed ? 1.5 : indent ? 4 : 2,
          pr: collapsed ? 1.5 : 2,
          py: 1,
          color: active ? COLORS.textActive : COLORS.text,
          position: "relative",
          background: active ? COLORS.itemActiveBg : "transparent",
          borderTopRightRadius: 0,
          borderBottomRightRadius: 0,
          justifyContent: collapsed ? "center" : "flex-start",
          transition:
            "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
          "&:hover": {
            background: active ? COLORS.itemActiveBg : COLORS.hover,
          },
          "&:active": { transform: "translateY(0.5px)" },
        }}
        selected={active}
        title={collapsed ? label : undefined}
      >
        {icon && (
          <Box
            sx={{
              mr: collapsed ? 0 : 1,
              display: "flex",
              alignItems: "center",
              color: active ? COLORS.cyan : "#8fbfff",
              transition: "color 160ms ease",
            }}
          >
            {icon}
          </Box>
        )}
        <ListItemText
          primary={label}
          sx={{ display: collapsed ? "none" : "block" }}
          primaryTypographyProps={{
            fontSize: "0.8rem",
            fontWeight: active ? 600 : 400,
            letterSpacing: 0.2,
          }}
        />
      </ListItemButton>
    );
  };

  return (
    <Box
      sx={{
        width: collapsed ? 45 : 260,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        background:
          "linear-gradient(180deg, rgba(64,164,255,0.05) 0%, rgba(192,132,252,0.04) 100%), " +
          COLORS.matte,
        borderRight: `1px solid ${COLORS.line}`,
        backdropFilter: "blur(8px) saturate(130%)",
      }}
    >
      <Divider sx={{ borderColor: COLORS.line }} />

      <Box sx={{ flex: 1, overflowY: "auto", p: 0.25 }}>
        {/* Sites */}
        <Section title="Sites" k="sites">
          <NavItem
            icon={<SitesIcon fontSize="small" />}
            label="All Sites"
            pageKey="sites"
          />
        </Section>

        {/* Inventory */}
        <Section title="Inventory" k="devices">
          <NavItem
            icon={<AdminPanelSettingsIcon fontSize="small" />}
            label="Device Approvals"
            pageKey="admin_device_approvals"
          />
          <NavItem
            icon={<DevicesIcon fontSize="small" />}
            label="All Devices"
            pageKey="devices"
          />
          {/* <NavItem
            icon={<DevicesIcon fontSize="small" />}
            label="Agent Devices"
            pageKey="agent_devices"
            indent
          />
          <NavItem
            icon={<DevicesIcon fontSize="small" />}
            label="SSH Devices"
            pageKey="ssh_devices"
            indent
          />
          <NavItem
            icon={<DevicesIcon fontSize="small" />}
            label="WinRM Devices"
            pageKey="winrm_devices"
            indent
          />
          */}
        </Section>

        {/* Automation */}
        <Section title="Task Automation" k="automation">
          <NavItem
            icon={<AssembliesIcon fontSize="small" />}
            label="Assemblies"
            pageKey="assemblies"
          />
          <NavItem
            icon={<ScheduleIcon fontSize="small" />}
            label="Scheduled Jobs"
            pageKey="jobs"
          />
          {/* Community Content 
            <NavItem
            icon={<CommunityIcon fontSize="small" />}
            label="Community Content"
            pageKey="community"
          />
          */}

        </Section>

        {/* Filters & Groups */}
        <Section title="Filters & Groups" k="filters">
          <NavItem
            icon={<FilterIcon fontSize="small" />}
            label="Filters"
            pageKey="filters"
          />
        </Section>

        {/* Access Management */}
        {isAdmin && (
          <Section title="Access Management" k="access">
            <NavItem
              icon={<CredentialIcon fontSize="small" />}
              label="Credentials"
              pageKey="access_credentials"
            />
            <NavItem
              icon={<GitHubIcon fontSize="small" />}
              label="GitHub API Token"
              pageKey="access_github_token"
            />
            <NavItem
              icon={<UserIcon fontSize="small" />}
              label="Users"
              pageKey="access_users"
            />
          </Section>
        )}

        {/* Admin Settings */}
        {isAdmin && (
          <Section title="Admin Settings" k="admin">
            <NavItem
              icon={<ServerInfoIcon fontSize="small" />}
              label="Server Info"
              pageKey="server_info"
            />
            <NavItem
              icon={<LogsIcon fontSize="small" />}
              label="Log Management"
              pageKey="log_management"
            />
          </Section>
        )}

        {/* Developer Tools (navigation hidden) */}
        {/* {isAdmin && (
          <Section title="Developer Tools" k="developer">
            <NavItem
              icon={<PageTemplateIcon fontSize="small" />}
              label="Page Template"
              pageKey="page_template"
            />
          </Section>
        )} */}
      </Box>

      <Box sx={{ px: 1, pb: 1 }}>
        <Box
          component="button"
          type="button"
          onClick={() => setCollapsed((prev) => !prev)}
          aria-label={collapsed ? "Expand navigation" : "Collapse navigation"}
          sx={{
            width: "100%",
            height: 28,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            background: "rgba(255,255,255,0.04)",
            border: `1px solid ${COLORS.line}`,
            borderRadius: 6,
            color: COLORS.cyan,
            cursor: "pointer",
            transition: "background 160ms ease, transform 120ms ease",
            "&:hover": {
              background: "rgba(255,255,255,0.08)",
            },
            "&:active": {
              transform: "translateY(1px)",
            },
          }}
        >
          {collapsed ? <ChevronRightIcon fontSize="small" /> : <ChevronLeftIcon fontSize="small" />}
        </Box>
      </Box>

      <Divider sx={{ borderColor: COLORS.line }} />
    </Box>
  );
}

export default React.memo(NavigationSidebar);
