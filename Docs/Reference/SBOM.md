# Software Bill of Materials

This software bill of materials inventories direct, repo-declared, bundled, container base image, or script-installed third-party software used by Borealis. Engine dependencies are grouped by container/domain so reviewers can trace licensing impact to the runtime that uses each dependency.

Repeated entries are intentional when multiple Engine containers install the same requirement file or use the same runtime package. Python requirement files are currently unpinned, so exact resolved versions can change between installs. Explicitly pinned script, Go, Node, and container versions are called out below.

Primary sources:

- `Data/Agent/go.mod`
- `Data/Agent/build-agent.sh`
- `Engine.sh`
- `Data/Engine/Containers/build-manifest.json`
- `Data/Engine/Containers/compose.yaml`
- `Data/Engine/Containers/*/Dockerfile`
- `Data/Engine/Containers/api-backend/go.mod`
- `Data/Engine/Containers/api-backend/build-api-backend.sh`
- `Data/Engine/Containers/api-backend/data/engine-requirements.txt`
- `Data/Engine/Containers/api-backend/data/engine-worker-requirements.txt`
- `Data/Engine/Containers/api-backend/data/Ansible/collections.yml`
- `Data/Engine/Containers/webui-frontend/data/web-interface/package.json`
- `Data/Engine/Containers/webui-frontend/data/web-interface/src/vendor/guacamole/guacamole-common-js.js`

## Borealis Agent Dependencies

- Go standard library/runtime (compiled into `Agent.exe`) - BSD-3-Clause - https://go.dev/LICENSE
- github.com/gorilla/websocket v1.5.3 - BSD-2-Clause - https://github.com/gorilla/websocket/blob/main/LICENSE
- golang.org/x/sys v0.28.0 - BSD-3-Clause - https://cs.opensource.google/go/x/sys/+/master:LICENSE
- WireGuard (Windows MSI package 1.1 / client 0.5.3 and Linux `wireguard-tools`) - GPL-2.0-only - https://spdx.org/licenses/GPL-2.0-only.html
- UltraVNC Server 1.8.2.1 - GPL-2.0-only - https://spdx.org/licenses/GPL-2.0-only.html
- Go toolchain (native Linux build helper installs official Go into `Dependencies/Go` when missing) - BSD-3-Clause - https://go.dev/LICENSE

## Borealis Engine Dependencies

### `api-backend`

- Python container base image (`python:3.12-slim-bookworm`) - PSF License plus Debian package licenses - https://github.com/docker-library/python/blob/master/LICENSE
- Go standard library/runtime (compiled into the Go `api-backend` gateway) - BSD-3-Clause - https://go.dev/LICENSE
- github.com/lib/pq v1.10.9 (Go PostgreSQL driver) - MIT - https://github.com/lib/pq/blob/master/LICENSE.md
- golang.org/x/crypto v0.33.0 (Go scrypt KDF support) - BSD-3-Clause - https://cs.opensource.google/go/x/crypto/+/master:LICENSE
- github.com/go-ldap/ldap/v3 v3.4.8 (Go LDAP/LDAPS directory-provider support) - MIT - https://github.com/go-ldap/ldap/blob/master/LICENSE
- github.com/Azure/go-ntlmssp v0.0.0-20221128193559-754e69321358 (Go LDAP NTLM support dependency) - MIT - https://github.com/Azure/go-ntlmssp/blob/master/LICENSE
- github.com/go-asn1-ber/asn1-ber v1.5.5 (Go LDAP ASN.1 BER codec dependency) - MIT - https://github.com/go-asn1-ber/asn1-ber/blob/master/LICENSE
- github.com/go-webauthn/webauthn v0.10.2 (Go WebAuthn passkey ceremonies) - BSD-3-Clause - https://github.com/go-webauthn/webauthn/blob/master/LICENSE
- github.com/fxamacker/cbor/v2 v2.6.0 (Go WebAuthn CBOR codec dependency) - MIT - https://github.com/fxamacker/cbor/blob/master/LICENSE
- github.com/go-webauthn/x v0.1.9 (Go WebAuthn support dependency) - BSD-3-Clause - https://github.com/go-webauthn/x/blob/master/LICENSE
- github.com/golang-jwt/jwt/v5 v5.2.1 (Go WebAuthn transitive JWT support dependency) - MIT - https://github.com/golang-jwt/jwt/blob/main/LICENSE
- github.com/google/go-tpm v0.9.0 (Go WebAuthn TPM attestation support dependency) - Apache-2.0 - https://github.com/google/go-tpm/blob/main/LICENSE
- github.com/google/uuid v1.6.0 (Go WebAuthn UUID support dependency) - BSD-3-Clause - https://github.com/google/uuid/blob/master/LICENSE
- github.com/mitchellh/mapstructure v1.5.0 (Go WebAuthn config decode dependency) - MIT - https://github.com/mitchellh/mapstructure/blob/main/LICENSE
- github.com/x448/float16 v0.8.4 (Go CBOR half-float dependency) - MIT - https://github.com/x448/float16/blob/master/LICENSE
- golang.org/x/sys v0.30.0 (Go WebAuthn system support dependency) - BSD-3-Clause - https://cs.opensource.google/go/x/sys/+/master:LICENSE
- Docker CLI (`docker-ce-cli`, container service-action helper) - Apache-2.0 - https://github.com/docker/cli/blob/master/LICENSE
- Docker Compose plugin (container service-action helper) - Apache-2.0 - https://github.com/docker/compose/blob/main/LICENSE
- Docker Buildx plugin / BuildKit (container image rebuild helper) - Apache-2.0 - https://github.com/docker/buildx/blob/master/LICENSE
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
- pytest - MIT - https://github.com/pytest-dev/pytest/blob/main/LICENSE
- Tesseract OCR - Apache-2.0 - https://github.com/tesseract-ocr/tesseract/blob/main/LICENSE
- WireGuard tools - GPL-2.0-only - https://spdx.org/licenses/GPL-2.0-only.html

