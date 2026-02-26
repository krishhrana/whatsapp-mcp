import sqlite3
from datetime import datetime, timedelta
from dataclasses import dataclass
from pathlib import Path
from typing import Optional, Tuple
import os
import requests
import json
import audio
from dotenv import load_dotenv

load_dotenv(dotenv_path=Path(__file__).resolve().parent / ".env", override=False)

MESSAGES_DB_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', 'whatsapp-bridge', 'store', 'messages.db')
WHATSAPP_API_BASE_URL = os.getenv("WHATSAPP_BRIDGE_API_BASE_URL", "http://127.0.0.1:8080")


def _validated_bridge_auth_headers(auth_headers: dict[str, str] | None) -> dict[str, str]:
    if not auth_headers:
        raise RuntimeError("Missing Authorization header for bridge request.")
    auth_value = str(auth_headers.get("Authorization") or "").strip()
    if not auth_value.lower().startswith("bearer "):
        raise RuntimeError("Bridge Authorization header must be a Bearer token.")
    return {"Authorization": auth_value}


def _get_db_connection() -> sqlite3.Connection:
    return sqlite3.connect(MESSAGES_DB_PATH)

@dataclass
class Message:
    timestamp: datetime
    sender_id: str
    content: str
    is_from_me: bool
    chat_jid: str
    id: str
    chat_name: Optional[str] = None
    media_type: Optional[str] = None

@dataclass
class Chat:
    chat_jid: str
    name: Optional[str]
    last_message_time: Optional[datetime]
    last_message: Optional[str] = None
    last_sender_id: Optional[str] = None
    last_is_from_me: Optional[bool] = None

    @property
    def is_group(self) -> bool:
        """Determine if chat is a group based on JID pattern."""
        return self.chat_jid.endswith("@g.us")

@dataclass
class Contact:
    sender_id: str
    name: Optional[str]
    chat_jid: str

@dataclass
class MessageContext:
    message: Message
    before: list[Message]
    after: list[Message]


def _parse_iso_timestamp(value: str, field_name: str) -> str:
    """Parse strict ISO timestamp input into ISO-8601 with timezone."""
    raw = value.strip()
    if not raw:
        raise ValueError(f"Invalid empty timestamp for '{field_name}'")

    now_tz = datetime.now().astimezone().tzinfo
    try:
        candidate = raw[:-1] + "+00:00" if raw.endswith("Z") else raw
        parsed = datetime.fromisoformat(candidate)
        if parsed.tzinfo is None:
            parsed = parsed.replace(tzinfo=now_tz)
        return parsed.isoformat()
    except ValueError as exc:
        raise ValueError(
            f"Invalid ISO timestamp for '{field_name}': {value}. "
            "Use ISO-8601, for example 2026-02-20T12:00:00-08:00."
        ) from exc


def _resolve_time_window(
    *,
    after_iso: Optional[str] = None,
    before_iso: Optional[str] = None,
    lookback_value: Optional[int] = None,
    lookback_unit: Optional[str] = None,
) -> tuple[Optional[str], Optional[str]]:
    """Resolve absolute/relative time window into (after, before) ISO-8601 timestamps.

    Rules:
    - Absolute mode: use after_iso and/or before_iso
    - Relative mode: use both lookback_value and lookback_unit (h|d|w)
    - Absolute and relative modes are mutually exclusive
    """
    has_absolute = bool(after_iso or before_iso)
    has_relative = lookback_value is not None or lookback_unit is not None

    if has_absolute and has_relative:
        raise ValueError(
            "Use either absolute bounds (after_iso/before_iso) or relative bounds "
            "(lookback_value + lookback_unit), not both."
        )

    if has_relative:
        if lookback_value is None or lookback_unit is None:
            raise ValueError(
                "Relative bounds require both lookback_value and lookback_unit."
            )
        if lookback_value <= 0:
            raise ValueError("lookback_value must be greater than 0.")

        unit = lookback_unit.strip().lower()
        if unit == "h":
            delta = timedelta(hours=lookback_value)
        elif unit == "d":
            delta = timedelta(days=lookback_value)
        elif unit == "w":
            delta = timedelta(weeks=lookback_value)
        else:
            raise ValueError("lookback_unit must be one of: h, d, w.")

        now = datetime.now().astimezone()
        return (now - delta).isoformat(), now.isoformat()

    resolved_after = _parse_iso_timestamp(after_iso, "after_iso") if after_iso else None
    resolved_before = _parse_iso_timestamp(before_iso, "before_iso") if before_iso else None

    if resolved_after and resolved_before:
        after_dt = datetime.fromisoformat(resolved_after)
        before_dt = datetime.fromisoformat(resolved_before)
        if after_dt >= before_dt:
            raise ValueError("after_iso must be earlier than before_iso.")

    return resolved_after, resolved_before

