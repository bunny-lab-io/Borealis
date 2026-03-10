# Standardized Page Bodies

## Summary
This document defines the implementation plan for standardizing the main body area of Borealis primary pages: the section below the shared page title, subtitle, and header action rail.

The shared header band is already standardized in `App.jsx`, but the body area is still page-local. Different pages currently use different horizontal padding, bottom padding, top offsets, container hierarchies, and shell chrome. The result is visible inconsistency in how the main content aligns to the page edges and how the rounded body container is presented.

The chosen implementation is to introduce a shared page-level body wrapper component, tentatively named `PageBodyFrame`, and migrate primary pages to it. `App.jsx` will continue to own the page header. Pages will continue to own their content, but only through one of a small number of sanctioned body layout variants.

This plan is intended to be decision complete so a future Codex agent can implement it directly.

## Goals
- Make primary page bodies align consistently to the left and right edges of the content area.
- Standardize the vertical relationship between the shared header band and the body.
- Standardize the rounded outer shell, border, background, and shadow for primary page bodies.
- Support pages whose body content is usually an AG Grid table, but not always.
- Eliminate page-local one-off body wrappers using ad hoc `px`, `pb`, `pt`, and `mt` combinations.
- Keep the standard simple enough that future pages do not invent new body patterns unnecessarily.

## Non-Goals
- This does not standardize embedded sub-pages or page-within-a-page surfaces such as device detail sub-tabs, VNC, or remote shell panes.
- This does not redesign AG Grid internals, row spacing, column rules, or table typography beyond ensuring they sit inside the standardized shell.
- This does not move body ownership into the App page metadata contract. The standard stays page-level, not App-injected.

## Current State
The shared header band is already owned by `App.jsx`, but the page body is still assembled manually in each page.

The main inconsistency comes from page-local body wrappers using different tokens and patterns:
- `px: 2` vs `px: 3`
- `pb: 2` vs `pb: 3`
- `p: 2` vs `p: 3`
- ad hoc `mt: "10px"` body drops
- some pages render the rounded shell outside the grid
- some pages let the grid wrapper itself become the shell
- some pages place banners and control rows outside the main body shell
- split-pane pages use fully custom body structures

Observed baseline judgments from the user:
- Wrong: `Site_List.jsx`
- Correct: `Device_Approvals.jsx`
- Wrong: `Device_List.jsx`
- Correct but vertically too low: `Assembly_List.jsx`
- Correct: `Scheduled_Jobs_List.jsx`
- Wrong: `Filter_List.jsx`
- Wrong: `Credential_List.jsx`
- Wrong: `Log_Management.jsx`

Source review confirmed there is currently no shared body-frame standard in `Docs/ui-and-notifications.md`.

## Chosen Direction
Use a shared page-owned body wrapper component, tentatively `PageBodyFrame`, rather than an App-owned automatic wrapper.

### Why This Direction
- The App already correctly owns the shared header.
- The body needs layout variants that depend on page content shape.
- A page-owned shared component gives consistency without overloading the App metadata contract.
- This makes the standard explicit and easier for future page authors to apply correctly.

### Visual Baseline
Use `Device_Approvals.jsx` as the primary visual baseline for top alignment and outer inset.

Implications:
- `Assemblies` should lose its extra vertical drop and align with `Device Approvals`.
- New pages should not introduce a manual top offset below the shared header unless they are using a sanctioned variant behavior.

## Public Interface To Add
Add a shared component under `Data/Engine/web-interface/src/`:

`PageBodyFrame`

### Proposed Interface
- `variant: "grid" | "grid_with_stack" | "split_tool" | "content_panel"`
- `children?: ReactNode`
- `stack?: ReactNode`
- `sidebar?: ReactNode`
- `main?: ReactNode`
- `fillHeight?: boolean`

### Variant Meanings
- `grid`
  - One rounded body shell containing one main data surface, usually an AG Grid table.
- `grid_with_stack`
  - One rounded body shell containing one or more rows above the main data surface.
  - Examples: banners, filter chips, tab-like pill rows, view selectors, and secondary control rows.
- `split_tool`
  - One rounded outer shell containing a left sidebar/tool rail and a right main work surface.
  - Intended for pages like `Log_Management.jsx`.
- `content_panel`
  - One rounded shell for non-grid content such as forms, detail layouts, or mixed-content pages.

## Shared Layout Rules

### Outer Body Frame Rules
These rules apply to all `PageBodyFrame` variants unless a variant explicitly overrides them.

- The shared page header in `App.jsx` remains above the body.
- The body frame begins immediately below the shared header without ad hoc extra top margin.
- Standard outer inset:
  - `px: 2`
  - `pb: 2`
