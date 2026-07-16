import { describe, expect, it } from "vitest";
import {
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
    expect(normalizeDeviceConnectivityStatus("DISCONNECTED")).toBe("Disconnected");
    expect(normalizeDeviceConnectivityStatus("offline")).toBe("Offline");
  });

  it("maps online heartbeat plus socket state to site list status labels", () => {
    expect(deviceConnectivityStatusFromState({ status: "Online", agentSocket: true })).toBe("Connected");
    expect(deviceConnectivityStatusFromState({ status: "Online", agentSocket: false })).toBe("Disconnected");
    expect(deviceConnectivityStatusFromState({ status: "Offline", agentSocket: true })).toBe("Offline");
    expect(deviceConnectivityStatusFromState({ status: "Disconnected", agentSocket: true })).toBe("Connected");
  });

  it("builds exact AG Grid filter for status drilldowns", () => {
    expect(buildDeviceConnectivityStatusFilter("disconnected")).toEqual({
      filterType: "text",
      type: "equals",
      filter: "Disconnected",
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