### `job-scheduler`

- Python container base image (`python:3.12-slim-bookworm`) - PSF License plus Debian package licenses - https://github.com/docker-library/python/blob/master/LICENSE
- Docker CLI (`docker-ce-cli`, site-worker container launcher) - Apache-2.0 - https://github.com/docker/cli/blob/master/LICENSE
- Docker Compose plugin (Engine service orchestration helper) - Apache-2.0 - https://github.com/docker/compose/blob/main/LICENSE
- Docker Buildx plugin / BuildKit (container image rebuild helper) - Apache-2.0 - https://github.com/docker/buildx/blob/master/LICENSE
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
- pytest - MIT - https://github.com/pytest-dev/pytest/blob/main/LICENSE
- ansible-core - GPL-3.0-or-later - https://spdx.org/licenses/GPL-3.0-or-later.html
- ansible-runner - Apache-2.0 - https://spdx.org/licenses/Apache-2.0.html
- jmespath - MIT - https://spdx.org/licenses/MIT.html
- pywinrm (`pywinrm[credssp]`) - MIT - https://github.com/diyan/pywinrm/blob/master/LICENSE
- pypsrp (`pypsrp[credssp]`) - MIT - https://github.com/jborean93/pypsrp/blob/master/LICENSE
- Impacket - Apache-2.0 - https://github.com/fortra/impacket/blob/master/LICENSE
- ansible.windows collection - GPL-3.0-or-later - https://github.com/ansible-collections/ansible.windows/blob/main/LICENSE
- ansible.posix collection - GPL-3.0-or-later - https://github.com/ansible-collections/ansible.posix/blob/main/LICENSE
- community.general collection - GPL-3.0-or-later - https://github.com/ansible-collections/community.general/blob/main/COPYING
- WireGuard tools - GPL-2.0-only - https://spdx.org/licenses/GPL-2.0-only.html

### `postgres-db`

- PostgreSQL container image (`postgres:17-bookworm`) - PostgreSQL License plus Debian package licenses - https://www.postgresql.org/about/licence/

### `remote-desktop-guacd`

- Apache Guacamole Server container image (`guacamole/guacd:1.6.0`) - Apache-2.0 - https://github.com/apache/guacamole-server/blob/1.6.0/LICENSE
- Apache Guacamole Server (`guacd` and VNC plugin) 1.6.0 - Apache-2.0 - https://github.com/apache/guacamole-server/blob/1.6.0/LICENSE
- LibVNCServer / LibVNCClient - GPL-2.0-or-later - https://github.com/LibVNC/libvncserver/blob/master/COPYING

### `site-worker`

