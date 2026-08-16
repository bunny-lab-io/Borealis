FROM traefik:v3.7

COPY entrypoint.sh /usr/local/bin/borealis-traefik-edge
RUN chmod +x /usr/local/bin/borealis-traefik-edge

EXPOSE 80 443

ENTRYPOINT ["/usr/local/bin/borealis-traefik-edge"]
