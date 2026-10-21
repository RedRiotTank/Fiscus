# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
        -o /out/fiscus ./cmd/fiscus

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 fiscus \
    && adduser -S -D -H -u 10001 -G fiscus -s /sbin/nologin fiscus \
    && mkdir -p /data \
    && chown fiscus:fiscus /data

COPY --from=build /out/fiscus /usr/local/bin/fiscus

USER fiscus

ENV FISCUS_SERVER_ADDRESS=:8080 \
    FISCUS_DATABASE_PATH=/data/fiscus.db

EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O - http://127.0.0.1:8080/healthz >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/fiscus"]
CMD ["serve"]
