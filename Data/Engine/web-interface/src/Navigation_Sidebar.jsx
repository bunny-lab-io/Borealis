////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Engine/web-interface/src/Navigation_Sidebar.jsx

import React, { useEffect, useMemo, useRef, useState } from "react";
import {
  Accordion,
  AccordionSummary,
  AccordionDetails,
  Typography,
  Box,
  Menu,
  MenuItem,
  ListItemButton,
  ListItemText,
  Divider,
  Tooltip,
} from "@mui/material";
import {
  ExpandMore as ExpandMoreIcon,
  ChevronLeft as ChevronLeftIcon,
  ChevronRight as ChevronRightIcon,
  Devices as DevicesIcon,
  Inventory2Rounded as SoftwareIcon,
  FilterAlt as FilterIcon,
  Schedule as ScheduleIcon,
  Apps as AssembliesIcon,
  Policy as WatchdogIcon,
  NotificationsActive as AlertsIcon,
  SettingsRounded as SettingsIcon,
  LocationCity as SitesIcon,
  Dns as ServerInfoIcon,
  VpnKey as CredentialIcon,
  PersonOutline as UserIcon,
  AccountTree as DirectoryIcon,
  AdminPanelSettings as AdminPanelSettingsIcon,
  DashboardCustomizeRounded as PageStyleTemplateIcon,
  ReceiptLong as LogsIcon,
} from "@mui/icons-material";
import { useLocation, useNavigate } from "react-router-dom";
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
const DEVELOPER_MODE_STORAGE_KEY = "borealis_sidebar_developer_mode";
const SITE_SCOPED_NAV_KEYS = new Set([
  "alerts",
  "devices",
  "filters",
  "watchdogs",
  "jobs",
  "software",
]);

function buildSiteScopedPath(path, siteId, extraParams = {}) {
  const params = new URLSearchParams();
  params.set("site", String(siteId || ""));
  Object.entries(extraParams).forEach(([key, value]) => {
    if (value != null && value !== "") {
      params.set(key, String(value));
    }
  });
  return `${path}?${params.toString()}`;
}

function EllipsisTooltip({ children, title, tooltipTitle, ...typographyProps }) {
  const ref = useRef(null);
  const [showTooltip, setShowTooltip] = useState(false);
  const resolvedTitle = tooltipTitle || title || children || "";

  const updateOverflow = () => {
    const node = ref.current;
    if (!node) return;
    setShowTooltip(node.scrollWidth > node.clientWidth);
  };

  return (
    <Tooltip title={showTooltip ? resolvedTitle : ""} placement="top-start">
      <Typography
        {...typographyProps}
        ref={ref}
        noWrap
        onMouseEnter={updateOverflow}
        onFocus={updateOverflow}
        title=""
      >
        {children}
      </Typography>
    </Tooltip>
  );
}

