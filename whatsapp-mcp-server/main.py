from mcp_config import load_environment, parse_args
from mcp_server import create_mcp_server
from mcp_tools import register_tools

load_environment()

if __name__ == "__main__":
    args = parse_args()

    # Initialize FastMCP after parsing runtime host so transport security
    # settings are derived from the actual bind host (not localhost default).
    mcp = create_mcp_server(host=args.host)
    register_tools(mcp)

    # FastMCP reads host/port/path from settings for HTTP transports.
    mcp.settings.host = args.host
    mcp.settings.port = args.port
    mcp.settings.streamable_http_path = args.streamable_http_path

    mount_path = args.mount_path if args.transport == "sse" else None
    mcp.run(transport=args.transport, mount_path=mount_path)
