////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Device_Details.js

import React, { useState, useEffect, useMemo } from "react";
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
  TextField
} from "@mui/material";

export default function DeviceDetails({ device, onBack }) {
  const [tab, setTab] = useState(0);
  const [agent, setAgent] = useState(device || {});
  const [details, setDetails] = useState({});
  const [softwareOrderBy, setSoftwareOrderBy] = useState("name");
  const [softwareOrder, setSoftwareOrder] = useState("asc");
  const [softwareSearch, setSoftwareSearch] = useState("");
  const [description, setDescription] = useState("");

  const statusFromHeartbeat = (tsSec, offlineAfter = 15) => {
    if (!tsSec) return "Offline";
    const now = Date.now() / 1000;
    return now - tsSec <= offlineAfter ? "Online" : "Offline";
  };

  const statusColor = (s) => (s === "Online" ? "#00d18c" : "#ff4f4f");

  useEffect(() => {
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
      return `${m.toString().padStart(2, "0")}/${d.toString().padStart(2, "0")}/${y} - ${hh}:${mm
        .toString()
        .padStart(2, "0")}${ampm}`;
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
    { label: "Device Name", value: summary.hostname || agent.hostname || device?.hostname || "unknown" },
    { label: "Operating System", value: summary.operating_system || agent.agent_operating_system || "unknown" },
    { label: "Last User", value: summary.last_user || "unknown" },
    { label: "Internal IP", value: summary.internal_ip || "unknown" },
    { label: "External IP", value: summary.external_ip || "unknown" },
    { label: "Last Reboot", value: summary.last_reboot ? formatDateTime(summary.last_reboot) : "unknown" },
    { label: "Created", value: summary.created ? formatDateTime(summary.created) : "unknown" }
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
      let usage = toNum(d.usage);
      let freePct;

      if (usage !== undefined) {
        if (usage <= 1) usage *= 100;
        freePct = 100 - usage;
      } else {
        const freeRaw = toNum(d.free);
        if (freeRaw !== undefined) {
          if (freeRaw > 1 && freeRaw > 100 && total) {
            freePct = (freeRaw / total) * 100;
          } else if (freeRaw <= 1) {
            freePct = freeRaw * 100;
          } else {
            freePct = freeRaw;
          }
          usage = freePct !== undefined ? 100 - freePct : undefined;
        } else {
          const usedRaw = toNum(d.used);
          if (usedRaw !== undefined && total) {
            usage = (usedRaw / total) * 100;
            freePct = 100 - usage;
          }
        }
      }

      return {
        drive: d.drive,
        disk_type: d.disk_type,
        usage,
        total,
        free: freePct,
      };
    });

    if (!rows.length)
      return placeholderTable(["Drive Letter", "Disk Type", "Usage", "Total Size", "Free %"]);

    return (
      <Box sx={{ maxHeight: 400, overflowY: "auto" }}>
        <Table size="small">
          <TableHead>
            <TableRow>
              <TableCell>Drive Letter</TableCell>
              <TableCell>Disk Type</TableCell>
              <TableCell>Usage</TableCell>
              <TableCell>Total Size</TableCell>
              <TableCell>Free %</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {rows.map((d, i) => (
              <TableRow key={`${d.drive}-${i}`}>
                <TableCell>{d.drive}</TableCell>
                <TableCell>{d.disk_type}</TableCell>
                <TableCell>
                  <Box sx={{ display: "flex", alignItems: "center" }}>
                    <Box sx={{ flexGrow: 1, mr: 1 }}>
                      <LinearProgress
                        variant="determinate"
                        value={d.usage ?? 0}
                        sx={{
                          height: 10,
                          bgcolor: "#333",
                          "& .MuiLinearProgress-bar": { bgcolor: "#58a6ff" }
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
                <TableCell>
                  {d.total !== undefined && !Number.isNaN(d.total)
                    ? formatBytes(d.total)
                    : "unknown"}
                </TableCell>
                <TableCell>
                  {d.free !== undefined && !Number.isNaN(d.free)
                    ? `${d.free.toFixed(1)}%`
                    : "unknown"}
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

  const tabs = [
    { label: "Summary", content: renderSummary() },
    { label: "Software", content: renderSoftware() },
    { label: "Memory", content: renderMemory() },
    { label: "Storage", content: renderStorage() },
    { label: "Network", content: renderNetwork() }
  ];
  const status = statusFromHeartbeat(agent.last_seen || device?.lastSeen);

  return (
    <Paper sx={{ m: 2, p: 2, bgcolor: "#1e1e1e" }} elevation={2}>
      <Box sx={{ mb: 2, display: "flex", alignItems: "center" }}>
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
    </Paper>
  );
}

