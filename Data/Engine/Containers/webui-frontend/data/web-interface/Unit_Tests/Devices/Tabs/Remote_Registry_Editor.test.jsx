import { describe, expect, it } from "vitest";
import {
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
});
