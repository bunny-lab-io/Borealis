import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useLoaderData } from "react-router-dom";
import { Alert, Box, LinearProgress, TextField, Tooltip, Typography } from "@mui/material";
import LabelRoundedIcon from "@mui/icons-material/LabelRounded";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";

import PageBodyFrame from "../PageBodyFrame.jsx";
import {
  FieldNumberCellRenderer,
  RESERVED_METADATA_TOOLTIP,
  isReservedMetadataField,
} from "../Metadata_Field_Cells.jsx";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import {
  createRouteRequestPlan,
  fetchRouteJson,
  getRouteErrorMessage,
  requireAuthenticatedRequest,
  rethrowIfRouteRedirect,
} from "../app/routes/routeData.js";

ModuleRegistry.registerModules([AllCommunityModule]);

const PAGE_ICON = LabelRoundedIcon;
const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';

const gridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 14,
});

const GRID_SX = {
  width: "100%",
  height: "100%",
  minHeight: 520,
  fontFamily: gridFontFamily,
  "--ag-icon-font-family": iconFontFamily,
  "& .ag-root-wrapper": {
    border: "1px solid rgba(148,163,184,0.28)",
    borderRadius: 2,
    background: "rgba(5,8,20,0.92)",
  },
  "& .ag-root, & .ag-header, & .ag-center-cols-container, & .ag-paging-panel": {
    fontFamily: gridFontFamily,
  },
  "& .ag-header": {
    backgroundColor: "rgba(15,23,42,0.9)",
    borderBottom: "1px solid rgba(148,163,184,0.25)",
  },
  "& .ag-row": {
    borderColor: "rgba(255,255,255,0.04)",
  },
  "& .ag-row:nth-of-type(even)": {
    backgroundColor: "rgba(15,23,42,0.38)",
  },
  "& .ag-row-hover": {
    backgroundColor: "rgba(73,156,196,0.18) !important",
  },
};

function formatTimestamp(value) {
  if (!value) return "";
  const numeric = Number(value);
  const date = Number.isFinite(numeric) && numeric > 0 ? new Date(numeric * 1000) : new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString();
}

