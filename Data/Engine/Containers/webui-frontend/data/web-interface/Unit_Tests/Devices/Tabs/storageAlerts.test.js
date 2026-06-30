import { describe, expect, it } from "vitest";
import { isCdRomStorageDevice, isStorageUsageAlert } from "@/Devices/Tabs/storageAlerts.js";

describe("storage alert helpers", () => {
  it("flags fixed disks above the usage threshold", () => {
    expect(isStorageUsageAlert({ disk_type: "Fixed Disk", usage: 91 })).toBe(true);
    expect(isStorageUsageAlert({ disk_type: "Fixed Disk", usage: 90 })).toBe(false);
  });

  it("ignores CD-ROM storage devices for usage alerts", () => {
    expect(isCdRomStorageDevice({ disk_type: "CD-ROM" })).toBe(true);
    expect(isStorageUsageAlert({ disk_type: "CD-ROM", usage: 100 })).toBe(false);
    expect(isStorageUsageAlert({ type: "cdrom", usage: 99 })).toBe(false);
  });
});
