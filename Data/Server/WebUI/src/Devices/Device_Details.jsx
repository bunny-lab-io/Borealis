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
  IconButton,
  Menu,
  MenuItem,
  LinearProgress,
  TableSortLabel,
  TextField,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions
} from "@mui/material";
import StorageRoundedIcon from "@mui/icons-material/StorageRounded";
import MemoryRoundedIcon from "@mui/icons-material/MemoryRounded";
import SpeedRoundedIcon from "@mui/icons-material/SpeedRounded";
import DeveloperBoardRoundedIcon from "@mui/icons-material/DeveloperBoardRounded";
import MoreHorizIcon from "@mui/icons-material/MoreHoriz";
import { ClearDeviceActivityDialog } from "../Dialogs.jsx";
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
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [clearDialogOpen, setClearDialogOpen] = useState(false);
  // Snapshotted status for the lifetime of this page
  const [lockedStatus, setLockedStatus] = useState(() => {
    // Prefer status provided by the device list row if available
    if (device?.status) return device.status;
    // Fallback: compute once from the provided lastSeen timestamp
    const tsSec = device?.lastSeen;
    if (!tsSec) return "Offline";
    const now = Date.now() / 1000;
    return now - tsSec <= 300 ? "Online" : "Offline";
  });

  const statusFromHeartbeat = (tsSec, offlineAfter = 300) => {
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

  // No explicit live recap tab; recaps are recorded into Activity History

  const clearHistory = async () => {
    if (!device?.hostname) return;
    try {
      const resp = await fetch(`/api/device/activity/${encodeURIComponent(device.hostname)}`, { method: "DELETE" });
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      setHistoryRows([]);
    } catch (e) {
      console.warn("Failed to clear activity history", e);
    }
  };

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
  // Build a best-effort CPU display from summary fields
  const cpuInfo = useMemo(() => {
    const cpu = summary.cpu || {};
    const cores = cpu.logical_cores || cpu.cores || cpu.physical_cores;
    let ghz = cpu.base_clock_ghz;
    if (!ghz && typeof (summary.processor || '') === 'string') {
      const m = String(summary.processor).match(/\(([^)]*?)ghz\)/i);
      if (m && m[1]) {
        const n = parseFloat(m[1]);
        if (!Number.isNaN(n)) ghz = n;
      }
    }
    const name = (cpu.name || '').trim();
    const fromProcessor = (summary.processor || '').trim();
    const display = fromProcessor || [name, ghz ? `(${Number(ghz).toFixed(1)}GHz)` : null, cores ? `@ ${cores} Cores` : null].filter(Boolean).join(' ');
    return { cores, ghz, name, display };
  }, [summary]);

  const summaryItems = [
    { label: "Hostname", value: summary.hostname || agent.hostname || device?.hostname || "unknown" },
    { label: "Operating System", value: summary.operating_system || agent.agent_operating_system || "unknown" },
    { label: "Processor", value: cpuInfo.display || "unknown" },
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

  const MetricCard = ({ icon, title, main, sub, color }) => {
    const edgeColor = color || '#232323';
    const parseHex = (hex) => {
      const v = String(hex || '').replace('#', '');
      const n = parseInt(v.length === 3 ? v.split('').map(c => c + c).join('') : v, 16);
      return { r: (n >> 16) & 255, g: (n >> 8) & 255, b: n & 255 };
    };
    const hexToRgba = (hex, alpha = 1) => {
      try { const { r, g, b } = parseHex(hex); return `rgba(${r}, ${g}, ${b}, ${alpha})`; } catch { return `rgba(88,166,255, ${alpha})`; }
    };
    const lightenToRgba = (hex, p = 0.5, alpha = 1) => {
      try {
        const { r, g, b } = parseHex(hex);
        const mix = (c) => Math.round(c + (255 - c) * p);
        const R = mix(r), G = mix(g), B = mix(b);
        return `rgba(${R}, ${G}, ${B}, ${alpha})`;
      } catch { return hexToRgba('#58a6ff', alpha); }
    };
    return (
      <Paper elevation={0} sx={{
        px: 2, py: 1.5, borderRadius: 2, position: 'relative',
        color: '#fff', minWidth: 260,
        border: '1px solid #2f2f2f',
        // Base color with subtle left-to-right Borealis-blue gradient overlay
        background: `linear-gradient(90deg, rgba(88,166,255,0.12) 0%, rgba(88,166,255,0.06) 55%, rgba(88,166,255,0) 100%), ${edgeColor}`,
        boxShadow: `inset 0 0 0 1px ${lightenToRgba(edgeColor, 0.35, 0.6)}`
      }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1 }}>
          {icon}
          <Typography variant="caption" sx={{ opacity: 0.95, fontWeight: 700 }}>{title}</Typography>
        </Box>
      <Typography variant="h6" sx={{ lineHeight: 1.2, mb: 1 }}>{main}</Typography>
      <Box sx={{ flexGrow: 1, minHeight: 12 }} />
      <Box sx={{ minHeight: 8 }} />
      {sub ? <Typography variant="body2" sx={{ opacity: 0.85 }}>{sub}</Typography> : null}
      </Paper>
    );
  };

  const Island = ({ title, children, sx }) => (
    <Paper elevation={0} sx={{ p: 1.5, borderRadius: 2, bgcolor: '#1c1c1c', border: '1px solid #2a2a2a', mb: 1.5, ...(sx || {}) }}>
      <Typography variant="caption" sx={{ color: '#58a6ff', fontWeight: 400, fontSize: '14px', letterSpacing: 0.2, display: 'block', mb: 1 }}>{title}</Typography>
      {children}
    </Paper>
  );

  const renderSummary = () => {
    // Derive metric values
    // CPU tile: model as main, speed as sub (like screenshot)
    const cpuMain = (cpuInfo.name || (summary.processor || '') || '').split('\n')[0] || 'Unknown CPU';
    const cpuSub = cpuInfo.ghz || cpuInfo.cores
      ? (
          <span>
            {cpuInfo.ghz ? `${Number(cpuInfo.ghz).toFixed(2)}GHz ` : ''}
            {cpuInfo.cores ? <span style={{ opacity: 0.75 }}>({cpuInfo.cores}-Cores)</span> : null}
          </span>
        )
      : '';

    // MEMORY: total RAM
    let totalRam = summary.total_ram;
    if (!totalRam && Array.isArray(details.memory)) {
      try { totalRam = details.memory.reduce((a, m) => a + (Number(m.capacity || 0) || 0), 0); } catch {}
    }
    const memVal = totalRam ? `${formatBytes(totalRam)}` : 'Unknown';
    // RAM speed best-effort: use max speed among modules
    let memSpeed = '';
    try {
      const speeds = (details.memory || [])
        .map(m => parseInt(String(m.speed || '').replace(/[^0-9]/g, ''), 10))
        .filter(v => !Number.isNaN(v) && v > 0);
      if (speeds.length) memSpeed = `Speed: ${Math.max(...speeds)} MT/s`;
    } catch {}

    // STORAGE: OS drive (Windows C: if available)
    let osDrive = null;
    if (Array.isArray(details.storage)) {
      osDrive = details.storage.find((d) => String(d.drive || '').toUpperCase().startsWith('C:')) || details.storage[0] || null;
    }
    const storageMain = osDrive && osDrive.total != null ? `${formatBytes(osDrive.total)}` : 'Unknown';
    const storageSub = (osDrive && osDrive.used != null && osDrive.total != null)
      ? `${formatBytes(osDrive.used)} of ${formatBytes(osDrive.total)} used`
      : (osDrive && osDrive.free != null && osDrive.total != null)
        ? `${formatBytes(osDrive.total - osDrive.free)} of ${formatBytes(osDrive.total)} used`
        : '';

    // NETWORK: Speed of adapter with internal IP or first
    const primaryIp = (summary.internal_ip || '').trim();
    let nic = null;
    if (Array.isArray(details.network)) {
      nic = details.network.find((n) => (n.ips || []).includes(primaryIp)) || details.network[0] || null;
    }
    function normalizeSpeed(val) {
      const s = String(val || '').trim();
      if (!s) return 'unknown';
      const low = s.toLowerCase();
      if (low.includes('gbps') || low.includes('mbps')) return s;
      const m = low.match(/(\d+\.?\d*)\s*([gmk]?)(bps)/);
      if (!m) return s;
      let num = parseFloat(m[1]);
      const unit = m[2];
      if (unit === 'g') return `${num} Gbps`;
      if (unit === 'm') return `${num} Mbps`;
      if (unit === 'k') return `${(num/1000).toFixed(1)} Mbps`;
      // raw bps
      if (num >= 1e9) return `${(num/1e9).toFixed(1)} Gbps`;
      if (num >= 1e6) return `${(num/1e6).toFixed(0)} Mbps`;
      return s;
    }
    const netVal = nic ? normalizeSpeed(nic.link_speed || nic.speed) : 'Unknown';

    return (
      <Box>
        {/* Metrics row at the very top */}
        <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat( auto-fit, minmax(260px, 1fr) )', gap: 1.5, mb: 2 }}>
          <MetricCard
            icon={<DeveloperBoardRoundedIcon sx={{ fontSize: 32, opacity: 0.95 }} />}
            title="Processor"
            main={cpuMain}
            sub={cpuSub}
            color="#132332"  
          />
          <MetricCard
            icon={<MemoryRoundedIcon sx={{ fontSize: 32, opacity: 0.95 }} />}
            title="Installed RAM"
            main={memVal}
            sub={memSpeed || ' '}
            color="#291a2e"
          />
          <MetricCard
            icon={<StorageRoundedIcon sx={{ fontSize: 32, opacity: 0.95 }} />}
            title="Storage"
            main={storageMain}
            sub={storageSub || ' '}
            color="#142616"
          />
          <MetricCard
            icon={<SpeedRoundedIcon sx={{ fontSize: 32, opacity: 0.95 }} />}
            title="Network"
            main={netVal}
            sub={(nic && nic.adapter) ? nic.adapter : ' '}
            color="#2b1a18"
          />
        </Box>
        {/* Split pane: three-column layout (Summary | Storage | Memory/Network) */}
        <Box sx={{
          display: 'grid',
          gridTemplateColumns: { xs: '1fr', md: '1.2fr 1fr 1fr' },
          gap: 2
        }}>
          {/* Left column: Summary table */}
          <Island title="Device Summary">
            <Box>
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
                          input: { color: '#fff' },
                          '& .MuiOutlinedInput-root': {
                            '& fieldset': { borderColor: '#555' },
                            '&:hover fieldset': { borderColor: '#888' }
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
          </Island>

          {/* Middle column: Storage */}
          <Island title="Storage">{renderStorage()}</Island>

          {/* Right column: Memory + Network */}
          <Box>
            <Island title="Memory">{renderMemory()}</Island>
            <Island title="Network">{renderNetwork()}</Island>
          </Box>
        </Box>
      </Box>
    );
  };

  const placeholderTable = (headers) => (
    <Box>
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
      <Box sx={{ width: '100%' }}>
        <Box sx={{ mb: 1, width: '100%' }}>
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
        {/* Constrain the table height within the page and enable scrolling */}
        <Box
          sx={{
            width: '100%',
            maxHeight: 'calc(100vh - 320px)',
            overflow: 'auto',
            pr: 1,
          }}
        >
          <Table size="small" sx={{ width: '100%' }}>
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
      <Box>
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

    if (!rows.length) {
      return placeholderTable(["Drive", "Type", "Capacity"]);
    }

    const fmtPct = (v) => (v !== undefined && !Number.isNaN(v) ? `${v.toFixed(0)}%` : "unknown");

    return (
      <Box>
        {rows.map((d, i) => {
          const usage = d.usage ?? (d.total ? ((d.used || 0) / d.total) * 100 : 0);
          const used = d.used;
          const free = d.freeBytes;
          const total = d.total;
          return (
            <Box key={`${d.drive}-${i}`} sx={{ p: 1, borderBottom: '1px solid #2a2a2a', '&:last-child': { borderBottom: 'none' } }}>
              <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 1 }}>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.2 }}>
                  <Box sx={{ width: 8, height: 8, bgcolor: '#58a6ff', borderRadius: 0.5 }} />
                  <Typography variant="body2" sx={{ fontWeight: 600 }}>{`Drive ${String(d.drive || '').replace('\\', '')}`}</Typography>
                  <Typography variant="caption" sx={{ opacity: 0.7 }}>{d.disk_type || 'Fixed Disk'}</Typography>
                </Box>
                <Typography variant="body2" sx={{ opacity: 0.9 }}>{total !== undefined ? formatBytes(total) : 'unknown'}</Typography>
              </Box>
              <Box sx={{
                position: 'relative', height: 8, borderRadius: 1,
                bgcolor: '#2b2b2b',
                background: 'linear-gradient(180deg, #323232 0%, #2a2a2a 100%)',
                boxShadow: 'inset 0 0 0 1px #3a3a3a, inset 0 1px 0 rgba(255,255,255,0.06)'
              }}>
                <Box sx={{
                  position: 'absolute', left: 0, top: 0, bottom: 0,
                  width: `${Math.max(0, Math.min(100, usage || 0)).toFixed(0)}%`,
                  borderRadius: 1,
                  background: 'linear-gradient(90deg, rgba(0,209,140,0.95) 0%, rgba(0,209,140,0.80) 45%, rgba(0,209,140,0.70) 100%)',
                  boxShadow: 'inset 0 0 0 1px rgba(255,255,255,0.15), 0 0 6px rgba(0,209,140,0.25)'
                }} />
              </Box>
              <Box sx={{ display: 'flex', justifyContent: 'space-between', mt: 0.75 }}>
                <Typography variant="caption" sx={{ opacity: 0.85 }}>
                  {used !== undefined ? `${formatBytes(used)} - ${fmtPct(usage)} in use` : 'unknown'}
                </Typography>
                <Typography variant="caption" sx={{ opacity: 0.85 }}>
                  {free !== undefined && total !== undefined ? `${formatBytes(free)} - ${fmtPct(100 - (usage || 0))} remaining` : ''}
                </Typography>
              </Box>
            </Box>
          );
        })}
      </Box>
    );
  };

  const renderNetwork = () => {
    const rows = details.network || [];
    if (!rows.length) return placeholderTable(["Adapter", "IP Address", "MAC Address"]);
    return (
      <Box>
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
    <Box>
      <Table size="small">
        <TableHead>
          <TableRow>
            <TableCell>Assembly</TableCell>
            <TableCell sortDirection={historyOrderBy === "script_name" ? historyOrder : false}>
              <TableSortLabel active={historyOrderBy === "script_name"} direction={historyOrderBy === "script_name" ? historyOrder : "asc"} onClick={() => handleHistorySort("script_name")}>
                Task
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
              <TableCell>{(r.script_type || '').toLowerCase() === 'ansible' ? 'Ansible Playbook' : 'Script'}</TableCell>
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
            <TableRow><TableCell colSpan={5} sx={{ color: "#888" }}>No activity yet.</TableCell></TableRow>
          )}
        </TableBody>
      </Table>
    </Box>
  );

  

  const tabs = [
    { label: "Summary", content: renderSummary() },
    { label: "Installed Software", content: renderSoftware() },
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
          <IconButton
            size="small"
            disabled={!(agent?.hostname || device?.hostname)}
            onClick={(e) => setMenuAnchor(e.currentTarget)}
            sx={{
              color: !(agent?.hostname || device?.hostname) ? "#666" : "#58a6ff",
              borderColor: !(agent?.hostname || device?.hostname) ? "#333" : "#58a6ff",
              border: "1px solid",
              borderRadius: 1,
              width: 32,
              height: 32
            }}
          >
            <MoreHorizIcon fontSize="small" />
          </IconButton>
          <Menu
            anchorEl={menuAnchor}
            open={Boolean(menuAnchor)}
            onClose={() => setMenuAnchor(null)}
          >
            <MenuItem
              onClick={() => {
                setMenuAnchor(null);
                setQuickJobOpen(true);
              }}
            >
              Quick Job
            </MenuItem>
            <MenuItem
              onClick={() => {
                setMenuAnchor(null);
                setClearDialogOpen(true);
              }}
            >
              Clear Device Activity
            </MenuItem>
          </Menu>
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

      {/* Recap dialog removed; recaps flow into Activity History stdout */}

        <ClearDeviceActivityDialog
          open={clearDialogOpen}
          onCancel={() => setClearDialogOpen(false)}
          onConfirm={() => {
            clearHistory();
            setClearDialogOpen(false);
          }}
        />

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
