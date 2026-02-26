# WhatsApp MCP Server

This is a Model Context Protocol (MCP) server for WhatsApp.

With this you can search and read your personal Whatsapp messages (including images, videos, documents, and audio messages), search your contacts and send messages to either individuals or groups. You can also send media files including images, videos, documents, and audio messages.

It connects to your **personal WhatsApp account** directly via the Whatsapp web multidevice API (using the [whatsmeow](https://github.com/tulir/whatsmeow) library). All your messages are stored locally in a SQLite database and only sent to an LLM (such as Claude) when the agent accesses them through tools (which you control).

Here's an example of what you can do when it's connected to Claude.

![WhatsApp MCP](./example-use.png)

> To get updates on this and other projects I work on [enter your email here](https://docs.google.com/forms/d/1rTF9wMBTN0vPfzWuQa2BjfGKdKIpTbyeKxhPMcEzgyI/preview)

> *Caution:* as with many MCP servers, the WhatsApp MCP is subject to [the lethal trifecta](https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/). This means that project injection could lead to private data exfiltration.

## Installation

### Prerequisites

- Go
- Python 3.11+
- Anthropic Claude Desktop app (or Cursor)
- FFmpeg (_optional_) - Only needed for audio messages. If you want to send audio files as playable WhatsApp voice messages, they must be in `.ogg` Opus format. With FFmpeg installed, the MCP server will automatically convert non-Opus audio files. Without FFmpeg, you can still send raw audio files using the `send_file` tool.

### Steps

1. **Clone this repository**

   ```bash
   git clone https://github.com/lharries/whatsapp-mcp.git
   cd whatsapp-mcp
   ```

2. **Run the WhatsApp bridge**

   Navigate to the whatsapp-bridge directory and run the Go application:

   ```bash
   cd whatsapp-bridge
   export WHATSAPP_BRIDGE_JWT_SECRET=change-me
   export WHATSAPP_BRIDGE_JWT_AUDIENCE=whatsapp-bridge
   export WHATSAPP_BRIDGE_JWT_ISSUER=omicron-api
   go run main.go
   ```

   The first time you run it, you will be prompted to scan a QR code. Scan the QR code with your WhatsApp mobile app to authenticate.

   After approximately 20 days, you will might need to re-authenticate.

3. **Set up the Python MCP server dependencies**

   ```bash
   cd whatsapp-mcp-server
   python3 -m venv .venv
   source .venv/bin/activate
   python -m pip install --upgrade pip
   python -m pip install -r requirements.txt
   ```

   If you are using conda instead of `venv`:

   ```bash
   conda activate whatsapp-mcp
   cd whatsapp-mcp-server
   python -m pip install --upgrade pip
   python -m pip install -r requirements.txt
   ```

4. **Run the MCP server (streamable HTTP)**

   ```bash
   cd whatsapp-mcp-server
   source .venv/bin/activate
   export WHATSAPP_BRIDGE_JWT_SECRET=change-me
   export WHATSAPP_BRIDGE_JWT_AUDIENCE=whatsapp-bridge
   export WHATSAPP_BRIDGE_JWT_ISSUER=omicron-api
   export WHATSAPP_MCP_JWT_AUDIENCE=whatsapp-mcp
   export WHATSAPP_MCP_REQUIRED_SCOPE=whatsapp:mcp
   python main.py --transport streamable-http --host 127.0.0.1 --port 8000 --streamable-http-path /mcp
   ```

   If you are using conda, run the same command after `conda activate whatsapp-mcp`.

   The streamable HTTP endpoint will be:

   ```
   http://127.0.0.1:8000/mcp
   ```

5. **Connect your MCP client**

   - If your MCP client supports streamable HTTP, configure it to use `http://127.0.0.1:8000/mcp`.
   - Include an `Authorization: Bearer <short-lived-internal-jwt>` header. The backend should mint
     this JWT with `WHATSAPP_BRIDGE_JWT_SECRET`, include audience `whatsapp-mcp`, and pass it through
     unchanged to bridge calls.
   - If your MCP client expects stdio (for example some Claude Desktop/Cursor setups), use this process config instead:

   ```json
   {
     "mcpServers": {
       "whatsapp": {
         "command": "{{PATH_TO_PYTHON}}", // Run `which python3` in whatsapp-mcp-server/.venv and place the output here
         "args": [
           "{{PATH_TO_SRC}}/whatsapp-mcp/whatsapp-mcp-server/main.py", // cd into the repo, run `pwd` and enter the output here + "/whatsapp-mcp-server/main.py"
           "--transport",
           "stdio"
         ]
       }
     }
   }
   ```

   For **Claude**, save this as `claude_desktop_config.json` in your Claude Desktop configuration directory at:

   ```
   ~/Library/Application Support/Claude/claude_desktop_config.json
   ```

   For **Cursor**, save this as `mcp.json` in your Cursor configuration directory at:

   ```
   ~/.cursor/mcp.json
   ```

6. **Restart Claude Desktop / Cursor**

   Open Claude Desktop and you should now see WhatsApp as an available integration.

   Or restart Cursor.

### Windows Compatibility

If you're running this project on Windows, be aware that `go-sqlite3` requires **CGO to be enabled** in order to compile and work properly. By default, **CGO is disabled on Windows**, so you need to explicitly enable it and have a C compiler installed.

#### Steps to get it working:

1. **Install a C compiler**  
   We recommend using [MSYS2](https://www.msys2.org/) to install a C compiler for Windows. After installing MSYS2, make sure to add the `ucrt64\bin` folder to your `PATH`.  
   → A step-by-step guide is available [here](https://code.visualstudio.com/docs/cpp/config-mingw).

2. **Enable CGO and run the app**

   ```bash
   cd whatsapp-bridge
   go env -w CGO_ENABLED=1
   go run main.go
   ```

Without this setup, you'll likely run into errors like:

> `Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo to work.`

## Architecture Overview

This application consists of two main components:

1. **Go WhatsApp Bridge** (`whatsapp-bridge/`): A Go application that connects to WhatsApp's web API, handles authentication via QR code, and stores message history in SQLite. It serves as the bridge between WhatsApp and the MCP server.

2. **Python MCP Server** (`whatsapp-mcp-server/`): A Python server implementing the Model Context Protocol (MCP), which provides standardized tools for Claude to interact with WhatsApp data and send/receive messages.

### Data Storage

- All message history is stored in a SQLite database within the `whatsapp-bridge/store/` directory
- The database maintains tables for chats and messages
- Messages are indexed for efficient searching and retrieval

### Standard Identifier Terms

- `sender_id`: Canonical normalized user ID (no JID suffix), for example `919930575574`
- `chat_jid`: Chat identifier, for example `120363024375560616@g.us` or `919930575574`
- `last_sender_id`: Canonical normalized user ID of the sender of the last message in a chat

Note: the SQLite schema keeps legacy column names (`messages.sender`, `chats.jid`) for compatibility, while MCP-facing payloads use the standard terms above.

## Usage

Once connected, you can interact with your WhatsApp contacts through Claude, leveraging Claude's AI capabilities in your WhatsApp conversations.

### MCP Tools

Claude can access the following tools to interact with WhatsApp:

- **search_contacts**: Search for contacts by name or phone number
- **list_messages**: Retrieve messages with sender/chat/date filters and optional context (no text query)
- **list_messages_for_sender_id**: Retrieve messages for one `sender_id` with pagination, optional time window, and optional context (`after_iso`/`before_iso` or `lookback_value`+`lookback_unit`)
- **list_messages_for_chat_id**: Retrieve messages for one `chat_jid` with pagination, optional time window, and optional context (`after_iso`/`before_iso` or `lookback_value`+`lookback_unit`)
- **search_messages**: Search message content with pagination and optional `sender_id`; use either absolute bounds (`after_iso`/`before_iso`) or relative lookback (`lookback_value`+`lookback_unit` where unit is `h|d|w`). If `query` is empty/null, a time window is required.
- **search_chat_messages**: Search message content within one chat using `chat_jid` + query, with the same absolute/relative time-window pattern
- **list_chats**: List available chats with metadata
- **get_chat**: Get information about a specific chat
- **get_direct_chat_by_contact**: Find a direct chat with a specific contact
- **get_contact_chats**: List all chats involving a specific contact
- **get_last_interaction**: Get the most recent message with a contact
- **get_message_context**: Retrieve context around a specific message
- **send_message**: Send a WhatsApp message to a specified phone number or group JID
- **send_file**: Send a file (image, video, raw audio, document) to a specified recipient
- **send_audio_message**: Send an audio file as a WhatsApp voice message (requires the file to be an .ogg opus file or ffmpeg must be installed)
- **download_media**: Download media from a WhatsApp message and get the local file path

### Media Handling Features

The MCP server supports both sending and receiving various media types:

#### Media Sending

You can send various media types to your WhatsApp contacts:

- **Images, Videos, Documents**: Use the `send_file` tool to share any supported media type.
- **Voice Messages**: Use the `send_audio_message` tool to send audio files as playable WhatsApp voice messages.
  - For optimal compatibility, audio files should be in `.ogg` Opus format.
  - With FFmpeg installed, the system will automatically convert other audio formats (MP3, WAV, etc.) to the required format.
  - Without FFmpeg, you can still send raw audio files using the `send_file` tool, but they won't appear as playable voice messages.

#### Media Downloading

By default, just the metadata of the media is stored in the local database. The message will indicate that media was sent. To access this media you need to use the download_media tool which takes the `message_id` and `chat_jid` (which are shown when printing messages containing the meda), this downloads the media and then returns the file path which can be then opened or passed to another tool.

## Technical Details

1. Claude sends requests to the Python MCP server
2. The MCP server queries the Go bridge for WhatsApp data or directly to the SQLite database
3. The Go accesses the WhatsApp API and keeps the SQLite database up to date
4. Data flows back through the chain to Claude
5. When sending messages, the request flows from Claude through the MCP server to the Go bridge and to WhatsApp

## Troubleshooting

- If you use streamable HTTP, ensure the server is running and your client points to the correct URL (default `http://127.0.0.1:8000/mcp`).
- If the MCP server fails to start, make sure the configured Python path points to `whatsapp-mcp-server/.venv/bin/python3` (or your platform equivalent), and that dependencies were installed from `requirements.txt`.
- Make sure both the Go application and the Python server are running for the integration to work properly.

### Authentication Issues

- **QR Code Not Displaying**: If the QR code doesn't appear, try restarting the authentication script. If issues persist, check if your terminal supports displaying QR codes.
- **WhatsApp Already Logged In**: If your session is already active, the Go bridge will automatically reconnect without showing a QR code.
- **Device Limit Reached**: WhatsApp limits the number of linked devices. If you reach this limit, you'll need to remove an existing device from WhatsApp on your phone (Settings > Linked Devices).
- **No Messages Loading**: After initial authentication, it can take several minutes for your message history to load, especially if you have many chats.
- **WhatsApp Out of Sync**: If your WhatsApp messages get out of sync with the bridge, delete both database files (`whatsapp-bridge/store/messages.db` and `whatsapp-bridge/store/whatsapp.db`) and restart the bridge to re-authenticate.

For additional Claude Desktop integration troubleshooting, see the [MCP documentation](https://modelcontextprotocol.io/quickstart/server#claude-for-desktop-integration-issues). The documentation includes helpful tips for checking logs and resolving common issues.
