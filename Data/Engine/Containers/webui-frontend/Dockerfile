FROM node:22-alpine AS deps

ARG BOREALIS_ENGINE_RUNTIME_UID=64646
ARG BOREALIS_ENGINE_RUNTIME_GID=64646

ENV NODE_ENV=development

RUN addgroup -S -g "${BOREALIS_ENGINE_RUNTIME_GID}" borealis-engine \
    && adduser -S -D -H -u "${BOREALIS_ENGINE_RUNTIME_UID}" -G borealis-engine -s /sbin/nologin borealis-engine

WORKDIR /opt/Borealis/Data/Engine/web-interface

COPY data/web-interface/package.json data/web-interface/package-lock.json /opt/Borealis/Data/Engine/web-interface/
RUN npm ci --include=dev --no-fund --audit=false

FROM deps AS source

COPY data/web-interface /opt/Borealis/Data/Engine/web-interface

FROM source AS dev

COPY entrypoint.sh /usr/local/bin/borealis-webui-frontend
COPY healthcheck.js /usr/local/bin/borealis-webui-healthcheck
COPY static-server.js /usr/local/bin/borealis-webui-static-server.js

RUN chmod +x \
    /usr/local/bin/borealis-webui-frontend \
    /usr/local/bin/borealis-webui-healthcheck \
    /usr/local/bin/borealis-webui-static-server.js

EXPOSE 8000

ENTRYPOINT ["/usr/local/bin/borealis-webui-frontend"]

FROM source AS build

RUN NODE_ENV=production npm run build

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

RUN chmod +x \
    /usr/local/bin/borealis-webui-frontend \
    /usr/local/bin/borealis-webui-healthcheck \
    /usr/local/bin/borealis-webui-static-server.js

EXPOSE 8000

ENTRYPOINT ["/usr/local/bin/borealis-webui-frontend"]
