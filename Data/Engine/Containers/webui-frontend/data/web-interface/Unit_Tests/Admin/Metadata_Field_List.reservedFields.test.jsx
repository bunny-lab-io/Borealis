import React from "react";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import {
  DescriptionCellRenderer,
  FieldNumberCellRenderer,
  RESERVED_METADATA_TOOLTIP,
  getReservedAssemblyPath,
} from "@/Admin/Metadata_Field_List.jsx";

const reservedField = {
  field_number: 1,
  field_key: "field_001",
  default_label: "Field 001",
  label: "Server Roles",
  description: "Server Roles",
  reserved: true,
  reserved_tooltip: RESERVED_METADATA_TOOLTIP,
  linked_assembly: {
    guid: "628f6686-c7c4-477d-bf9a-13c73d8246ba",
    name: "Detect Server Roles [WIN]",
    type: "script",
  },
};

describe("MetadataFieldList reserved fields", () => {
  it("builds reserved assembly links from field metadata", () => {
    expect(getReservedAssemblyPath(reservedField)).toBe(
      "/assemblies/scripts/628f6686-c7c4-477d-bf9a-13c73d8246ba"
    );
  });

  it("renders reserved field number as assembly link", () => {
    render(
      <MemoryRouter>
        <FieldNumberCellRenderer data={reservedField} value="Field 001" />
      </MemoryRouter>
    );

    expect(screen.getByRole("link", { name: "Field 001" })).toHaveAttribute(
      "href",
      "/assemblies/scripts/628f6686-c7c4-477d-bf9a-13c73d8246ba"
    );
  });

  it("renders reserved description as read-only input", () => {
    const onSaveDescription = vi.fn();

    render(
      <DescriptionCellRenderer
        data={reservedField}
        value="Server Roles"
        onSaveDescription={onSaveDescription}
      />
    );

    expect(screen.getByDisplayValue("Server Roles")).toHaveAttribute("readonly");
    expect(onSaveDescription).not.toHaveBeenCalled();
  });
});
