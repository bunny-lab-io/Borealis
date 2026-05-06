# Borealis SBOM

This software bill of materials is an inventory of the direct, repo-declared, bundled, container base image, or script-installed third-party software used by Borealis. It was assembled from `bootstrap.ps1`, `Engine.sh`, `Agent.sh`, `Borealis.ps1`, `Data/Agent/agent-requirements.txt`, `Data/Engine/Containers/api-backend/data/engine-requirements.txt`, `Data/Engine/Containers/webui-frontend/data/web-interface/package.json`, `Data/Engine/Containers/api-backend/data/Ansible/collections.yml`, and `Data/Engine/Containers/`.

The Python requirement files are currently unpinned, so the exact resolved version can change between installs. Where the install scripts pin a version explicitly, that version is called out below.

## Borealis Agent Dependencies

- Python (system Python on Linux; Windows bootstrap downloads Python 3.13.3 MSI packages and installer fallback) - PSF License - https://docs.python.org/3/license.html
- requests - Apache-2.0 - https://spdx.org/licenses/Apache-2.0.html
- python-socketio - MIT - https://spdx.org/licenses/MIT.html
- websocket-client - Apache-2.0 - https://spdx.org/licenses/Apache-2.0.html
- eventlet - MIT - https://spdx.org/licenses/MIT.html
- aiohttp - Apache-2.0 AND MIT - https://github.com/aio-libs/aiohttp/blob/master/LICENSE.txt
- cryptography - Apache-2.0 OR BSD-3-Clause - https://github.com/pyca/cryptography/blob/main/LICENSE
- PySide6 - LGPL-3.0-only OR GPL-3.0-only - https://doc.qt.io/qt-6/licensing.html
- qasync - BSD-2-Clause - https://github.com/CabbageDevelopment/qasync/blob/master/LICENSE
- opencv-python - Apache-2.0 - https://spdx.org/licenses/Apache-2.0.html
- Pillow - MIT-CMU - https://github.com/python-pillow/Pillow/blob/main/LICENSE
- pywinauto - BSD-3-Clause - https://spdx.org/licenses/BSD-3-Clause.html
- sounddevice - MIT - https://spdx.org/licenses/MIT.html
- numpy - BSD-3-Clause AND 0BSD AND MIT AND Zlib AND CC0-1.0 - https://github.com/numpy/numpy/blob/main/LICENSE.txt
- pywin32 (Windows only) - PSF License - https://github.com/mhammond/pywin32/blob/main/LICENSE.txt
- psutil - BSD-3-Clause - https://spdx.org/licenses/BSD-3-Clause.html
- PyInstaller (used by `Data/Agent/Package_Borealis-Agent.ps1`) - GPL-2.0-or-later with PyInstaller exception - https://pyinstaller.org/en/stable/license.html
- WireGuard (Windows client 0.5.3 and Linux `wireguard-tools`) - GPL-2.0-only - https://spdx.org/licenses/GPL-2.0-only.html
- UltraVNC Server 1.6.4.0 - GPL-2.0-only - https://spdx.org/licenses/GPL-2.0-only.html
- UltraVNC `createpassword.exe` helper - GPL-2.0-only - https://spdx.org/licenses/GPL-2.0-only.html
- MinGit 2.47.1 (Windows bootstrap only) - GPL-2.0-only - https://spdx.org/licenses/GPL-2.0-only.html
- curl (vendored Windows download helper) - curl license - https://curl.se/docs/copyright.html
- 7-Zip CLI (vendored Windows archive helper) - LGPL-2.1-or-later with unRAR restriction - https://www.7-zip.org/license.txt

## Borealis Engine Dependencies

