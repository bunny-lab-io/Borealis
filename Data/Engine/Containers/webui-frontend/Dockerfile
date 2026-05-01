FROM node:22-bookworm-slim

ENV NODE_ENV=production

WORKDIR /opt/Borealis/Data/Engine/web-interface

COPY Data/Engine/web-interface/package.json /opt/Borealis/Data/Engine/web-interface/package.json
RUN npm install --no-fund --audit=false

COPY Data/Engine/web-interface /opt/Borealis/Data/Engine/web-interface
COPY Data/Engine/Containers/webui-frontend/entrypoint.sh /usr/local/bin/borealis-webui-frontend

RUN chmod +x /usr/local/bin/borealis-webui-frontend \
    && npm run build

EXPOSE 8080 5173

ENTRYPOINT ["/usr/local/bin/borealis-webui-frontend"]
