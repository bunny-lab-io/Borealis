import React, { Fragment, useMemo } from "react";
import { Box, Button, CircularProgress, Tooltip } from "@mui/material";

export const PAGE_HEADER_ACTION_FONT_FAMILY = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
export const PAGE_HEADER_ACTION_HEIGHT = 38;
export const PAGE_HEADER_ACTION_GAP = 1.25;
export const PAGE_HEADER_ACTION_TONE_ORDER = Object.freeze({
  danger: 0,
  warning: 0,
  secondary: 1,
  accent: 2,
  primary: 3,
});

const PAGE_HEADER_ACTION_BASE_SX = {
  minHeight: PAGE_HEADER_ACTION_HEIGHT,
  height: PAGE_HEADER_ACTION_HEIGHT,
  borderRadius: 999,
  px: 2.15,
  minWidth: 0,
  textTransform: "none",
  whiteSpace: "nowrap",
  fontWeight: 600,
  fontSize: "0.85rem",
  lineHeight: 1,
  fontFamily: PAGE_HEADER_ACTION_FONT_FAMILY,
  boxShadow: "none",
  borderWidth: 1,
  borderStyle: "solid",
  transition: "background 180ms ease, border-color 180ms ease, box-shadow 180ms ease, color 180ms ease, transform 120ms ease",
  "& .MuiButton-startIcon, & .MuiButton-endIcon": {
    marginInline: 0,
  },
  "& .MuiButton-startIcon": {
    marginRight: 0.75,
  },
  "& .MuiButton-endIcon": {
    marginLeft: 0.75,
  },
  "& .MuiSvgIcon-root": {
    fontSize: 18,
  },
  "&:hover": {
    boxShadow: "none",
    transform: "translateY(-0.5px)",
  },
  "&:active": {
    transform: "translateY(0.5px)",
  },
};

const PAGE_HEADER_ACTION_TONE_SX = {
  primary: {
    color: "#0b1220",
    borderColor: "transparent",
    backgroundImage: "linear-gradient(135deg,#7dd3fc,#c084fc)",
    boxShadow: "0 10px 26px rgba(124,58,237,0.28)",
    "&:hover": {
      backgroundImage: "linear-gradient(135deg,#86e1ff,#d1a6ff)",
      borderColor: "transparent",
      boxShadow: "0 12px 34px rgba(124,58,237,0.36)",
    },
    "&.Mui-disabled": {
      color: "rgba(226,232,240,0.7)",
      borderColor: "rgba(148,163,184,0.24)",
      backgroundImage: "linear-gradient(135deg, rgba(148,163,184,0.32), rgba(51,65,85,0.46))",
      boxShadow: "none",
    },
  },
  accent: {
    color: "#041317",
    borderColor: "transparent",
    backgroundImage: "linear-gradient(135deg,#34d399,#22d3ee)",
    boxShadow: "0 0 24px rgba(45,212,191,0.34)",
    "&:hover": {
      backgroundImage: "linear-gradient(135deg,#48e2a9,#44dbf6)",
      borderColor: "transparent",
      boxShadow: "0 0 28px rgba(45,212,191,0.42)",
    },
    "&.Mui-disabled": {
      color: "rgba(226,232,240,0.7)",
      borderColor: "rgba(148,163,184,0.24)",
      backgroundImage: "linear-gradient(135deg, rgba(148,163,184,0.28), rgba(51,65,85,0.4))",
      boxShadow: "none",
    },
  },
  secondary: {
    color: "#e2e8f0",
    borderColor: "rgba(148,163,184,0.38)",
    backgroundColor: "rgba(8,12,24,0.78)",
    backgroundImage: "none",
    "&:hover": {
      borderColor: "rgba(125,211,252,0.82)",
      backgroundColor: "rgba(16,24,44,0.95)",
      backgroundImage: "none",
    },
    "&.Mui-disabled": {
      color: "rgba(148,163,184,0.62)",
      borderColor: "rgba(148,163,184,0.24)",
      backgroundColor: "rgba(8,12,24,0.48)",
      backgroundImage: "none",
    },
  },
  warning: {
    color: "#ffd58b",
    borderColor: "rgba(251,191,36,0.58)",
    backgroundColor: "rgba(43,28,8,0.68)",
    backgroundImage: "none",
    "&:hover": {
      borderColor: "rgba(253,224,71,0.85)",
      backgroundColor: "rgba(56,37,10,0.82)",
      backgroundImage: "none",
    },
    "&.Mui-disabled": {
      color: "rgba(251,191,36,0.4)",
      borderColor: "rgba(251,191,36,0.22)",
      backgroundColor: "rgba(43,28,8,0.4)",
      backgroundImage: "none",
    },
  },
  danger: {
    color: "#fda4af",
    borderColor: "rgba(251,113,133,0.56)",
    backgroundColor: "rgba(49,11,24,0.58)",
    backgroundImage: "none",
    "&:hover": {
      borderColor: "rgba(251,113,133,0.82)",
      backgroundColor: "rgba(63,12,28,0.76)",
      backgroundImage: "none",
    },
    "&.Mui-disabled": {
      color: "rgba(251,113,133,0.42)",
      borderColor: "rgba(251,113,133,0.2)",
      backgroundColor: "rgba(49,11,24,0.36)",
      backgroundImage: "none",
    },
  },
};

