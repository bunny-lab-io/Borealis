FROM guacamole/guacd:1.6.0

COPY --chmod=755 healthcheck.sh /usr/local/bin/borealis-guacd-healthcheck

EXPOSE 4822

CMD ["guacd", "-b", "127.0.0.1", "-l", "4822", "-f", "-L", "info"]
