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
        iptables \
        openssh-client \
        procps \
        rsync \
        sshpass \
        tesseract-ocr \
        wireguard-tools \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/Borealis

COPY Data/Engine/Containers/api-backend/data/engine-requirements.txt /tmp/borealis-engine-requirements.txt
RUN python -m pip install --upgrade pip \
    && python -m pip install -r /tmp/borealis-engine-requirements.txt

COPY Borealis.ps1 /opt/Borealis/Borealis.ps1
COPY Data/Engine/Containers/api-backend/data /opt/Borealis/Data/Engine
COPY Data/Agent /opt/Borealis/Data/Agent
COPY Data/Engine/Containers/api-backend/entrypoint.sh /usr/local/bin/borealis-api-backend
COPY Data/Engine/Containers/api-backend/healthcheck.py /usr/local/bin/borealis-api-healthcheck

RUN chmod +x /usr/local/bin/borealis-api-backend /usr/local/bin/borealis-api-healthcheck

EXPOSE 5000

ENTRYPOINT ["/usr/local/bin/borealis-api-backend"]
