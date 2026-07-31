import React from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  ROW_CONTEXT_MENU_COL_ID,
  ROW_CONTEXT_MENU_COLUMN_WIDTH,
  buildRowContextMenuColumnDef,
} from "@/Grid_Row_Context_Menu_Button.jsx";

describe("Grid row context menu button", () => {
  it("builds a pinned right AG Grid action column", () => {
    const columnDef = buildRowContextMenuColumnDef(() => {});

    expect(columnDef.colId).toBe(ROW_CONTEXT_MENU_COL_ID);
    expect(columnDef.pinned).toBe("right");
    expect(columnDef.width).toBe(ROW_CONTEXT_MENU_COLUMN_WIDTH);
    expect(columnDef.sortable).toBe(false);
    expect(columnDef.filter).toBe(false);
    expect(columnDef.suppressHeaderContextMenu).toBe(true);
  });

  it("opens a row context menu with row and node details", () => {
    const row = { id: "row-1", name: "Example Row" };
    const node = { id: "node-1" };
    const openContextMenu = vi.fn();
    const columnDef = buildRowContextMenuColumnDef(openContextMenu, {
      tooltip: "Open row actions",
    });

    render(columnDef.cellRenderer({ data: row, node }));

    fireEvent.click(screen.getByRole("button", { name: "Open row actions" }), {
      clientX: 230,
      clientY: 90,
    });

    expect(openContextMenu).toHaveBeenCalledTimes(1);
    expect(openContextMenu.mock.calls[0][1]).toBe(row);
    expect(openContextMenu.mock.calls[0][2]).toBe(node);
    expect(openContextMenu.mock.calls[0][0].clientX).toBe(230);
    expect(openContextMenu.mock.calls[0][0].clientY).toBe(90);
  });
});
