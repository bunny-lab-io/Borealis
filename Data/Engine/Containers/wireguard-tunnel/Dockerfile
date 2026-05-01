FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        firewalld \
        iproute2 \
        iptables \
        procps \
        python3 \
        wireguard-tools \
    && rm -rf /var/lib/apt/lists/*

COPY Data/Engine/Containers/wireguard-tunnel/control_server.py /usr/local/bin/borealis-wireguard-control
COPY Data/Engine/Containers/wireguard-tunnel/control_client.py /usr/local/bin/borealis-wireguard-control-client
RUN chmod +x /usr/local/bin/borealis-wireguard-control /usr/local/bin/borealis-wireguard-control-client

EXPOSE 30000/udp

ENTRYPOINT ["/usr/local/bin/borealis-wireguard-control"]
