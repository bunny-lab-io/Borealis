import React from "react";
import { Box, Divider, Menu, MenuItem, Tooltip, Typography } from "@mui/material";

const TEXT_BRIGHT = "#e2e8f0";
const TEXT_MUTED = "rgba(148,163,184,0.78)";

export const ROW_CONTEXT_MENU_GROUP_LABELS = {
  primary: "Primary",
  organize: "Organize",
  danger: "Danger Zone",
  view: "View",
};

export const ROW_CONTEXT_MENU_GROUP_ORDER = ["primary", "organize", "danger", "view"];

export const ROW_CONTEXT_MENU_PAPER_WIDTHS = {
  compact: 236,
  standard: 288,
  extended: 340,
};

export const ROW_CONTEXT_MENU_PAPER_SX = {
  bgcolor: "rgba(8,12,24,0.96)",
  border: "1px solid rgba(148,163,184,0.35)",
  backdropFilter: "blur(14px)",
  borderRadius: 2,
  px: 0.8,
  py: 0.8,
  boxShadow: "0 18px 46px rgba(2,8,23,0.85)",
};

const ROW_CONTEXT_MENU_HEADER_SX = {
  display: "flex",
  alignItems: "center",
  gap: 1,
  px: 1.1,
  pt: 0.55,
  pb: 0.85,
};

const ROW_CONTEXT_MENU_HEADER_ICON_SX = {
  width: 32,
  height: 32,
  borderRadius: 1.35,
  flexShrink: 0,
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  border: "1px solid rgba(148,163,184,0.14)",
  background: "rgba(255,255,255,0.04)",
  color: "#8fd3ff",
};

const ROW_CONTEXT_MENU_SECTION_LABEL_SX = {
  px: 1.2,
  pt: 0.65,
  pb: 0.45,
  color: "rgba(148,163,184,0.72)",
  fontSize: "0.68rem",
  fontWeight: 700,
  letterSpacing: "0.08em",
  textTransform: "uppercase",
};

const ROW_CONTEXT_MENU_DIVIDER_SX = {
  my: 0.55,
  borderColor: "rgba(148,163,184,0.16)",
};

const ROW_CONTEXT_MENU_ITEM_SX = {
  minHeight: 42,
  borderRadius: 1.6,
  color: TEXT_BRIGHT,
  alignItems: "center",
  px: 1,
  py: 0.85,
  position: "relative",
  overflow: "hidden",
  "&:hover": {
    backgroundColor: "rgba(88,166,255,0.12)",
  },
  "&::before": {
    content: '""',
    position: "absolute",
    left: 0,
    top: 8,
    bottom: 8,
    width: 3,
    borderRadius: 999,
    background: "transparent",
    transition: "background-color 0.16s ease",
  },
  "&:hover::before": {
    background: "#58a6ff",
  },
};

const ROW_CONTEXT_MENU_DANGER_ITEM_SX = {
  ...ROW_CONTEXT_MENU_ITEM_SX,
  "&:hover": {
    backgroundColor: "rgba(248,113,113,0.1)",
  },
  "&:hover::before": {
    background: "#58a6ff",
  },
};

const ROW_CONTEXT_MENU_ROW_ICON_SX = {
  mt: 0.18,
  mr: 1,
  fontSize: 18,
  flexShrink: 0,
};