def _canonical_sender_id(value: str) -> str:
    sender_id = value.strip()
    if not sender_id:
        return ""
    if "@" in sender_id:
        raise ValueError("sender_id must be the canonical normalized user ID without JID suffix")
    return sender_id

def _direct_chat_jid(sender_id: str) -> str:
    return f"{_canonical_sender_id(sender_id)}@s.whatsapp.net"

def _direct_chat_jids_for_candidates(sender_ids: list[str]) -> list[str]:
    chat_jids: list[str] = []
    seen: set[str] = set()
    for sender_id in sender_ids:
        canonical_id = _canonical_sender_id(sender_id)
        for chat_jid in (canonical_id, _direct_chat_jid(canonical_id)):
            if chat_jid and chat_jid not in seen:
                seen.add(chat_jid)
                chat_jids.append(chat_jid)
    return chat_jids

def _sender_id_candidates(conn: sqlite3.Connection, sender_id: str) -> tuple[list[str], str]:
    canonical = _canonical_sender_id(sender_id)
    cursor = conn.cursor()

    try:
        cursor.execute(
            "SELECT canonical_id FROM sender_id_aliases WHERE alias_id = ? LIMIT 1",
            (canonical,),
        )
        row = cursor.fetchone()
        resolved_canonical = row[0] if row and row[0] else canonical

        cursor.execute(
            "SELECT alias_id FROM sender_id_aliases WHERE canonical_id = ?",
            (resolved_canonical,),
        )
        aliases = {resolved_canonical}
        for alias_row in cursor.fetchall():
            if alias_row and alias_row[0]:
                aliases.add(alias_row[0])
        return sorted(aliases), resolved_canonical
    except sqlite3.OperationalError:
        # Alias table may not be initialized yet; keep canonical-only fallback.
        return [canonical], canonical

def get_sender_name(sender_id: str) -> str:
    try:
        sender_id = _canonical_sender_id(sender_id)
        if not sender_id:
            return sender_id

        conn = _get_db_connection()
        cursor = conn.cursor()
        sender_candidates, resolved_canonical = _sender_id_candidates(conn, sender_id)
        ordered_candidates = [resolved_canonical] + [
            candidate for candidate in sender_candidates if candidate != resolved_canonical
        ]

        for candidate in ordered_candidates:
            for chat_jid in _direct_chat_jids_for_candidates([candidate]):
                cursor.execute("""
                    SELECT name
                    FROM chats
                    WHERE jid = ?
                    LIMIT 1
                """, (chat_jid,))
                result = cursor.fetchone()
                if result and result[0]:
                    return result[0]

        return resolved_canonical

    except sqlite3.Error as e:
        print(f"Database error while getting sender name: {e}")
        return sender_id
    finally:
        if 'conn' in locals():
            conn.close()

def format_message(message: Message, show_chat_info: bool = True) -> str:
    """Format a single message as text."""
    output = ""
    
    if show_chat_info and message.chat_name:
        output += f"[{message.timestamp:%Y-%m-%d %H:%M:%S}] Chat: {message.chat_name} "
    else:
        output += f"[{message.timestamp:%Y-%m-%d %H:%M:%S}] "
        
    content_prefix = ""
    if hasattr(message, 'media_type') and message.media_type:
        content_prefix = f"[{message.media_type} - Message ID: {message.id} - Chat JID: {message.chat_jid}] "
    
    try:
        sender_name = get_sender_name(message.sender_id) if not message.is_from_me else "Me"
        output += f"From: {sender_name}: {content_prefix}{message.content}\n"
    except Exception as e:
        print(f"Error formatting message: {e}")
    return output

def format_messages_list(messages: list[Message], show_chat_info: bool = True) -> str:
    """Format a list of messages as text."""
    output = ""
    if not messages:
        output += "No messages to display."
        return output
    
    for message in messages:
        output += format_message(message, show_chat_info)
    return output

