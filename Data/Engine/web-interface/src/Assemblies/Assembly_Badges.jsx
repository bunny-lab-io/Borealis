import React from "react";
import { Box, Typography } from "@mui/material";

const DOMAIN_METADATA = {
  official: {
    label: "Official",
    textColor: "#89c2ff",
    backgroundColor: "rgba(88, 166, 255, 0.16)",
    borderColor: "rgba(88, 166, 255, 0.45)",
  },
  community: {
    label: "Community",
    textColor: "#d4b5ff",
    backgroundColor: "rgba(180, 137, 255, 0.18)",
    borderColor: "rgba(180, 137, 255, 0.38)",
  },
  user: {
    label: "User-Created",
    textColor: "#8fdaa2",
    backgroundColor: "rgba(56, 161, 105, 0.16)",
    borderColor: "rgba(56, 161, 105, 0.4)",
  },
};

export function resolveDomainMeta(domain) {
  const key = (domain || "").toLowerCase();
  return DOMAIN_METADATA[key] || {
    label: domain ? String(domain).charAt(0).toUpperCase() + String(domain).slice(1) : "Unknown",
    textColor: "#96a3b6",
    backgroundColor: "rgba(150, 163, 182, 0.14)",
    borderColor: "rgba(150, 163, 182, 0.32)",
  };
}

export function DomainBadge({ domain, size = "medium", sx }) {
  const meta = resolveDomainMeta(domain);
  const padding = size === "small" ? "2px 6px" : "4px 8px";
  const fontSize = size === "small" ? 11 : 12.5;
  return (
    <Box
      sx={{
        display: "inline-flex",
        alignItems: "center",
        borderRadius: 999,
        px: padding.split(" ")[1],
        py: padding.split(" ")[0],
        fontSize,
        gap: 0.5,
        fontWeight: 500,
        color: meta.textColor,
        border: `1px solid ${meta.borderColor}`,
        backgroundColor: meta.backgroundColor,
        textTransform: "none",
        ...sx,
      }}
    >
      <Typography
        component="span"
        sx={{
          fontSize,
          color: meta.textColor,
          lineHeight: 1,
          fontWeight: 500,
        }}
      >
        {meta.label}
      </Typography>
    </Box>
  );
}

export function DirtyStatePill({ compact = false, sx }) {
  const padding = compact ? "2px 6px" : "4px 8px";
  const fontSize = compact ? 11 : 12;
  return (
    <Box
      sx={{
        display: "inline-flex",
        alignItems: "center",
        borderRadius: 999,
        px: padding.split(" ")[1],
        py: padding.split(" ")[0],
        fontSize,
        gap: 0.5,
        fontWeight: 600,
        color: "#f8d47a",
        border: "1px solid rgba(248, 212, 122, 0.4)",
        backgroundColor: "rgba(248, 212, 122, 0.18)",
        textTransform: "none",
        ...sx,
      }}
    >
      <Typography
        component="span"
        sx={{
          fontSize,
          color: "#f8d47a",
          lineHeight: 1,
          fontWeight: 600,
        }}
      >
        Queued to Write to DB
      </Typography>
    </Box>
  );
}

export const DOMAIN_OPTIONS = [
  { value: "user", label: DOMAIN_METADATA.user.label },
  { value: "community", label: DOMAIN_METADATA.community.label },
  { value: "official", label: DOMAIN_METADATA.official.label },
];