- Standard layout behavior:
  - `flexGrow: 1`
  - `minHeight: 0`
  - column layout by default
- Do not use page-local `mt: "10px"` offsets for primary page bodies.
- Do not use page-local full-page padding like `p: 3` on the top-level page wrapper when the header already owns the top band.

### Shared Shell Chrome Rules
For the standard data-page variants, the shared body frame owns the outer shell chrome.

The shell should standardize:
- rounded corners
- border
- background
- shadow
- overflow behavior
- minimum body height behavior

This chrome should be extracted from the current modern AG Grid shell treatment rather than reinvented separately on every page.

### Pre-Grid Content Rules
For pages with banners, filter pills, chips, or control rows above the grid:
- those elements belong inside the same rounded body shell
- they sit above the main grid or content area within the shell
- they do not create a separate external padding rhythm
- they do not live outside the shell unless a future variant explicitly requires that

### App Relationship Rules
`App.jsx` continues to own:
- title
- subtitle
- icon
- header action rail

`PageBodyFrame` owns:
- body outer inset
- body shell alignment
- body shell chrome
- variant-specific internal structure

## Variant Specifications

### 1. `grid`
Use when the main body is one primary data surface and nothing substantial sits above it inside the body.

#### Structure
- outer body frame
- one rounded shell
- main content fills available height
- usually one AG Grid surface inside

#### Intended Pages
- `Site_List.jsx`
- `Assembly_List.jsx`
- `Filter_List.jsx` if no pre-grid rows remain after migration

#### Rules
- no ad hoc page-local body spacing wrappers
- no extra top drop
- no external banner stack
- grid wrapper should inherit shell dimensions and fill available height

### 2. `grid_with_stack`
Use when the page body needs one or more internal rows above the main surface.

#### Structure
- outer body frame
- one rounded shell
- stack area at top of shell
- divider or spacing rhythm as needed
- main content area below, typically AG Grid

#### Intended Pages
- `Device_List.jsx`
- `Scheduled_Jobs_List.jsx`
- `Credential_List.jsx`

#### Stack Examples
- view selector rows
- filter pill rows
- info banners
- empty-state guidance rows
- page-body-local toolbars

#### Rules
- stack content is inside the shell, not outside it
- stack spacing is variant-owned, not page-invented
- stack area should not introduce extra outer offset from the shared header

### 3. `split_tool`
Use when the page body is fundamentally a tool workspace with a split layout.

#### Structure
- outer body frame
- one rounded outer shell
- left sidebar or tool panel
- right main work surface
- responsive collapse behavior for narrower widths

#### Intended Pages
- `Log_Management.jsx`

#### Rules
- the split layout still aligns to the same outer inset and top baseline as every other page
- sidebar and main pane live inside one shared outer shell
- internal panel chrome may differ, but the outer shell must remain standardized
- avoid page-local top-level `p: 3` wrappers that bypass the shared body rhythm

### 4. `content_panel`
Use when the body is not primarily an AG Grid page.

#### Structure
- outer body frame
- one rounded shell
- mixed content, forms, or other structured page content inside

#### Intended Usage
- future form-heavy or mixed-content primary pages
- fallback standard for non-grid primary pages that still need to look like first-class Borealis pages

#### Rules
- use the same outer inset and outer chrome
- internal layout remains page-specific, but only inside the standardized shell

## Migration Mapping

### `Site_List.jsx`
Target variant: `grid`

Current issue:
- visually wrong body alignment relative to the desired baseline

Migration:
- remove page-local body wrapper spacing logic
- move the table into `PageBodyFrame variant="grid"`
- keep the selected-count text behavior, but place it in a sanctioned internal slot if needed
- ensure the outer shell aligns to the shared frame baseline

### `Device_Approvals.jsx`
Target variant: `grid`

Role:
- baseline page for alignment and outer inset

Migration:
- convert existing layout to the shared frame without changing the intended visual baseline
- preserve its relationship to the shared header
- use it as the reference page during QA

### `Device_List.jsx`
Target variant: `grid_with_stack`

Current issue:
- wrong body structure and spacing
- custom-view controls sit outside the main shell rhythm

Migration:
- move the custom-view selector row into the `stack` area of the shared shell
- keep the grid as the main body surface below the stack
- remove page-local body wrappers that create inconsistent spacing

### `Assembly_List.jsx`
Target variant: `grid`

Current issue:
- visually correct shell, but vertically lower than `Device Approvals`

Migration:
- remove the extra vertical drop
- align to the same header-to-body baseline as `Device Approvals`

### `Scheduled_Jobs_List.jsx`
Target variant: `grid_with_stack`

Current issue:
- visually acceptable, but should be expressed through the new shared variant rather than page-local structure

