import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Box, CircularProgress, InputBase, Typography } from "@mui/material";
import SearchIcon from "@mui/icons-material/Search";
import { AgGridReact } from "ag-grid-react";
import { AllCommunityModule, ModuleRegistry, themeQuartz } from "ag-grid-community";
import { DirtyStatePill, DomainBadge } from "./Assembly_Badges";
import { CountSliderGroup as FilterSlider } from "../Automation/Watchdogs/shared.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const MAGIC_UI = "#8fd3ff";
const SEARCH_PANEL_BORDER = "rgba(148, 163, 184, 0.28)";
const SEARCH_PANEL_BORDER_STRONG = "rgba(125, 183, 255, 0.34)";
const SEARCH_SHADOW = "0 10px 28px rgba(4, 8, 24, 0.38)";
const GRID_PANEL_SX = {
  border: "1px solid rgba(143, 211, 255, 0.16)",
  borderRadius: 1,
  background: "linear-gradient(180deg, rgba(9, 27, 42, 0.96) 0%, rgba(6, 18, 30, 0.98) 100%)",
  boxShadow: "0 18px 42px rgba(0, 0, 0, 0.28)",
  overflow: "hidden",
};
const LEFT_ALIGN_CELL_STYLE = {
  display: "flex",
  alignItems: "center",
};
const SINGLE_ROW_SELECTION = {
  mode: "singleRow",
  checkboxes: false,
  headerCheckbox: false,
  enableClickSelection: true,
};

const gridTheme = themeQuartz.withParams({
  accentColor: MAGIC_UI,
  backgroundColor: "#06121e",
  borderColor: "rgba(143, 211, 255, 0.14)",
  browserColorScheme: "dark",
  chromeBackgroundColor: "#081827",
  foregroundColor: "#d8ecff",
  headerBackgroundColor: "#0b2033",
  headerTextColor: "#f5fbff",
  oddRowBackgroundColor: "#071625",
  rowHoverColor: "rgba(143, 211, 255, 0.10)",
  selectedRowBackgroundColor: "rgba(143, 211, 255, 0.20)",
  wrapperBorderRadius: 8,
  fontFamily: "'Inter', 'Roboto', 'Helvetica', 'Arial', sans-serif",
  headerFontFamily: "'Inter', 'Roboto', 'Helvetica', 'Arial', sans-serif",
  cellHorizontalPadding: 12,
  headerColumnBorder: "rgba(143, 211, 255, 0.10)",
  rowBorderColor: "rgba(143, 211, 255, 0.08)",
});

const ASSEMBLY_TYPE_FILTER_OPTIONS = [
  { key: "applications", label: "Applications", match: "applications" },
  { key: "playbooks", label: "Playbooks", match: "playbooks" },
  { key: "scripts", label: "Scripts", match: "scripts" },
  { key: "workflows", label: "Workflows", match: "workflows" },
];

const ASSEMBLY_OS_FILTER_OPTIONS = [
  { key: "windows", label: "Windows", match: "windows" },
  { key: "linux", label: "Linux", match: "linux" },
  { key: "macos", label: "macOS", match: "macos" },
];

function normalizePath(path) {
  return String(path || "").replace(/\\/g, "/").toLowerCase();
}

function isCanonicalAssemblyPath(path) {
  const normalized = normalizePath(path);
  return (
    normalized.startsWith("scripts/") ||
    normalized.startsWith("ansible_playbooks/") ||
    normalized.startsWith("workflows/")
  );
}

function isCanonicalAssemblyPathForKind(path, kind) {
  const normalized = normalizePath(path);
  if (kind === "ansible") return normalized.startsWith("ansible_playbooks/");
  if (kind === "workflow") return normalized.startsWith("workflows/");
  return normalized.startsWith("scripts/");
}

function hasAssemblyCategoryPath(path) {
  const segments = normalizePath(path).split("/").filter(Boolean);
  return segments.some((segment) => ["applications", "playbooks", "scripts", "workflows"].includes(segment));
}

function formatPathDisplay(path) {
  const cleaned = String(path || "")
    .replace(/\\/g, "/")
    .replace(/\/+/g, "/")
    .replace(/^\/+/, "")
    .replace(/\/+$/, "");
  if (!cleaned) return "Root";
  return cleaned
    .split("/")
    .filter(Boolean)
    .map((part) => part.replace(/[_-]+/g, " ").replace(/\b\w/g, (char) => char.toUpperCase()))
    .join(" / ");
}

