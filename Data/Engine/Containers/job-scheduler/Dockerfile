FROM python:3.12-slim-bookworm

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        gnupg \
        iproute2 \
        openssh-client \
        procps \
        rsync \
        sshpass \
        wireguard-tools \
    && rm -rf /var/lib/apt/lists/*

RUN install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc \
    && chmod a+r /etc/apt/keyrings/docker.asc \
    && . /etc/os-release \
    && printf 'Types: deb\nURIs: https://download.docker.com/linux/debian\nSuites: %s\nComponents: stable\nArchitectures: %s\nSigned-By: /etc/apt/keyrings/docker.asc\n' "$VERSION_CODENAME" "$(dpkg --print-architecture)" > /etc/apt/sources.list.d/docker.sources \
    && apt-get update \
    && apt-get install -y --no-install-recommends docker-ce-cli docker-buildx-plugin docker-compose-plugin \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/Borealis

COPY Data/Engine/Containers/api-backend/data/engine-requirements.txt /tmp/engine-requirements.txt
COPY Data/Engine/Containers/api-backend/data/engine-worker-requirements.txt /tmp/engine-worker-requirements.txt
RUN python -m pip install --upgrade pip \
    && python -m pip install -r /tmp/engine-worker-requirements.txt

COPY Data/Engine/Containers/api-backend/data /opt/Borealis/Data/Engine
RUN mkdir -p /opt/Borealis/Data/Engine/Containers
COPY Data/Engine/Containers/compose.yaml /opt/Borealis/Data/Engine/Containers/compose.yaml
COPY Data/Agent /opt/Borealis/Data/Agent
COPY Data/Engine/Containers/api-backend/dist/api-backend /usr/local/bin/borealis-api-backend-go
COPY Data/Engine/Containers/job-scheduler/entrypoint.sh /usr/local/bin/borealis-job-scheduler
COPY Data/Engine/Containers/job-scheduler/healthcheck.sh /usr/local/bin/borealis-job-scheduler-healthcheck

RUN chmod +x /usr/local/bin/borealis-api-backend-go /usr/local/bin/borealis-job-scheduler /usr/local/bin/borealis-job-scheduler-healthcheck

ENTRYPOINT ["/usr/local/bin/borealis-job-scheduler"]
