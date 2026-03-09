import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Button,
  CircularProgress,
  ListItemIcon,
  ListItemText,
  Menu,
  MenuItem,
  Tooltip,
} from "@mui/material";
import MoreHorizIcon from "@mui/icons-material/MoreHoriz";

const PAGE_HEADER_FONT_FAMILY = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const PAGE_HEADER_ACTION_HEIGHT = 38;
const PAGE_HEADER_ACTION_GAP = 1;
const PAGE_HEADER_PRIMARY_BG = "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)";

const BASE_BUTTON_SX = {
  minHeight: PAGE_HEADER_ACTION_HEIGHT,
  height: PAGE_HEADER_ACTION_HEIGHT,
  px: 1.8,
  borderRadius: 999,
  fontFamily: PAGE_HEADER_FONT_FAMILY,
  fontWeight: 600,
  fontSize: "0.92rem",
  lineHeight: 1,
  textTransform: "none",
  whiteSpace: "nowrap",
  boxSizing: "border-box",
  borderWidth: "1px",
  borderStyle: "solid",
  transition:
    "background 160ms ease, border-color 160ms ease, color 160ms ease, box-shadow 160ms ease, transform 120ms ease, opacity 120ms ease",
  "& .MuiButton-startIcon": {
    mr: 0.8,
  },
  "&:hover": {
    transform: "translateY(-0.5px)",
  },
  "&:active": {
    transform: "translateY(0)",
  },
  "&.Mui-disabled": {
    opacity: 0.42,
    color: "rgba(226, 232, 240, 0.7)",
    borderColor: "rgba(148, 163, 184, 0.22)",
    background: "rgba(51, 65, 85, 0.5)",
  },
};

const SECONDARY_BUTTON_SX = {
  ...BASE_BUTTON_SX,
  color: "#e8eef9",
  borderColor: "rgba(148, 163, 184, 0.34)",
  background: "rgba(5, 10, 24, 0.84)",
  boxShadow: "0 10px 24px rgba(2, 6, 23, 0.3)",
  "&:hover": {
    ...BASE_BUTTON_SX["&:hover"],
    borderColor: "rgba(125, 211, 252, 0.52)",
    background: "rgba(8, 14, 30, 0.92)",
    boxShadow: "0 14px 32px rgba(2, 6, 23, 0.42)",
  },
};

const PRIMARY_BUTTON_SX = {
  ...BASE_BUTTON_SX,
  color: "#06101d",
  borderColor: "transparent",
  background: PAGE_HEADER_PRIMARY_BG,
  boxShadow: "0 16px 34px rgba(125, 211, 252, 0.18)",
  "&:hover": {
    ...BASE_BUTTON_SX["&:hover"],
    background: "linear-gradient(135deg, #91dcff 0%, #cfa0ff 100%)",
    boxShadow: "0 18px 36px rgba(192, 132, 252, 0.22)",
  },
  "&.Mui-disabled": {
    ...BASE_BUTTON_SX["&.Mui-disabled"],
    color: "rgba(9, 18, 33, 0.6)",
    background: "linear-gradient(135deg, rgba(125, 211, 252, 0.38) 0%, rgba(192, 132, 252, 0.34) 100%)",
    borderColor: "transparent",
  },
};

const WARNING_BUTTON_SX = {
  ...BASE_BUTTON_SX,
  color: "#ffd58a",
  borderColor: "rgba(245, 158, 11, 0.55)",
  background: "rgba(35, 22, 4, 0.7)",
  "&:hover": {
    ...BASE_BUTTON_SX["&:hover"],
    borderColor: "rgba(251, 191, 36, 0.7)",
    background: "rgba(48, 30, 6, 0.82)",
  },
};

const DANGER_BUTTON_SX = {
  ...BASE_BUTTON_SX,
  color: "#ff8c98",
  borderColor: "rgba(244, 63, 94, 0.38)",
  background: "rgba(44, 8, 22, 0.58)",
  "&:hover": {
    ...BASE_BUTTON_SX["&:hover"],
    borderColor: "rgba(251, 113, 133, 0.58)",
    background: "rgba(58, 10, 28, 0.72)",
  },
};

