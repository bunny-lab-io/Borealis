# Technical Debt
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Document known technical debt items that affect developer experience or runtime behavior. This page captures the Vite dev server failure caused by noVNC 1.6.0 packaging so a future agent can recognize and resolve it.

## Issue Summary
The Borealis WebUI uses `@novnc/novnc` for the VNC client. Version 1.6.0 ships a CommonJS build that includes a top level `await` in `lib/util/browser.js`. Vite dev mode prebundles dependencies with esbuild, and esbuild rejects top level `await` in CommonJS. The result is a dev server that starts but fails to serve the app (blank page, no useful browser error) because the prebundle crashes.

Production still works because it serves the prebuilt SPA bundle via Flask and does not use Vite dev prebundle at runtime.

## Symptoms
- Vite dev server appears to start on `https://localhost:5173`.
- Browser shows a blank page or nothing renders; DevTools may show no useful app errors.
- `Engine/Logs/vite-dev.stderr.log` shows esbuild errors similar to:
  - "require call is not allowed because the imported file ... contains a top level await"
  - References `node_modules/@novnc/novnc/lib/util/browser.js`

## Root Cause
`@novnc/novnc` 1.6.0 includes a top level `await` inside the CommonJS bundle. Vite dev uses esbuild to prebundle dependencies, and esbuild rejects top level `await` in CommonJS modules.

## Current Mitigation (Patch)
A small postinstall patch rewrites the top level await to an async initialization that does not block module evaluation.

Files involved:
- `Data/Engine/web-interface/package.json`
  - Adds `postinstall` to run the patch.
- `Data/Engine/web-interface/scripts/patch-novnc.js`
  - Rewrites the top level await in `node_modules/@novnc/novnc/lib/util/browser.js`.

The patched behavior sets `supportsWebCodecsH264Decode` to `false` initially and updates it asynchronously after the check completes. This matches the intent of the original check without breaking esbuild.

## Recovery Steps
1. Run the normal dev task: `Borealis - Engine (Dev)` or `./Borealis.ps1 -EngineDev`.
2. Ensure `npm install` runs and executes the postinstall patch.
3. Confirm `Engine/Logs/vite-dev.stderr.log` no longer reports the top level await errors.

## Long Term Fix Options
1. Upstream fix in noVNC.
   - Remove this patch once noVNC ships a CommonJS build without top level await or provides a proper ESM entry that Vite can consume.
2. Use `patch-package`.
   - Convert the custom patch script into a `patch-package` diff so changes are explicit and verified.
3. Lazy load noVNC.
   - Delay importing the VNC module so Vite does not prebundle it, combined with `optimizeDeps.exclude` if needed.

## Related Documentation
- [Engine Runtime](engine-runtime.md)
- [UI and Notifications](ui-and-notifications.md)
