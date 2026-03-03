# syntax=docker/dockerfile:1.7

ARG TARGETPLATFORM=linux/arm64
FROM --platform=$TARGETPLATFORM golang:1.25-bookworm AS builder

ARG TARGETOS=linux
ARG TARGETARCH=arm64

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/whatsapp-bridge ./cmd/whatsapp-bridge


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
