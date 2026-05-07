FROM python:3.12-slim-bookworm

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        iproute2 \
        openssh-client \
        procps \
        rsync \
        sshpass \
        wireguard-tools \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/Borealis

COPY Data/Engine/Containers/api-backend/data/engine-requirements.txt /tmp/borealis-engine-requirements.txt
RUN python -m pip install --upgrade pip \
    && python -m pip install -r /tmp/borealis-engine-requirements.txt

COPY Agent.ps1 /opt/Borealis/Agent.ps1
COPY Data/Engine/Containers/api-backend/data /opt/Borealis/Data/Engine
COPY Data/Agent /opt/Borealis/Data/Agent
COPY Data/Agent/Bootstrapper/dist/Agent_Service_Bootstrapper.exe /opt/Borealis/Data/Agent/Bootstrapper/dist/Agent_Service_Bootstrapper.exe
COPY Data/Engine/Containers/site-worker/entrypoint.sh /usr/local/bin/borealis-site-worker

RUN chmod +x /usr/local/bin/borealis-site-worker

ENTRYPOINT ["/usr/local/bin/borealis-site-worker"]
