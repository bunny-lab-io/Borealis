import { describe, expect, it } from "vitest";
import {
  buildDeviceListColumnGroups,
  buildDeviceListMetadataColumnOptions,
  buildDeviceConnectivityStatusFilter,
  deviceMetadataColumnId,
  deviceConnectivityStatusFromState,
  normalizeDeviceCollection,
  normalizeDeviceConnectivityStatus,
} from "@/Devices/Device_List.jsx";

describe("device list connectivity status", () => {
  it("normalizes route status tokens to Device List labels", () => {
    expect(normalizeDeviceConnectivityStatus("connected")).toBe("Connected");
    expect(normalizeDeviceConnectivityStatus("DISCONNECTED")).toBe("Reconnecting");
    expect(normalizeDeviceConnectivityStatus("reconnecting")).toBe("Reconnecting");
    expect(normalizeDeviceConnectivityStatus("offline")).toBe("Offline");
  });

  it("maps online heartbeat plus socket state to site list status labels", () => {
    expect(deviceConnectivityStatusFromState({ status: "Online", agentSocket: true })).toBe("Connected");
    expect(deviceConnectivityStatusFromState({ status: "Online", agentSocket: false })).toBe("Reconnecting");
    expect(deviceConnectivityStatusFromState({ status: "Offline", agentSocket: true })).toBe("Offline");
    expect(deviceConnectivityStatusFromState({ status: "Disconnected", agentSocket: true })).toBe("Connected");
  });

  it("builds exact AG Grid filter for status drilldowns", () => {
    expect(buildDeviceConnectivityStatusFilter("disconnected")).toEqual({
      filterType: "text",
      type: "equals",
      filter: "Reconnecting",
    });
  });

  it("builds chooser options for described metadata fields and skips Reserved placeholders", () => {
    expect(
      buildDeviceListMetadataColumnOptions([
        { field_number: 1, description: "Server Roles", reserved: true },
        { field_number: 10, description: "Reserved", reserved: true },
        { field_number: 11, description: "Asset Tag" },
        { field_number: 12, description: "" },
      ])
    ).toEqual([
      {
        id: "metadataField001",
        label: "Server Roles",
        fieldKey: "field_001",
        fieldNumber: 1,
      },
      {
        id: "metadataField011",
        label: "Asset Tag",
        fieldKey: "field_011",
        fieldNumber: 11,
      },
    ]);
    expect(deviceMetadataColumnId(11)).toBe("metadataField011");
  });

  it("groups Device List chooser columns and sorts each group alphabetically", () => {
    const groups = buildDeviceListColumnGroups({
      staticLabels: {
        hostname: "Hostname",
        os: "OS",
        type: "Type",
        uptime: "Uptime",
        memory: "Memory",
        storage: "Storage",
        cpu: "CPU",
        description: "Description",
        software: "Software",
        site: "Site",
        domain: "Domain",
        siteDescription: "Site Description",
        internalIp: "Internal IP",
        externalIp: "External IP",
        wireguardVpnStatus: "WireGuard VPN Status",
        wireguardPeerIp: "WireGuard Peer IP",
        network: "Network",
        lastUser: "Last User",
        lastReboot: "Last Reboot",
        created: "Created",
        lastSeen: "Last Seen",
        agentId: "Agent ID",
        agentGuid: "Agent GUID",
      },
      metadataFields: [
        { id: "metadataField012", label: "Warranty Date" },
        { id: "metadataField011", label: "Asset Tag" },
      ],
    });

    expect(groups.map((group) => group.label)).toEqual([
      "Device Specs",
      "Agent",
      "Location",
      "Networking",
      "Heartbeat",
      "Metadata",
    ]);
    expect(groups.find((group) => group.label === "Device Specs").options.map((option) => option.label)).toEqual([
      "CPU",
      "Description",
      "Hostname",
      "Memory",
      "OS",
      "Software",
      "Storage",
      "Type",
      "Uptime",
    ]);
    expect(groups.find((group) => group.label === "Agent").options.map((option) => option.label)).toEqual([
      "Agent GUID",
      "Agent ID",
    ]);
    expect(groups.find((group) => group.label === "Location").options.map((option) => option.label)).toEqual([
      "Domain",
      "Site",
      "Site Description",
    ]);
    expect(groups.find((group) => group.label === "Networking").options.map((option) => option.label)).toEqual([
      "External IP",
      "Internal IP",
      "Network",
      "WireGuard Peer IP",
      "WireGuard VPN Status",
    ]);
    expect(groups.find((group) => group.label === "Heartbeat").options.map((option) => option.label)).toEqual([
      "Created",
      "Last Reboot",
      "Last Seen",
      "Last User",
    ]);
    expect(groups.find((group) => group.label === "Metadata").options.map((option) => option.label)).toEqual([
      "Asset Tag",
      "Warranty Date",
    ]);
  });

  it("flattens sparse metadata values into Device List column fields", () => {
    const [row] = normalizeDeviceCollection([
      {
        hostname: "LAB-OPERATOR-01",
        agent_guid: "2540DA38-E2B1-45B9-9113-BF7CF0E1778A",
        metadata_fields: {
          field_011: "Asset-123",
          field_012: { value: "Rack A7" },
        },
      },
    ]);

    expect(row.metadataFields).toEqual({
      field_011: "Asset-123",
      field_012: "Rack A7",
    });
    expect(row.metadataField011).toBe("Asset-123");
    expect(row.metadataField012).toBe("Rack A7");
  });

});
