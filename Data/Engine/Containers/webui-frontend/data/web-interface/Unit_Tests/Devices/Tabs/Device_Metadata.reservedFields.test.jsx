import { describe, expect, it, vi } from "vitest";

import { FieldNumberCellRenderer } from "@/Metadata_Field_Cells.jsx";
import { buildDeviceMetadataColumnDefs } from "@/Devices/Tabs/Device_Metadata.jsx";

describe("DeviceMetadata reserved fields", () => {
  it("uses reserved assembly field-number links in device metadata grid", () => {
    const columns = buildDeviceMetadataColumnDefs({ saveValue: vi.fn(), valueLimit: 1024 });

    expect(columns[0]).toMatchObject({
      headerName: "Field Number",
      field: "default_label",
      pinned: "left",
    });
    expect(columns[0].cellRenderer).toBe(FieldNumberCellRenderer);
  });
});
