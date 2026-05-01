FROM node:22-bookworm-slim

ENV NODE_ENV=development

WORKDIR /opt/Borealis/Data/Engine/web-interface

COPY Data/Engine/Containers/webui-frontend/data/web-interface/package.json /opt/Borealis/Data/Engine/web-interface/package.json
RUN npm install --include=dev --no-fund --audit=false

COPY Data/Engine/Containers/webui-frontend/data/web-interface /opt/Borealis/Data/Engine/web-interface
COPY Data/Engine/Containers/webui-frontend/entrypoint.sh /usr/local/bin/borealis-webui-frontend

RUN chmod +x /usr/local/bin/borealis-webui-frontend \
    && NODE_ENV=production npm run build

EXPOSE 8080 5173

ENTRYPOINT ["/usr/local/bin/borealis-webui-frontend"]
