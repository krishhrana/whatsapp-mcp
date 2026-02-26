from typing import Any

from mcp.server.fastmcp import FastMCP

from mcp_auth import bridge_auth_headers_from_request_context
from mcp_serialization import serialize_for_mcp
from whatsapp import (
    download_media as whatsapp_download_media,
    get_chat as whatsapp_get_chat,
    get_contact_chats as whatsapp_get_contact_chats,
    get_direct_chat_by_contact as whatsapp_get_direct_chat_by_contact,
    get_last_interaction as whatsapp_get_last_interaction,
    get_message_context as whatsapp_get_message_context,
    list_chats as whatsapp_list_chats,
    list_messages_for_chat_id as whatsapp_list_messages_for_chat_id,
    list_messages_for_sender_id as whatsapp_list_messages_for_sender_id,
    search_chat_messages as whatsapp_search_chat_messages,
    search_contacts as whatsapp_search_contacts,
    search_messages as whatsapp_search_messages,
    send_audio_message as whatsapp_audio_voice_message,
    send_file as whatsapp_send_file,
    send_message as whatsapp_send_message,
)


def register_tools(mcp: FastMCP) -> None:
    @mcp.tool()
    def search_contacts(query: str) -> list[dict[str, Any]]:
        """Search WhatsApp contacts by name or phone number.

        Args:
            query: Search term to match against contact names or phone numbers

        Returns:
            list[dict] where each contact includes:
            - sender_id (str): Canonical normalized user ID
            - name (str | None): Contact display name
            - chat_jid (str): Chat ID key in DB (canonical user ID for direct chats)

        """
        contacts = whatsapp_search_contacts(query)
        return serialize_for_mcp(contacts)
    
    
    @mcp.tool()
    def search_messages(
        query: str | None = None,
        sender_id: str | None = None,
        page: int = 0,
        limit: int = 20,
        after_iso: str | None = None,
        before_iso: str | None = None,
        lookback_value: int | None = None,
        lookback_unit: str | None = None,
    ) -> list[dict[str, Any]]:
        """
        - Search WhatsApp messages by content query globally.

        Args:
            query: Optional text query to match message content
            sender_id: Optional canonical normalized user ID to filter messages by sender
            page: Page number for pagination (0-based)
            limit: Maximum number of messages to return (default 20)
            after_iso: Optional lower ISO-8601 timestamp bound (exclusive)
            before_iso: Optional upper ISO-8601 timestamp bound (exclusive)
            lookback_value: Optional relative lookback amount
            lookback_unit: Optional lookback unit, one of h, d, w

        Rules:
            - Use either absolute bounds (after_iso/before_iso) or relative bounds (lookback_value + lookback_unit), not both.
            - If query is empty or null, a time window is required.
            - With empty/null query + timestamp bound(s), returns all messages within the time window.

        Returns:
            list[dict] of messages metadata with fields:
            timestamp (str), sender_id (str), content (str), is_from_me (bool),
            chat_jid (str), id (str), chat_name (str | None), media_type (str | None)
        """
        messages = whatsapp_search_messages(
            query=query,
            sender_id=sender_id,
            page=page,
            limit=limit,
            after_iso=after_iso,
            before_iso=before_iso,
            lookback_value=lookback_value,
            lookback_unit=lookback_unit,
        )
        return serialize_for_mcp(messages)

    @mcp.tool()
    def search_chat_messages(
        chat_jid: str,
        query: str,
        page: int = 0,
        limit: int = 20,
        after_iso: str | None = None,
        before_iso: str | None = None,
        lookback_value: int | None = None,
        lookback_unit: str | None = None,
    ) -> list[dict[str, Any]]:
        """
        - Search WhatsApp messages by content query within a specific chat.

        Args:
            chat_jid: Chat JID to scope search to
            query: Non-empty text query to match message content
            page: Page number for pagination (0-based)
            limit: Maximum number of messages to return (default 20)
            after_iso: Optional lower ISO-8601 timestamp bound (exclusive)
            before_iso: Optional upper ISO-8601 timestamp bound (exclusive)
            lookback_value: Optional relative lookback amount
            lookback_unit: Optional lookback unit, one of h, d, w

        Rules:
            - Use either absolute bounds (after_iso/before_iso) or relative bounds (lookback_value + lookback_unit), not both.

        Returns:
            list[dict] of messages metadata with fields:
            timestamp (str), sender_id (str), content (str), is_from_me (bool),
            chat_jid (str), id (str), chat_name (str | None), media_type (str | None)
        """
        messages = whatsapp_search_chat_messages(
            chat_jid=chat_jid,
            query=query,
            page=page,
            limit=limit,
            after_iso=after_iso,
            before_iso=before_iso,
            lookback_value=lookback_value,
            lookback_unit=lookback_unit,
        )
        return serialize_for_mcp(messages)

    
    @mcp.tool()
    def list_messages_for_sender_id(
        sender_id: str,
        after_iso: str | None = None,
        before_iso: str | None = None,
        lookback_value: int | None = None,
        lookback_unit: str | None = None,
        limit: int = 20,
        page: int = 0,
        include_context: bool = True,
        context_before: int = 1,
        context_after: int = 1,
    ) -> list[dict[str, Any]]:
        """
        - Get messages for one sender_id with optional date filters and context.

        Args:
            sender_id: Canonical normalized user ID to filter messages by sender
            after_iso: Optional lower ISO-8601 timestamp bound (exclusive)
            before_iso: Optional upper ISO-8601 timestamp bound (exclusive)
            lookback_value: Optional relative lookback amount
            lookback_unit: Optional lookback unit, one of h, d, w
            limit: Maximum number of messages to return (default 20)
            page: Page number for pagination (default 0)
            include_context: Whether to include messages before and after each result (default True)
            context_before: Number of context messages before each result (default 1)
            context_after: Number of context messages after each result (default 1)

        Returns:
            When include_context=False:
            - list[dict] of messages metadata with fields:
              timestamp (str), sender_id (str), content (str), is_from_me (bool),
              chat_jid (str), id (str), chat_name (str | None), media_type (str | None)
            When include_context=True:
            - list[dict] of message contexts with fields:
              message (dict), before (list[dict]), after (list[dict])

        Rules:
            - Use either absolute bounds (after_iso/before_iso) or relative bounds (lookback_value + lookback_unit), not both.
        """
        messages = whatsapp_list_messages_for_sender_id(
            sender_id=sender_id,
            after_iso=after_iso,
            before_iso=before_iso,
            lookback_value=lookback_value,
            lookback_unit=lookback_unit,
            limit=limit,
            page=page,
            include_context=include_context,
            context_before=context_before,
            context_after=context_after,
        )
        return serialize_for_mcp(messages)

    @mcp.tool()
    def list_messages_for_chat_id(
        chat_jid: str,
        after_iso: str | None = None,
        before_iso: str | None = None,
        lookback_value: int | None = None,
        lookback_unit: str | None = None,
        limit: int = 20,
        page: int = 0,
        include_context: bool = True,
        context_before: int = 1,
        context_after: int = 1,
    ) -> list[dict[str, Any]]:
        """
        - Get messages for one chat_jid with optional date filters and context.

        Args:
            chat_jid: Chat JID to filter messages by chat
            after_iso: Optional lower ISO-8601 timestamp bound (exclusive)
            before_iso: Optional upper ISO-8601 timestamp bound (exclusive)
            lookback_value: Optional relative lookback amount
            lookback_unit: Optional lookback unit, one of h, d, w
            limit: Maximum number of messages to return (default 20)
            page: Page number for pagination (default 0)
            include_context: Whether to include messages before and after each result (default True)
            context_before: Number of context messages before each result (default 1)
            context_after: Number of context messages after each result (default 1)

        Returns:
            When include_context=False:
            - list[dict] of messages metadata with fields:
              timestamp (str), sender_id (str), content (str), is_from_me (bool),
              chat_jid (str), id (str), chat_name (str | None), media_type (str | None)
            When include_context=True:
            - list[dict] of message contexts with fields:
              message (dict), before (list[dict]), after (list[dict])

        Rules:
            - Use either absolute bounds (after_iso/before_iso) or relative bounds (lookback_value + lookback_unit), not both.
        """
        messages = whatsapp_list_messages_for_chat_id(
            chat_jid=chat_jid,
            after_iso=after_iso,
            before_iso=before_iso,
            lookback_value=lookback_value,
            lookback_unit=lookback_unit,
            limit=limit,
            page=page,
            include_context=include_context,
            context_before=context_before,
            context_after=context_after,
        )
        return serialize_for_mcp(messages)


    @mcp.tool()
    def get_chat_metadata_by_id(chat_jid: str, include_last_message: bool = True) -> dict[str, Any] | None:
        """
        - Get WhatsApp chat metadata by a specific chat JID.

        Args:
            chat_jid: The JID of the chat to retrieve
            include_last_message: Whether to include the last message (default True)

        Returns:
            dict | None:
            - When found, returns chat object with fields:
              chat_jid, name, last_message_time, last_message, last_sender_id, last_is_from_me
            - When not found, returns None
        """
        chat = whatsapp_get_chat(chat_jid, include_last_message)
        return serialize_for_mcp(chat)

    @mcp.tool()
    def get_chat_metadata_by_contact_id(sender_id: str) -> dict[str, Any] | None:
        """Get WhatsApp chat metadata by canonical normalized user ID.

        Args:
            sender_id: Canonical normalized user ID (no JID suffix)

        Returns:
            dict | None:
            - Direct chat object for the contact with fields:
              chat_jid, name, last_message_time, last_message, last_sender_id, last_is_from_me
            - None if no matching direct chat is found
        """
        chat = whatsapp_get_direct_chat_by_contact(sender_id)
        return serialize_for_mcp(chat)

    @mcp.tool()
    def get_all_chats_for_contact(sender_id: str, limit: int = 20, page: int = 0) -> list[dict[str, Any]]:
        """Get all WhatsApp chats metadata involving the contact.

        Args:
            sender_id: Canonical normalized user ID (no JID suffix)
            limit: Maximum number of chats to return (default 20)
            page: Page number for pagination (default 0)

        Returns:
            list[dict] of chats involving the contact. Each chat has:
            chat_jid, name, last_message_time, last_message, last_sender_id, last_is_from_me
        """
        chats = whatsapp_get_contact_chats(sender_id, limit, page)
        return serialize_for_mcp(chats)

    @mcp.tool()
    def get_last_interaction_for_contact(sender_id: str) -> str | None:
        """Get most recent WhatsApp message involving the contact.

        Args:
            sender_id: Canonical normalized user ID (no JID suffix)

        Returns:
            str | None:
            - Formatted message string for the most recent interaction
            - None if no interaction is found
        """
        message = whatsapp_get_last_interaction(sender_id)
        return message

    @mcp.tool()
    def get_message_context(
        message_id: str,
        before: int = 5,
        after: int = 5,
    ) -> dict[str, Any]:
        """Get context around a specific WhatsApp message.

        Args:
            message_id: The ID of the message to get context for
            before: Number of messages to include before the target message (default 5)
            after: Number of messages to include after the target message (default 5)

        Returns:
            dict with fields:
            - message (dict): Target message
            - before (list[dict]): Messages before target
            - after (list[dict]): Messages after target
            Each message dict has:
            timestamp, sender_id, content, is_from_me, chat_jid, id, chat_name, media_type
        """
        context = whatsapp_get_message_context(message_id, before, after)
        return serialize_for_mcp(context)

    @mcp.tool()
    def send_message(
        recipient: str,
        message: str,
    ) -> dict[str, Any]:
        """Send a WhatsApp message to a person or group. For group chats use the JID.

        Args:
            recipient: The recipient - either a phone number with country code but no + or other symbols,
                     or a JID (e.g., "123456789@s.whatsapp.net" or a group JID like "123456789@g.us")
            message: The message text to send

        Returns:
            dict with fields:
            - success (bool): Whether message send succeeded
            - message (str): Status/result message
        """
        if not recipient:
            return {
                "success": False,
                "message": "Recipient must be provided",
            }

        try:
            bridge_auth_headers = bridge_auth_headers_from_request_context()
        except RuntimeError as exc:
            return {"success": False, "message": str(exc)}

        success, status_message = whatsapp_send_message(
            recipient,
            message,
            auth_headers=bridge_auth_headers,
        )
        return {
            "success": success,
            "message": status_message,
        }

    @mcp.tool()
    def send_file(recipient: str, media_path: str) -> dict[str, Any]:
        """Send a file such as a picture, raw audio, video or document via WhatsApp to the specified recipient. For group messages use the JID.

        Args:
            recipient: The recipient - either a phone number with country code but no + or other symbols,
                     or a JID (e.g., "123456789@s.whatsapp.net" or a group JID like "123456789@g.us")
            media_path: The absolute path to the media file to send (image, video, document)

        Returns:
            dict with fields:
            - success (bool): Whether file send succeeded
            - message (str): Status/result message
        """
        try:
            bridge_auth_headers = bridge_auth_headers_from_request_context()
        except RuntimeError as exc:
            return {"success": False, "message": str(exc)}

        success, status_message = whatsapp_send_file(
            recipient,
            media_path,
            auth_headers=bridge_auth_headers,
        )
        return {
            "success": success,
            "message": status_message,
        }

    @mcp.tool()
    def send_audio_message(recipient: str, media_path: str) -> dict[str, Any]:
        """Send any audio file as a WhatsApp audio message to the specified recipient. For group messages use the JID. If it errors due to ffmpeg not being installed, use send_file instead.

        Args:
            recipient: The recipient - either a phone number with country code but no + or other symbols,
                     or a JID (e.g., "123456789@s.whatsapp.net" or a group JID like "123456789@g.us")
            media_path: The absolute path to the audio file to send (will be converted to Opus .ogg if it's not a .ogg file)

        Returns:
            dict with fields:
            - success (bool): Whether audio send succeeded
            - message (str): Status/result message
        """
        try:
            bridge_auth_headers = bridge_auth_headers_from_request_context()
        except RuntimeError as exc:
            return {"success": False, "message": str(exc)}

        success, status_message = whatsapp_audio_voice_message(
            recipient,
            media_path,
            auth_headers=bridge_auth_headers,
        )
        return {
            "success": success,
            "message": status_message,
        }

    @mcp.tool()
    def download_media(message_id: str, chat_jid: str) -> dict[str, Any]:
        """Download media from a WhatsApp message and get the local file path.

        Args:
            message_id: The ID of the message containing the media
            chat_jid: The JID of the chat containing the message

        Returns:
            dict with fields:
            - success (bool): Whether download succeeded
            - message (str): Status/result message
            - file_path (str, optional): Local absolute path when success=True
        """
        try:
            bridge_auth_headers = bridge_auth_headers_from_request_context()
        except RuntimeError as exc:
            return {"success": False, "message": str(exc)}

        file_path = whatsapp_download_media(
            message_id,
            chat_jid,
            auth_headers=bridge_auth_headers,
        )

        if file_path:
            return {
                "success": True,
                "message": "Media downloaded successfully",
                "file_path": file_path,
            }
        return {
            "success": False,
            "message": "Failed to download media",
        }
