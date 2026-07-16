import React from "react";
import { Link as RouterLink } from "react-router-dom";
import { Link, Tooltip } from "@mui/material";

import { APP_PATHS } from "./app/routes/paths.js";

export const RESERVED_METADATA_TOOLTIP =
  "Reserved Borealis Metadata Field - Create a scheduled job using the hyperlinked assembly to collect data for this field.";

export function isReservedMetadataField(row) {
  return Boolean(row?.reserved);
}

export function getReservedAssemblyPath(row) {
  const assembly = row?.linked_assembly && typeof row.linked_assembly === "object" ? row.linked_assembly : {};
  const explicitPath = String(row?.linked_assembly_path || row?.assembly_path || assembly.path || "").trim();
  if (explicitPath) return explicitPath;
  const guid = String(row?.linked_assembly_guid || row?.assembly_guid || assembly.guid || "").trim();
  if (!guid) return "";
  const type = String(row?.linked_assembly_type || row?.assembly_type || assembly.type || "script")
    .trim()
    .toLowerCase();
  if (type === "ansible_playbook") return APP_PATHS.assemblyAnsible(guid);
  if (type === "workflow") return APP_PATHS.assemblyWorkflow(guid);
  return APP_PATHS.assemblyScript(guid);
}

export function getReservedAssemblyName(row) {
  const assembly = row?.linked_assembly && typeof row.linked_assembly === "object" ? row.linked_assembly : {};
  return String(row?.linked_assembly_name || row?.assembly_name || assembly.name || "").trim();
}

export function FieldNumberCellRenderer(props) {
  const { data, value } = props;
  const safeValue = typeof value === "string" ? value : value == null ? "" : String(value);
  const assemblyPath = isReservedMetadataField(data) ? getReservedAssemblyPath(data) : "";
  if (!assemblyPath) return safeValue;
  const tooltip = getReservedAssemblyName(data) || safeValue;
  return (
    <Tooltip title={tooltip} arrow placement="top-start" describeChild>
      <Link
        component={RouterLink}
        to={assemblyPath}
        underline="none"
        onClick={(event) => event.stopPropagation()}
        sx={{
          color: "#7dd3fc",
          fontWeight: 700,
          textDecoration: "none",
          "&:hover": {
            color: "#bae6fd",
            textDecoration: "none",
          },
        }}
      >
        {safeValue}
      </Link>
    </Tooltip>
  );
}