def _query_messages(
    after: Optional[str] = None,
    before: Optional[str] = None,
    sender_id: Optional[str] = None,
    chat_jid: Optional[str] = None,
    query: Optional[str] = None,
    limit: int = 20,
    page: int = 0,
    include_context: bool = True,
    context_before: int = 1,
    context_after: int = 1
) -> list[Message] | list[MessageContext]:
    """Internal query helper for message retrieval and full-text search.

    Returns:
        - include_context=False: List[Message]
        - include_context=True: List[MessageContext]
    """
    try:
        conn = _get_db_connection()
        cursor = conn.cursor()
        
        # Build base query
        query_parts = ["SELECT messages.timestamp, messages.sender, chats.name, messages.content, messages.is_from_me, chats.jid, messages.id, messages.media_type FROM messages"]
        query_parts.append("JOIN chats ON messages.chat_jid = chats.jid")
        where_clauses = []
        params = []
        
        # Add filters
        if after:
            after = _parse_iso_timestamp(after, "after")
            where_clauses.append("messages.timestamp > ?")
            params.append(after)

        if before:
            before = _parse_iso_timestamp(before, "before")
            where_clauses.append("messages.timestamp < ?")
            params.append(before)

        if sender_id:
            sender_candidates, _ = _sender_id_candidates(conn, sender_id)
            placeholders = ", ".join(["?"] * len(sender_candidates))
            where_clauses.append(f"messages.sender IN ({placeholders})")
            params.extend(sender_candidates)
            
        if chat_jid:
            where_clauses.append("messages.chat_jid = ?")
            params.append(chat_jid)
            
        if query:
            where_clauses.append("LOWER(messages.content) LIKE LOWER(?)")
            params.append(f"%{query}%")
            
        if where_clauses:
            query_parts.append("WHERE " + " AND ".join(where_clauses))
            
        # Add pagination
        offset = page * limit
        query_parts.append("ORDER BY messages.timestamp DESC")
        query_parts.append("LIMIT ? OFFSET ?")
        params.extend([limit, offset])
        
        cursor.execute(" ".join(query_parts), tuple(params))
        messages = cursor.fetchall()
        
        result = []
        for msg in messages:
            message = Message(
                timestamp=datetime.fromisoformat(msg[0]),
                sender_id=msg[1],
                chat_name=msg[2],
                content=msg[3],
                is_from_me=msg[4],
                chat_jid=msg[5],
                id=msg[6],
                media_type=msg[7]
            )
            result.append(message)
            
        if include_context and result:
            messages_with_context: list[MessageContext] = []
            for msg in result:
                messages_with_context.append(
                    get_message_context(msg.id, context_before, context_after)
                )
            return messages_with_context

        return result
        
    except sqlite3.Error as e:
        print(f"Database error: {e}")
        return []
    finally:
        if 'conn' in locals():
            conn.close()


def list_messages(
    after: Optional[str] = None,
    before: Optional[str] = None,
    sender_id: Optional[str] = None,
    chat_jid: Optional[str] = None,
    limit: int = 20,
    page: int = 0,
    include_context: bool = True,
    context_before: int = 1,
    context_after: int = 1
) -> list[Message] | list[MessageContext]:
    """Get messages with optional sender/chat/date filters and context.

    Note:
        Full-text content search is intentionally excluded from this API.
        Use search_messages() for query-based search.
    """
    return _query_messages(
        after=after,
        before=before,
        sender_id=sender_id,
        chat_jid=chat_jid,
        query=None,
        limit=limit,
        page=page,
        include_context=include_context,
        context_before=context_before,
        context_after=context_after,
    )


def list_messages_for_sender_id(
    sender_id: str,
    after_iso: Optional[str] = None,
    before_iso: Optional[str] = None,
    lookback_value: Optional[int] = None,
    lookback_unit: Optional[str] = None,
    limit: int = 20,
    page: int = 0,
    include_context: bool = True,
    context_before: int = 1,
    context_after: int = 1,
) -> list[Message] | list[MessageContext]:
    """Get messages for a specific sender_id with optional time window and context."""
    normalized_sender_id = _canonical_sender_id(sender_id)
    if not normalized_sender_id:
        raise ValueError("sender_id must be a non-empty string")
    resolved_after, resolved_before = _resolve_time_window(
        after_iso=after_iso,
        before_iso=before_iso,
        lookback_value=lookback_value,
        lookback_unit=lookback_unit,
    )

    return list_messages(
        after=resolved_after,
        before=resolved_before,
        sender_id=normalized_sender_id,
        chat_jid=None,
        limit=limit,
        page=page,
        include_context=include_context,
        context_before=context_before,
        context_after=context_after,
    )


