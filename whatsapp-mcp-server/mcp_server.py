from mcp.server.fastmcp import FastMCP

from mcp_auth import build_auth_settings, build_token_verifier


def create_mcp_server() -> FastMCP:
    return FastMCP(
        "whatsapp",
        auth=build_auth_settings(),
        token_verifier=build_token_verifier(),
    )