export function DescriptionCellRenderer(props) {
  const { data, value, onSaveDescription } = props;
  const safeValue = typeof value === "string" ? value : value == null ? "" : String(value);
  const [draft, setDraft] = useState(safeValue);
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!editing && !saving) setDraft(safeValue);
  }, [editing, safeValue, saving]);

  const finishSave = useCallback(async () => {
    const nextValue = String(draft || "").trim();
    if (nextValue === safeValue.trim()) {
      setEditing(false);
      setError("");
      return;
    }
    if (typeof onSaveDescription !== "function" || !data) {
      setEditing(false);
      return;
    }
    setSaving(true);
    setError("");
    const ok = await onSaveDescription(data, nextValue);
    setSaving(false);
    if (ok) {
      setEditing(false);
      return;
    }
    setError("Failed to save description");
  }, [data, draft, onSaveDescription, safeValue]);

  if (isReservedMetadataField(data)) {
    const tooltip = String(data?.reserved_tooltip || RESERVED_METADATA_TOOLTIP);
    const reservedLabel = safeValue || data?.label || "";
    return (
      <Tooltip title={tooltip} arrow placement="top-start">
        <Box component="span" sx={{ display: "block" }}>
          <TextField
            value={reservedLabel}
            variant="outlined"
            size="small"
            fullWidth
            InputProps={{ readOnly: true }}
            onClick={(event) => event.stopPropagation()}
            onMouseDown={(event) => event.stopPropagation()}
            onFocus={(event) => event.stopPropagation()}
            sx={{
              mt: 0.45,
              mb: 0.45,
              "& .MuiOutlinedInput-root": {
                height: 34,
                color: "#dff7ff",
                fontFamily: gridFontFamily,
                fontSize: "0.875rem",
                backgroundColor: "rgba(14,116,144,0.18)",
                "& fieldset": {
                  borderColor: "rgba(125,211,252,0.45)",
                },
                "&:hover fieldset": {
                  borderColor: "#7dd3fc",
                },
                "&.Mui-focused fieldset": {
                  borderColor: "#7dd3fc",
                },
              },
              "& .MuiOutlinedInput-input": {
                py: 0.75,
                px: 1.5,
                cursor: "help",
              },
            }}
          />
        </Box>
      </Tooltip>
    );
  }

  return (
    <TextField
      value={draft}
      placeholder={data?.default_label || "Field"}
      onFocus={(event) => {
        event.stopPropagation();
        setEditing(true);
        setError("");
      }}
      onChange={(event) => setDraft(event.target.value)}
      onKeyDown={(event) => {
        event.stopPropagation();
        if (event.key === "Enter") {
          event.preventDefault();
          void finishSave();
        } else if (event.key === "Escape") {
          event.preventDefault();
          setDraft(safeValue);
          setEditing(false);
          setError("");
        }
      }}
      onBlur={(event) => {
        event.stopPropagation();
        if (!saving) void finishSave();
      }}
      onClick={(event) => event.stopPropagation()}
      onMouseDown={(event) => event.stopPropagation()}
      variant="outlined"
      size="small"
      fullWidth
      disabled={saving}
      error={Boolean(error)}
      helperText={error || undefined}
      FormHelperTextProps={error ? { sx: { fontSize: "0.72rem", minHeight: 17 } } : { sx: { display: "none" } }}
      sx={{
        mt: 0.45,
        mb: 0.45,
        "& .MuiOutlinedInput-root": {
          height: 34,
          color: "rgba(255,255,255,0.88)",
          fontFamily: gridFontFamily,
          fontSize: "0.875rem",
          backgroundColor: saving
            ? "rgba(255,255,255,0.04)"
            : editing
              ? "rgba(255,255,255,0.14)"
              : "rgba(255,255,255,0.02)",
          "& fieldset": {
            borderColor: editing ? "#7dd3fc" : "rgba(255,255,255,0.24)",
          },
          "&:hover fieldset": {
            borderColor: "#7dd3fc",
          },
          "&.Mui-focused fieldset": {
            borderColor: "#7dd3fc",
          },
        },
        "& .MuiOutlinedInput-input": {
          py: 0.75,
          px: 1.5,
        },
        "& .MuiFormHelperText-root": {
          color: "#ff7b7b",
          mt: 0.25,
        },
      }}
    />
  );
}

export async function loadMetadataFieldsPageData(request) {
  const progress = createRouteRequestPlan(request, 2);
  try {
    await requireAuthenticatedRequest(request, progress);
    const payload = await progress.fetchJson("/api/metadata_fields");
    return {
      fields: Array.isArray(payload?.fields) ? payload.fields : [],
      initialError: "",
    };
  } catch (error) {
    rethrowIfRouteRedirect(error);
    return {
      fields: [],
      initialError: getRouteErrorMessage(error, "Unable to load metadata fields."),
    };
  } finally {
    progress.finalize();
  }
}

export function buildMetadataFieldColumnDefs({ saveDescription }) {
  return [
    {
      headerName: "Field Number",
      field: "default_label",
      width: 160,
      pinned: "left",
      sortable: true,
      filter: "agTextColumnFilter",
      resizable: false,
      cellRenderer: FieldNumberCellRenderer,
    },
    {
      headerName: "Field Description",
      field: "description",
      flex: 1,
      minWidth: 300,
      cellRenderer: DescriptionCellRenderer,
      cellRendererParams: { onSaveDescription: saveDescription },
    },
    {
      headerName: "Modified",
      field: "updated_at",
      width: 200,
      minWidth: 180,
      valueFormatter: (params) => formatTimestamp(params.value),
    },
    {
      headerName: "Source",
      field: "updated_by",
      width: 180,
      minWidth: 140,
      valueFormatter: (params) => params.value || "",
    },
  ];
}