function buildPathSearchValue(folderPath) {
  const rawPath = String(folderPath || "").replace(/\\/g, "/");
  const formattedPath = formatPathDisplay(rawPath);
  return [rawPath, formattedPath].filter(Boolean).join(" ").toLowerCase();
}

function assemblyKind(record) {
  const kind = String(record?.kind || record?.type || record?.assembly_type || "").toLowerCase();
  const path = normalizePath(record?.path || record?.folder_path || record?.source_path || record?.name);
  if (kind.includes("workflow") || path.includes("/workflows/") || path.startsWith("workflows/")) return "workflow";
  if (
    kind.includes("playbook") ||
    kind.includes("ansible") ||
    path.includes("/playbooks/") ||
    path.includes("/ansible_playbooks/") ||
    path.startsWith("playbooks/") ||
    path.startsWith("ansible_playbooks/")
  ) {
    return "ansible";
  }
  return "script";
}

function pathSearchValue(record) {
  return [
    record?.name,
    record?.displayName,
    record?.description,
    record?.summary,
    record?.path,
    record?.folder_path,
    record?.source_path,
    record?.domainLabel,
    record?.domain,
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}

function buildFilterFlags(path, options) {
  return options.reduce((flags, option) => {
    const match = option.match;
    flags[option.key] =
      typeof match === "function"
        ? match(path)
        : String(path || "").includes(String(match || "").toLowerCase());
    return flags;
  }, {});
}

function assemblyKindSearchTerms(kind) {
  if (kind === "ansible") return "ansible playbook playbooks";
  if (kind === "workflow") return "workflow workflows";
  return "script scripts";
}

function assemblySubtype(record, rawRecord = {}) {
  return String(
    record?.type ||
      record?.assembly_subtype ||
      record?.assemblySubtype ||
      rawRecord?.assembly_subtype ||
      rawRecord?.assemblySubtype ||
      rawRecord?.type ||
      rawRecord?.script_type ||
      rawRecord?.runtime_type ||
      "",
  ).toLowerCase();
}

function resolveAssemblyTypeFlags(path, kind) {
  const segments = normalizePath(path).split("/").filter(Boolean);
  const hasSegment = (name) => segments.includes(name);
  const flags = ASSEMBLY_TYPE_FILTER_OPTIONS.reduce((acc, option) => {
    acc[option.key] = false;
    return acc;
  }, {});

  if (hasSegment("applications")) {
    flags.applications = true;
  } else if (kind === "ansible" || hasSegment("playbooks") || hasSegment("ansible_playbooks")) {
    flags.playbooks = true;
  } else if (kind === "workflow" || hasSegment("workflows")) {
    flags.workflows = true;
  } else {
    flags.scripts = true;
  }

  return flags;
}

function resolveTargetOsFlags(path, record, rawRecord, kind) {
  const flags = buildFilterFlags(path, ASSEMBLY_OS_FILTER_OPTIONS);
  if (Object.values(flags).some(Boolean)) return flags;

  const subtype = assemblySubtype(record, rawRecord);
  if (["powershell", "batch", "cmd", "windows", "winrm"].includes(subtype)) {
    flags.windows = true;
  } else if (["bash", "shell", "sh", "linux", "ansible", "ssh"].includes(subtype) || kind === "ansible") {
    flags.linux = true;
  } else if (["macos", "darwin", "zsh"].includes(subtype)) {
    flags.macos = true;
  } else if (kind === "script") {
    flags.windows = true;
  }

  return flags;
}

function FilterSliderBlock({ label, options, activeKey, counts, onChange }) {
  return (
    <Box sx={{ minWidth: 0, display: "flex", flexDirection: "column", alignItems: "flex-start", gap: "8px" }}>
      <Typography
        component="span"
        sx={{
          color: "#58a6ff",
          fontSize: 11,
          fontWeight: 600,
          lineHeight: 1.1,
          pl: 1,
        }}
      >
        {label}
      </Typography>
      <FilterSlider options={options} activeKey={activeKey} counts={counts} onChange={onChange} />
    </Box>
  );
}

function SourceCellRenderer({ data }) {
  if (!data) return null;
  return (
    <Box sx={{ display: "flex", alignItems: "center", gap: 1, minWidth: 0, width: "100%" }}>
      <DomainBadge domain={data.domain} label={data.domainLabel} size="small" />
    </Box>
  );
}

function NameCellRenderer({ data }) {
  if (!data) return null;
  return (
    <Box sx={{ display: "flex", flexDirection: "column", minWidth: 0, width: "100%", justifyContent: "center" }}>
      <Box sx={{ display: "flex", alignItems: "center", gap: 0.8, minWidth: 0 }}>
        <Typography
          variant="body2"
          sx={{
            color: "#f5fbff",
            fontWeight: 700,
            lineHeight: 1.25,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {data.name}
        </Typography>
        {data.isDirty ? <DirtyStatePill size="small" /> : null}
      </Box>
      {data.description ? (
        <Typography
          variant="caption"
          sx={{
            color: "rgba(216, 236, 255, 0.62)",
            lineHeight: 1.25,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {data.description}
        </Typography>
      ) : null}
    </Box>
  );
}

function PathCellRenderer({ data }) {
  if (!data) return null;
  return (
    <Typography
      variant="caption"
      sx={{
        color: "rgba(216, 236, 255, 0.72)",
        overflow: "hidden",
        textOverflow: "ellipsis",
        whiteSpace: "nowrap",
      }}
    >
      {data.pathDisplay}
    </Typography>
  );
}

function UpdateCellRenderer({ data }) {
  if (!data?.officialUpdateAvailable) return <Typography variant="caption" sx={{ color: "rgba(216, 236, 255, 0.36)" }}>Current</Typography>;
  return (
    <Typography variant="caption" sx={{ color: "#ffd479", fontWeight: 700 }}>
      Update
    </Typography>
  );
}

export default function AssemblyPicker({
  records = [],
  loading = false,
  error = "",
  allowedKinds = null,
  selectedAssemblyGuid = "",
  onSelectionChange,
  onChoose,
  height = 452,
}) {
  const gridApiRef = useRef(null);
  const [filterText, setFilterText] = useState("");
  const [typeFilterMode, setTypeFilterMode] = useState("");
  const [osFilterMode, setOsFilterMode] = useState("");

  const rows = useMemo(() => {
    const allowed = Array.isArray(allowedKinds) && allowedKinds.length
      ? new Set(allowedKinds.map((kind) => String(kind).toLowerCase()))
      : null;
    return records
      .map((record) => {
        const kind = assemblyKind(record);
        const guid = String(record?.assemblyGuid || record?.guid || record?.id || "").toLowerCase();
        const rawRecord = record?.raw || {};
        const sourcePathCandidates = [
          rawRecord.virtual_path ||
            rawRecord.virtualPath,
          rawRecord.source_path ||
            rawRecord.sourcePath ||
            rawRecord.rel_path ||
            rawRecord.relPath,
          rawRecord.path,
          record?.path,
          record?.folder_path ||
            record?.source_path,
        ]
          .map((candidate) => String(candidate || "").replace(/\\/g, "/").trim())
          .filter(Boolean);
        const sourcePath =
          sourcePathCandidates.find(hasAssemblyCategoryPath) ||
          sourcePathCandidates.find((candidate) => isCanonicalAssemblyPathForKind(candidate, kind)) ||
          sourcePathCandidates.find(isCanonicalAssemblyPath) ||
          sourcePathCandidates.find((candidate) => candidate.includes("/")) ||
          sourcePathCandidates[0] ||
          "";
        const pathParts = sourcePath ? sourcePath.split("/") : [];
        const folderPath = pathParts.length > 1 ? pathParts.slice(0, -1).join("/") : "";
        const pathCategorySearchValue = buildPathSearchValue(folderPath);
        const assemblyTypeFlags = resolveAssemblyTypeFlags(folderPath || sourcePath, kind);
        const targetOsFlags = resolveTargetOsFlags(pathCategorySearchValue, record, rawRecord, kind);
        return {
          id: guid || `${record?.name || "assembly"}-${sourcePath}`,
          assemblyGuid: guid,
          name: record?.displayName || record?.name || "Unnamed Assembly",
          domain: record?.domain || "custom",
          domainLabel: record?.domainLabel || record?.domain || "Custom",
          description: record?.summary || record?.description || "",
          pathDisplay: formatPathDisplay(folderPath),
          pathSearchValue: [pathCategorySearchValue, pathSearchValue(record), assemblyKindSearchTerms(kind)]
            .filter(Boolean)
            .join(" "),
          typeKey: kind,
          kind,
          isDirty: Boolean(record?.isDirty),
          officialUpdateAvailable: Boolean(record?.officialUpdateAvailable || record?.raw?.official_update_available),
          rawPath: sourcePath,
          folderPath,
          assemblyTypeFlags,
          targetOsFlags,
          record,
        };
      })
      .filter((row) => row.assemblyGuid && (!allowed || allowed.has(row.kind)))
      .sort((left, right) =>
        String(left.name || "").localeCompare(String(right.name || ""), undefined, {
          sensitivity: "base",
          numeric: true,
        }),
      );
  }, [allowedKinds, records]);

  const filteredRows = useMemo(() => {
    const query = filterText.trim().toLowerCase();
    return rows.filter((row) => {
      if (query && !row.pathSearchValue.includes(query)) return false;
      if (typeFilterMode && typeFilterMode !== "all" && !row.assemblyTypeFlags[typeFilterMode]) return false;
      if (osFilterMode && osFilterMode !== "all" && !row.targetOsFlags[osFilterMode]) return false;
      return true;
    });
  }, [filterText, osFilterMode, rows, typeFilterMode]);

  const typeCountRows = useMemo(
    () => (osFilterMode && osFilterMode !== "all" ? rows.filter((row) => row.targetOsFlags?.[osFilterMode]) : rows),
    [osFilterMode, rows],
  );
  const osCountRows = useMemo(
    () => (typeFilterMode && typeFilterMode !== "all" ? rows.filter((row) => row.assemblyTypeFlags?.[typeFilterMode]) : rows),
    [rows, typeFilterMode],
  );
  const assemblyTypeCounts = useMemo(
    () =>
      ASSEMBLY_TYPE_FILTER_OPTIONS.reduce((acc, option) => {
        acc[option.key] = typeCountRows.filter((row) => row.assemblyTypeFlags?.[option.key]).length;
        return acc;
      }, {}),
    [typeCountRows],
  );
  const assemblyOsCounts = useMemo(
    () =>
      ASSEMBLY_OS_FILTER_OPTIONS.reduce((acc, option) => {
        acc[option.key] = osCountRows.filter((row) => row.targetOsFlags?.[option.key]).length;
        return acc;
      }, {}),
    [osCountRows],
  );

  const emitSelection = useCallback(
    (row) => {
      onSelectionChange?.(row?.record || null);
    },
    [onSelectionChange],
  );

  useEffect(() => {
    const api = gridApiRef.current;
    if (!api) return;
    const selectedGuid = String(selectedAssemblyGuid || "").toLowerCase();
    api.forEachNode((node) => {
      node.setSelected(Boolean(selectedGuid && node.data?.assemblyGuid === selectedGuid));
    });
  }, [filteredRows, selectedAssemblyGuid]);

  const columnDefs = useMemo(
    () => [
      {
        headerName: "Source",
        field: "domainLabel",
        width: 170,
        minWidth: 150,
        cellRenderer: SourceCellRenderer,
        cellStyle: LEFT_ALIGN_CELL_STYLE,
        sortable: true,
      },
      {
        headerName: "Assembly",
        field: "name",
        minWidth: 280,
        flex: 1.4,
        cellRenderer: NameCellRenderer,
        cellStyle: LEFT_ALIGN_CELL_STYLE,
        sortable: true,
      },
      {
        headerName: "Path",
        field: "pathDisplay",
        minWidth: 220,
        flex: 1,
        cellRenderer: PathCellRenderer,
        cellStyle: LEFT_ALIGN_CELL_STYLE,
        sortable: true,
      },
      {
        headerName: "Update",
        field: "officialUpdateAvailable",
        width: 112,
        cellRenderer: UpdateCellRenderer,
        cellStyle: LEFT_ALIGN_CELL_STYLE,
        sortable: true,
      },
    ],
    [],
  );

  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 2, minHeight: 0, height: "100%" }}>
      <Box
        sx={{
          display: "grid",
          gridTemplateColumns: { xs: "1fr", md: "minmax(280px, 1fr) minmax(240px, auto) minmax(240px, auto)" },
          columnGap: 1,
          rowGap: 1.5,
          alignItems: "end",
        }}
      >
        <Box
          sx={{
            position: "relative",
            display: "flex",
            alignItems: "center",
            gap: 1.2,
            width: "100%",
            minWidth: 0,
            height: 42,
            px: 1.6,
            borderRadius: "999px",
            border: `1px solid ${SEARCH_PANEL_BORDER}`,
            background:
              "linear-gradient(135deg, rgba(10, 16, 31, 0.94) 0%, rgba(8, 13, 28, 0.92) 60%, rgba(20, 8, 33, 0.92) 100%)",
            boxShadow: SEARCH_SHADOW,
            backdropFilter: "blur(16px) saturate(145%)",
            transition: "border-color 160ms ease, box-shadow 180ms ease, transform 120ms ease",
            "&:hover": {
              borderColor: SEARCH_PANEL_BORDER_STRONG,
            },
            "&:focus-within": {
              borderColor: SEARCH_PANEL_BORDER_STRONG,
              transform: "translateY(-0.5px)",
            },
          }}
        >
          <SearchIcon sx={{ color: "#7dd3fc", fontSize: 19, flexShrink: 0 }} />
          <InputBase
            value={filterText}
            onChange={(event) => setFilterText(event.target.value)}
            placeholder="Search Assemblies..."
            inputProps={{ "aria-label": "Search assemblies" }}
            fullWidth
            sx={{
              flex: 1,
              minWidth: 0,
              color: "#f5fbff",
              fontSize: "0.95rem",
              fontWeight: 500,
              "& input::placeholder": {
                color: "rgba(148, 163, 184, 0.92)",
                opacity: 1,
              },
            }}
          />
        </Box>
        <FilterSliderBlock
          label="Type"
          options={ASSEMBLY_TYPE_FILTER_OPTIONS}
          activeKey={typeFilterMode}
          counts={assemblyTypeCounts}
          onChange={setTypeFilterMode}
        />
        <FilterSliderBlock
          label="Target OS"
          options={ASSEMBLY_OS_FILTER_OPTIONS}
          activeKey={osFilterMode}
          counts={assemblyOsCounts}
          onChange={setOsFilterMode}
        />
      </Box>

      <Box sx={{ ...GRID_PANEL_SX, height, minHeight: 280 }}>
        {loading ? (
          <Box sx={{ height: "100%", display: "grid", placeItems: "center", gap: 1, color: "rgba(216, 236, 255, 0.72)" }}>
            <CircularProgress size={24} sx={{ color: MAGIC_UI }} />
            <Typography variant="body2">Loading assemblies...</Typography>
          </Box>
        ) : error ? (
          <Box sx={{ height: "100%", display: "grid", placeItems: "center", p: 3, textAlign: "center" }}>
            <Typography variant="body2" sx={{ color: "#ffb4b4", fontWeight: 700 }}>
              {error}
            </Typography>
          </Box>
        ) : (
          <AgGridReact
            theme={gridTheme}
            rowData={filteredRows}
            columnDefs={columnDefs}
            rowHeight={56}
            headerHeight={44}
            getRowId={(params) => params.data.id}
            rowSelection={SINGLE_ROW_SELECTION}
            suppressRowClickSelection={false}
            noRowsOverlayComponent={() => (
              <Box sx={{ p: 3, color: "rgba(216, 236, 255, 0.62)", fontWeight: 700 }}>
                No assemblies found
              </Box>
            )}
            onGridReady={(params) => {
              gridApiRef.current = params.api;
              const selectedGuid = String(selectedAssemblyGuid || "").toLowerCase();
              if (selectedGuid) {
                params.api.forEachNode((node) => {
                  node.setSelected(node.data?.assemblyGuid === selectedGuid);
                });
              }
            }}
            onSelectionChanged={(event) => {
              const selected = event.api.getSelectedRows()?.[0];
              if (selected) emitSelection(selected);
            }}
            onRowClicked={(event) => emitSelection(event.data)}
            onRowDoubleClicked={(event) => onChoose?.(event.data?.record || null)}
          />
        )}
      </Box>
    </Box>
  );
}