- Python container base image (`python:3.12-slim-bookworm`) - PSF License plus Debian package licenses - https://github.com/docker-library/python/blob/master/LICENSE
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
- pytest - MIT - https://github.com/pytest-dev/pytest/blob/main/LICENSE
- ansible-core - GPL-3.0-or-later - https://spdx.org/licenses/GPL-3.0-or-later.html
- ansible-runner - Apache-2.0 - https://spdx.org/licenses/Apache-2.0.html
- jmespath - MIT - https://spdx.org/licenses/MIT.html
- pywinrm (`pywinrm[credssp]`) - MIT - https://github.com/diyan/pywinrm/blob/master/LICENSE
- pypsrp (`pypsrp[credssp]`) - MIT - https://github.com/jborean93/pypsrp/blob/master/LICENSE
- Impacket - Apache-2.0 - https://github.com/fortra/impacket/blob/master/LICENSE
- ansible.windows collection - GPL-3.0-or-later - https://github.com/ansible-collections/ansible.windows/blob/main/LICENSE
- ansible.posix collection - GPL-3.0-or-later - https://github.com/ansible-collections/ansible.posix/blob/main/LICENSE
- community.general collection - GPL-3.0-or-later - https://github.com/ansible-collections/community.general/blob/main/COPYING
- WireGuard tools - GPL-2.0-only - https://spdx.org/licenses/GPL-2.0-only.html

### `traefik-edge`

- Traefik container image (`traefik:v3.3`) - MIT - https://github.com/traefik/traefik/blob/master/LICENSE.md
- Traefik (Borealis-managed local HTTPS edge and ACME client) - MIT - https://github.com/traefik/traefik/blob/master/LICENSE.md

### `webui-frontend`

- Node.js container base image (`node:22-bookworm-slim`) - MIT plus Debian package licenses - https://github.com/nodejs/node/blob/main/LICENSE
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
- Apache Guacamole Client (`guacamole-common-js`) 1.6.0 - Apache-2.0 - https://github.com/apache/guacamole-client/blob/1.6.0/LICENSE
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
- @lezer/highlight - MIT - https://spdx.org/licenses/MIT.html
- @uiw/react-codemirror - MIT - https://github.com/uiwjs/react-codemirror/blob/master/LICENSE
- codemirror - MIT - https://spdx.org/licenses/MIT.html
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

### `wireguard-tunnel`

- Debian Bookworm base image (`debian:bookworm-slim`) - Debian Free Software Guidelines / package-specific licenses - https://www.debian.org/legal/licenses/
- Python (system Python on Linux) - PSF License - https://docs.python.org/3/license.html
- WireGuard tools - GPL-2.0-only - https://spdx.org/licenses/GPL-2.0-only.html

### Shared Engine Dependencies

- Docker Engine (Linux Engine deployment runtime; Docker Desktop not used) - Apache-2.0 - https://github.com/moby/moby/blob/master/LICENSE
- Docker CLI (`docker-ce-cli`, host deployment and service-management helper) - Apache-2.0 - https://github.com/docker/cli/blob/master/LICENSE
- Docker Compose plugin (Linux Engine deployment orchestration) - Apache-2.0 - https://github.com/docker/compose/blob/main/LICENSE
- Docker Buildx plugin / BuildKit (optional local Engine image build cache acceleration) - Apache-2.0 - https://github.com/docker/buildx/blob/master/LICENSE
- Tecnativa Docker Socket Proxy container image (`ghcr.io/tecnativa/docker-socket-proxy:v0.4.2`) - Apache-2.0 - https://github.com/Tecnativa/docker-socket-proxy/blob/master/LICENSE.txt
- Python (system Python on Linux, used by `Engine.sh` deployment helpers) - PSF License - https://docs.python.org/3/license.html
- Go toolchain (native Linux `api-backend` build helper installs official Go into `Dependencies/Go` when missing) - BSD-3-Clause - https://go.dev/LICENSE

## Maintenance Notes

- Update this file whenever a dependency is added, removed, upgraded to a materially different licensed product, bundled into `Dependencies/`, or downloaded by bootstrap/runtime scripts.
- Keep Agent and Engine inventories separate so deployment reviewers can quickly assess licensing impact by runtime.
- Keep Engine dependency entries under the container/domain that installs or vendors them. If a dependency only supports deployment or cross-container orchestration, list it under Shared Engine Dependencies.
- If Borealis later adopts lockfiles or generated SBOM tooling, this file can be expanded to include resolved transitive dependencies.
