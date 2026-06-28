FROM alpine:3.24

RUN apk add --no-cache \
        bash \
        ca-certificates \
        docker-cli \
        docker-cli-compose \
        python3 \
        tzdata

WORKDIR /opt/Borealis

RUN mkdir -p /opt/Borealis/Data/Engine/Containers
COPY Data/Engine/Containers/compose.yaml /opt/Borealis/Data/Engine/Containers/compose.yaml
COPY Data/Engine/Containers/api-backend/dist/api-backend /usr/local/bin/borealis-api-backend-go
COPY Data/Engine/Containers/job-scheduler/entrypoint.sh /usr/local/bin/borealis-job-scheduler
COPY Data/Engine/Containers/job-scheduler/healthcheck.sh /usr/local/bin/borealis-job-scheduler-healthcheck

RUN chmod +x /usr/local/bin/borealis-api-backend-go /usr/local/bin/borealis-job-scheduler /usr/local/bin/borealis-job-scheduler-healthcheck

ENTRYPOINT ["/usr/local/bin/borealis-job-scheduler"]