export const PAGE_HEADER_CONTROL_SX = {
  minWidth: 180,
  "& .MuiInputLabel-root": {
    color: "#94a3b8",
    fontFamily: PAGE_HEADER_ACTION_FONT_FAMILY,
    fontSize: "0.82rem",
  },
  "& .MuiInputLabel-root.Mui-focused": {
    color: "#cbd5e1",
  },
  "& .MuiOutlinedInput-root": {
    minHeight: PAGE_HEADER_ACTION_HEIGHT,
    height: PAGE_HEADER_ACTION_HEIGHT,
    borderRadius: 999,
    backgroundColor: "rgba(8,12,24,0.78)",
    color: "#e2e8f0",
    fontFamily: PAGE_HEADER_ACTION_FONT_FAMILY,
    fontSize: "0.85rem",
    "& fieldset": {
      borderColor: "rgba(148,163,184,0.38)",
    },
    "&:hover fieldset": {
      borderColor: "rgba(125,211,252,0.82)",
    },
    "&.Mui-focused fieldset": {
      borderColor: "rgba(125,211,252,0.82)",
    },
  },
  "& .MuiSelect-select": {
    display: "flex",
    alignItems: "center",
    minHeight: "unset !important",
    paddingTop: "8px",
    paddingBottom: "8px",
  },
  "& .MuiSvgIcon-root": {
    color: "#8fbfff",
  },
};

export const PAGE_HEADER_BADGE_SX = {
  minHeight: PAGE_HEADER_ACTION_HEIGHT,
  px: 1.5,
  borderRadius: 999,
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  whiteSpace: "nowrap",
  fontFamily: PAGE_HEADER_ACTION_FONT_FAMILY,
  fontSize: "0.82rem",
  fontWeight: 600,
  color: "#a5e0ff",
  backgroundColor: "rgba(8,12,24,0.7)",
  border: "1px solid rgba(125,211,252,0.28)",
};

export function sortPageHeaderActions(actions = []) {
  return [...actions]
    .filter(Boolean)
    .map((action, index) => ({ action, index }))
    .sort((left, right) => {
      const leftPriority = PAGE_HEADER_ACTION_TONE_ORDER[left.action?.tone] ?? PAGE_HEADER_ACTION_TONE_ORDER.secondary;
      const rightPriority = PAGE_HEADER_ACTION_TONE_ORDER[right.action?.tone] ?? PAGE_HEADER_ACTION_TONE_ORDER.secondary;
      if (leftPriority !== rightPriority) {
        return leftPriority - rightPriority;
      }
      return left.index - right.index;
    })
    .map((entry) => entry.action);
}

function resolveActionIcon(icon) {
  if (!icon) return null;
  if (React.isValidElement(icon)) return icon;
  if (typeof icon === "function") {
    const IconComponent = icon;
    return <IconComponent fontSize="inherit" />;
  }
  return icon;
}

function PageHeaderActionButton({ action }) {
  const tone = PAGE_HEADER_ACTION_TONE_SX[action?.tone] ? action.tone : "secondary";
  const label = action?.label || "";
  const tooltip = action?.tooltip || "";
  const loading = Boolean(action?.loading);
  const icon = loading
    ? <CircularProgress size={16} thickness={5} sx={{ color: tone === "primary" || tone === "accent" ? "#041224" : "currentColor" }} />
    : resolveActionIcon(action?.icon);
  const button = (
    <Button
      variant={tone === "primary" || tone === "accent" ? "contained" : "outlined"}
      startIcon={icon}
      onClick={action?.onClick}
      disabled={Boolean(action?.disabled) || loading}
      sx={{
        ...PAGE_HEADER_ACTION_BASE_SX,
        ...PAGE_HEADER_ACTION_TONE_SX[tone],
        ...(action?.sx || {}),
      }}
    >
      {label}
    </Button>
  );

  if (!tooltip) {
    return button;
  }

  return (
    <Tooltip title={tooltip}>
      <span>{button}</span>
    </Tooltip>
  );
}

export function PageHeaderActionRail({
  actions = [],
  controls = [],
  badges = [],
  justifyContent = "flex-end",
  sx,
}) {
  const orderedActions = useMemo(() => sortPageHeaderActions(actions), [actions]);
  const hasContent = badges.length || controls.length || orderedActions.length;

  if (!hasContent) {
    return null;
  }

  return (
    <Box
      sx={{
        width: { xs: "100%", xl: "auto" },
        minWidth: 0,
        display: "flex",
        justifyContent,
        ...(sx || {}),
      }}
    >
      <Box
        sx={{
          minWidth: 0,
          display: "flex",
          alignItems: "center",
          justifyContent,
          flexWrap: "wrap",
          columnGap: PAGE_HEADER_ACTION_GAP,
          rowGap: 1,
        }}
      >
        {badges.map((badge, index) => (
          <Box key={badge?.key || `badge-${index}`} sx={{ display: "flex", alignItems: "center", minHeight: PAGE_HEADER_ACTION_HEIGHT }}>
            {badge}
          </Box>
        ))}
        {controls.map((control, index) => (
          <Box key={control?.key || `control-${index}`} sx={{ display: "flex", alignItems: "center", minHeight: PAGE_HEADER_ACTION_HEIGHT }}>
            {control}
          </Box>
        ))}
        {orderedActions.map((action, index) => (
          <Fragment key={action?.id || `action-${index}`}>
            <PageHeaderActionButton action={action} />
          </Fragment>
        ))}
      </Box>
    </Box>
  );
}