def list_messages_for_chat_id(
    chat_jid: str,
    after_iso: Optional[str] = None,
    before_iso: Optional[str] = None,
    lookback_value: Optional[int] = None,
    lookback_unit: Optional[str] = None,
    limit: int = 20,
    page: int = 0,
    include_context: bool = True,
    context_before: int = 1,
    context_after: int = 1,
) -> list[Message] | list[MessageContext]:
    """Get messages for a specific chat_jid with optional time window and context."""
    normalized_chat_jid = chat_jid.strip()
    if not normalized_chat_jid:
        raise ValueError("chat_jid must be a non-empty string")
    resolved_after, resolved_before = _resolve_time_window(
        after_iso=after_iso,
        before_iso=before_iso,
        lookback_value=lookback_value,
        lookback_unit=lookback_unit,
    )

    return list_messages(
        after=resolved_after,
        before=resolved_before,
        sender_id=None,
        chat_jid=normalized_chat_jid,
        limit=limit,
        page=page,
        include_context=include_context,
        context_before=context_before,
        context_after=context_after,
    )


def search_messages(
    query: Optional[str] = None,
    sender_id: Optional[str] = None,
    page: int = 0,
    limit: int = 20,
    after_iso: Optional[str] = None,
    before_iso: Optional[str] = None,
    lookback_value: Optional[int] = None,
    lookback_unit: Optional[str] = None,
) -> list[Message]:
    """Search message content with pagination and optional time window.

    Args:
        query: Optional message content query (case-insensitive partial match)
        sender_id: Optional canonical normalized user ID to filter by sender
        page: Page number for pagination (0-based)
        limit: Max number of results per page
        after_iso: Optional lower ISO-8601 timestamp bound (exclusive)
        before_iso: Optional upper ISO-8601 timestamp bound (exclusive)
        lookback_value: Optional relative lookback amount
        lookback_unit: Optional lookback unit, one of h, d, w
    """
    normalized_query = (query or "").strip()
    if page < 0:
        raise ValueError("page must be greater than or equal to 0")
    if limit <= 0:
        raise ValueError("limit must be greater than 0")
    resolved_after, resolved_before = _resolve_time_window(
        after_iso=after_iso,
        before_iso=before_iso,
        lookback_value=lookback_value,
        lookback_unit=lookback_unit,
    )
    if not normalized_query and not (resolved_before or resolved_after):
        raise ValueError(
            "Either a non-empty query or a time window must be provided."
        )

    return _query_messages(
        after=resolved_after,
        before=resolved_before,
        sender_id=sender_id,
        chat_jid=None,
        query=normalized_query if normalized_query else None,
        limit=limit,
        page=page,
        include_context=False,
        context_before=1,
        context_after=1,
    )


def search_chat_messages(
    chat_jid: str,
    query: str,
    page: int = 0,
    limit: int = 20,
    after_iso: Optional[str] = None,
    before_iso: Optional[str] = None,
    lookback_value: Optional[int] = None,
    lookback_unit: Optional[str] = None,
) -> list[Message]:
    """Search message content within a specific chat and optional time window.

    Args:
        chat_jid: Chat JID to scope the search to
        query: Message content query (case-insensitive partial match)
        page: Page number for pagination (0-based)
        limit: Max number of results per page
        after_iso: Optional lower ISO-8601 timestamp bound (exclusive)
        before_iso: Optional upper ISO-8601 timestamp bound (exclusive)
        lookback_value: Optional relative lookback amount
        lookback_unit: Optional lookback unit, one of h, d, w
    """
    normalized_chat_jid = chat_jid.strip()
    normalized_query = query.strip()

    if not normalized_chat_jid:
        raise ValueError("chat_jid must be a non-empty string")
    if not normalized_query:
        raise ValueError("query must be a non-empty string")
    if page < 0:
        raise ValueError("page must be greater than or equal to 0")
    if limit <= 0:
        raise ValueError("limit must be greater than 0")
    resolved_after, resolved_before = _resolve_time_window(
        after_iso=after_iso,
        before_iso=before_iso,
        lookback_value=lookback_value,
        lookback_unit=lookback_unit,
    )

    return _query_messages(
        after=resolved_after,
        before=resolved_before,
        sender_id=None,
        chat_jid=normalized_chat_jid,
        query=normalized_query,
        limit=limit,
        page=page,
        include_context=False,
        context_before=1,
        context_after=1,
    )


