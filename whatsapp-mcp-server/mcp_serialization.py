from dataclasses import asdict, is_dataclass
from datetime import date, datetime
from typing import Any


def serialize_for_mcp(value: Any) -> Any:
    """Convert dataclasses/datetimes into JSON-serializable structures."""
    if is_dataclass(value):
        return serialize_for_mcp(asdict(value))
    if isinstance(value, (datetime, date)):
        return value.isoformat()
    if isinstance(value, dict):
        return {key: serialize_for_mcp(val) for key, val in value.items()}
    if isinstance(value, (list, tuple, set)):
        return [serialize_for_mcp(item) for item in value]
    return value
