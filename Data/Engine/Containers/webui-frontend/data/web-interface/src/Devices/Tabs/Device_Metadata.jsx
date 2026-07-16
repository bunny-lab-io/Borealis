import React, { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, Box, LinearProgress, Stack, TextField, Typography } from "@mui/material";
import { AgGridReact } from "ag-grid-react";

import { DEVICE_DETAILS_GRID_THEME, GridShell, DEVICE_GRID_STYLE, gridFontFamily } from "./Shared.jsx";
import { FieldNumberCellRenderer } from "../../Metadata_Field_Cells.jsx";
import { useAppNotifications } from "../../app/hooks/useAppNotifications.js";
import { fetchRouteJson, getRouteErrorMessage } from "../../app/routes/routeData.js";

const FIELD_DESCRIPTION_AUTO_SIZE_COLUMNS = ["label"];

function formatTimestamp(value) {
  if (!value) return "";
  const numeric = Number(value);
  const date = Number.isFinite(numeric) && numeric > 0 ? new Date(numeric * 1000) : new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString();
}

function MetadataValueCell(props) {
  const { data, value, onSaveValue, valueLimit = 1024 } = props;
  const safeValue = typeof value === "string" ? value : value == null ? "" : String(value);
  const [draft, setDraft] = useState(safeValue);
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!editing && !saving) setDraft(safeValue);
  }, [editing, safeValue, saving]);

  const finishSave = useCallback(async () => {
    const nextValue = String(draft || "");
    if (nextValue === safeValue) {
      setEditing(false);
      setError("");
      return;
    }
    if (typeof onSaveValue !== "function" || !data) {
      setEditing(false);
      return;
    }
    setSaving(true);
    setError("");
    const ok = await onSaveValue(data, nextValue);
    setSaving(false);
    if (ok) {
      setEditing(false);
      return;
    }
    setError("Failed to save value");
  }, [data, draft, onSaveValue, safeValue]);

  return (
    <TextField
      value={draft}
      onFocus={(event) => {
        event.stopPropagation();
        setEditing(true);
        setError("");
      }}
      onChange={(event) => setDraft(event.target.value.slice(0, valueLimit))}
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

export function buildDeviceMetadataColumnDefs({ saveValue, valueLimit }) {
  return [
    {
      headerName: "Field Number",
      field: "default_label",
      width: 150,
      pinned: "left",
      resizable: false,
      cellRenderer: FieldNumberCellRenderer,
    },
    {
      headerName: "Field Description",
      field: "label",
      width: 260,
      minWidth: 220,
      maxWidth: 460,
      suppressSizeToFit: true,
      valueGetter: (params) => params.data?.description || params.data?.default_label || "",
    },
    {
      headerName: "Value",
      field: "value",
      minWidth: 320,
      flex: 1,
      cellRenderer: MetadataValueCell,
      cellRendererParams: { onSaveValue: saveValue, valueLimit },
    },
    {
      headerName: "Modified",
      field: "modified_at",
      width: 108,
      minWidth: 108,
      valueFormatter: (params) => formatTimestamp(params.value),
    },
    {
      headerName: "Source",
      field: "source",
      width: 72,
      minWidth: 72,
      resizable: false,
      valueFormatter: (params) => params.value || "",
    },
  ];
}

export default function DeviceMetadata({ deviceId, deviceGuid, hostname }) {
  const targetId = String(deviceGuid || deviceId || hostname || "").trim();
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [valueLimit, setValueLimit] = useState(1024);
  const notifyOperator = useAppNotifications({
    title: "Device Metadata",
    icon: "metadata",
    variant: "info",
  });

  const loadRows = useCallback(async () => {
    if (!targetId) {
      setRows([]);
      setError("Device identifier missing.");
      return;
    }
    setLoading(true);
    setError("");
    try {
      const payload = await fetchRouteJson(`/api/devices/${encodeURIComponent(targetId)}/metadata_fields`);
      setRows(Array.isArray(payload?.fields) ? payload.fields : []);
      setValueLimit(Number(payload?.value_limit || 1024) || 1024);
    } catch (err) {
      setRows([]);
      setError(getRouteErrorMessage(err, "Unable to load device metadata."));
    } finally {
      setLoading(false);
    }
  }, [targetId]);

  useEffect(() => {
    void loadRows();
  }, [loadRows]);

  const saveValue = useCallback(
    async (row, value) => {
      const fieldNumber = Number(row?.field_number || 0);
      if (!targetId || !Number.isInteger(fieldNumber) || fieldNumber < 1 || fieldNumber > 500) return false;
      try {
        const response = await fetch(
          `/api/devices/${encodeURIComponent(targetId)}/metadata_fields/${fieldNumber}`,
          {
            method: "PUT",
            credentials: "include",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ value }),
          }
        );
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(payload?.message || payload?.error || "Unable to save metadata value.");
        }
        const updated = payload?.field || {};
        setRows((prev) =>
          prev.map((item) =>
            Number(item.field_number) === fieldNumber
              ? {
                  ...item,
                  ...updated,
                  label: updated.description || item.default_label,
                  value: updated.value || "",
                  has_value: Boolean(updated.value),
                }
              : item
          )
        );
        void notifyOperator("Metadata field saved.", { variant: "success" });
        return true;
      } catch (err) {
        void notifyOperator(err?.message || "Unable to save metadata value.", { variant: "error" });
        return false;
      }
    },
    [notifyOperator, targetId]
  );

  const columnDefs = useMemo(
    () => buildDeviceMetadataColumnDefs({ saveValue, valueLimit }),
    [saveValue, valueLimit]
  );

  const autoSizeDescriptionColumn = useCallback((params) => {
    if (typeof params?.api?.autoSizeColumns === "function") {
      params.api.autoSizeColumns(FIELD_DESCRIPTION_AUTO_SIZE_COLUMNS, true);
    }
  }, []);

  const defaultColDef = useMemo(
    () => ({
      sortable: true,
      resizable: true,
      filter: "agTextColumnFilter",
      cellStyle: { color: "#e2e8f0", fontSize: "0.9rem" },
    }),
    []
  );

  return (
    <Stack spacing={1.5} sx={{ minHeight: 0, flexGrow: 1, height: "100%" }}>
      {loading ? <LinearProgress /> : null}
      {error ? <Alert severity="error">{error}</Alert> : null}
      <Box sx={{ minHeight: 0, flexGrow: 1, height: "100%" }}>
        <GridShell sx={{ height: "100%", minHeight: 520 }}>
          <AgGridReact
            rowData={rows}
            columnDefs={columnDefs}
            defaultColDef={defaultColDef}
            getRowId={(params) => String(params.data?.field_key || params.data?.field_number)}
            onFirstDataRendered={autoSizeDescriptionColumn}
            onRowDataUpdated={autoSizeDescriptionColumn}
            suppressCellFocus
            animateRows
            pagination
            paginationPageSize={50}
            paginationPageSizeSelector={[50, 100, 250, 500]}
            theme={DEVICE_DETAILS_GRID_THEME}
            style={DEVICE_GRID_STYLE}
          />
        </GridShell>
      </Box>
      <Typography sx={{ color: "#94a3b8", fontSize: "0.82rem" }}>
        Blank values clear fields after next Agent sync acknowledgement.
      </Typography>
    </Stack>
  );
}