export const PAGE_HEADER_CONTROL_SX = {
  "& .MuiInputLabel-root": {
    color: "rgba(203, 213, 225, 0.82)",
    fontFamily: PAGE_HEADER_FONT_FAMILY,
    fontSize: "0.82rem",
  },
  "& .MuiInputLabel-root.Mui-focused": {
    color: "#9fd8ff",
  },
  "& .MuiOutlinedInput-root, & .MuiInputBase-root": {
    minHeight: PAGE_HEADER_ACTION_HEIGHT,
    height: PAGE_HEADER_ACTION_HEIGHT,
    borderRadius: 999,
    background: "rgba(5, 10, 24, 0.84)",
    color: "#e8eef9",
    fontFamily: PAGE_HEADER_FONT_FAMILY,
    fontSize: "0.92rem",
    boxShadow: "0 10px 24px rgba(2, 6, 23, 0.28)",
    "& fieldset": {
      borderColor: "rgba(148, 163, 184, 0.34)",
    },
    "&:hover fieldset": {
      borderColor: "rgba(125, 211, 252, 0.52)",
    },
    "&.Mui-focused fieldset": {
      borderColor: "#7dd3fc",
    },
  },
  "& .MuiInputBase-input, & .MuiSelect-select": {
    minHeight: `${PAGE_HEADER_ACTION_HEIGHT}px`,
    boxSizing: "border-box",
    display: "flex",
    alignItems: "center",
    py: 0,
    px: 1.6,
  },
  "& .MuiSvgIcon-root": {
    color: "#9fd8ff",
  },
};

function getToneButtonSx(tone) {
  switch (String(tone || "").toLowerCase()) {
    case "primary":
      return PRIMARY_BUTTON_SX;
    case "warning":
      return WARNING_BUTTON_SX;
    case "danger":
      return DANGER_BUTTON_SX;
    case "secondary":
    default:
      return SECONDARY_BUTTON_SX;
  }
}

function createOverflowEvent(sourceEvent, anchorEl) {
  return {
    currentTarget: anchorEl || sourceEvent?.currentTarget || null,
    target: anchorEl || sourceEvent?.target || null,
    nativeEvent: sourceEvent?.nativeEvent,
    preventDefault: () => sourceEvent?.preventDefault?.(),
    stopPropagation: () => sourceEvent?.stopPropagation?.(),
  };
}

function HeaderActionButton({ action, onClick, measureOnly = false }) {
  const tone = String(action?.tone || "secondary").toLowerCase();
  const buttonContent = (
    <Button
      component={measureOnly ? "div" : "button"}
      disableElevation
      disableRipple={measureOnly}
      disableFocusRipple={measureOnly}
      startIcon={
        action?.loading ? (
          <CircularProgress
            size={15}
            thickness={5}
            sx={{ color: tone === "primary" ? "#06101d" : "currentColor" }}
          />
        ) : action?.icon ? (
          action.icon
        ) : null
      }
      onClick={measureOnly ? undefined : onClick}
      disabled={Boolean(action?.disabled || action?.loading)}
      sx={getToneButtonSx(tone)}
    >
      {action?.label || ""}
    </Button>
  );

  if (measureOnly || !action?.tooltip) {
    return buttonContent;
  }

  return (
    <Tooltip title={action.tooltip} arrow>
      <span>{buttonContent}</span>
    </Tooltip>
  );
}

