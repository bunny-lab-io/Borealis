FROM debian:bookworm-slim

ARG BOREALIS_ENGINE_RUNTIME_UID=64646
ARG BOREALIS_ENGINE_RUNTIME_GID=64646

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        iproute2 \
        iptables \
        procps \
        python3 \
        wireguard-tools \
    && groupadd --system --gid "${BOREALIS_ENGINE_RUNTIME_GID}" borealis-engine \
    && useradd --system --uid "${BOREALIS_ENGINE_RUNTIME_UID}" --gid "${BOREALIS_ENGINE_RUNTIME_GID}" --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin borealis-engine \
    && rm -rf /var/lib/apt/lists/*

COPY control_server.py /usr/local/bin/borealis-wireguard-control
COPY control_client.py /usr/local/bin/borealis-wireguard-control-client
COPY healthcheck.sh /usr/local/bin/borealis-wireguard-healthcheck
RUN chmod +x /usr/local/bin/borealis-wireguard-control /usr/local/bin/borealis-wireguard-control-client /usr/local/bin/borealis-wireguard-healthcheck

EXPOSE 30000/udp

ENTRYPOINT ["/usr/local/bin/borealis-wireguard-control"]
