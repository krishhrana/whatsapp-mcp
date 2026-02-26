from mcp_config import load_environment, parse_args
from mcp_server import create_mcp_server
from mcp_tools import register_tools

load_environment()

# Initialize FastMCP server with mandatory JWT auth for streamable HTTP.
mcp = create_mcp_server()
register_tools(mcp)


if __name__ == "__main__":
    args = parse_args()

    # FastMCP reads host/port/path from settings for HTTP transports.
    mcp.settings.host = args.host
    mcp.settings.port = args.port
    mcp.settings.streamable_http_path = args.streamable_http_path

    mount_path = args.mount_path if args.transport == "sse" else None
    mcp.run(transport=args.transport, mount_path=mount_path)
