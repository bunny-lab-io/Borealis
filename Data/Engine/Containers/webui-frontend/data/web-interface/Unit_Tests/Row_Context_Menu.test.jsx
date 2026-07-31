import { describe, expect, it } from "vitest";
import { buildContextMenuGroups } from "@/Row_Context_Menu.jsx";

describe("Row context menu grouping", () => {
  it("keeps visible actions in Borealis group order", () => {
    const groups = buildContextMenuGroups([
      { id: "delete", group: "danger", label: "Delete" },
      { id: "open", group: "primary", label: "Open" },
      { id: "hidden", group: "primary", label: "Hidden", hidden: true },
      { id: "archive", group: "organize", label: "Archive" },
    ]);

    expect(groups.map((group) => group.id)).toEqual(["primary", "organize", "danger"]);
    expect(groups[0].actions.map((action) => action.id)).toEqual(["open"]);
    expect(groups[1].actions.map((action) => action.id)).toEqual(["archive"]);
    expect(groups[2].actions.map((action) => action.id)).toEqual(["delete"]);
  });
});
