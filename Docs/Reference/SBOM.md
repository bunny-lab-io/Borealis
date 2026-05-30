# Software Bill of Materials

This software bill of materials is an inventory of the direct, repo-declared, bundled, container base image, or script-installed third-party software used by Borealis. It was assembled from `Agent.exe`, `Engine.sh`, `Data/Agent/go.mod`, `Data/Engine/Containers/api-backend/data/engine-requirements.txt`, `Data/Engine/Containers/api-backend/data/engine-worker-requirements.txt`, `Data/Engine/Containers/webui-frontend/data/web-interface/package.json`, `Data/Engine/Containers/api-backend/data/Ansible/collections.yml`, and `Data/Engine/Containers/`.

The Python requirement files are currently unpinned, so the exact resolved version can change between installs. Where the install scripts pin a version explicitly, that version is called out below.

## Borealis Agent Dependencies

- Go standard library/runtime (compiled into `Agent.exe`) - BSD-3-Clause - https://go.dev/LICENSE
- github.com/gorilla/websocket v1.5.3 - BSD-2-Clause - https://github.com/gorilla/websocket/blob/main/LICENSE
- golang.org/x/sys v0.28.0 - BSD-3-Clause - https://cs.opensource.google/go/x/sys/+/master:LICENSE
- WireGuard (Windows MSI package 1.1 / client 0.5.3 and Linux `wireguard-tools`) - GPL-2.0-only - https://spdx.org/licenses/GPL-2.0-only.html
- UltraVNC Server 1.8.2.1 - GPL-2.0-only - https://spdx.org/licenses/GPL-2.0-only.html
- Go toolchain (native Linux build helper installs official Go into `Dependencies/Go` when missing) - BSD-3-Clause - https://go.dev/LICENSE

## Borealis Engine Dependencies

- Docker Engine (Linux Engine deployment runtime; Docker Desktop not used) - Apache-2.0 - https://github.com/moby/moby/blob/master/LICENSE
- Docker CLI (`docker-ce-cli`, host and api-backend service-action helper) - Apache-2.0 - https://github.com/docker/cli/blob/master/LICENSE
- Docker Compose plugin (Linux Engine deployment orchestration and api-backend service-action helper) - Apache-2.0 - https://github.com/docker/compose/blob/main/LICENSE
- Docker Buildx plugin / BuildKit (optional local Engine image build cache acceleration) - Apache-2.0 - https://github.com/docker/buildx/blob/master/LICENSE
- Tecnativa Docker Socket Proxy container image (`ghcr.io/tecnativa/docker-socket-proxy:v0.4.2`) - Apache-2.0 - https://github.com/Tecnativa/docker-socket-proxy/blob/master/LICENSE.txt
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
- @codemirror/lang-css - MIT - https://spdx.org/licenses/MIT.html
- @codemirror/lang-html - MIT - https://spdx.org/licenses/MIT.html
- @codemirror/lang-javascript - MIT - https://spdx.org/licenses/MIT.html
- @codemirror/lang-json - MIT - https://spdx.org/licenses/MIT.html
- @codemirror/lang-markdown - MIT - https://spdx.org/licenses/MIT.html
- @codemirror/lang-python - MIT - https://spdx.org/licenses/MIT.html
- @codemirror/lang-sql - MIT - https://spdx.org/licenses/MIT.html
- @codemirror/lang-xml - MIT - https://spdx.org/licenses/MIT.html
- @codemirror/lang-yaml - MIT - https://spdx.org/licenses/MIT.html
- @codemirror/language - MIT - https://spdx.org/licenses/MIT.html
- @codemirror/legacy-modes - MIT - https://spdx.org/licenses/MIT.html
- @codemirror/lint - MIT - https://spdx.org/licenses/MIT.html
- @codemirror/merge - MIT - https://spdx.org/licenses/MIT.html
- @codemirror/search - MIT - https://spdx.org/licenses/MIT.html
- @codemirror/state - MIT - https://spdx.org/licenses/MIT.html
- @codemirror/theme-one-dark - MIT - https://spdx.org/licenses/MIT.html
- @codemirror/view - MIT - https://spdx.org/licenses/MIT.html
- dayjs - MIT - https://spdx.org/licenses/MIT.html
- normalize.css - MIT - https://spdx.org/licenses/MIT.html
- @lezer/highlight - MIT - https://spdx.org/licenses/MIT.html
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
- @uiw/react-codemirror - MIT - https://github.com/uiwjs/react-codemirror/blob/master/LICENSE
- codemirror - MIT - https://spdx.org/licenses/MIT.html
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
