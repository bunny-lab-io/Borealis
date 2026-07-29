import React from "react";
import { Box } from "@mui/material";

const OS_PAGE_ICON_CLASSES = Object.freeze({
  windows: "fa-brands fa-windows",
  linux: "fa-brands fa-linux",
  macos: "fa-brands fa-apple",
});

function normalizeText(value) {
  return String(value ?? "").trim();
}

export function PatchOSPageIcon({ os = "windows", sx, className = "", ...props }) {
  return (
    <Box
      component="i"
      aria-hidden="true"
      className={`${OS_PAGE_ICON_CLASSES[os] || OS_PAGE_ICON_CLASSES.windows} ${className}`.trim()}
      sx={{
        width: 22,
        lineHeight: 1,
        textAlign: "center",
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        ...sx,
      }}
      {...props}
    />
  );
}

export function WindowsPatchPageIcon(props) {
  return <PatchOSPageIcon os="windows" {...props} />;
}

export function LinuxPatchPageIcon(props) {
  return <PatchOSPageIcon os="linux" {...props} />;
}

export function MacOSPatchPageIcon(props) {
  return <PatchOSPageIcon os="macos" {...props} />;
}

export const PATCH_PLATFORM_TABS = [
  { key: "windows", label: "Windows", Icon: WindowsPatchPageIcon },
  { key: "linux", label: "Linux", Icon: LinuxPatchPageIcon },
  { key: "macos", label: "MacOS", Icon: MacOSPatchPageIcon },
];

const PATCH_PLATFORM_ALIASES = Object.freeze({
  darwin: "macos",
  mac: "macos",
  osx: "macos",
  win: "windows",
});

export function normalizePatchPlatform(value) {
  const requested = normalizeText(value).toLowerCase();
  const normalized = PATCH_PLATFORM_ALIASES[requested] || requested;
  return PATCH_PLATFORM_TABS.some((tab) => tab.key === normalized) ? normalized : "windows";
}

export function resolvePatchPlatformCopy(platform) {
  const normalized = normalizePatchPlatform(platform);
  if (normalized === "linux") {
    return {
      title: "Linux Patch Management",
      subtitle: "Linux patch inventory and policies are planned for a future Borealis release.",
      label: "Linux",
      Icon: LinuxPatchPageIcon,
    };
  }
  if (normalized === "macos") {
    return {
      title: "MacOS Patch Management",
      subtitle: "macOS patch inventory and policies are planned for a future Borealis release.",
      label: "MacOS",
      Icon: MacOSPatchPageIcon,
    };
  }
  return {
    title: "Windows Patch Management",
    subtitle:
      "Ad-hoc installation of Windows Updates and patch policies. Policies are applied on a hierarchal granular level, where the deepest nested policies apply last.",
    label: "Windows",
    Icon: WindowsPatchPageIcon,
  };
}

export const PATCH_PAGE_TABS = [
  { key: "patch_list", label: "Patch List" },
  { key: "policies", label: "Patch Management Policies" },
];

export const PATCH_GRID_SX = {
  "--ag-background-color": "#070b1a",
  "--ag-foreground-color": "#f4f7ff",
  "--ag-header-background-color": "#0f172a",
  "--ag-header-foreground-color": "#cfe0ff",
  "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
  "--ag-row-hover-color": "rgba(73,156,196,0.2)",
  "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
  "--ag-border-color": "rgba(125,183,255,0.18)",
  "--ag-row-border-color": "rgba(125,183,255,0.14)",
  "--ag-border-radius": "8px",
  "& .ag-row-hover": {
    backgroundColor: "rgba(73,156,196,0.2) !important",
  },
  "& .ag-row-selected": {
    backgroundColor: "rgba(125,211,252,0.2) !important",
    boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
  },
  "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
  },
  "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
    width: "100%",
    height: "100%",
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    paddingTop: 0,
    paddingBottom: 0,
    minWidth: 0,
  },
  "& .ag-center-cols-container .ag-cell .ag-cell-value, & .ag-pinned-left-cols-container .ag-cell .ag-cell-value, & .ag-pinned-right-cols-container .ag-cell .ag-cell-value": {
    width: "100%",
    height: "100%",
    display: "flex",
    alignItems: "center",
    minWidth: 0,
  },
  "& .patch-chip-cell .ag-cell-wrapper, & .patch-chip-cell .ag-cell-value": {
    alignItems: "center",
  },
};

export function formatScheduleType(value) {
  const raw = normalizeText(value);
  if (!raw) return "";
  return raw
    .split(/[_\s-]+/)
    .filter(Boolean)
    .map((segment) => `${segment.charAt(0).toUpperCase()}${segment.slice(1).toLowerCase()}`)
    .join(" ");
}