def get_message_context(
    message_id: str,
    before: int = 5,
    after: int = 5
) -> MessageContext:
    """Get context around a specific message."""
    try:
        conn = _get_db_connection()
        cursor = conn.cursor()
        
        # Get the target message first
        cursor.execute("""
            SELECT messages.timestamp, messages.sender, chats.name, messages.content, messages.is_from_me, chats.jid, messages.id, messages.chat_jid, messages.media_type
            FROM messages
            JOIN chats ON messages.chat_jid = chats.jid
            WHERE messages.id = ?
        """, (message_id,))
        msg_data = cursor.fetchone()
        
        if not msg_data:
            raise ValueError(f"Message with ID {message_id} not found")
            
        target_message = Message(
            timestamp=datetime.fromisoformat(msg_data[0]),
            sender_id=msg_data[1],
            chat_name=msg_data[2],
            content=msg_data[3],
            is_from_me=msg_data[4],
            chat_jid=msg_data[5],
            id=msg_data[6],
            media_type=msg_data[8]
        )
        
        # Get messages before
        cursor.execute("""
            SELECT messages.timestamp, messages.sender, chats.name, messages.content, messages.is_from_me, chats.jid, messages.id, messages.media_type
            FROM messages
            JOIN chats ON messages.chat_jid = chats.jid
            WHERE messages.chat_jid = ? AND messages.timestamp < ?
            ORDER BY messages.timestamp DESC
            LIMIT ?
        """, (msg_data[7], msg_data[0], before))
        
        before_messages = []
        for msg in cursor.fetchall():
            before_messages.append(Message(
                timestamp=datetime.fromisoformat(msg[0]),
                sender_id=msg[1],
                chat_name=msg[2],
                content=msg[3],
                is_from_me=msg[4],
                chat_jid=msg[5],
                id=msg[6],
                media_type=msg[7]
            ))
        
        # Get messages after
        cursor.execute("""
            SELECT messages.timestamp, messages.sender, chats.name, messages.content, messages.is_from_me, chats.jid, messages.id, messages.media_type
            FROM messages
            JOIN chats ON messages.chat_jid = chats.jid
            WHERE messages.chat_jid = ? AND messages.timestamp > ?
            ORDER BY messages.timestamp ASC
            LIMIT ?
        """, (msg_data[7], msg_data[0], after))
        
        after_messages = []
        for msg in cursor.fetchall():
            after_messages.append(Message(
                timestamp=datetime.fromisoformat(msg[0]),
                sender_id=msg[1],
                chat_name=msg[2],
                content=msg[3],
                is_from_me=msg[4],
                chat_jid=msg[5],
                id=msg[6],
                media_type=msg[7]
            ))
        
        return MessageContext(
            message=target_message,
            before=before_messages,
            after=after_messages
        )
        
    except sqlite3.Error as e:
        print(f"Database error: {e}")
        raise
    finally:
        if 'conn' in locals():
            conn.close()


def list_chats(
    query: Optional[str] = None,
    limit: int = 20,
    page: int = 0,
    include_last_message: bool = True,
    sort_by: str = "last_active"
) -> list[Chat]:
    """Get chats matching the specified criteria."""
    try:
        conn = _get_db_connection()
        cursor = conn.cursor()
        
        # Build base query
        query_parts = ["""
            SELECT 
                chats.jid as chat_jid,
                chats.name,
                chats.last_message_time,
                messages.content as last_message,
                messages.sender as last_sender_id,
                messages.is_from_me as last_is_from_me
            FROM chats
        """]
        
        if include_last_message:
            query_parts.append("""
                LEFT JOIN messages ON chats.jid = messages.chat_jid 
                AND chats.last_message_time = messages.timestamp
            """)
            
        where_clauses = []
        params = []
        
        if query:
            where_clauses.append("(LOWER(chats.name) LIKE LOWER(?) OR chats.jid LIKE ?)")
            params.extend([f"%{query}%", f"%{query}%"])
            
        if where_clauses:
            query_parts.append("WHERE " + " AND ".join(where_clauses))
            
        # Add sorting
        order_by = "chats.last_message_time DESC" if sort_by == "last_active" else "chats.name"
        query_parts.append(f"ORDER BY {order_by}")
        
        # Add pagination
        offset = (page ) * limit
        query_parts.append("LIMIT ? OFFSET ?")
        params.extend([limit, offset])
        
        cursor.execute(" ".join(query_parts), tuple(params))
        chats = cursor.fetchall()
        
        result = []
        for chat_data in chats:
            chat = Chat(
                chat_jid=chat_data[0],
                name=chat_data[1],
                last_message_time=datetime.fromisoformat(chat_data[2]) if chat_data[2] else None,
                last_message=chat_data[3],
                last_sender_id=chat_data[4],
                last_is_from_me=chat_data[5]
            )
            result.append(chat)
            
        return result
        
    except sqlite3.Error as e:
        print(f"Database error: {e}")
        return []
    finally:
        if 'conn' in locals():
            conn.close()


