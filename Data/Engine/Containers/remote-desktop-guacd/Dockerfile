FROM guacamole/guacd:1.6.0

COPY --chmod=755 healthcheck.sh /usr/local/bin/borealis-guacd-healthcheck
COPY --chmod=755 entrypoint.sh /usr/local/bin/borealis-guacd-entrypoint

EXPOSE 4822

USER root
ENTRYPOINT ["/usr/local/bin/borealis-guacd-entrypoint"]
CMD []
