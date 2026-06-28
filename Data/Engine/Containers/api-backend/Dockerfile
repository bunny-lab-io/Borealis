FROM alpine:3.24

RUN apk add --no-cache \
        ca-certificates \
        git \
        tzdata

WORKDIR /opt/Borealis

COPY Data/Engine/Containers/api-backend/data /opt/Borealis/Data/Engine
COPY Data/Agent /opt/Borealis/Data/Agent
COPY Data/Engine/Containers/api-backend/dist/api-backend /usr/local/bin/borealis-api-backend-go
COPY Data/Engine/Containers/api-backend/entrypoint.sh /usr/local/bin/borealis-api-backend

RUN chmod +x /usr/local/bin/borealis-api-backend-go /usr/local/bin/borealis-api-backend

EXPOSE 5000

ENTRYPOINT ["/usr/local/bin/borealis-api-backend"]
