import { describe, expect, it, vi } from "vitest";

import { FieldNumberCellRenderer } from "@/Metadata_Field_Cells.jsx";
import { buildDeviceMetadataColumnDefs } from "@/Devices/Tabs/Device_Metadata.jsx";

describe("DeviceMetadata reserved fields", () => {
  it("uses reserved assembly field-number links in device metadata grid", () => {
    const columns = buildDeviceMetadataColumnDefs({ saveValue: vi.fn(), valueLimit: 1024 });

    expect(columns[0]).toMatchObject({
      headerName: "Field Number",
      field: "default_label",
      width: 150,
      pinned: "left",
      resizable: false,
    });
    expect(columns[0].cellRenderer).toBe(FieldNumberCellRenderer);
  });

  it("sizes device metadata columns with value filling unused width", () => {
    const columns = buildDeviceMetadataColumnDefs({ saveValue: vi.fn(), valueLimit: 1024 });
    const descriptionColumn = columns.find((column) => column.field === "label");
    const valueColumn = columns.find((column) => column.field === "value");
    const modifiedColumn = columns.find((column) => column.field === "modified_at");
    const sourceColumn = columns.find((column) => column.field === "source");

    expect(descriptionColumn).toMatchObject({
      headerName: "Field Description",
      width: 260,
      minWidth: 220,
      maxWidth: 460,
      suppressSizeToFit: true,
    });
    expect(descriptionColumn).not.toHaveProperty("flex");
    expect(valueColumn).toMatchObject({
      headerName: "Value",
      minWidth: 320,
      flex: 1,
    });
    expect(modifiedColumn).toMatchObject({
      headerName: "Modified",
      width: 108,
      minWidth: 108,
    });
    expect(modifiedColumn).not.toHaveProperty("flex");
    expect(sourceColumn).toMatchObject({
      headerName: "Source",
      width: 72,
      minWidth: 72,
      resizable: false,
    });
    expect(sourceColumn).not.toHaveProperty("flex");
  });
});
