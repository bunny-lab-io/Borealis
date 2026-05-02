import React from "react";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import PageSubtitleMarkdown, {
  parsePageSubtitleMarkdown,
} from "@/app/shell/PageSubtitleMarkdown.jsx";

describe("PageSubtitleMarkdown", () => {
  it("keeps plain subtitle text unchanged when no markdown links are present", () => {
    render(
      <MemoryRouter>
        <PageSubtitleMarkdown text="Collections of scripts and workflows." />
      </MemoryRouter>
    );

    expect(screen.getByText("Collections of scripts and workflows.")).toBeInTheDocument();
  });

  it("renders external markdown-style links as clickable anchors", () => {
    render(
      <MemoryRouter>
        <PageSubtitleMarkdown text="Browse the [Aurora Assembly Repository](https://github.com/bunny-lab-io/Aurora)." />
      </MemoryRouter>
    );

    const link = screen.getByRole("link", { name: "Aurora Assembly Repository" });
    expect(link).toHaveAttribute("href", "https://github.com/bunny-lab-io/Aurora");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("renders internal markdown-style links through the router", () => {
    render(
      <MemoryRouter>
        <PageSubtitleMarkdown text="Review the [API Reference](/api/reference) for endpoint details." />
      </MemoryRouter>
    );

    const link = screen.getByRole("link", { name: "API Reference" });
    expect(link.getAttribute("href")).toBe("/api/reference");
    expect(link).not.toHaveAttribute("target");
  });

  it("parses multiple markdown-style links in a single subtitle string", () => {
    expect(
      parsePageSubtitleMarkdown(
        "See [Docs](https://example.com/docs) or [Local Help](/help) for more information."
      )
    ).toEqual([
      { type: "text", value: "See " },
      {
        type: "link",
        label: "Docs",
        href: "https://example.com/docs",
        raw: "[Docs](https://example.com/docs)",
      },
      { type: "text", value: " or " },
      {
        type: "link",
        label: "Local Help",
        href: "/help",
        raw: "[Local Help](/help)",
      },
      { type: "text", value: " for more information." },
    ]);
  });
});
