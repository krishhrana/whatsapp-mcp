import os
import time
from typing import Any

import jwt
from jwt import PyJWTError
from mcp.server.auth.middleware.auth_context import get_access_token
from mcp.server.auth.provider import AccessToken, TokenVerifier
from mcp.server.auth.settings import AuthSettings


def _required_internal_jwt_secret() -> str:
    secret = (os.getenv("WHATSAPP_BRIDGE_JWT_SECRET") or "").strip()
    if not secret:
        raise RuntimeError("WHATSAPP_BRIDGE_JWT_SECRET is required for MCP and bridge auth.")
    return secret


def _required_mcp_audience() -> str:
    audience = os.getenv("WHATSAPP_MCP_JWT_AUDIENCE", "whatsapp-mcp").strip()
    if not audience:
        raise RuntimeError("WHATSAPP_MCP_JWT_AUDIENCE must be non-empty.")
    return audience


def _required_token_issuer() -> str:
    issuer = os.getenv("WHATSAPP_BRIDGE_JWT_ISSUER", "omicron-api").strip()
    if not issuer:
        raise RuntimeError("WHATSAPP_BRIDGE_JWT_ISSUER must be non-empty.")
    return issuer


def _required_mcp_scope() -> str:
    scope = os.getenv("WHATSAPP_MCP_REQUIRED_SCOPE", "whatsapp:mcp").strip()
    if not scope:
        raise RuntimeError("WHATSAPP_MCP_REQUIRED_SCOPE must be non-empty.")
    return scope


def _auth_issuer_url() -> str:
    return os.getenv("WHATSAPP_MCP_AUTH_ISSUER_URL", "http://127.0.0.1:8000").strip()


def _resource_server_url() -> str:
    return os.getenv("WHATSAPP_MCP_RESOURCE_SERVER_URL", "http://127.0.0.1:8000/mcp").strip()


def _parse_scope_claims(raw_scope: Any, raw_scopes: Any) -> list[str]:
    scopes: list[str] = []
    for candidate in (raw_scope, raw_scopes):
        if isinstance(candidate, str):
            scopes.extend(
                part.strip()
                for part in candidate.replace(",", " ").split()
                if part.strip()
            )
        elif isinstance(candidate, list):
            scopes.extend(str(part).strip() for part in candidate if str(part).strip())
    return list(dict.fromkeys(scopes))


class InternalJWTTokenVerifier(TokenVerifier):
    def __init__(self, *, jwt_secret: str, expected_audience: str, expected_issuer: str) -> None:
        self._jwt_secret = jwt_secret
        self._expected_audience = expected_audience
        self._expected_issuer = expected_issuer

    async def verify_token(self, token: str) -> AccessToken | None:
        try:
            claims = jwt.decode(
                token,
                self._jwt_secret,
                algorithms=["HS256"],
                audience=self._expected_audience,
                issuer=self._expected_issuer,
                options={"require": ["sub", "exp", "iat", "runtime_id"]},
            )
        except PyJWTError:
            return None

        subject = claims.get("sub")
        runtime_id = claims.get("runtime_id")
        if not isinstance(subject, str) or not subject.strip():
            return None
        if not isinstance(runtime_id, str) or not runtime_id.strip():
            return None

        scopes = _parse_scope_claims(claims.get("scope"), claims.get("scopes"))
        if not scopes:
            return None

        expires_at_raw = claims.get("exp")
        expires_at: int | None = None
        if isinstance(expires_at_raw, (int, float)):
            expires_at = int(expires_at_raw)
            if expires_at < int(time.time()):
                return None

        return AccessToken(
            token=token,
            client_id=subject.strip(),
            scopes=scopes,
            expires_at=expires_at,
            resource=runtime_id.strip(),
        )


def build_auth_settings() -> AuthSettings:
    return AuthSettings(
        issuer_url=_auth_issuer_url(),
        resource_server_url=_resource_server_url(),
        required_scopes=[_required_mcp_scope()],
    )


def build_token_verifier() -> TokenVerifier:
    return InternalJWTTokenVerifier(
        jwt_secret=_required_internal_jwt_secret(),
        expected_audience=_required_mcp_audience(),
        expected_issuer=_required_token_issuer(),
    )


def bridge_auth_headers_from_request_context() -> dict[str, str]:
    access_token = get_access_token()
    if access_token is None:
        raise RuntimeError("Missing authenticated MCP access token in request context.")
    if not access_token.token.strip():
        raise RuntimeError("Authenticated MCP access token is empty.")
    print(access_token.token)
    return {"Authorization": f"Bearer {access_token.token}"}
