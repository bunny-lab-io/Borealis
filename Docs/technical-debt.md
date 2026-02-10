# Technical Debt
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Track technical debt items that affect developer experience, runtime behavior, or maintainability. This document is the staging area for items that will later become GitHub Issues.

## How To Use This Doc
- Add new entries at the top of the Issues section.
- Use the template below so future agents can understand impact, mitigation, and removal criteria.
- Keep entries concise but action oriented. Link to files and logs instead of pasting large outputs.
- When you open a GitHub Issue, note the issue URL and mark the entry as migrated.

## Logging Rules
- Log any patchy or non-standard workaround, even if it is small.
- Log any behavior that works in production but fails in dev (or vice versa).
- Log any changes that are likely to be removed once upstream or platform fixes land.
- If a change touches dependencies, include the exact version and why it matters.
- Use ASCII only unless the file already contains Unicode.

## Entry Template
```
ID: TD-YYYYMMDD-##
Status: active | mitigated | migrated | resolved
Owner: <name or team>
Date Added: YYYY-MM-DD
Summary: <one sentence>
Impact: <what breaks and who is affected>
Root Cause: <why it happens>
Current Mitigation: <what we did>
Removal Criteria: <what must be true to remove the workaround>
Files: <paths>
Evidence: <logs, errors, or reproduction hints>
Next Step: <smallest useful follow-up>
GitHub Issue: <link or "not yet">
```

## Issues
ID: TD-20260210-01
Status: mitigated
Owner: WebUI
Date Added: 2026-02-10
Summary: Vite dev server fails to serve the WebUI when `@novnc/novnc` 1.6.0 is installed.
Impact: Dev UI loads blank or fails to render; production UI continues to work.
Root Cause: noVNC 1.6.0 ships a CommonJS build with a top level `await` in `lib/util/browser.js`; esbuild refuses this during Vite dev prebundle.
Current Mitigation: Postinstall patch replaces the top level `await` with async initialization so esbuild can prebundle.
Removal Criteria: noVNC ships a CJS build without top level await or provides a stable ESM entry that Vite can use without patching.
Files: `Data/Engine/web-interface/package.json`, `Data/Engine/web-interface/scripts/patch-novnc.js`
Evidence: `Engine/Logs/vite-dev.stderr.log` errors referencing top level await in `@novnc/novnc/lib/util/browser.js`.
Next Step: Track upstream noVNC packaging changes and remove patch when safe.
GitHub Issue: not yet

## Related Documentation
- [Engine Runtime](engine-runtime.md)
- [UI and Notifications](ui-and-notifications.md)

## Related Documentation
- [Engine Runtime](engine-runtime.md)
- [UI and Notifications](ui-and-notifications.md)
