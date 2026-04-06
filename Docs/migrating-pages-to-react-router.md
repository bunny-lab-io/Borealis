# Migrating Pages to React Router
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Document the Borealis WebUI router architecture and provide a repeatable migration runbook for moving legacy pages onto the shared React Router app shell.

## When To Use This Guide
- Use this guide when a page still depends on the old `currentPage`, `navigateTo`, or `onPageMetaChange` conventions.
- Use this guide when adding a new routed page under the shared Borealis shell.
- Use this guide when converting a legacy page from adapter-backed props to route-native hooks.

## Router Architecture Summary
- `Data/Engine/web-interface/src/App.jsx` is now a thin bootstrap only.
- `Data/Engine/web-interface/src/app/routes/router.jsx` owns the canonical route tree.
- `Data/Engine/web-interface/src/app/shell/AppShell.jsx` owns the authenticated shell, top app bar, breadcrumbs, sidebar, dialogs, and notifications.
- `Data/Engine/web-interface/src/app/providers/AuthContext.jsx` owns session bootstrap, login/logout, MFA reset, password reset, Aegis status, and admin Aegis prompt behavior.
- `Data/Engine/web-interface/src/app/providers/PageChromeContext.jsx` owns page title, subtitle, icon, header actions, header controls, and breadcrumb-tail overrides.
- `Data/Engine/web-interface/src/app/route-modules/` contains thin lazy-route entrypoints that render the routed page directly.
- Shared page migration helpers now live in:
  - `Data/Engine/web-interface/src/app/hooks/useRoutePageChrome.js`
  - `Data/Engine/web-interface/src/app/hooks/useUrlTabState.js`
  - `Data/Engine/web-interface/src/app/hooks/useAppNotifications.js`

## Canonical Routing Rules
- Use URL paths for durable resource identity.
- Use query strings only for shareable view state or multi-select inputs, such as `?tab=` or repeated `?user=`.
- Use `location.state` only for ephemeral one-shot state such as quick-job seeding or "new workflow with suggested name".
- Do not add `/edit` suffixes to routes with identifiers.
- Use `/new/<type>` routes for creation flows.
- Keep route segments resource-first and consistent with the shared path helpers in `src/app/routes/paths.js`.

## Migration Workflow
1. Find the legacy surface area.
   Search for `currentPage`, `navigateTo(`, `window.history`, `window.location.search`, and `onPageMetaChange`.
2. Register the canonical route.
   Add the page to `src/app/routes/router.jsx` with a `handle` that defines `title`, `breadcrumb`, `navKey`, and `pageKey`.
3. Decide the route data shape.
   Put durable identity in params, view state in query, and one-shot launch state in `location.state`.
4. Update the route module entrypoint.
   Keep `src/app/route-modules/*` thin. It should lazy-load the page and render it directly, not translate router state into legacy props.
5. Replace legacy page chrome.
   Use `useRoutePageChrome(...)` or `usePageChrome()` directly inside the page component.
6. Replace legacy navigation and query state.
   Use `useNavigate`, `useParams`, `useSearchParams`, and `useLocation` directly in the page. For `?tab=` synchronization, prefer `useUrlTabState(...)`.
7. Replace inline notifications.
   Use `useAppNotifications(...)` instead of posting `/api/notifications/notify` inline.
8. Verify deep-link refresh.
   Refresh the routed page directly in the browser and make sure it still hydrates.
9. Remove compatibility code only after the route-native version is stable.

## Route Module Pattern
- Keep business logic in the page component.
- Keep `src/app/route-modules/*` as the lazy-loading route boundary layer.
- Route modules should normally just render the page component:
  - `return <DeviceSummary />;`
  - `return <CreateJob />;`
- Do not move API fetching into router loaders/actions in this migration unless the product work explicitly calls for it.

## Route-Native Conversion Pattern
- Replace prop-derived identifiers with `useParams()`.
- Replace manual query parsing with `useSearchParams()`.
- Replace `navigateTo(pageKey, options)` with `useNavigate()` plus shared path helpers from `src/app/routes/paths.js`.
- Replace `onPageMetaChange?.({...})` with `useRoutePageChrome({...})` or `usePageChrome()`.
- Replace manual `window.history` / `window.location.search` tab handling with `useUrlTabState(...)`.
- Replace inline notification posts with `useAppNotifications(...)`.
- Remove adapter-only props after the page no longer depends on them.