def search_contacts(query: str) -> list[Contact]:
    """Search contacts by name or phone number."""
    try:
        conn = _get_db_connection()
        cursor = conn.cursor()
        
        # Build wildcard pattern for partial matching.
        search_pattern = "%" + query + "%"
        
        cursor.execute("""
            SELECT DISTINCT 
                jid,
                name
            FROM chats
            WHERE 
                (LOWER(name) LIKE LOWER(?) OR LOWER(jid) LIKE LOWER(?))
                AND jid NOT LIKE '%@g.us'
            ORDER BY name, jid
            LIMIT 50
        """, (search_pattern, search_pattern))
        
        contacts = cursor.fetchall()
        
        contacts_by_sender_id: dict[str, Contact] = {}
        for contact_data in contacts:
            chat_jid = (contact_data[0] or "").strip()
            name = contact_data[1]

            # Contacts search should return users only, even if group rows are present.
            if not chat_jid or chat_jid.endswith("@g.us"):
                continue

            sender_alias = chat_jid.split("@", 1)[0].strip() if "@" in chat_jid else chat_jid
            if not sender_alias:
                continue

            _, canonical_sender_id = _sender_id_candidates(conn, sender_alias)
            existing = contacts_by_sender_id.get(canonical_sender_id)
            canonical_chat_jid = canonical_sender_id

            if existing is None:
                contacts_by_sender_id[canonical_sender_id] = Contact(
                    sender_id=canonical_sender_id,
                    name=name,
                    chat_jid=canonical_chat_jid if chat_jid == canonical_chat_jid else chat_jid,
                )
                continue

            if (not existing.name) and name:
                existing.name = name

            if existing.chat_jid != canonical_chat_jid and chat_jid == canonical_chat_jid:
                existing.chat_jid = canonical_chat_jid

        return sorted(
            contacts_by_sender_id.values(),
            key=lambda contact: ((contact.name or "").lower(), contact.sender_id),
        )
        
    except sqlite3.Error as e:
        print(f"Database error: {e}")
        return []
    finally:
        if 'conn' in locals():
            conn.close()


def get_contact_chats(sender_id: str, limit: int = 20, page: int = 0) -> list[Chat]:
    """Get all chats involving the contact.
    
    Args:
        sender_id: Canonical normalized user ID (no JID suffix)
        limit: Maximum number of chats to return (default 20)
        page: Page number for pagination (default 0)
    """
    try:
        conn = _get_db_connection()
        cursor = conn.cursor()
        sender_candidates, _ = _sender_id_candidates(conn, sender_id)
        direct_chat_jids = _direct_chat_jids_for_candidates(sender_candidates)
        sender_placeholders = ", ".join(["?"] * len(sender_candidates))
        chat_placeholders = ", ".join(["?"] * len(direct_chat_jids))
        
        cursor.execute("""
            SELECT DISTINCT
                c.jid as chat_jid,
                c.name,
                c.last_message_time,
                m.content as last_message,
                m.sender as last_sender_id,
                m.is_from_me as last_is_from_me
            FROM chats c
            JOIN messages m ON c.jid = m.chat_jid
            WHERE m.sender IN (""" + sender_placeholders + """)
               OR c.jid IN (""" + chat_placeholders + """)
            ORDER BY c.last_message_time DESC
            LIMIT ? OFFSET ?
        """, tuple(sender_candidates + direct_chat_jids + [limit, page * limit]))
        
        chats = cursor.fetchall()
        
        result = []
        for chat_data in chats:
            chat = Chat(
                chat_jid=chat_data[0],
                name=chat_data[1],
                last_message_time=datetime.fromisoformat(chat_data[2]) if chat_data[2] else None,
                last_message=chat_data[3],
                last_sender_id=chat_data[4],
                last_is_from_me=chat_data[5]
            )
            result.append(chat)
            
        return result
        
    except sqlite3.Error as e:
        print(f"Database error: {e}")
        return []
    finally:
        if 'conn' in locals():
            conn.close()


