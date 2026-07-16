import React from "react";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { DescriptionCellRenderer } from "@/Admin/Metadata_Field_List.jsx";
import {
  FieldNumberCellRenderer,
  RESERVED_METADATA_TOOLTIP,
  getReservedAssemblyName,
  getReservedAssemblyPath,
} from "@/Metadata_Field_Cells.jsx";

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

const placeholderReservedField = {
  field_number: 10,
  field_key: "field_010",
  default_label: "Field 010",
  label: "Reserved",
  description: "Reserved",
  reserved: true,
  reserved_tooltip: "Reserved Borealis Metadata Field - Reserved for future Borealis use.",
};

describe("MetadataFieldList reserved fields", () => {
  it("builds reserved assembly links from field metadata", () => {
    expect(getReservedAssemblyPath(reservedField)).toBe(
      "/assemblies/scripts/628f6686-c7c4-477d-bf9a-13c73d8246ba"
    );
    expect(getReservedAssemblyName(reservedField)).toBe("Detect Server Roles [WIN]");
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

  it("renders placeholder reserved field number without assembly link", () => {
    render(
      <MemoryRouter>
        <FieldNumberCellRenderer data={placeholderReservedField} value="Field 010" />
      </MemoryRouter>
    );

    expect(screen.getByText("Field 010")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Field 010" })).not.toBeInTheDocument();
    expect(getReservedAssemblyPath(placeholderReservedField)).toBe("");
    expect(getReservedAssemblyName(placeholderReservedField)).toBe("");
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