export function PageHeaderActionRail({ actions = [], controls = [] }) {
  const normalizedActions = useMemo(
    () => (Array.isArray(actions) ? actions.filter(Boolean) : []),
    [actions]
  );
  const normalizedControls = useMemo(
    () => (Array.isArray(controls) ? controls.filter(Boolean) : []),
    [controls]
  );
  const secondaryActions = useMemo(
    () =>
      normalizedActions.filter(
        (action) => String(action?.tone || "secondary").toLowerCase() === "secondary"
      ),
    [normalizedActions]
  );
  const overflowActions = useMemo(() => secondaryActions.slice().reverse(), [secondaryActions]);
  const hasContent = normalizedControls.length > 0 || normalizedActions.length > 0;
  const canCollapseSecondaryActions = secondaryActions.length > 0;

  const containerRef = useRef(null);
  const controlsRef = useRef(null);
  const measureActionsRef = useRef(null);
  const overflowButtonRef = useRef(null);
  const [collapseSecondaryActions, setCollapseSecondaryActions] = useState(false);
  const [overflowAnchorEl, setOverflowAnchorEl] = useState(null);

  const measureOverflow = useCallback(() => {
    if (!canCollapseSecondaryActions) {
      setCollapseSecondaryActions(false);
      return;
    }

    const containerWidth = containerRef.current?.getBoundingClientRect?.().width || 0;
    const controlsWidth = controlsRef.current?.getBoundingClientRect?.().width || 0;
    const fullActionWidth = measureActionsRef.current?.scrollWidth || 0;
    if (!containerWidth || !fullActionWidth) return;

    const gapWidth = normalizedControls.length > 0 && normalizedActions.length > 0 ? 8 : 0;
    const requiredWidth = controlsWidth + gapWidth + fullActionWidth;
    const shouldCollapse = requiredWidth > containerWidth + 1;
    setCollapseSecondaryActions((prev) => (prev === shouldCollapse ? prev : shouldCollapse));
  }, [canCollapseSecondaryActions, normalizedActions.length, normalizedControls.length]);

  useEffect(() => {
    if (!hasContent) return undefined;

    const scheduleMeasure = () => {
      if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
        window.requestAnimationFrame(() => {
          measureOverflow();
        });
        return;
      }
      setTimeout(measureOverflow, 0);
    };

    scheduleMeasure();

    if (typeof ResizeObserver === "undefined") {
      if (typeof window === "undefined") return undefined;
      window.addEventListener("resize", measureOverflow);
      return () => window.removeEventListener("resize", measureOverflow);
    }

    const observer = new ResizeObserver(() => {
      measureOverflow();
    });

    [containerRef.current, controlsRef.current, measureActionsRef.current]
      .filter(Boolean)
      .forEach((node) => observer.observe(node));

    return () => observer.disconnect();
  }, [hasContent, measureOverflow]);

  useEffect(() => {
    if (!collapseSecondaryActions && overflowAnchorEl) {
      setOverflowAnchorEl(null);
    }
  }, [collapseSecondaryActions, overflowAnchorEl]);

  const handleActionClick = useCallback((action) => {
    return (event) => {
      if (!action || action.disabled || action.loading) return;
      action.onClick?.(event);
    };
  }, []);

  const handleOverflowActionClick = useCallback((action) => {
    return (event) => {
      setOverflowAnchorEl(null);
      if (!action || action.disabled || action.loading) return;
      action.onClick?.(createOverflowEvent(event, overflowButtonRef.current));
    };
  }, []);

  const visibleActions = useMemo(() => {
    if (!collapseSecondaryActions) {
      return normalizedActions;
    }

    const firstSecondaryIndex = normalizedActions.findIndex(
      (action) => String(action?.tone || "secondary").toLowerCase() === "secondary"
    );
    if (firstSecondaryIndex < 0) {
      return normalizedActions;
    }

    const withoutSecondaryActions = normalizedActions.filter(
      (action) => String(action?.tone || "secondary").toLowerCase() !== "secondary"
    );

    withoutSecondaryActions.splice(firstSecondaryIndex, 0, {
      id: "__overflow__",
      label: "Actions",
      icon: <MoreHorizIcon />,
      tone: "secondary",
      onClick: (event) => setOverflowAnchorEl(event.currentTarget),
    });

    return withoutSecondaryActions;
  }, [collapseSecondaryActions, normalizedActions]);

  if (!hasContent) {
    return null;
  }

  return (
    <Box
      ref={containerRef}
      sx={{
        width: { xs: "100%", xl: "auto" },
        minWidth: 0,
        display: "flex",
        flexShrink: 0,
        justifyContent: { xs: "flex-start", xl: "flex-end" },
        position: "relative",
      }}
    >
      <Box
        sx={{
          position: "absolute",
          visibility: "hidden",
          pointerEvents: "none",
          height: 0,
          overflow: "hidden",
          whiteSpace: "nowrap",
        }}
      >
        <Box ref={measureActionsRef} sx={{ display: "inline-flex", alignItems: "center", gap: PAGE_HEADER_ACTION_GAP }}>
          {normalizedActions.map((action) => (
            <HeaderActionButton
              key={`measure-${action.id || action.label}`}
              action={action}
              measureOnly
            />
          ))}
        </Box>
      </Box>

      <Box
        sx={{
          width: { xs: "100%", xl: "auto" },
          ml: { xs: 0, xl: "auto" },
          maxWidth: { xs: "100%", xl: "none" },
          minWidth: 0,
          display: "flex",
          alignItems: "center",
          gap: PAGE_HEADER_ACTION_GAP,
          overflowX: "auto",
          overflowY: "hidden",
          py: 0.25,
          "&::-webkit-scrollbar": {
            display: "none",
          },
          scrollbarWidth: "none",
        }}
      >
        {normalizedControls.length ? (
          <Box
            ref={controlsRef}
            sx={{ display: "flex", alignItems: "center", gap: PAGE_HEADER_ACTION_GAP, flexShrink: 0 }}
          >
            {normalizedControls.map((control, index) => (
              <Box key={`control-${index}`} sx={{ flexShrink: 0 }}>
                {control}
              </Box>
            ))}
          </Box>
        ) : null}

        <Box sx={{ display: "flex", alignItems: "center", gap: PAGE_HEADER_ACTION_GAP, flexShrink: 0 }}>
          {visibleActions.map((action) => {
            const key = action.id || action.label;
            const isOverflowButton = key === "__overflow__";
            return (
              <Box
                key={key}
                ref={isOverflowButton ? overflowButtonRef : undefined}
                sx={{ display: "inline-flex" }}
              >
                <HeaderActionButton action={action} onClick={handleActionClick(action)} />
              </Box>
            );
          })}
        </Box>
      </Box>

      <Menu
        anchorEl={overflowAnchorEl}
        open={Boolean(overflowAnchorEl)}
        onClose={() => setOverflowAnchorEl(null)}
        PaperProps={{
          sx: {
            mt: 1,
            minWidth: 220,
            borderRadius: 2,
            border: "1px solid rgba(148, 163, 184, 0.22)",
            background: "rgba(8, 12, 24, 0.96)",
            backdropFilter: "blur(18px) saturate(135%)",
            color: "#e8eef9",
            boxShadow: "0 18px 38px rgba(2, 6, 23, 0.5)",
          },
        }}
      >
        {overflowActions.map((action) => (
          <MenuItem
            key={`overflow-${action.id || action.label}`}
            disabled={Boolean(action.disabled || action.loading)}
            onClick={handleOverflowActionClick(action)}
            sx={{
              minHeight: 40,
              fontFamily: PAGE_HEADER_FONT_FAMILY,
              fontSize: "0.92rem",
            }}
          >
            {action.icon || action.loading ? (
              <ListItemIcon sx={{ minWidth: 34, color: "inherit" }}>
                {action.loading ? <CircularProgress size={16} thickness={5} sx={{ color: "#9fd8ff" }} /> : action.icon}
              </ListItemIcon>
            ) : null}
            <ListItemText primary={action.label} />
          </MenuItem>
        ))}
      </Menu>
    </Box>
  );
}