## Templates
### Route Registration
```jsx
{
  path: "filters/:filterId",
  handle: {
    title: "Filter",
    breadcrumb: "Filter",
    navKey: "filters",
    pageKey: "filter",
  },
  lazy: lazyNamed(
    () => import("../route-modules/filterRoutes.jsx"),
    "FilterEditorRoute"
  ),
}
```

### Thin Route Module
```jsx
export function FilterEditorRoute() {
  return <DeviceFilterEditor />;
}
```

### Route-Native Page Chrome
```jsx
useRoutePageChrome({
  title: "Scheduled Job",
  subtitle: "Edit the selected job configuration.",
  Icon: ScheduleIcon,
  actions: pageHeaderActions,
  breadcrumbLabel: "Job",
});
```

### Replacing Legacy Navigation
```jsx
const navigate = useNavigate();
const { jobId } = useParams();
const { activeKey, setActiveKey } = useUrlTabState({
  param: "tab",
  defaultKey: "name",
  allowedKeys: ["name", "components", "targets"],
  keyByUrl: JOB_TAB_KEY_BY_URL,
  urlByKey: JOB_TAB_URL_BY_KEY,
});

function openJob(nextJobId) {
  navigate(APP_PATHS.job(nextJobId));
}

function openQuickJob(hostnames) {
  const quickJobDraft = createQuickJobDraft(hostnames);
  if (!quickJobDraft) return;
  navigate(APP_PATHS.jobNew, { state: { quickJobDraft } });
}
```

## Acceptance Checklist
- The page has a canonical route in `router.jsx`.
- Refreshing the page at its direct URL works.
- Breadcrumbs and nav highlighting are correct.
- Page header title, subtitle, icon, and actions render through the shared shell.
- The page no longer mutates browser history manually.
- The route module is a thin entrypoint and no longer translates router state into legacy props.

## Manual QA Checklist
- Open the page from sidebar navigation.
- Open the page from a deep link.
- Refresh the browser on the page.
- Use browser back/forward navigation.
- Confirm any `?tab=` state is preserved correctly.
- Confirm auth/admin gating still behaves correctly.
- Confirm quick-job or other ephemeral launch state is consumed once and does not persist after refresh unless it was intentionally encoded into the URL.

## Common Pitfalls
- Do not store durable identity only in `location.state`; refresh will lose it.
- Do not add new ad hoc route parsing helpers outside `router.jsx`, `paths.js`, or page-local tab query logic.
- Do not bypass `PageChromeProvider` by rendering local header rails inside shell-routed pages.
- Do not keep both the adapter bridge and route-native hooks active for the same page logic longer than necessary.
- Do not create legacy redirects unless product intent explicitly requires them.

## Related Documentation
- [UI and Notifications](ui-and-notifications.md)
- [Architecture Overview](architecture-overview.md)
- [Scheduled Jobs](scheduled-jobs.md)
- [Assemblies and Quick Jobs](assemblies.md)

## Codex Agent (Detailed)
### Default migration sequence for future agents
- Start by reading `Docs/ui-and-notifications.md` and this guide.
- Open the target page component and find every dependency on:
  - `currentPage`
  - `navigateTo`
  - `onPageMetaChange`
  - direct `window.history` writes
  - direct `window.location.search` parsing
- Register the page's canonical route in `src/app/routes/router.jsx`.
- Decide which values belong in params, query, or `location.state`.
- Implement or update the thin route module in `src/app/route-modules/`.
- Convert the page itself to route-native hooks instead of adding new compatibility props.
- Only after the page works via the new route should you remove any local compatibility glue.

### Params vs query vs location.state
- Params:
  - resource identifiers such as `deviceId`, `filterId`, `jobId`, `assemblyGuid`, `workflowGuid`, `runId`
- Query:
  - active tab keys
  - repeated values that must survive refresh and be shareable, such as `?user=alice&user=bob`
- `location.state`:
  - quick-job draft payloads
  - "new workflow" suggested names
  - other launch-only state that should not survive refresh

### What to do with legacy helpers
- `onPageMetaChange`:
  - remove it from the page contract
  - replace it with `useRoutePageChrome()` or `usePageChrome()`
- `navigateTo`:
  - remove it from the page contract
  - replace it with `useNavigate()` and shared path helpers
- Manual `window.history` writes:
  - replace them with `useUrlTabState(...)` or `useSearchParams()`

### Code review expectations for migrations
- Prefer smaller, route-complete migrations over half-converted pages.
- If a page still needs the adapter bridge, keep the bridge thin and obvious.
- Preserve deep-link refresh behavior before cleaning up internal code style.
- When a migration adds dependencies or changes shell ownership, update docs and `SBOM.md` in the same change.
