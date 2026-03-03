# syntax=docker/dockerfile:1.7

ARG TARGETPLATFORM=linux/arm64
FROM --platform=$TARGETPLATFORM python:3.13-slim

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV PIP_NO_CACHE_DIR=1

WORKDIR /app/whatsapp-mcp-server

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt ./
RUN python -m pip install --upgrade pip \
    && python -m pip install -r requirements.txt

COPY . ./

RUN useradd --uid 10001 --create-home appuser \
    && mkdir -p /app/whatsapp-bridge/store \
    && chown -R appuser:appuser /app

ENV WHATSAPP_MCP_TRANSPORT=streamable-http
ENV WHATSAPP_MCP_HOST=0.0.0.0
ENV WHATSAPP_MCP_PORT=8000
ENV WHATSAPP_MCP_STREAMABLE_HTTP_PATH=/mcp
ENV WHATSAPP_BRIDGE_API_BASE_URL=http://127.0.0.1:8080

VOLUME ["/app/whatsapp-bridge/store"]

EXPOSE 8000

USER appuser

CMD ["python", "main.py", "--transport", "streamable-http", "--host", "0.0.0.0", "--port", "8000", "--streamable-http-path", "/mcp"]
