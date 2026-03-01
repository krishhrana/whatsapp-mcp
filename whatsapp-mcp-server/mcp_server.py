from datetime import datetime, timezone

from mcp.server.fastmcp import FastMCP
from starlette.requests import Request
from starlette.responses import JSONResponse

from mcp_auth import build_auth_settings, build_token_verifier


def create_mcp_server() -> FastMCP:
    server = FastMCP(
        "whatsapp",
        auth=build_auth_settings(),
        token_verifier=build_token_verifier(),
    )

    custom_route = getattr(server, "custom_route", None)
    if not callable(custom_route):
        raise RuntimeError(
            "FastMCP custom_route API is required to expose /health. "
            "Upgrade the mcp package in whatsapp-mcp-server."
        )

    @custom_route("/health", methods=["GET"])
    async def health_handler(_: Request) -> JSONResponse:
        return JSONResponse(
            {
                "status": "ok",
                "updated_at": datetime.now(timezone.utc).isoformat(),
            }
        )

    return server
