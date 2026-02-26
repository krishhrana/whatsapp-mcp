import argparse
import os
from pathlib import Path

from dotenv import load_dotenv


def load_environment() -> None:
    load_dotenv(dotenv_path=Path(__file__).resolve().parent / ".env", override=False)


def _default_port() -> int:
    port_value = os.getenv("WHATSAPP_MCP_PORT", "8000")
    try:
        return int(port_value)
    except ValueError as exc:
        raise ValueError("WHATSAPP_MCP_PORT must be an integer") from exc


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Run the WhatsApp MCP server")
    parser.add_argument(
        "--transport",
        choices=["stdio", "sse", "streamable-http"],
        default=os.getenv("WHATSAPP_MCP_TRANSPORT", "streamable-http"),
        help="MCP transport to use",
    )
    parser.add_argument(
        "--host",
        default=os.getenv("WHATSAPP_MCP_HOST", "127.0.0.1"),
        help="Host to bind for HTTP transports",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=_default_port(),
        help="Port to bind for HTTP transports",
    )
    parser.add_argument(
        "--streamable-http-path",
        default=os.getenv("WHATSAPP_MCP_STREAMABLE_HTTP_PATH", "/mcp"),
        help="Route path for streamable HTTP transport",
    )
    parser.add_argument(
        "--mount-path",
        default=os.getenv("WHATSAPP_MCP_MOUNT_PATH", "/"),
        help="Mount path for SSE transport",
    )
    return parser.parse_args()
