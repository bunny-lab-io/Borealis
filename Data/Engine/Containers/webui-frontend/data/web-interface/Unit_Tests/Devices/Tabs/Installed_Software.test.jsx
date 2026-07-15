import { describe, expect, it } from "vitest";
import {
  formatSoftwareInstallDate,
  formatSoftwareInstallDateDetail,
  formatSoftwareInstallDateValue,
  getSoftwareInstallDateSortValue,
} from "@/Devices/Tabs/Installed_Software.jsx";

describe("Installed Software helpers", () => {
  it("formats Windows registry InstallDate values", () => {
    expect(formatSoftwareInstallDate({ metadata: { install_date: "20240115" } })).toBe("01/15/2024");
    expect(formatSoftwareInstallDate({ metadata: { install_date: "2024-01-15" } })).toBe("01/15/2024");
    expect(formatSoftwareInstallDateValue("01/15/2024")).toBe("01/15/2024");
  });

  it("falls back through install date aliases and top-level values", () => {
    expect(formatSoftwareInstallDate({ metadata: { installed_on: "20231205" } })).toBe("12/05/2023");
    expect(formatSoftwareInstallDate({ install_date: "20221130" })).toBe("11/30/2022");
  });

  it("sorts valid install dates ahead of missing values", () => {
    const newer = getSoftwareInstallDateSortValue({ metadata: { install_date: "20240201" } });
    const older = getSoftwareInstallDateSortValue({ metadata: { install_date: "20240101" } });
    const missing = getSoftwareInstallDateSortValue({ metadata: {} });

    expect(newer).toBeGreaterThan(older);
    expect(older).toBeGreaterThan(missing);
  });

  it("formats unsupported install date values as unknown", () => {
    expect(formatSoftwareInstallDateValue("not-a-date")).toBe("—");
    expect(formatSoftwareInstallDate({ metadata: { install_date: "20241301" } })).toBe("—");
  });

  it("describes exact and estimated install date provenance", () => {
    expect(
      formatSoftwareInstallDateDetail({
        metadata: {
          install_date: "20240115",
          install_date_source: "registry_install_date",
          install_date_confidence: "exact",
        },
      })
    ).toBe("01/15/2024 • Exact from Registry InstallDate");
    expect(
      formatSoftwareInstallDateDetail({
        metadata: {
          install_date: "20240116",
          install_date_source: "install_location_creation_time",
          install_date_confidence: "estimated",
        },
      })
    ).toBe("01/16/2024 • Estimated from Install folder creation time");
  });
});
