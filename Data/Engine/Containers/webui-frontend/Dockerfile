FROM node:22-bookworm-slim AS base

ENV NODE_ENV=development

WORKDIR /opt/Borealis/Data/Engine/web-interface

COPY data/web-interface/package.json /opt/Borealis/Data/Engine/web-interface/package.json
RUN npm install --include=dev --no-fund --audit=false

COPY data/web-interface /opt/Borealis/Data/Engine/web-interface
COPY entrypoint.sh /usr/local/bin/borealis-webui-frontend
COPY healthcheck.js /usr/local/bin/borealis-webui-healthcheck

RUN chmod +x /usr/local/bin/borealis-webui-frontend /usr/local/bin/borealis-webui-healthcheck

EXPOSE 8080 5173

FROM base AS dev

ENTRYPOINT ["/usr/local/bin/borealis-webui-frontend"]

FROM base AS prod

RUN NODE_ENV=production npm run build

ENTRYPOINT ["/usr/local/bin/borealis-webui-frontend"]