const ROW_CONTEXT_MENU_LABEL_SX = {
  color: TEXT_BRIGHT,
  fontSize: "0.84rem",
  fontWeight: 500,
  lineHeight: 1.2,
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

const ROW_CONTEXT_MENU_DESCRIPTION_SX = {
  color: TEXT_MUTED,
  fontSize: "0.73rem",
  lineHeight: 1.25,
  mt: 0.25,
};

const ROW_CONTEXT_MENU_TITLE_TRUNCATE_SX = {
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

function menuWidthForVariant(widthVariant) {
  return ROW_CONTEXT_MENU_PAPER_WIDTHS[widthVariant] || ROW_CONTEXT_MENU_PAPER_WIDTHS.standard;
}

function renderIcon(icon, sx = {}) {
  if (!icon) return null;
  if (React.isValidElement(icon)) {
    return React.cloneElement(icon, {
      sx: {
        ...sx,
        ...(icon.props?.sx || {}),
      },
    });
  }
  const IconComponent = icon;
  return <IconComponent sx={sx} />;
}

export function buildContextMenuGroups(actions = [], groupOrder = ROW_CONTEXT_MENU_GROUP_ORDER) {
  const visibleActions = (Array.isArray(actions) ? actions : []).filter((action) => action && !action.hidden);
  return groupOrder
    .map((groupId) => ({
      id: groupId,
      label: ROW_CONTEXT_MENU_GROUP_LABELS[groupId] || groupId,
      actions: visibleActions.filter((action) => (action.group || "primary") === groupId),
    }))
    .filter((group) => group.actions.length);
}

export default function RowContextMenu({
  open,
  onClose,
  position,
  headerIcon,
  title = "",
  subtitle = "",
  actions = [],
  widthVariant = "standard",
  groupOrder = ROW_CONTEXT_MENU_GROUP_ORDER,
}) {
  const groups = buildContextMenuGroups(actions, groupOrder);
  const width = menuWidthForVariant(widthVariant);
  const titleMaxWidth = Math.max(180, width - 48);
  const actionNodes = [];

  if (groups.length) {
    groups.forEach((group) => {
      actionNodes.push(<Divider key={`divider-before-${group.id}`} component="li" sx={ROW_CONTEXT_MENU_DIVIDER_SX} />);
      actionNodes.push(
        <Box key={`label-${group.id}`} component="li" role="presentation" sx={ROW_CONTEXT_MENU_SECTION_LABEL_SX}>
          {group.label}
        </Box>
      );
      group.actions.forEach((action) => {
        const helperText = action.disabledReason || action.description || "";
        actionNodes.push(
          <MenuItem
            key={action.id}
            disabled={Boolean(action.disabled)}
            onClick={() => {
              onClose?.();
              action.onClick?.();
            }}
            sx={action.intent === "danger" ? ROW_CONTEXT_MENU_DANGER_ITEM_SX : ROW_CONTEXT_MENU_ITEM_SX}
          >
            {renderIcon(action.icon, {
              ...ROW_CONTEXT_MENU_ROW_ICON_SX,
              color: action.intent === "danger" ? "rgba(248,113,113,0.92)" : "rgba(226,232,240,0.92)",
            })}
            <Box
              sx={{
                flex: 1,
                minWidth: 0,
                display: "flex",
                flexDirection: "column",
                justifyContent: helperText ? "flex-start" : "center",
              }}
            >
              <Typography sx={ROW_CONTEXT_MENU_LABEL_SX}>{action.label}</Typography>
              {helperText ? <Typography sx={ROW_CONTEXT_MENU_DESCRIPTION_SX}>{helperText}</Typography> : null}
            </Box>
          </MenuItem>
        );
      });
    });
  } else {
    actionNodes.push(
      <MenuItem key="no-actions" disabled sx={ROW_CONTEXT_MENU_ITEM_SX}>
        <Box sx={{ minWidth: 0 }}>
          <Typography sx={ROW_CONTEXT_MENU_LABEL_SX}>No Actions Available</Typography>
        </Box>
      </MenuItem>
    );
  }

  return (
    <Menu
      open={Boolean(open)}
      onClose={onClose}
      anchorReference="anchorPosition"
      anchorPosition={
        open && position
          ? {
              top: Number(position.top || 0),
              left: Number(position.left || 0),
            }
          : undefined
      }
      PaperProps={{ sx: { ...ROW_CONTEXT_MENU_PAPER_SX, minWidth: width, maxWidth: width } }}
    >
      <Box component="li" role="presentation" sx={ROW_CONTEXT_MENU_HEADER_SX}>
        <Box sx={ROW_CONTEXT_MENU_HEADER_ICON_SX}>{renderIcon(headerIcon, { fontSize: 19 })}</Box>
        <Box sx={{ minWidth: 0 }}>
          <Tooltip title={title || ""} placement="top-start">
            <Typography
              sx={{
                ...ROW_CONTEXT_MENU_TITLE_TRUNCATE_SX,
                color: TEXT_BRIGHT,
                fontSize: "0.88rem",
                fontWeight: 600,
                lineHeight: 1.2,
                maxWidth: titleMaxWidth,
              }}
            >
              {title || "Actions"}
            </Typography>
          </Tooltip>
          <Tooltip title={subtitle || ""} placement="top-start">
            <Typography
              sx={{
                ...ROW_CONTEXT_MENU_TITLE_TRUNCATE_SX,
                color: "rgba(148,163,184,0.82)",
                fontSize: "0.73rem",
                lineHeight: 1.25,
                mt: 0.22,
                maxWidth: titleMaxWidth,
              }}
            >
              {subtitle || "Row actions"}
            </Typography>
          </Tooltip>
        </Box>
      </Box>

      {actionNodes}
    </Menu>
  );
}
