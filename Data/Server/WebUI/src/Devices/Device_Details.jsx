////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Device_Details.js

import React, { useState, useEffect, useMemo, useCallback } from "react";
import {
  Paper,
  Box,
  Tabs,
  Tab,
  Typography,
  Table,
  TableHead,
  TableRow,
  TableCell,
  TableBody,
  Button,
  LinearProgress,
  TableSortLabel,
  TextField,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions
} from "@mui/material";
import Prism from "prismjs";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-powershell";
import "prismjs/components/prism-batch";
import "prismjs/themes/prism-okaidia.css";
import Editor from "react-simple-code-editor";
import QuickJob from "../Scheduling/Quick_Job.jsx";

export default function DeviceDetails({ device, onBack }) {
  const [tab, setTab] = useState(0);
  const [agent, setAgent] = useState(device || {});
  const [details, setDetails] = useState({});
  const [softwareOrderBy, setSoftwareOrderBy] = useState("name");
  const [softwareOrder, setSoftwareOrder] = useState("asc");
  const [softwareSearch, setSoftwareSearch] = useState("");
  const [description, setDescription] = useState("");
  const [historyRows, setHistoryRows] = useState([]);
  const [historyOrderBy, setHistoryOrderBy] = useState("ran_at");
  const [historyOrder, setHistoryOrder] = useState("desc");
  const [outputOpen, setOutputOpen] = useState(false);
  const [outputTitle, setOutputTitle] = useState("");
  const [outputContent, setOutputContent] = useState("");
  const [outputLang, setOutputLang] = useState("powershell");
  const [quickJobOpen, setQuickJobOpen] = useState(false);
  // Snapshotted status for the lifetime of this page
  const [lockedStatus, setLockedStatus] = useState(() => {
    // Prefer status provided by the device list row if available
    if (device?.status) return device.status;
    // Fallback: compute once from the provided lastSeen timestamp
    const tsSec = device?.lastSeen;
    if (!tsSec) return "Offline";
    const now = Date.now() / 1000;
    return now - tsSec <= 120 ? "Online" : "Offline";
  });

  const statusFromHeartbeat = (tsSec, offlineAfter = 120) => {
    if (!tsSec) return "Offline";
    const now = Date.now() / 1000;
    return now - tsSec <= offlineAfter ? "Online" : "Offline";
  };

  const statusColor = (s) => (s === "Online" ? "#00d18c" : "#ff4f4f");

  const formatLastSeen = (tsSec, offlineAfter = 120) => {
    if (!tsSec) return "unknown";
    const now = Date.now() / 1000;
    if (now - tsSec <= offlineAfter) return "Currently Online";
    const d = new Date(tsSec * 1000);
    const date = d.toLocaleDateString("en-US", {
      month: "2-digit",
      day: "2-digit",
      year: "numeric",
    });
    const time = d.toLocaleTimeString("en-US", {
      hour: "numeric",
      minute: "2-digit",
    });
    return `${date} @ ${time}`;
  };

  useEffect(() => {
    // When navigating to a different device, take a fresh snapshot of its status
    if (device) {
      setLockedStatus(device.status || statusFromHeartbeat(device.lastSeen));
    }

    if (!device || !device.hostname) return;
    const load = async () => {
      try {
        const [agentsRes, detailsRes] = await Promise.all([
          fetch("/api/agents"),
          fetch(`/api/device/details/${device.hostname}`)
        ]);
        const agentsData = await agentsRes.json();
        if (agentsData && agentsData[device.id]) {
          setAgent({ id: device.id, ...agentsData[device.id] });
        }
        const detailData = await detailsRes.json();
        setDetails(detailData || {});
        setDescription(detailData?.summary?.description || "");
      } catch (e) {
        console.warn("Failed to load device info", e);
      }
    };
    load();
  }, [device]);

  const loadHistory = useCallback(async () => {
    if (!device?.hostname) return;
    try {
      const resp = await fetch(`/api/device/activity/${encodeURIComponent(device.hostname)}`);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      setHistoryRows(data.history || []);
    } catch (e) {
      console.warn("Failed to load activity history", e);
      setHistoryRows([]);
    }
  }, [device]);

  useEffect(() => { loadHistory(); }, [loadHistory]);

  const saveDescription = async () => {
    if (!details.summary?.hostname) return;
    try {
      await fetch(`/api/device/description/${details.summary.hostname}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ description })
      });
      setDetails((d) => ({
        ...d,
        summary: { ...(d.summary || {}), description }
      }));
    } catch (e) {
      console.warn("Failed to save description", e);
    }
  };

  const formatDateTime = (str) => {
    if (!str) return "unknown";
    try {
      const [datePart, timePart] = str.split(" ");
      const [y, m, d] = datePart.split("-").map(Number);
      let [hh, mm, ss] = timePart.split(":").map(Number);
      const ampm = hh >= 12 ? "PM" : "AM";
      hh = hh % 12 || 12;
      return `${m.toString().padStart(2, "0")}/${d.toString().padStart(2, "0")}/${y} @ ${hh}:${mm
        .toString()
        .padStart(2, "0")} ${ampm}`;
    } catch {
      return str;
    }
  };

  const formatMac = (mac) => (mac ? mac.replace(/-/g, ":").toUpperCase() : "unknown");

  const formatBytes = (val) => {
    if (val === undefined || val === null || val === "unknown") return "unknown";
    let num = Number(val);
    const units = ["B", "KB", "MB", "GB", "TB"];
    let i = 0;
    while (num >= 1024 && i < units.length - 1) {
      num /= 1024;
      i++;
    }
    return `${num.toFixed(1)} ${units[i]}`;
  };

  const formatTimestamp = (epochSec) => {
    const ts = Number(epochSec || 0);
    if (!ts) return "unknown";
    const d = new Date(ts * 1000);
    const mm = String(d.getMonth() + 1).padStart(2, "0");
    const dd = String(d.getDate()).padStart(2, "0");
    const yyyy = d.getFullYear();
    let hh = d.getHours();
    const ampm = hh >= 12 ? "PM" : "AM";
    hh = hh % 12 || 12;
    const min = String(d.getMinutes()).padStart(2, "0");
    return `${mm}/${dd}/${yyyy} @ ${hh}:${min} ${ampm}`;
  };

  const handleSoftwareSort = (col) => {
    if (softwareOrderBy === col) {
      setSoftwareOrder(softwareOrder === "asc" ? "desc" : "asc");
    } else {
      setSoftwareOrderBy(col);
      setSoftwareOrder("asc");
    }
  };

  const softwareRows = useMemo(() => {
    const rows = details.software || [];
    const filtered = rows.filter((s) =>
      s.name.toLowerCase().includes(softwareSearch.toLowerCase())
    );
    const dir = softwareOrder === "asc" ? 1 : -1;
    return [...filtered].sort((a, b) => {
      const A = a[softwareOrderBy] || "";
      const B = b[softwareOrderBy] || "";
      return String(A).localeCompare(String(B)) * dir;
    });
  }, [details.software, softwareSearch, softwareOrderBy, softwareOrder]);

  const summary = details.summary || {};
  const summaryItems = [
    { label: "Hostname", value: summary.hostname || agent.hostname || device?.hostname || "unknown" },
    { label: "Operating System", value: summary.operating_system || agent.agent_operating_system || "unknown" },
    { label: "Device Type", value: summary.device_type || "unknown" },
    { label: "Last User", value: (
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        <Box component="span" sx={{
          display: 'inline-block', width: 10, height: 10, borderRadius: 10,
          bgcolor: agent?.collector_active ? '#00d18c' : '#ff4f4f'
        }} />
        <span>{summary.last_user || 'unknown'}</span>
      </Box>
    ) },
    { label: "Internal IP", value: summary.internal_ip || "unknown" },
    { label: "External IP", value: summary.external_ip || "unknown" },
    { label: "Last Reboot", value: summary.last_reboot ? formatDateTime(summary.last_reboot) : "unknown" },
    { label: "Created", value: summary.created ? formatDateTime(summary.created) : "unknown" },
    { label: "Last Seen", value: formatLastSeen(agent.last_seen || device?.lastSeen) }
  ];

  const renderSummary = () => (
    <Box sx={{ maxHeight: 400, overflowY: "auto" }}>
      <Table size="small">
        <TableBody>
          <TableRow>
            <TableCell sx={{ fontWeight: 500 }}>Description</TableCell>
            <TableCell>
              <TextField
                size="small"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                onBlur={saveDescription}
                placeholder="Enter description"
                sx={{
                  input: { color: "#fff" },
                  "& .MuiOutlinedInput-root": {
                    "& fieldset": { borderColor: "#555" },
                    "&:hover fieldset": { borderColor: "#888" }
                  }
                }}
              />
            </TableCell>
          </TableRow>
          {summaryItems.map((item) => (
            <TableRow key={item.label}>
              <TableCell sx={{ fontWeight: 500 }}>{item.label}</TableCell>
              <TableCell>{item.value}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Box>
  );

  const placeholderTable = (headers) => (
    <Box sx={{ maxHeight: 400, overflowY: "auto" }}>
      <Table size="small">
        <TableHead>
          <TableRow>
            {headers.map((h) => (
              <TableCell key={h}>{h}</TableCell>
            ))}
          </TableRow>
        </TableHead>
        <TableBody>
          <TableRow>
            <TableCell colSpan={headers.length} sx={{ color: "#888" }}>
              No data available.
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </Box>
  );

  const renderSoftware = () => {
    if (!softwareRows.length)
      return placeholderTable(["Software Name", "Version", "Action"]);

    return (
      <Box>
        <Box sx={{ mb: 1 }}>
          <TextField
            size="small"
            placeholder="Search software..."
            value={softwareSearch}
            onChange={(e) => setSoftwareSearch(e.target.value)}
            sx={{
              input: { color: "#fff" },
              "& .MuiOutlinedInput-root": {
                "& fieldset": { borderColor: "#555" },
                "&:hover fieldset": { borderColor: "#888" }
              }
            }}
          />
        </Box>
        <Box sx={{ maxHeight: 400, overflowY: "auto" }}>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell sortDirection={softwareOrderBy === "name" ? softwareOrder : false}>
                  <TableSortLabel
                    active={softwareOrderBy === "name"}
                    direction={softwareOrderBy === "name" ? softwareOrder : "asc"}
                    onClick={() => handleSoftwareSort("name")}
                  >
                    Software Name
                  </TableSortLabel>
                </TableCell>
                <TableCell sortDirection={softwareOrderBy === "version" ? softwareOrder : false}>
                  <TableSortLabel
                    active={softwareOrderBy === "version"}
                    direction={softwareOrderBy === "version" ? softwareOrder : "asc"}
                    onClick={() => handleSoftwareSort("version")}
                  >
                    Version
                  </TableSortLabel>
                </TableCell>
                <TableCell>Action</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {softwareRows.map((s, i) => (
                <TableRow key={`${s.name}-${i}`}>
                  <TableCell>{s.name}</TableCell>
                  <TableCell>{s.version}</TableCell>
                  <TableCell></TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Box>
      </Box>
    );
  };

  const renderMemory = () => {
    const rows = details.memory || [];
    if (!rows.length) return placeholderTable(["Slot", "Speed", "Serial Number", "Capacity"]);
    return (
      <Box sx={{ maxHeight: 400, overflowY: "auto" }}>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell>Slot</TableCell>
              <TableCell>Speed</TableCell>
              <TableCell>Serial Number</TableCell>
              <TableCell>Capacity</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {rows.map((m, i) => (
              <TableRow key={`${m.slot}-${i}`}>
                <TableCell>{m.slot}</TableCell>
                <TableCell>{m.speed}</TableCell>
                <TableCell>{m.serial}</TableCell>
                <TableCell>{formatBytes(m.capacity)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Box>
    );
  };

  const renderStorage = () => {
    const toNum = (val) => {
      if (val === undefined || val === null) return undefined;
      if (typeof val === "number") {
        return Number.isNaN(val) ? undefined : val;
      }
      const n = parseFloat(String(val).replace(/[^0-9.]+/g, ""));
      return Number.isNaN(n) ? undefined : n;
    };

    const rows = (details.storage || []).map((d) => {
      const total = toNum(d.total);
      let usagePct = toNum(d.usage);
      let usedBytes = toNum(d.used);
      let freeBytes = toNum(d.free);
      let freePct;

      if (usagePct !== undefined) {
        if (usagePct <= 1) usagePct *= 100;
        freePct = 100 - usagePct;
      }

      if (usedBytes === undefined && total !== undefined && usagePct !== undefined) {
        usedBytes = (usagePct / 100) * total;
      }

      if (freeBytes === undefined && total !== undefined && usedBytes !== undefined) {
        freeBytes = total - usedBytes;
      }

      if (freePct === undefined && total !== undefined && freeBytes !== undefined) {
        freePct = (freeBytes / total) * 100;
      }

      if (usagePct === undefined && freePct !== undefined) {
        usagePct = 100 - freePct;
      }

      return {
        drive: d.drive,
        disk_type: d.disk_type,
        used: usedBytes,
        freePct,
        freeBytes,
        total,
        usage: usagePct,
      };
    });

    if (!rows.length)
      return placeholderTable([
        "Drive Letter",
        "Disk Type",
        "Used",
        "Free %",
        "Free GB",
        "Total Size",
        "Usage",
      ]);

    return (
      <Box sx={{ maxHeight: 400, overflowY: "auto" }}>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell>Drive Letter</TableCell>
              <TableCell>Disk Type</TableCell>
              <TableCell>Used</TableCell>
              <TableCell>Free %</TableCell>
              <TableCell>Free GB</TableCell>
              <TableCell>Total Size</TableCell>
              <TableCell>Usage</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {rows.map((d, i) => (
              <TableRow key={`${d.drive}-${i}`}>
                <TableCell>{d.drive}</TableCell>
                <TableCell>{d.disk_type}</TableCell>
                <TableCell>
                  {d.used !== undefined && !Number.isNaN(d.used)
                    ? formatBytes(d.used)
                    : "unknown"}
                </TableCell>
                <TableCell>
                  {d.freePct !== undefined && !Number.isNaN(d.freePct)
                    ? `${d.freePct.toFixed(1)}%`
                    : "unknown"}
                </TableCell>
                <TableCell>
                  {d.freeBytes !== undefined && !Number.isNaN(d.freeBytes)
                    ? formatBytes(d.freeBytes)
                    : "unknown"}
                </TableCell>
                <TableCell>
                  {d.total !== undefined && !Number.isNaN(d.total)
                    ? formatBytes(d.total)
                    : "unknown"}
                </TableCell>
                <TableCell>
                  <Box sx={{ display: "flex", alignItems: "center" }}>
                    <Box sx={{ flexGrow: 1, mr: 1 }}>
                      <LinearProgress
                        variant="determinate"
                        value={d.usage ?? 0}
                        sx={{
                          height: 10,
                          bgcolor: "#333",
                          "& .MuiLinearProgress-bar": { bgcolor: "#00d18c" }
                        }}
                      />
                    </Box>
                    <Typography variant="body2">
                      {d.usage !== undefined && !Number.isNaN(d.usage)
                        ? `${d.usage.toFixed(1)}%`
                        : "unknown"}
                    </Typography>
                  </Box>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Box>
    );
  };

  const renderNetwork = () => {
    const rows = details.network || [];
    if (!rows.length) return placeholderTable(["Adapter", "IP Address", "MAC Address"]);
    return (
      <Box sx={{ maxHeight: 400, overflowY: "auto" }}>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell>Adapter</TableCell>
              <TableCell>IP Address</TableCell>
              <TableCell>MAC Address</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {rows.map((n, i) => (
              <TableRow key={`${n.adapter}-${i}`}>
                <TableCell>{n.adapter}</TableCell>
                <TableCell>{(n.ips || []).join(", ")}</TableCell>
                <TableCell>{formatMac(n.mac)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Box>
    );
  };

  const jobStatusColor = (s) => {
    const val = String(s || "").toLowerCase();
    if (val === "running") return "#58a6ff"; // borealis blue
    if (val === "success") return "#00d18c";
    if (val === "failed") return "#ff4f4f";
    return "#666";
  };

  const highlightCode = (code, lang) => {
    try {
      return Prism.highlight(code ?? "", Prism.languages[lang] || Prism.languages.markup, lang);
    } catch {
      return String(code || "");
    }
  };

  const handleViewOutput = async (row, which) => {
    try {
      const resp = await fetch(`/api/device/activity/job/${row.id}`);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      const lang = ((data.script_path || "").toLowerCase().endsWith(".ps1")) ? "powershell"
        : ((data.script_path || "").toLowerCase().endsWith(".bat")) ? "batch"
        : ((data.script_path || "").toLowerCase().endsWith(".sh")) ? "bash"
        : ((data.script_path || "").toLowerCase().endsWith(".yml")) ? "yaml" : "powershell";
      setOutputLang(lang);
      setOutputTitle(`${which === 'stderr' ? 'StdErr' : 'StdOut'} - ${data.script_name}`);
      setOutputContent(which === 'stderr' ? (data.stderr || "") : (data.stdout || ""));
      setOutputOpen(true);
    } catch (e) {
      console.warn("Failed to load output", e);
    }
  };

  const handleHistorySort = (col) => {
    if (historyOrderBy === col) setHistoryOrder(historyOrder === "asc" ? "desc" : "asc");
    else {
      setHistoryOrderBy(col);
      setHistoryOrder("asc");
    }
  };

  const sortedHistory = useMemo(() => {
    const dir = historyOrder === "asc" ? 1 : -1;
    return [...historyRows].sort((a, b) => {
      const A = a[historyOrderBy];
      const B = b[historyOrderBy];
      if (historyOrderBy === "ran_at") return ((A || 0) - (B || 0)) * dir;
      return String(A ?? "").localeCompare(String(B ?? "")) * dir;
    });
  }, [historyRows, historyOrderBy, historyOrder]);

  const renderHistory = () => (
    <Box sx={{ maxHeight: 400, overflowY: "auto" }}>
      <Table size="small">
        <TableHead>
          <TableRow>
            <TableCell sortDirection={historyOrderBy === "script_name" ? historyOrder : false}>
              <TableSortLabel
                active={historyOrderBy === "script_name"}
                direction={historyOrderBy === "script_name" ? historyOrder : "asc"}
                onClick={() => handleHistorySort("script_name")}
              >
                Script Executed
              </TableSortLabel>
            </TableCell>
            <TableCell sortDirection={historyOrderBy === "ran_at" ? historyOrder : false}>
              <TableSortLabel
                active={historyOrderBy === "ran_at"}
                direction={historyOrderBy === "ran_at" ? historyOrder : "asc"}
                onClick={() => handleHistorySort("ran_at")}
              >
                Ran On
              </TableSortLabel>
            </TableCell>
            <TableCell sortDirection={historyOrderBy === "status" ? historyOrder : false}>
              <TableSortLabel
                active={historyOrderBy === "status"}
                direction={historyOrderBy === "status" ? historyOrder : "asc"}
                onClick={() => handleHistorySort("status")}
              >
                Job Status
              </TableSortLabel>
            </TableCell>
            <TableCell>
              StdOut / StdErr
            </TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {sortedHistory.map((r) => (
            <TableRow key={r.id}>
              <TableCell>{r.script_name}</TableCell>
              <TableCell>{formatTimestamp(r.ran_at)}</TableCell>
              <TableCell>
                <Box sx={{
                  display: "inline-block",
                  px: 1.2,
                  py: 0.25,
                  borderRadius: 999,
                  bgcolor: jobStatusColor(r.status),
                  color: "#fff",
                  fontWeight: 600,
                  fontSize: "12px"
                }}>
                  {r.status}
                </Box>
              </TableCell>
              <TableCell>
                <Box sx={{ display: "flex", gap: 1 }}>
                  {r.has_stdout ? (
                    <Button size="small" onClick={() => handleViewOutput(r, 'stdout')} sx={{ color: "#58a6ff", textTransform: "none", minWidth: 0, p: 0 }}>
                      StdOut
                    </Button>
                  ) : null}
                  {r.has_stderr ? (
                    <Button size="small" onClick={() => handleViewOutput(r, 'stderr')} sx={{ color: "#ff4f4f", textTransform: "none", minWidth: 0, p: 0 }}>
                      StdErr
                    </Button>
                  ) : null}
                </Box>
              </TableCell>
            </TableRow>
          ))}
          {sortedHistory.length === 0 && (
            <TableRow>
              <TableCell colSpan={4} sx={{ color: "#888" }}>No activity yet.</TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </Box>
  );

  const tabs = [
    { label: "Summary", content: renderSummary() },
    { label: "Software", content: renderSoftware() },
    { label: "Memory", content: renderMemory() },
    { label: "Storage", content: renderStorage() },
    { label: "Network", content: renderNetwork() },
    { label: "Activity History", content: renderHistory() }
  ];
  // Use the snapshotted status so it stays static while on this page
  const status = lockedStatus || statusFromHeartbeat(agent.last_seen || device?.lastSeen);

  return (
    <Paper sx={{ m: 2, p: 2, bgcolor: "#1e1e1e" }} elevation={2}>
      <Box sx={{ mb: 2, display: "flex", alignItems: "center", justifyContent: "space-between" }}>
        <Box sx={{ display: "flex", alignItems: "center" }}>
          {onBack && (
            <Button variant="outlined" size="small" onClick={onBack} sx={{ mr: 2 }}>
              Back
            </Button>
          )}
          <Typography
            variant="h6"
            sx={{ color: "#58a6ff", display: "flex", alignItems: "center" }}
          >
            <span
              style={{
                display: "inline-block",
                width: 10,
                height: 10,
                borderRadius: 10,
                background: statusColor(status),
                marginRight: 8,
              }}
            />
            {agent.hostname || "Device Details"}
          </Typography>
        </Box>
        <Box>
          <Button
            variant="outlined"
            size="small"
            disabled={!(agent?.hostname || device?.hostname)}
            onClick={() => setQuickJobOpen(true)}
            sx={{
              color: !(agent?.hostname || device?.hostname) ? "#666" : "#58a6ff",
              borderColor: !(agent?.hostname || device?.hostname) ? "#333" : "#58a6ff",
              textTransform: "none"
            }}
          >
            Quick Job
          </Button>
        </Box>
      </Box>
      <Tabs
        value={tab}
        onChange={(e, v) => setTab(v)}
        sx={{ borderBottom: 1, borderColor: "#333" }}
      >
        {tabs.map((t) => (
          <Tab key={t.label} label={t.label} />
        ))}
      </Tabs>
      <Box sx={{ mt: 2 }}>{tabs[tab].content}</Box>

      <Dialog open={outputOpen} onClose={() => setOutputOpen(false)} fullWidth maxWidth="md"
        PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}
      >
        <DialogTitle>{outputTitle}</DialogTitle>
        <DialogContent>
          <Box sx={{ border: "1px solid #333", borderRadius: 1, bgcolor: "#1e1e1e", maxHeight: 500, overflow: "auto" }}>
            <Editor
              value={outputContent}
              onValueChange={() => {}}
              highlight={(code) => highlightCode(code, outputLang)}
              padding={12}
              style={{
                fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
                fontSize: 12,
                color: "#e6edf3",
                minHeight: 200
              }}
              textareaProps={{ readOnly: true }}
            />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setOutputOpen(false)} sx={{ color: "#58a6ff" }}>Close</Button>
        </DialogActions>
      </Dialog>

      {quickJobOpen && (
        <QuickJob
          open={quickJobOpen}
          onClose={() => setQuickJobOpen(false)}
          hostnames={[agent?.hostname || device?.hostname].filter(Boolean)}
        />
      )}
    </Paper>
  );
}
