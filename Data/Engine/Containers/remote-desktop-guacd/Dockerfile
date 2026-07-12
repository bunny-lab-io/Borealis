FROM guacamole/guacd:1.6.0

ARG BOREALIS_ENGINE_RUNTIME_UID=64646
ARG BOREALIS_ENGINE_RUNTIME_GID=64646

USER root
RUN groupadd --system --gid "${BOREALIS_ENGINE_RUNTIME_GID}" borealis-engine \
    && useradd --system --uid "${BOREALIS_ENGINE_RUNTIME_UID}" --gid "${BOREALIS_ENGINE_RUNTIME_GID}" --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin borealis-engine

COPY --chmod=755 healthcheck.sh /usr/local/bin/borealis-guacd-healthcheck
COPY --chmod=755 entrypoint.sh /usr/local/bin/borealis-guacd-entrypoint

EXPOSE 4822

CMD ["borealis-guacd-entrypoint"]
