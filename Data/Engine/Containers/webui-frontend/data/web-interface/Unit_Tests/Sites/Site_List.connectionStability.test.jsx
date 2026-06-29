import { describe, expect, it } from "vitest";
import {
  applySiteConnectionStability,
  recordSiteConnectionSnapshots,
  shouldShowConnectedDevicesPlaceholder,
  siteListRowHeightForData,
  siteListRowHeightSignature,
} from "@/Sites/Site_List.jsx";

describe("site connection stability", () => {
  it("shows connection analysis placeholder until worker metrics are visible", () => {
    expect(shouldShowConnectedDevicesPlaceholder({ site_worker_metrics_visible: false })).toBe(true);
    expect(shouldShowConnectedDevicesPlaceholder({ site_worker_metrics_visible: true })).toBe(false);
  });

  it("keeps the last connected breakdown during a short zero-connected poll", () => {
    const snapshots = new Map();
    const stableRows = [
      {
        site_id: 7,
        connected_devices: 3,
        disconnected_devices: 0,
        offline_devices: 0,
        site_device_count: 3,
        site_online_device_count: 3,
      },
    ];

    expect(recordSiteConnectionSnapshots(snapshots, stableRows, 1000)).toBe(1);

    const flickerRows = applySiteConnectionStability(
      [
        {
          site_id: 7,
          connected_devices: 0,
          disconnected_devices: 3,
          offline_devices: 0,
          site_device_count: 3,
          site_online_device_count: 3,
        },
      ],
      snapshots,
      1010
    );

    expect(flickerRows[0].connected_devices).toBe(3);
    expect(flickerRows[0].disconnected_devices).toBe(0);
    expect(flickerRows[0].site_connection_stabilized).toBe(true);
  });

  it("lets sustained zero-connected polls through after the grace window", () => {
    const snapshots = new Map();
    recordSiteConnectionSnapshots(
      snapshots,
      [
        {
          site_id: 7,
          connected_devices: 3,
          disconnected_devices: 0,
          offline_devices: 0,
          site_device_count: 3,
          site_online_device_count: 3,
        },
      ],
      1000
    );

    const expiredRows = applySiteConnectionStability(
      [
        {
          site_id: 7,
          connected_devices: 0,
          disconnected_devices: 3,
          offline_devices: 0,
          site_device_count: 3,
          site_online_device_count: 3,
        },
      ],
      snapshots,
      1021
    );

    expect(expiredRows[0].connected_devices).toBe(0);
    expect(expiredRows[0].disconnected_devices).toBe(3);
    expect(expiredRows[0].site_connection_stabilized).toBeUndefined();
  });
});

describe("site running task row heights", () => {
  it("scales row height from running task group count", () => {
    expect(siteListRowHeightForData({ assigned_task_groups: [] })).toBe(56);
    expect(siteListRowHeightForData({ assigned_task_groups: [{ key: "a" }, { key: "b" }] })).toBe(112);
  });

  it("changes signature when running task group count changes", () => {
    const before = siteListRowHeightSignature([{ id: 7, assigned_task_groups: [{ key: "a" }] }]);
    const after = siteListRowHeightSignature([{ id: 7, assigned_task_groups: [] }]);

    expect(before).not.toBe(after);
  });
});
