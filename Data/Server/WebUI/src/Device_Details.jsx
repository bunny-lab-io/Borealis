////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Device_Details.jsx

import React, { useState, useEffect } from "react";
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
  Button
} from "@mui/material";

export default function DeviceDetails({ device, onBack }) {
  const [tab, setTab] = useState(0);
  const [agent, setAgent] = useState(device || {});
  const [details, setDetails] = useState({});

  useEffect(() => {
    if (!device || !device.id) return;
    const load = async () => {
      try {
        const [agentsRes, detailsRes] = await Promise.all([
          fetch("/api/agents"),
          fetch(`/api/agent/details/${device.id}`)
        ]);
        const agentsData = await agentsRes.json();
        if (agentsData && agentsData[device.id]) {
          setAgent({ id: device.id, ...agentsData[device.id] });
        }
        const detailData = await detailsRes.json();
        setDetails(detailData || {});
      } catch (e) {
        console.warn("Failed to load device info", e);
      }
    };
    load();
  }, [device]);

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

  const summary = details.summary || {};
  const summaryItems = [
    { label: "Device Name", value: summary.hostname || agent.hostname || device?.hostname || "unknown" },
    { label: "Description", value: summary.description || "unknown" },
    { label: "Operating System", value: summary.operating_system || agent.agent_operating_system || "unknown" },
    { label: "Last User", value: summary.last_user || "unknown" },
    { label: "Internal IP", value: summary.internal_ip || "unknown" },
    { label: "External IP", value: summary.external_ip || "unknown" },
    { label: "Last Reboot", value: summary.last_reboot || "unknown" },
    { label: "Created", value: summary.created || "unknown" }
  ];

  const renderSummary = () => (
    <Table size="small">
      <TableBody>
        {summaryItems.map((item) => (
          <TableRow key={item.label}>
            <TableCell sx={{ fontWeight: 500 }}>{item.label}</TableCell>
            <TableCell>{item.value}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );

  const placeholderTable = (headers) => (
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
  );

  const renderSoftware = () => {
    const rows = details.software || [];
    if (!rows.length) return placeholderTable(["Software Name", "Version", "Action"]);
    return (
      <Table size="small">
        <TableHead>
          <TableRow>
            <TableCell>Software Name</TableCell>
            <TableCell>Version</TableCell>
            <TableCell>Action</TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {rows.map((s, i) => (
            <TableRow key={`${s.name}-${i}`}>
              <TableCell>{s.name}</TableCell>
              <TableCell>{s.version}</TableCell>
              <TableCell></TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    );
  };

  const renderMemory = () => {
    const rows = details.memory || [];
    if (!rows.length) return placeholderTable(["Slot", "Speed", "Serial Number", "Capacity"]);
    return (
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
    );
  };

  const renderStorage = () => {
    const rows = details.storage || [];
    if (!rows.length)
      return placeholderTable(["Drive Letter", "Disk Type", "Usage", "Total Size", "Free %"]);
    return (
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
                {d.usage !== undefined && d.usage !== null && d.usage !== "unknown"
                  ? `${d.usage.toFixed ? d.usage.toFixed(1) : d.usage}%`
                  : "unknown"}
              </TableCell>
              <TableCell>{formatBytes(d.total)}</TableCell>
              <TableCell>
                {d.free !== undefined && d.free !== null && d.free !== "unknown"
                  ? `${d.free.toFixed ? d.free.toFixed(1) : d.free}%`
                  : "unknown"}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    );
  };

  const renderNetwork = () => {
    const rows = details.network || [];
    if (!rows.length) return placeholderTable(["Adapter", "IP Address", "MAC Address"]);
    return (
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
              <TableCell>{n.mac}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    );
  };

  const tabs = [
    { label: "Summary", content: renderSummary() },
    {
      label: "Monitors",
      content: placeholderTable([
        "Type",
        "Description",
        "Latest Value",
        "Policy",
        "Latest 10 Days of Alerts",
        "Enabled/Disabled Status"
      ])
    },
    { label: "Software", content: renderSoftware() },
    { label: "Memory", content: renderMemory() },
    { label: "Storage", content: renderStorage() },
    { label: "Network", content: renderNetwork() }
  ];

  return (
    <Paper sx={{ m: 2, p: 2, bgcolor: "#1e1e1e" }} elevation={2}>
      <Box sx={{ mb: 2, display: "flex", alignItems: "center" }}>
        {onBack && (
          <Button variant="outlined" size="small" onClick={onBack} sx={{ mr: 2 }}>
            Back
          </Button>
        )}
        <Typography variant="h6" sx={{ color: "#58a6ff" }}>
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