def get_last_interaction(sender_id: str) -> Optional[str]:
    """Get most recent message involving the contact."""
    try:
        conn = _get_db_connection()
        cursor = conn.cursor()
        sender_candidates, _ = _sender_id_candidates(conn, sender_id)
        direct_chat_jids = _direct_chat_jids_for_candidates(sender_candidates)
        sender_placeholders = ", ".join(["?"] * len(sender_candidates))
        chat_placeholders = ", ".join(["?"] * len(direct_chat_jids))
        
        cursor.execute("""
            SELECT 
                m.timestamp,
                m.sender,
                c.name,
                m.content,
                m.is_from_me,
                c.jid,
                m.id,
                m.media_type
            FROM messages m
            JOIN chats c ON m.chat_jid = c.jid
            WHERE m.sender IN (""" + sender_placeholders + """)
               OR c.jid IN (""" + chat_placeholders + """)
            ORDER BY m.timestamp DESC
            LIMIT 1
        """, tuple(sender_candidates + direct_chat_jids))
        
        msg_data = cursor.fetchone()
        
        if not msg_data:
            return None
            
        message = Message(
            timestamp=datetime.fromisoformat(msg_data[0]),
            sender_id=msg_data[1],
            chat_name=msg_data[2],
            content=msg_data[3],
            is_from_me=msg_data[4],
            chat_jid=msg_data[5],
            id=msg_data[6],
            media_type=msg_data[7]
        )
        
        return format_message(message)
        
    except sqlite3.Error as e:
        print(f"Database error: {e}")
        return None
    finally:
        if 'conn' in locals():
            conn.close()


def get_chat(chat_jid: str, include_last_message: bool = True) -> Optional[Chat]:
    """Get chat metadata by JID."""
    try:
        conn = _get_db_connection()
        cursor = conn.cursor()
        
        query = """
            SELECT 
                c.jid as chat_jid,
                c.name,
                c.last_message_time,
                m.content as last_message,
                m.sender as last_sender_id,
                m.is_from_me as last_is_from_me
            FROM chats c
        """
        
        if include_last_message:
            query += """
                LEFT JOIN messages m ON c.jid = m.chat_jid 
                AND c.last_message_time = m.timestamp
            """
            
        query += " WHERE c.jid = ?"
        
        cursor.execute(query, (chat_jid,))
        chat_data = cursor.fetchone()
        
        if not chat_data:
            return None
            
        return Chat(
            chat_jid=chat_data[0],
            name=chat_data[1],
            last_message_time=datetime.fromisoformat(chat_data[2]) if chat_data[2] else None,
            last_message=chat_data[3],
            last_sender_id=chat_data[4],
            last_is_from_me=chat_data[5]
        )
        
    except sqlite3.Error as e:
        print(f"Database error: {e}")
        return None
    finally:
        if 'conn' in locals():
            conn.close()


def get_direct_chat_by_contact(sender_id: str) -> Optional[Chat]:
    """Get direct chat metadata by canonical normalized user ID."""
    try:
        conn = _get_db_connection()
        cursor = conn.cursor()
        sender_candidates, resolved_canonical = _sender_id_candidates(conn, sender_id)
        ordered_candidates = [resolved_canonical] + [
            candidate for candidate in sender_candidates if candidate != resolved_canonical
        ]
        direct_chat_jids = _direct_chat_jids_for_candidates(ordered_candidates)
        placeholders = ", ".join(["?"] * len(direct_chat_jids))
        
        cursor.execute("""
            SELECT 
                c.jid as chat_jid,
                c.name,
                c.last_message_time,
                m.content as last_message,
                m.sender as last_sender_id,
                m.is_from_me as last_is_from_me
            FROM chats c
            LEFT JOIN messages m ON c.jid = m.chat_jid 
                AND c.last_message_time = m.timestamp
            WHERE c.jid IN (""" + placeholders + """) AND c.jid NOT LIKE '%@g.us'
            ORDER BY c.last_message_time DESC
            LIMIT 1
        """, tuple(direct_chat_jids))
        
        chat_data = cursor.fetchone()
        
        if not chat_data:
            return None
            
        return Chat(
            chat_jid=chat_data[0],
            name=chat_data[1],
            last_message_time=datetime.fromisoformat(chat_data[2]) if chat_data[2] else None,
            last_message=chat_data[3],
            last_sender_id=chat_data[4],
            last_is_from_me=chat_data[5]
        )
        
    except sqlite3.Error as e:
        print(f"Database error: {e}")
        return None
    finally:
        if 'conn' in locals():
            conn.close()