- Docker Engine (Linux Engine deployment runtime; Docker Desktop not used) - Apache-2.0 - https://github.com/moby/moby/blob/master/LICENSE
- Docker CLI (`docker-ce-cli`, host and api-backend service-action helper) - Apache-2.0 - https://github.com/docker/cli/blob/master/LICENSE
- Docker Compose plugin (Linux Engine deployment orchestration and api-backend service-action helper) - Apache-2.0 - https://github.com/docker/compose/blob/main/LICENSE
- Docker Buildx plugin / BuildKit (optional local Engine image build cache acceleration) - Apache-2.0 - https://github.com/docker/buildx/blob/master/LICENSE
- Debian Bookworm base image (`debian:bookworm-slim`) - Debian Free Software Guidelines / package-specific licenses - https://www.debian.org/legal/licenses/
- Python container base image (`python:3.12-slim-bookworm`) - PSF License plus Debian package licenses - https://github.com/docker-library/python/blob/master/LICENSE
- Node.js container base image (`node:22-bookworm-slim`) - MIT plus Debian package licenses - https://github.com/nodejs/node/blob/main/LICENSE
- PostgreSQL container image (`postgres:17-bookworm`) - PostgreSQL License plus Debian package licenses - https://www.postgresql.org/about/licence/
- Traefik container image (`traefik:v3.3`) - MIT - https://github.com/traefik/traefik/blob/master/LICENSE.md
- Apache Guacamole Server container image (`guacamole/guacd:1.6.0`) - Apache-2.0 - https://github.com/apache/guacamole-server/blob/1.6.0/LICENSE
- Python (system Python on Linux) - PSF License - https://docs.python.org/3/license.html
- Flask - BSD-3-Clause - https://spdx.org/licenses/BSD-3-Clause.html
- flask-cors - MIT - https://spdx.org/licenses/MIT.html
- Flask-SocketIO - MIT - https://spdx.org/licenses/MIT.html
- eventlet - MIT - https://spdx.org/licenses/MIT.html
- cryptography - Apache-2.0 OR BSD-3-Clause - https://github.com/pyca/cryptography/blob/main/LICENSE
- PyJWT - MIT - https://spdx.org/licenses/MIT.html
- pyotp - MIT - https://spdx.org/licenses/MIT.html
- qrcode - BSD - https://github.com/lincolnloop/python-qrcode/blob/main/LICENSE
- webauthn - BSD-3-Clause - https://github.com/duo-labs/py_webauthn/blob/master/LICENSE
- Pillow - MIT-CMU - https://github.com/python-pillow/Pillow/blob/main/LICENSE
- requests - Apache-2.0 - https://spdx.org/licenses/Apache-2.0.html
- aiohttp - Apache-2.0 AND MIT - https://github.com/aio-libs/aiohttp/blob/master/LICENSE.txt
- python-socketio - MIT - https://spdx.org/licenses/MIT.html
- websockets - BSD-3-Clause - https://spdx.org/licenses/BSD-3-Clause.html
- packaging - Apache-2.0 OR BSD-2-Clause - https://github.com/pypa/packaging/blob/main/LICENSE
- regex - Apache-2.0 AND CNRI-Python - https://github.com/mrabarnett/mrab-regex/blob/hg/LICENSE.txt
- SQLAlchemy - MIT - https://spdx.org/licenses/MIT.html
- alembic - MIT - https://spdx.org/licenses/MIT.html
- psycopg (`psycopg[binary]`) - LGPL-3.0-only - https://spdx.org/licenses/LGPL-3.0-only.html
- ldap3 - LGPL-3.0-or-later - https://github.com/cannatag/ldap3/blob/dev/COPYING
- python-gssapi (`gssapi`, optional Kerberos Engine package) - ISC - https://github.com/pythongssapi/python-gssapi/blob/main/LICENSE.txt
- MIT Kerberos (`krb5-user`/`krb5-workstation` runtime packages) - MIT - https://web.mit.edu/kerberos/krb5-devel/doc/mitK5license.html
- ansible-core - GPL-3.0-or-later - https://spdx.org/licenses/GPL-3.0-or-later.html
- ansible-runner - Apache-2.0 - https://spdx.org/licenses/Apache-2.0.html
- jmespath - MIT - https://spdx.org/licenses/MIT.html
- pywinrm (`pywinrm[credssp]`) - MIT - https://github.com/diyan/pywinrm/blob/master/LICENSE
- pypsrp (`pypsrp[credssp]`) - MIT - https://github.com/jborean93/pypsrp/blob/master/LICENSE
- Impacket - Apache-2.0 - https://github.com/fortra/impacket/blob/master/LICENSE
- pytest - MIT - https://github.com/pytest-dev/pytest/blob/main/LICENSE
- ansible.windows collection - GPL-3.0-or-later - https://github.com/ansible-collections/ansible.windows/blob/main/LICENSE
- ansible.posix collection - GPL-3.0-or-later - https://github.com/ansible-collections/ansible.posix/blob/main/LICENSE
- community.general collection - GPL-3.0-or-later - https://github.com/ansible-collections/community.general/blob/main/COPYING
- Tesseract OCR - Apache-2.0 - https://github.com/tesseract-ocr/tesseract/blob/main/LICENSE
- WireGuard tools - GPL-2.0-only - https://spdx.org/licenses/GPL-2.0-only.html
- Traefik (Borealis-managed local HTTPS edge and ACME client) - MIT - https://github.com/traefik/traefik/blob/master/LICENSE.md
- @emotion/react - MIT - https://spdx.org/licenses/MIT.html
- @emotion/styled - MIT - https://spdx.org/licenses/MIT.html
- @fortawesome/fontawesome-free - CC-BY-4.0 AND OFL-1.1 AND MIT - https://github.com/FortAwesome/Font-Awesome/blob/7.x/LICENSE.txt
- @fontsource/ibm-plex-sans - OFL-1.1 - https://openfontlicense.org/open-font-license-official-text/
- @mui/icons-material - MIT - https://spdx.org/licenses/MIT.html
- @mui/material - MIT - https://spdx.org/licenses/MIT.html
- @mui/x-date-pickers - MIT - https://spdx.org/licenses/MIT.html
- @mui/x-tree-view - MIT - https://spdx.org/licenses/MIT.html
- @simplewebauthn/browser - MIT - https://github.com/MasterKale/SimpleWebAuthn/blob/master/LICENSE
- ag-grid-community - MIT - https://spdx.org/licenses/MIT.html
- ag-grid-react - MIT - https://spdx.org/licenses/MIT.html
- Apache Guacamole Server (`guacd` and VNC plugin) 1.6.0 - Apache-2.0 - https://github.com/apache/guacamole-server/blob/1.6.0/LICENSE
- Apache Guacamole Client (`guacamole-common-js`) 1.6.0 - Apache-2.0 - https://github.com/apache/guacamole-client/blob/1.6.0/LICENSE
- LibVNCServer / LibVNCClient - GPL-2.0-or-later - https://github.com/LibVNC/libvncserver/blob/master/COPYING
- dayjs - MIT - https://spdx.org/licenses/MIT.html
- normalize.css - MIT - https://spdx.org/licenses/MIT.html
- prismjs - MIT - https://spdx.org/licenses/MIT.html
- react-simple-code-editor - MIT - https://spdx.org/licenses/MIT.html
- react - MIT - https://spdx.org/licenses/MIT.html
- react-color - MIT - https://spdx.org/licenses/MIT.html
- react-dom - MIT - https://spdx.org/licenses/MIT.html
- react-router-dom - MIT - https://spdx.org/licenses/MIT.html
- react-resizable - MIT - https://spdx.org/licenses/MIT.html
- react-markdown - MIT - https://spdx.org/licenses/MIT.html
- reactflow - MIT - https://spdx.org/licenses/MIT.html
- react-simple-keyboard - MIT - https://spdx.org/licenses/MIT.html
- socket.io-client - MIT - https://spdx.org/licenses/MIT.html
- @testing-library/jest-dom - MIT - https://spdx.org/licenses/MIT.html
- @testing-library/react - MIT - https://spdx.org/licenses/MIT.html
- @vitejs/plugin-react - MIT - https://spdx.org/licenses/MIT.html
- jsdom - MIT - https://github.com/jsdom/jsdom/blob/main/LICENSE.txt
- vite - MIT - https://spdx.org/licenses/MIT.html
- vitest - MIT - https://github.com/vitest-dev/vitest/blob/main/LICENSE

## Maintenance Notes

- Update this file whenever a dependency is added, removed, upgraded to a materially different licensed product, bundled into `Dependencies/`, or downloaded by bootstrap/runtime scripts.
- Keep Agent and Engine inventories separate so deployment reviewers can quickly assess licensing impact by runtime.
- If Borealis later adopts lockfiles or generated SBOM tooling, this file can be expanded to include resolved transitive dependencies.