Migration:
- place the filter pill row and count summary in the `stack` area
- place the grid below it inside the same shell

### `Filter_List.jsx`
Target variant: `grid`

Current issue:
- top-level page wrapper uses a custom full-page padding structure
- body does not align to the target pattern

Migration:
- replace the custom body wrapper with `PageBodyFrame variant="grid"`
- keep AG Grid internals, but remove page-level spacing and chrome drift

### `Credential_List.jsx`
Target variant: `grid_with_stack`

Current issue:
- wrong body alignment
- placeholder warning banner currently sits outside the standardized shell pattern

Migration:
- move the banner into the shared shell stack area
- keep the placeholder AG Grid body below it
- preserve the current banner behavior until the backend exists

### `Log_Management.jsx`
Target variant: `split_tool`

Current issue:
- custom split-pane tool layout bypasses the body rhythm entirely

Migration:
- keep the tool-page concept
- wrap it in the standardized split-tool body shell
- align the whole page to the same outer inset and top baseline as other primary pages

## Required Changes Outside The New Component

### `App.jsx`
Keep the shared header ownership unchanged.

Adjust the main content host so it does not assume child pages are compensating for page-local margins with:
- `minHeight: "calc(100% - 32px)"`

The new shared body frame will own body spacing directly, so the App-level child-height compensation should be simplified or removed to avoid reinforcing legacy layout assumptions.

### `Page_Template.jsx`
Update the page template to use `PageBodyFrame` instead of the old `mt: "10px"` pattern.

The template must become the canonical example of:
- shared header from `App.jsx`
- standardized body frame below it
- correct outer inset
- correct outer shell chrome
- correct full-height behavior

### Documentation
Update `Docs/ui-and-notifications.md` so it references the page body standard and points to this document as the implementation guide until the migration is complete.

## Anti-Patterns To Avoid
Future implementations should not do the following on primary pages:

- add `mt: "10px"` or similar manual top drop below the shared header
- use page-local `p: 3` on the top-level page wrapper to create body spacing
- render a grid directly against the page canvas with no shared body shell when a sanctioned variant exists
- place page banners outside the standardized body shell on `grid_with_stack` pages
- create new body-layout categories when one of the four existing variants is sufficient
- use page-specific outer shell chrome when the shared frame already provides it

## Testing And Acceptance Criteria

### Visual Acceptance
After migration:
- `All Sites`, `Device Approvals`, `Device List`, `Assemblies`, `Scheduled Jobs`, `Device Filters`, and `Credentials` all share the same left and right body inset
- `Assemblies` no longer sits lower than `Device Approvals`
- `Device List` custom-view controls appear inside the same rounded shell as the grid
- `Credentials` warning banner appears inside the same rounded shell as the grid
- `Log Management` aligns to the same outer inset and top baseline even though it remains a split-pane layout

### Structural Acceptance
- migrated pages use `PageBodyFrame` instead of custom page-local body wrappers
- page-local `px`, `pb`, `pt`, and `mt` drift is removed from the affected pages
- `Page_Template.jsx` reflects the new standard
- `Docs/ui-and-notifications.md` references the body-frame standard

### Responsive Acceptance
- all variants retain full-height behavior without clipping their bottom content or pagination regions
- `grid_with_stack` pages do not introduce extra vertical drift below the shared header
- `split_tool` pages collapse reasonably at narrower widths without breaking the outer shell alignment

## Implementation Order
Recommended order for the future implementing agent:

1. Add `PageBodyFrame` and lock the shared tokens and shell chrome.
2. Update `Page_Template.jsx` to demonstrate the new standard.
3. Migrate `Device_Approvals.jsx` first as the no-regression baseline.
4. Migrate `Assembly_List.jsx` next to eliminate the extra vertical drop.
5. Migrate `Site_List.jsx`, `Filter_List.jsx`, and `Credential_List.jsx`.
6. Migrate `Device_List.jsx` and `Scheduled_Jobs_List.jsx` into `grid_with_stack`.
7. Migrate `Log_Management.jsx` into `split_tool`.
8. Update `Docs/ui-and-notifications.md` after the code pattern is settled.

## Assumptions
- The standard applies only to primary pages rendered under the shared App header.
- `Device Approvals` is the approved visual baseline for header-to-body alignment.
- The body frame should be page-owned, not App-metadata-driven.
- The shared body frame should own outer spacing and chrome, not just positioning.
- Banners and pre-grid controls belong inside the shared shell for `grid_with_stack` pages.
- The initial sanctioned variant set is exactly:
  - `grid`
  - `grid_with_stack`
  - `split_tool`
  - `content_panel`

If a future page does not fit one of those patterns, the implementer should first try to compose the existing variants before proposing a fifth category.
