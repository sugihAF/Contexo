# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/contexo-server ./cmd/contexo-server

# Runtime image: alpine + git (Contexo shells out to git for per-repo storage)
FROM alpine:3.20

RUN apk add --no-cache git ca-certificates \
 && adduser -D -u 10001 contexo

COPY --from=builder /out/contexo-server /usr/local/bin/contexo-server

ENV CONTEXO_DATA_ROOT=/data \
    PORT=8080 \
    GIT_AUTHOR_NAME=contexo-server \
    GIT_AUTHOR_EMAIL=server@contexo.local \
    GIT_COMMITTER_NAME=contexo-server \
    GIT_COMMITTER_EMAIL=server@contexo.local

RUN mkdir -p /data && chown contexo:contexo /data

VOLUME ["/data"]
EXPOSE 8080
USER contexo

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:8080/health || exit 1

ENTRYPOINT ["/usr/local/bin/contexo-server"]
