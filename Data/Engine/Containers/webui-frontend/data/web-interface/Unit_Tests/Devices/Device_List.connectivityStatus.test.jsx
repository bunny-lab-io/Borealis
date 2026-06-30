import { describe, expect, it } from "vitest";
import {
  buildDeviceConnectivityStatusFilter,
  deviceConnectivityStatusFromState,
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
});