def send_message(recipient: str, message: str, *, auth_headers: dict[str, str]) -> Tuple[bool, str]:
    try:
        # Validate input
        if not recipient:
            return False, "Recipient must be provided"
        
        url = f"{WHATSAPP_API_BASE_URL}/api/send"
        payload = {
            "recipient": recipient,
            "message": message,
        }
        
        response = requests.post(
            url,
            json=payload,
            headers=_validated_bridge_auth_headers(auth_headers),
            timeout=30,
        )
        
        # Check if the request was successful
        if response.status_code == 200:
            result = response.json()
            return result.get("success", False), result.get("message", "Unknown response")
        else:
            return False, f"Error: HTTP {response.status_code} - {response.text}"
            
    except requests.RequestException as e:
        return False, f"Request error: {str(e)}"
    except json.JSONDecodeError:
        return False, f"Error parsing response: {response.text}"
    except Exception as e:
        return False, f"Unexpected error: {str(e)}"

def send_file(recipient: str, media_path: str, *, auth_headers: dict[str, str]) -> Tuple[bool, str]:
    try:
        # Validate input
        if not recipient:
            return False, "Recipient must be provided"
        
        if not media_path:
            return False, "Media path must be provided"
        
        if not os.path.isfile(media_path):
            return False, f"Media file not found: {media_path}"
        
        url = f"{WHATSAPP_API_BASE_URL}/api/send"
        payload = {
            "recipient": recipient,
            "media_path": media_path
        }
        
        response = requests.post(
            url,
            json=payload,
            headers=_validated_bridge_auth_headers(auth_headers),
            timeout=30,
        )
        
        # Check if the request was successful
        if response.status_code == 200:
            result = response.json()
            return result.get("success", False), result.get("message", "Unknown response")
        else:
            return False, f"Error: HTTP {response.status_code} - {response.text}"
            
    except requests.RequestException as e:
        return False, f"Request error: {str(e)}"
    except json.JSONDecodeError:
        return False, f"Error parsing response: {response.text}"
    except Exception as e:
        return False, f"Unexpected error: {str(e)}"

def send_audio_message(recipient: str, media_path: str, *, auth_headers: dict[str, str]) -> Tuple[bool, str]:
    try:
        # Validate input
        if not recipient:
            return False, "Recipient must be provided"
        
        if not media_path:
            return False, "Media path must be provided"
        
        if not os.path.isfile(media_path):
            return False, f"Media file not found: {media_path}"

        if not media_path.endswith(".ogg"):
            try:
                media_path = audio.convert_to_opus_ogg_temp(media_path)
            except Exception as e:
                return False, f"Error converting file to opus ogg. You likely need to install ffmpeg: {str(e)}"
        
        url = f"{WHATSAPP_API_BASE_URL}/api/send"
        payload = {
            "recipient": recipient,
            "media_path": media_path
        }
        
        response = requests.post(
            url,
            json=payload,
            headers=_validated_bridge_auth_headers(auth_headers),
            timeout=30,
        )
        
        # Check if the request was successful
        if response.status_code == 200:
            result = response.json()
            return result.get("success", False), result.get("message", "Unknown response")
        else:
            return False, f"Error: HTTP {response.status_code} - {response.text}"
            
    except requests.RequestException as e:
        return False, f"Request error: {str(e)}"
    except json.JSONDecodeError:
        return False, f"Error parsing response: {response.text}"
    except Exception as e:
        return False, f"Unexpected error: {str(e)}"

def download_media(message_id: str, chat_jid: str, *, auth_headers: dict[str, str]) -> Optional[str]:
    """Download media from a message and return the local file path.
    
    Args:
        message_id: The ID of the message containing the media
        chat_jid: The JID of the chat containing the message
    
    Returns:
        The local file path if download was successful, None otherwise
    """
    try:
        url = f"{WHATSAPP_API_BASE_URL}/api/download"
        payload = {
            "message_id": message_id,
            "chat_jid": chat_jid
        }
        
        response = requests.post(
            url,
            json=payload,
            headers=_validated_bridge_auth_headers(auth_headers),
            timeout=30,
        )
        
        if response.status_code == 200:
            result = response.json()
            if result.get("success", False):
                path = result.get("path")
                print(f"Media downloaded successfully: {path}")
                return path
            else:
                print(f"Download failed: {result.get('message', 'Unknown error')}")
                return None
        else:
            print(f"Error: HTTP {response.status_code} - {response.text}")
            return None
            
    except requests.RequestException as e:
        print(f"Request error: {str(e)}")
        return None
    except json.JSONDecodeError:
        print(f"Error parsing response: {response.text}")
        return None
    except Exception as e:
        print(f"Unexpected error: {str(e)}")
        return None