const BASE_NAV_SECTIONS = Object.freeze([
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
      {
        icon: WatchdogIcon,
        label: "Watchdogs",
        navKey: "watchdogs",
        to: APP_PATHS.watchdogs,
      },
    ],
  },
  {
    id: "alerting",
    title: "Alerting & Reporting",
    items: [
      {
        icon: AlertsIcon,
        label: "Alerts",
        navKey: "alerts",
        to: APP_PATHS.alerts,
      },
      {
        icon: SoftwareIcon,
        label: "Software Audit",
        navKey: "software",
        to: APP_PATHS.software,
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
        icon: DirectoryIcon,
        label: "Directory Services",
        navKey: "directory-services",
        to: APP_PATHS.directoryServices,
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
  {
    id: "developer",
    title: "Dev Tools",
    adminOnly: true,
    developerOnly: true,
    items: [
      {
        icon: PageStyleTemplateIcon,
        label: "Page Style Template",
        navKey: "dev-tools",
        to: APP_PATHS.pageStyleTemplate,
      },
    ],
  },
]);

function NavigationSidebar({ activeNavKey, isAdmin = false }) {
  const navigate = useNavigate();
  const location = useLocation();
  const [collapsed, setCollapsed] = useState(false);
  const [sites, setSites] = useState([]);
  const [developerModeEnabled, setDeveloperModeEnabled] = useState(() => {
    if (typeof window === "undefined") {
      return false;
    }
    return window.localStorage.getItem(DEVELOPER_MODE_STORAGE_KEY) === "1";
  });
  const [contextMenuPosition, setContextMenuPosition] = useState(null);
  const [expandedNav, setExpandedNav] = useState({
    sites: true,
    devices: true,
    automation: true,
    alerting: true,
    filters: true,
    access: true,
    admin: true,
    developer: true,
  });

  const selectedSiteId = useMemo(() => {
    const params = new URLSearchParams(location.search || "");
    return String(params.get("site") || "").trim();
  }, [location.search]);

  useEffect(() => {
    if (!selectedSiteId) return;
    let active = true;
    const loadSites = async () => {
      try {
        const response = await fetch("/api/sites", { credentials: "include", cache: "no-store" });
        const payload = await response.json().catch(() => ({}));
        if (!active) return;
        setSites(Array.isArray(payload?.sites) ? payload.sites : []);
      } catch {
        if (active) setSites([]);
      }
    };
    void loadSites();
    return () => {
      active = false;
    };
  }, [selectedSiteId]);

  const selectedSite = useMemo(
    () => sites.find((site) => String(site?.id) === selectedSiteId) || null,
    [selectedSiteId, sites]
  );

  const siteScopedSection = useMemo(() => {
    if (!selectedSiteId) return null;
    const siteName = String(selectedSite?.name || `Site ${selectedSiteId}`).trim();
    return {
      id: `site-${selectedSiteId}`,
      title: siteName,
      siteScoped: true,
      items: [
        {
          icon: AlertsIcon,
          label: "Alerts",
          navKey: "alerts",
          to: buildSiteScopedPath(APP_PATHS.alerts, selectedSiteId),
        },
        {
          icon: DevicesIcon,
          label: "Devices",
          navKey: "devices",
          to: buildSiteScopedPath(APP_PATHS.devices, selectedSiteId),
        },
        {
          icon: FilterIcon,
          label: "Filters",
          navKey: "filters",
          to: buildSiteScopedPath(APP_PATHS.filters, selectedSiteId),
        },
        {
          icon: WatchdogIcon,
          label: "Watchdogs",
          navKey: "watchdogs",
          to: buildSiteScopedPath(APP_PATHS.watchdogs, selectedSiteId),
        },
        {
          icon: ScheduleIcon,
          label: "Scheduled Jobs",
          navKey: "jobs",
          to: buildSiteScopedPath(APP_PATHS.jobs, selectedSiteId),
        },
        {
          icon: SoftwareIcon,
          label: "Software Audit",
          navKey: "software",
          to: buildSiteScopedPath(APP_PATHS.software, selectedSiteId),
        },
        {
          icon: SettingsIcon,
          label: "Settings",
          navKey: "site-settings",
          to: buildSiteScopedPath(APP_PATHS.sites, selectedSiteId, { section: "settings" }),
        },
      ],
    };
  }, [selectedSite, selectedSiteId]);

  const navSections = useMemo(() => {
    if (!siteScopedSection) return BASE_NAV_SECTIONS;
    const sitesIndex = BASE_NAV_SECTIONS.findIndex((section) => section.id === "sites");
    if (sitesIndex < 0) return [siteScopedSection, ...BASE_NAV_SECTIONS];
    return [
      ...BASE_NAV_SECTIONS.slice(0, sitesIndex + 1),
      siteScopedSection,
      ...BASE_NAV_SECTIONS.slice(sitesIndex + 1),
    ];
  }, [siteScopedSection]);

  const visibleSections = useMemo(
    () =>
      navSections.filter((section) => {
        if (section.adminOnly && !isAdmin) {
          return false;
        }
        if (section.developerOnly && !developerModeEnabled) {
          return false;
        }
        return true;
      }),
    [developerModeEnabled, isAdmin, navSections]
  );

  const closeContextMenu = () => {
    setContextMenuPosition(null);
  };

  const handleSidebarContextMenu = (event) => {
    event.preventDefault();
    setContextMenuPosition({ mouseX: event.clientX + 2, mouseY: event.clientY - 6 });
  };

  const handleDeveloperModeToggle = () => {
    setDeveloperModeEnabled((current) => {
      const next = !current;
      if (typeof window !== "undefined") {
        window.localStorage.setItem(DEVELOPER_MODE_STORAGE_KEY, next ? "1" : "0");
      }
      return next;
    });
    closeContextMenu();
  };

  const Section = ({ title, k, children }) => (
    <Accordion
      expanded={collapsed ? true : (expandedNav[k] ?? true)}
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
            minWidth: 0,
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
          <EllipsisTooltip
            noWrap
            sx={{
              fontSize: "0.85rem",
              color: COLORS.cyan,
              fontWeight: 700,
              letterSpacing: 0.3,
              minWidth: 0,
            }}
          >
            {title}
          </EllipsisTooltip>
        )}
      </AccordionSummary>
      <AccordionDetails sx={{ p: 0 }}>{children}</AccordionDetails>
    </Accordion>
  );

  const NavItem = ({ icon, label, navKey, to, indent = 0, activeOverride }) => {
    const active = activeOverride ?? activeNavKey === navKey;
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
          disableTypography
          primary={
            <EllipsisTooltip
              component="span"
              sx={{
                display: "block",
                fontSize: "0.8rem",
                fontWeight: active ? 600 : 400,
                letterSpacing: 0.2,
                lineHeight: 1.45,
                color: "inherit",
              }}
            >
              {label}
            </EllipsisTooltip>
          }
          sx={{ display: collapsed ? "none" : "block" }}
        />
      </ListItemButton>
    );
  };

  return (
    <Box
      onContextMenu={handleSidebarContextMenu}
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
              if (item.kind === "subheader") {
                return collapsed ? null : (
                  <Typography
                    key={`${section.id}-${item.label}`}
                    sx={{
                      px: 2,
                      pt: 1.1,
                      pb: 0.35,
                      color: "rgba(203, 213, 225, 0.68)",
                      fontSize: "0.68rem",
                      fontWeight: 700,
                      letterSpacing: 0.8,
                      textTransform: "uppercase",
                    }}
                  >
                    {item.label}
                  </Typography>
                );
              }
              const IconComponent = item.icon;
              const activeOverride = section.siteScoped
                ? activeNavKey === item.navKey
                : selectedSiteId && SITE_SCOPED_NAV_KEYS.has(item.navKey)
                  ? false
                  : undefined;
              return (
                <NavItem
                  key={item.navKey}
                  icon={<IconComponent fontSize="small" />}
                  label={item.label}
                  navKey={item.navKey}
                  to={item.to}
                  indent={item.indent || 0}
                  activeOverride={activeOverride}
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
      <Menu
        open={Boolean(contextMenuPosition)}
        onClose={closeContextMenu}
        anchorReference="anchorPosition"
        anchorPosition={
          contextMenuPosition
            ? { top: contextMenuPosition.mouseY, left: contextMenuPosition.mouseX }
            : undefined
        }
        PaperProps={{
          sx: {
            width: 320,
            maxWidth: "calc(100vw - 24px)",
            borderRadius: 2,
            border: "1px solid rgba(148, 163, 184, 0.28)",
            background: "rgba(8,12,24,0.96)",
            color: COLORS.textActive,
            backdropFilter: "blur(14px) saturate(140%)",
            boxShadow: "0 22px 52px rgba(2, 6, 23, 0.72)",
            p: 0.75,
            overflow: "hidden",
          },
        }}
        MenuListProps={{
          dense: true,
          sx: { p: 0 },
        }}
      >
        <Box
          sx={{
            display: "flex",
            alignItems: "center",
            gap: 1,
            px: 1,
            py: 1,
          }}
        >
          <Box sx={{ display: "flex", color: COLORS.cyan }}>
            <PageStyleTemplateIcon fontSize="small" />
          </Box>
          <Box sx={{ minWidth: 0 }}>
            <Typography
              noWrap
              sx={{
                color: COLORS.textActive,
                fontSize: "0.86rem",
                fontWeight: 700,
              }}
            >
              Navigation
            </Typography>
            <Typography
              noWrap
              sx={{
                color: "rgba(203, 213, 225, 0.68)",
                fontSize: "0.74rem",
              }}
            >
              Sidebar visibility controls
            </Typography>
          </Box>
        </Box>
        <Divider sx={{ borderColor: "rgba(148, 163, 184, 0.14)", my: 0.5 }} />
        <Typography
          sx={{
            px: 1,
            pt: 0.8,
            pb: 0.45,
            color: "rgba(203, 213, 225, 0.62)",
            fontSize: "0.68rem",
            fontWeight: 800,
            letterSpacing: 0.8,
            textTransform: "uppercase",
          }}
        >
          View
        </Typography>
        <MenuItem
          onClick={handleDeveloperModeToggle}
          sx={{
            position: "relative",
            alignItems: "flex-start",
            gap: 1,
            px: 1,
            py: 0.95,
            borderRadius: 1.2,
            color: COLORS.textActive,
            whiteSpace: "normal",
            "&:before": {
              content: '""',
              position: "absolute",
              left: 0,
              top: 8,
              bottom: 8,
              width: 3,
              borderRadius: 999,
              backgroundColor: COLORS.cyan,
              opacity: 0,
              transition: "opacity 160ms ease",
            },
            "&:hover": {
              backgroundColor: "rgba(125, 211, 252, 0.08)",
              "&:before": {
                opacity: 1,
              },
            },
          }}
        >
          <Box sx={{ display: "flex", mt: 0.15, color: COLORS.cyan }}>
            <PageStyleTemplateIcon fontSize="small" />
          </Box>
          <Box sx={{ minWidth: 0 }}>
            <Typography sx={{ fontSize: "0.82rem", fontWeight: 700 }}>
              {developerModeEnabled ? "Disable Developer Mode" : "Enable Developer Mode"}
            </Typography>
            <Typography
              sx={{
                color: "rgba(203, 213, 225, 0.68)",
                fontSize: "0.74rem",
                lineHeight: 1.35,
              }}
            >
              {developerModeEnabled
                ? "Hide Dev Tools from the sidebar."
                : "Show Dev Tools in the sidebar."}
            </Typography>
          </Box>
        </MenuItem>
      </Menu>
    </Box>
  );
}

export default React.memo(NavigationSidebar);
