FROM alpine:3.24

ARG BOREALIS_ENGINE_RUNTIME_UID=64646
ARG BOREALIS_ENGINE_RUNTIME_GID=64646

RUN apk add --no-cache \
        bash \
        ca-certificates \
        tzdata \
    && addgroup -S -g "${BOREALIS_ENGINE_RUNTIME_GID}" borealis-engine \
    && adduser -S -D -H -u "${BOREALIS_ENGINE_RUNTIME_UID}" -G borealis-engine -s /sbin/nologin borealis-engine

WORKDIR /opt/Borealis

COPY Data/Engine/Containers/api-backend/dist/api-backend /usr/local/bin/borealis-api-backend-go
COPY Data/Engine/Containers/job-scheduler/entrypoint.sh /usr/local/bin/borealis-job-scheduler
COPY Data/Engine/Containers/job-scheduler/healthcheck.sh /usr/local/bin/borealis-job-scheduler-healthcheck

RUN chmod +x /usr/local/bin/borealis-api-backend-go /usr/local/bin/borealis-job-scheduler /usr/local/bin/borealis-job-scheduler-healthcheck

ENTRYPOINT ["/usr/local/bin/borealis-job-scheduler"]
