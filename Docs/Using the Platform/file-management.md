# File Management

File Management lets operators browse and change a device filesystem without opening a shell. Use it for remote upload, download, folder transfer, rename, move, delete, copy, paste, folder creation, and lightweight text edits.

<figure class="bo-screenshot">
  <img src="../Reference/images/repo_screenshots/Agent_File_Management.png" alt="Borealis Agent File Management" loading="lazy">
  <figcaption>File Management supports remote browse, upload, download, edit, rename, move, and delete workflows.</figcaption>
</figure>

## Browse Files

1. Open a device.
2. Open `File Management`.
3. Select a drive or root.
4. Expand folders or use the address bar.
5. Toggle `Show Hidden Items` only when hidden paths are relevant.

The current working directory is URL-synced so refreshes and shared links can reopen the same folder.

## Transfer Files

- `Upload` sends selected browser files to the current folder.
- `Upload Folder` preserves relative paths from the browser folder picker.
- `Download` stages selected file or folder content on the Engine before the browser fetches it.
- `Cancel` asks the Agent to stop an active upload or download.

Duplicate uploads show a replace-or-skip decision before transfer begins.

## Edit And Organize

- Right-click files or folders for contextual actions.
- Text editing opens one remote file at a time and saves back in place.
- Copy and cut stay operator-local until paste asks the remote agent to perform the filesystem operation.

!!! warning

    File actions run in the device service context: SYSTEM on Windows and root on Linux. Confirm path and selection before destructive actions.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/device/files/<hostname>/roots` - roots view.
    - `GET /api/device/files/<hostname>/children?path=<absolute-path>` - list directory.
    - `POST /api/device/files/<hostname>/upload/conflicts` - duplicate preflight.
    - `GET /api/device/files/<hostname>/text?path=<absolute-path>` - read text file.
    - `POST /api/device/files/<hostname>/text` - save text file.
    - `POST /api/device/files/<hostname>/mkdir` - create directory.
    - `POST /api/device/files/<hostname>/rename` - rename item.
    - `POST /api/device/files/<hostname>/move` - move item.
    - `POST /api/device/files/<hostname>/paste` - paste copied/cut items.
    - `POST /api/device/files/<hostname>/delete` - delete items.
    - `POST /api/device/files/<hostname>/upload` - start upload transfer.
    - `POST /api/device/files/<hostname>/download` - start download transfer.
    - `GET /api/device/files/<hostname>/transfer/<transfer_id>/status` - poll transfer.
    - `POST /api/device/files/<hostname>/transfer/<transfer_id>/cancel` - cancel transfer.
    - `GET /api/device/files/<hostname>/transfer/<transfer_id>/content` - fetch completed download artifact.

    ### Related documentation

    - [Device Auditing](device-auditing.md)
    - [Agent Runtime](../Reference/Core%20Runtimes/agent-runtime.md)
    - [UI and Notifications](../Reference/ui-and-notifications.md)
    - [API Reference](../Reference/Data%20and%20Schema/api-reference.md)

    ### Source map

    - File API: `Data/Engine/Containers/api-backend/cmd/api-backend/remote_files.go`
    - Site-worker transfer runtime: `Data/Engine/Containers/api-backend/data/services/job_scheduler/worker_socket.py`
    - Shared transfer store: `Data/Engine/Containers/api-backend/data/services/remote_files/transfers.py`
    - File tab UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Remote_File_Management.jsx`
    - Agent file role: `Data/Agent/internal/roles/file_management/`

    ### Runtime behavior

    - Browse and mutations use the device SYSTEM Socket.IO channel through `file_management_request`.
    - Large transfers use site-worker temp-file staging behind Go API proxy routes plus device-authenticated pull/push endpoints.
    - Folder uploads use a manifest so nested paths do not need to fit in one socket payload.
    - Transfer progress doubles as a cancellation checkpoint.
    - Inline text editing uses `@uiw/react-codemirror` plus Borealis-owned CodeMirror extensions. Vite dedupes `@codemirror/*` packages and npm overrides `@codemirror/state` so extension `instanceof` checks do not fail when editor setup and app extensions load through different dependency paths.
