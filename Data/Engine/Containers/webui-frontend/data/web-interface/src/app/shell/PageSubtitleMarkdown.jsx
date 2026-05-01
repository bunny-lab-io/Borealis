import React from "react";
import { Link as MuiLink } from "@mui/material";
import { Link as RouterLink } from "react-router-dom";

const PAGE_SUBTITLE_LINK_PATTERN = /\[([^\]]+)\]\(([^)]+)\)/g;
const PAGE_SUBTITLE_LINK_SX = {
  color: "#7db7ff",
  fontWeight: 500,
  textDecoration: "none",
  "&:hover": {
    color: "#9fcbff",
    textDecoration: "none",
  },
};

export function parsePageSubtitleMarkdown(value) {
  const subtitle = typeof value === "string" ? value : "";
  if (!subtitle) {
    return [];
  }

  const parts = [];
  let lastIndex = 0;

  for (const match of subtitle.matchAll(PAGE_SUBTITLE_LINK_PATTERN)) {
    const raw = match[0] || "";
    const label = match[1]?.trim() || "";
    const href = match[2]?.trim() || "";
    const startIndex = typeof match.index === "number" ? match.index : lastIndex;

    if (startIndex > lastIndex) {
      parts.push({
        type: "text",
        value: subtitle.slice(lastIndex, startIndex),
      });
    }

    if (label && href) {
      parts.push({
        type: "link",
        label,
        href,
        raw,
      });
    } else {
      parts.push({
        type: "text",
        value: raw,
      });
    }

    lastIndex = startIndex + raw.length;
  }

  if (lastIndex < subtitle.length) {
    parts.push({
      type: "text",
      value: subtitle.slice(lastIndex),
    });
  }

  return parts.length
    ? parts
    : [
        {
          type: "text",
          value: subtitle,
        },
      ];
}

function getSubtitleHrefKind(href) {
  if (/^(https?:)?\/\//i.test(href)) {
    return "external-web";
  }
  if (/^(mailto:|tel:)/i.test(href)) {
    return "external";
  }
  if (/^(\/|\.\/|\.\.\/|\?)/.test(href)) {
    return "route";
  }
  if (/^#/.test(href)) {
    return "anchor";
  }
  return "invalid";
}

export default function PageSubtitleMarkdown({ text }) {
  const parts = parsePageSubtitleMarkdown(text);
  if (!parts.length) {
    return null;
  }

  return parts.map((part, index) => {
    if (part.type !== "link") {
      return <React.Fragment key={`subtitle-text-${index}`}>{part.value}</React.Fragment>;
    }

    const hrefKind = getSubtitleHrefKind(part.href);

    if (hrefKind === "route") {
      return (
        <MuiLink
          key={`subtitle-link-${index}`}
          component={RouterLink}
          to={part.href}
          sx={PAGE_SUBTITLE_LINK_SX}
        >
          {part.label}
        </MuiLink>
      );
    }

    if (hrefKind === "anchor") {
      return (
        <MuiLink
          key={`subtitle-link-${index}`}
          href={part.href}
          sx={PAGE_SUBTITLE_LINK_SX}
        >
          {part.label}
        </MuiLink>
      );
    }

    if (hrefKind === "external-web" || hrefKind === "external") {
      return (
        <MuiLink
          key={`subtitle-link-${index}`}
          href={part.href}
          target={hrefKind === "external-web" ? "_blank" : undefined}
          rel={hrefKind === "external-web" ? "noopener noreferrer" : undefined}
          sx={PAGE_SUBTITLE_LINK_SX}
        >
          {part.label}
        </MuiLink>
      );
    }

    return <React.Fragment key={`subtitle-invalid-${index}`}>{part.raw}</React.Fragment>;
  });
}
