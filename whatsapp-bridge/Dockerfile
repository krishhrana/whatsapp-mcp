# syntax=docker/dockerfile:1.7

FROM golang:1.23-bookworm AS builder

WORKDIR /src

RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ENV CGO_ENABLED=1 GOOS=linux GOARCH=amd64
RUN go build -trimpath -ldflags="-s -w" -o /out/whatsapp-bridge ./cmd/whatsapp-bridge


FROM debian:bookworm-slim AS runtime

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app/whatsapp-bridge

RUN useradd --uid 10001 --create-home appuser \
    && mkdir -p /app/whatsapp-bridge/store \
    && chown -R appuser:appuser /app/whatsapp-bridge

COPY --from=builder /out/whatsapp-bridge /usr/local/bin/whatsapp-bridge

ENV WHATSAPP_BRIDGE_HOST=0.0.0.0
ENV WHATSAPP_BRIDGE_PORT=8080

VOLUME ["/app/whatsapp-bridge/store"]

EXPOSE 8080

USER appuser

ENTRYPOINT ["/usr/local/bin/whatsapp-bridge"]
