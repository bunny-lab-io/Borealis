FROM alpine:3.24

ARG BOREALIS_ENGINE_RUNTIME_UID=64646
ARG BOREALIS_ENGINE_RUNTIME_GID=64646

RUN apk add --no-cache \
        ca-certificates \
        tzdata \
    && addgroup -S -g "${BOREALIS_ENGINE_RUNTIME_GID}" borealis-engine \
    && adduser -S -D -H -u "${BOREALIS_ENGINE_RUNTIME_UID}" -G borealis-engine -s /sbin/nologin borealis-engine

WORKDIR /opt/Borealis

COPY Data/Engine/Containers/api-backend/dist/api-backend /usr/local/bin/borealis-api-backend-go

RUN chmod +x /usr/local/bin/borealis-api-backend-go

USER borealis-engine:borealis-engine

EXPOSE 8088

ENTRYPOINT ["/usr/local/bin/borealis-api-backend-go", "borealis-operator"]
