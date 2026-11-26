FROM scratch AS borealis-npm-seed

FROM node:22-alpine AS deps

ARG BOREALIS_ENGINE_RUNTIME_UID=64646
ARG BOREALIS_ENGINE_RUNTIME_GID=64646
ARG NPM_CONFIG_OFFLINE=false

ENV NODE_ENV=development

RUN addgroup -S -g "${BOREALIS_ENGINE_RUNTIME_GID}" borealis-engine \
    && adduser -S -D -H -u "${BOREALIS_ENGINE_RUNTIME_UID}" -G borealis-engine -s /sbin/nologin borealis-engine

WORKDIR /opt/Borealis/Data/Engine/web-interface

COPY data/web-interface/package.json data/web-interface/package-lock.json /opt/Borealis/Data/Engine/web-interface/
RUN --mount=type=bind,from=borealis-npm-seed,target=/root/.npm,rw \
    npm ci --include=dev --no-fund --audit=false \
    && mkdir -p node_modules/.vite-temp

FROM deps AS source

COPY data/web-interface /opt/Borealis/Data/Engine/web-interface

FROM source AS dev

COPY entrypoint.sh /usr/local/bin/borealis-webui-frontend
COPY healthcheck.js /usr/local/bin/borealis-webui-healthcheck
COPY static-server.js /usr/local/bin/borealis-webui-static-server.js

RUN chmod 0755 \
    /usr/local/bin/borealis-webui-frontend \
    /usr/local/bin/borealis-webui-healthcheck \
    /usr/local/bin/borealis-webui-static-server.js

EXPOSE 8000

ENTRYPOINT ["/usr/local/bin/borealis-webui-frontend"]

FROM node:22-alpine AS build

WORKDIR /opt/Borealis/Data/Engine/web-interface
COPY data/web-interface ./
# The native snapshotter would copy the entire dependency tree for each
# writable step. The production compiler only needs to read that tree.
RUN --mount=type=bind,from=deps,source=/opt/Borealis/Data/Engine/web-interface/node_modules,target=/opt/Borealis/Data/Engine/web-interface/node_modules \
    --mount=type=tmpfs,target=/opt/Borealis/Data/Engine/web-interface/node_modules/.vite-temp,size=8m \
    NODE_ENV=production npm run build

FROM node:22-alpine AS prod

ARG BOREALIS_ENGINE_RUNTIME_UID=64646
ARG BOREALIS_ENGINE_RUNTIME_GID=64646

ENV NODE_ENV=production

RUN addgroup -S -g "${BOREALIS_ENGINE_RUNTIME_GID}" borealis-engine \
    && adduser -S -D -H -u "${BOREALIS_ENGINE_RUNTIME_UID}" -G borealis-engine -s /sbin/nologin borealis-engine

WORKDIR /opt/Borealis/Data/Engine/web-interface

COPY --from=build /opt/Borealis/Data/Engine/web-interface/build /opt/Borealis/Data/Engine/web-interface/build
COPY entrypoint.sh /usr/local/bin/borealis-webui-frontend
COPY healthcheck.js /usr/local/bin/borealis-webui-healthcheck
COPY static-server.js /usr/local/bin/borealis-webui-static-server.js

RUN chmod 0755 \
    /usr/local/bin/borealis-webui-frontend \
    /usr/local/bin/borealis-webui-healthcheck \
    /usr/local/bin/borealis-webui-static-server.js

EXPOSE 8000

ENTRYPOINT ["/usr/local/bin/borealis-webui-frontend"]
