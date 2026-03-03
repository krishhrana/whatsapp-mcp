import os
import sys
import tempfile
import types
import unittest
from pathlib import Path
from unittest.mock import patch

# Keep unit tests isolated from optional runtime-only dependencies.
try:
    import requests  # noqa: F401
except ModuleNotFoundError:
    sys.modules["requests"] = types.ModuleType("requests")

try:
    import audio  # noqa: F401
except ModuleNotFoundError:
    sys.modules["audio"] = types.ModuleType("audio")

try:
    from dotenv import load_dotenv  # noqa: F401
except ModuleNotFoundError:
    dotenv_stub = types.ModuleType("dotenv")

    def _noop_load_dotenv(*args, **kwargs):
        return False

    dotenv_stub.load_dotenv = _noop_load_dotenv
    sys.modules["dotenv"] = dotenv_stub

import whatsapp


class RuntimePathResolutionTests(unittest.TestCase):
    def test_scope_required_in_ecs_mode(self) -> None:
        with patch.dict(
            os.environ,
            {"WHATSAPP_RUNTIME_ECS_MODE": "true", "WHATSAPP_RUNTIME_USER_SCOPE": ""},
            clear=True,
        ):
            with self.assertRaises(RuntimeError):
                whatsapp._resolve_runtime_scope()

    def test_local_mode_allows_fallback_scope(self) -> None:
        with patch.dict(os.environ, {}, clear=True):
            self.assertEqual(whatsapp._resolve_runtime_scope(), "local-dev")

    def test_ecs_mode_requires_hot_db_path(self) -> None:
        scope = "da537387-a2e6-4003-93d7-35935deec7c9"
        with tempfile.TemporaryDirectory() as hot_root:
            with patch.dict(
                os.environ,
                {
                    "WHATSAPP_RUNTIME_ECS_MODE": "true",
                    "WHATSAPP_RUNTIME_USER_SCOPE": scope,
                    "WHATSAPP_MESSAGE_STORE_HOT_DIR": hot_root,
                },
                clear=True,
            ):
                with self.assertRaises(RuntimeError):
                    whatsapp._runtime_messages_db_path()

    def test_non_ecs_falls_back_to_persistent_db(self) -> None:
        scope = "da537387-a2e6-4003-93d7-35935deec7c9"
        with tempfile.TemporaryDirectory() as persistent_root:
            with tempfile.TemporaryDirectory() as hot_root:
                persistent_db = (
                    Path(persistent_root) / "users" / scope / "messages.db"
                )
                persistent_db.parent.mkdir(parents=True, exist_ok=True)
                persistent_db.touch()

                with patch.dict(
                    os.environ,
                    {
                        "WHATSAPP_RUNTIME_ECS_MODE": "false",
                        "WHATSAPP_RUNTIME_USER_SCOPE": scope,
                        "WHATSAPP_MESSAGE_STORE_PERSISTENT_DIR": persistent_root,
                        "WHATSAPP_MESSAGE_STORE_HOT_DIR": hot_root,
                    },
                    clear=True,
                ):
                    resolved = whatsapp._runtime_messages_db_path()

                self.assertEqual(str(persistent_db), resolved)

    def test_hot_db_preferred_when_available(self) -> None:
        scope = "da537387-a2e6-4003-93d7-35935deec7c9"
        with tempfile.TemporaryDirectory() as persistent_root:
            with tempfile.TemporaryDirectory() as hot_root:
                persistent_db = (
                    Path(persistent_root) / "users" / scope / "messages.db"
                )
                hot_db = Path(hot_root) / "users" / scope / "messages.db"
                persistent_db.parent.mkdir(parents=True, exist_ok=True)
                hot_db.parent.mkdir(parents=True, exist_ok=True)
                persistent_db.touch()
                hot_db.touch()

                with patch.dict(
                    os.environ,
                    {
                        "WHATSAPP_RUNTIME_ECS_MODE": "false",
                        "WHATSAPP_RUNTIME_USER_SCOPE": scope,
                        "WHATSAPP_MESSAGE_STORE_PERSISTENT_DIR": persistent_root,
                        "WHATSAPP_MESSAGE_STORE_HOT_DIR": hot_root,
                    },
                    clear=True,
                ):
                    resolved = whatsapp._runtime_messages_db_path()

                self.assertEqual(str(hot_db), resolved)


if __name__ == "__main__":
    unittest.main()
