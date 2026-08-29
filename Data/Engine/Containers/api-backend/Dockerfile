FROM alpine:3.24

ARG BOREALIS_ENGINE_RUNTIME_UID=64646
ARG BOREALIS_ENGINE_RUNTIME_GID=64646

RUN apk add --no-cache \
        bash \
        ca-certificates \
        curl \
        git \
        tzdata \
    && addgroup -S -g "${BOREALIS_ENGINE_RUNTIME_GID}" borealis-engine \
    && adduser -S -D -H -u "${BOREALIS_ENGINE_RUNTIME_UID}" -G borealis-engine -s /sbin/nologin borealis-engine

WORKDIR /opt/Borealis

COPY Data/Agent /opt/Borealis/Data/Agent
COPY Data/Engine/Containers/api-backend/dist/api-backend /usr/local/bin/borealis-api-backend-go
COPY Data/Engine/Containers/api-backend/dist/borealis-cluster-controller /usr/local/bin/borealis-cluster-controller
COPY Data/Engine/Containers/api-backend/dist/borealis-node-manager /usr/local/bin/borealis-node-manager
COPY Data/Engine/Containers/api-backend/entrypoint.sh /usr/local/bin/borealis-api-backend

RUN chmod +x /usr/local/bin/borealis-api-backend-go /usr/local/bin/borealis-cluster-controller /usr/local/bin/borealis-node-manager /usr/local/bin/borealis-api-backend

EXPOSE 5000

ENTRYPOINT ["/usr/local/bin/borealis-api-backend"]
