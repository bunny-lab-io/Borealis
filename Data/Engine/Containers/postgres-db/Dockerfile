FROM postgres:17-bookworm

ARG BOREALIS_ENGINE_RUNTIME_UID=64646
ARG BOREALIS_ENGINE_RUNTIME_GID=64646

RUN groupadd --system --gid "${BOREALIS_ENGINE_RUNTIME_GID}" borealis-engine \
    && useradd --system --uid "${BOREALIS_ENGINE_RUNTIME_UID}" --gid "${BOREALIS_ENGINE_RUNTIME_GID}" --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin borealis-engine

EXPOSE 5432

CMD ["postgres", "-c", "listen_addresses=127.0.0.1", "-c", "port=5432"]
