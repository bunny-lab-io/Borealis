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
  Apps as AssembliesIcon,
  LocationCity as SitesIcon,
  Dns as ServerInfoIcon,
  VpnKey as CredentialIcon,
  PersonOutline as UserIcon,
  AdminPanelSettings as AdminPanelSettingsIcon,
  ReceiptLong as LogsIcon,
} from "@mui/icons-material";
import { useNavigate } from "react-router-dom";
import { APP_PATHS } from "./app/routes/paths.js";

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

const NAV_SECTIONS = Object.freeze([
  {
    id: "sites",
    title: "Sites",
    items: [
      {
        icon: SitesIcon,
        label: "Sites",
        navKey: "sites",
        to: APP_PATHS.sites,
      },
    ],
  },
  {
    id: "devices",
    title: "Inventory",
    items: [
      {
        icon: AdminPanelSettingsIcon,
        label: "Device Approvals",
        navKey: "device-approvals",
        to: APP_PATHS.deviceApprovals,
      },
      {
        icon: DevicesIcon,
        label: "Devices",
        navKey: "devices",
        to: APP_PATHS.devices,
      },
    ],
  },
  {
    id: "automation",
    title: "Automation",
    items: [
      {
        icon: AssembliesIcon,
        label: "Assemblies",
        navKey: "assemblies",
        to: APP_PATHS.assemblies,
      },
      {
        icon: ScheduleIcon,
        label: "Scheduled Jobs",
        navKey: "jobs",
        to: APP_PATHS.jobs,
      },
    ],
  },
  {
    id: "filters",
    title: "Filters & Groups",
    items: [
      {
        icon: FilterIcon,
        label: "Filters",
        navKey: "filters",
        to: APP_PATHS.filters,
      },
    ],
  },
  {
    id: "access",
    title: "Access Management",
    adminOnly: true,
    items: [
      {
        icon: CredentialIcon,
        label: "Credentials",
        navKey: "credentials",
        to: APP_PATHS.credentials,
      },
      {
        icon: UserIcon,
        label: "Users",
        navKey: "users",
        to: APP_PATHS.users,
      },
    ],
  },
  {
    id: "admin",
    title: "Admin Settings",
    adminOnly: true,
    items: [
      {
        icon: ServerInfoIcon,
        label: "Server Info",
        navKey: "server",
        to: APP_PATHS.server,
      },
      {
        icon: LogsIcon,
        label: "Log Management",
        navKey: "logs",
        to: APP_PATHS.logs,
      },
    ],
  },
]);

function NavigationSidebar({ activeNavKey, isAdmin = false }) {
  const navigate = useNavigate();
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

  const visibleSections = useMemo(
    () => NAV_SECTIONS.filter((section) => !section.adminOnly || isAdmin),
    [isAdmin]
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

  const NavItem = ({ icon, label, navKey, to, indent = 0 }) => {
    const active = activeNavKey === navKey;
    return (
      <ListItemButton
        onClick={() => navigate(to)}
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
        flexShrink: 0,
        position: "relative",
        zIndex: 2,
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
        {visibleSections.map((section) => (
          <Section key={section.id} title={section.title} k={section.id}>
            {section.items.map((item) => {
              const IconComponent = item.icon;
              return (
                <NavItem
                  key={item.navKey}
                  icon={<IconComponent fontSize="small" />}
                  label={item.label}
                  navKey={item.navKey}
                  to={item.to}
                />
              );
            })}
          </Section>
        ))}
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