export default function MetadataFieldList() {
  const loaderData = useLoaderData();
  const [rows, setRows] = useState(() => (Array.isArray(loaderData?.fields) ? loaderData.fields : []));
  const [loading, setLoading] = useState(() => !loaderData?.fields && !loaderData?.initialError);
  const [error, setError] = useState(() => String(loaderData?.initialError || ""));
  const gridRef = useRef(null);
  const notifyOperator = useAppNotifications({
    title: "Metadata Fields",
    icon: "metadata",
    variant: "info",
  });

  useRoutePageChrome({
    title: "Metadata Fields",
    subtitle: "Configure global labels for Agent metadata fields.",
    Icon: PAGE_ICON,
  });

  const loadFields = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const payload = await fetchRouteJson("/api/metadata_fields");
      setRows(Array.isArray(payload?.fields) ? payload.fields : []);
    } catch (err) {
      setRows([]);
      setError(getRouteErrorMessage(err, "Unable to load metadata fields."));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (loaderData?.fields || loaderData?.initialError) return;
    void loadFields();
  }, [loadFields, loaderData]);

  const saveDescription = useCallback(
    async (row, description) => {
      const fieldNumber = Number(row?.field_number || 0);
      if (!Number.isInteger(fieldNumber) || fieldNumber < 1 || fieldNumber > 500) return false;
      if (isReservedMetadataField(row)) {
        void notifyOperator?.(String(row?.reserved_tooltip || RESERVED_METADATA_TOOLTIP), { variant: "warning" });
        return false;
      }
      try {
        const response = await fetch(`/api/metadata_fields/${fieldNumber}`, {
          method: "PUT",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ description }),
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(payload?.message || payload?.error || "Unable to save metadata field.");
        }
        const updated = payload?.field || {};
        setRows((prev) =>
          prev.map((item) =>
            Number(item.field_number) === fieldNumber
              ? {
                  ...item,
                  ...updated,
                  description: updated.description || "",
                  label: updated.description || item.default_label,
                }
              : item
          )
        );
        void notifyOperator?.("Metadata field description saved.", { variant: "success" });
        return true;
      } catch (err) {
        void notifyOperator?.(err?.message || "Unable to save metadata field.", { variant: "error" });
        return false;
      }
    },
    [notifyOperator]
  );

  const columnDefs = useMemo(() => buildMetadataFieldColumnDefs({ saveDescription }), [saveDescription]);

  const defaultColDef = useMemo(
    () => ({
      sortable: true,
      resizable: true,
      filter: "agTextColumnFilter",
      cellStyle: { color: "#e2e8f0", fontSize: "0.9rem" },
    }),
    []
  );

  const stack = (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 1.25 }}>
      {loading ? <LinearProgress /> : null}
      {error ? <Alert severity="error">{error}</Alert> : null}
      <Typography sx={{ color: "#94a3b8", fontSize: "0.86rem" }}>
        Empty descriptions fall back to Field 001 through Field 500. Reserved Borealis fields link to the assembly that collects their values.
      </Typography>
    </Box>
  );

  return (
    <PageBodyFrame
      variant="grid_with_stack"
      stack={stack}
      main={
        <Box sx={GRID_SX}>
          <AgGridReact
            ref={gridRef}
            rowData={rows}
            columnDefs={columnDefs}
            defaultColDef={defaultColDef}
            getRowId={(params) => String(params.data?.field_key || params.data?.field_number)}
            suppressCellFocus
            animateRows
            pagination
            paginationPageSize={50}
            paginationPageSizeSelector={[50, 100, 250, 500]}
            theme={gridTheme}
            style={{
              width: "100%",
              height: "100%",
              fontFamily: gridFontFamily,
              "--ag-icon-font-family": iconFontFamily,
            }}
          />
        </Box>
      }
    />
  );
}
