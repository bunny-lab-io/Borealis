import { describe, expect, it } from "vitest";
import {
  buildRegistryAddressSegments,
  buildRegistryPathChain,
  buildVisibleRows,
  collapseExpandedBranch,
  normalizeKeyEntry,
  normalizeRegistryPath,
  normalizeValueEntry,
  valueDataToEditor,
} from "@/Devices/Tabs/Remote_Registry_Editor.jsx";

describe("Remote Registry Editor helpers", () => {
  it("normalizes registry paths", () => {
    expect(normalizeRegistryPath("\\HKLM//SOFTWARE\\\\Borealis\\")).toBe("HKLM\\SOFTWARE\\Borealis");
  });

  it("normalizes key rows", () => {
    const row = normalizeKeyEntry({
      path: "HKLM\\SOFTWARE",
      parent_path: "HKLM",
      name: "SOFTWARE",
      kind: "key",
      modified_at: 1700000000,
    });
    expect(row.id).toBe("key:HKLM\\SOFTWARE");
    expect(row.row_type).toBe("key");
    expect(row.editable).toBe(true);
  });

  it("normalizes default registry value rows", () => {
    const row = normalizeValueEntry(
      {
        name: "",
        type: "REG_MULTI_SZ",
        data: ["one", "two"],
        editable: true,
      },
      "HKLM\\SOFTWARE\\Borealis"
    );
    expect(row.display_name).toBe("(Default)");
    expect(row.data_label).toBe("one; two");
    expect(valueDataToEditor(row)).toBe("one\ntwo");
  });

  it("builds clickable registry breadcrumb segments", () => {
    expect(buildRegistryAddressSegments("HKLM\\SOFTWARE\\Borealis")).toEqual([
      { label: "HKLM", path: "HKLM" },
      { label: "SOFTWARE", path: "HKLM\\SOFTWARE" },
      { label: "Borealis", path: "HKLM\\SOFTWARE\\Borealis" },
    ]);
    expect(buildRegistryPathChain("HKLM\\SOFTWARE\\Borealis")).toEqual([
      "HKLM",
      "HKLM\\SOFTWARE",
      "HKLM\\SOFTWARE\\Borealis",
    ]);
  });

  it("flattens opened registry keys while preserving parent context", () => {
    const entriesByParent = {
      __registry_roots__: [
        normalizeKeyEntry({ path: "HKLM", name: "HKLM", kind: "hive", editable: false }),
        normalizeKeyEntry({ path: "HKCU", name: "HKCU", kind: "hive", editable: false }),
      ],
      HKLM: [
        normalizeKeyEntry({ path: "HKLM\\SOFTWARE", parent_path: "HKLM", name: "SOFTWARE", kind: "key" }),
        normalizeValueEntry({ name: "InstallPath", type: "REG_SZ", data: "C:\\Borealis" }, "HKLM"),
      ],
      "HKLM\\SOFTWARE": [
        normalizeKeyEntry({ path: "HKLM\\SOFTWARE\\Borealis", parent_path: "HKLM\\SOFTWARE", name: "Borealis", kind: "key" }),
      ],
    };

    const rows = buildVisibleRows(entriesByParent, new Set(["HKLM", "HKLM\\SOFTWARE"]), []);
    expect(rows.map((row) => `${row.depth}:${row.display_name}`)).toEqual([
      "0:HKCU",
      "0:HKLM",
      "1:SOFTWARE",
      "2:Borealis",
      "1:InstallPath",
    ]);
  });

  it("collapses expanded registry descendants under one branch", () => {
    const entriesByParent = {
      HKLM: [
        normalizeKeyEntry({ path: "HKLM\\SOFTWARE", parent_path: "HKLM", name: "SOFTWARE", kind: "key" }),
        normalizeKeyEntry({ path: "HKLM\\SYSTEM", parent_path: "HKLM", name: "SYSTEM", kind: "key" }),
      ],
      "HKLM\\SOFTWARE": [
        normalizeKeyEntry({ path: "HKLM\\SOFTWARE\\Borealis", parent_path: "HKLM\\SOFTWARE", name: "Borealis", kind: "key" }),
      ],
    };

    const collapsed = collapseExpandedBranch(
      new Set(["HKLM", "HKLM\\SOFTWARE", "HKLM\\SOFTWARE\\Borealis", "HKLM\\SYSTEM"]),
      entriesByParent,
      "HKLM\\SOFTWARE"
    );
    expect([...collapsed].sort()).toEqual(["HKLM", "HKLM\\SYSTEM"]);
  });
});
