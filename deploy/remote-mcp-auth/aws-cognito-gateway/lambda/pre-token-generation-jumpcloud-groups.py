"""Cognito Pre Token Generation Lambda template (trigger source V2_0+).

CLAIM NORMALIZATION ONLY - NOT AUTHORIZATION.

This starter copies a configured JumpCloud or IdP-sourced group value into an
access-token claim such as ``custom:jumpcloud_groups``. The gongmcp-gateway
validates Cognito access tokens only; ID-token claims are not sufficient.

Configure the Lambda environment variables, attach it as a Pre Token Generation
trigger on the user pool with Lambda version V2_0 or newer, and map JumpCloud
SAML/OIDC attributes into the source user attribute first.

Environment variables:
  SOURCE_USER_ATTRIBUTE  User-pool attribute to read, for example
                         ``custom:jumpcloud_group``. Optional when
                         INCLUDE_COGNITO_GROUPS=1.
  TARGET_ACCESS_TOKEN_CLAIM
                         Access-token claim to set. Defaults to
                         ``custom:jumpcloud_groups``.
  INCLUDE_COGNITO_GROUPS
                         When ``1``, also copy ``groupConfiguration.groupsToOverride``.
  GROUPS_DELIMITER       Join multiple values for string claims. Defaults to ``,``.
"""

from __future__ import annotations

import json
import os
from typing import Any


DEFAULT_TARGET_CLAIM = "custom:jumpcloud_groups"
DEFAULT_GROUPS_DELIMITER = ","


def _env(name: str, default: str = "") -> str:
    return os.environ.get(name, default).strip()


def _collect_source_values(request: dict[str, Any]) -> list[str]:
    values: list[str] = []
    seen: set[str] = set()

    source_attr = _env("SOURCE_USER_ATTRIBUTE")
    if source_attr:
        raw = request.get("userAttributes", {}).get(source_attr, "")
        for item in _normalize_group_values(raw):
            if item not in seen:
                seen.add(item)
                values.append(item)

    if _env("INCLUDE_COGNITO_GROUPS") == "1":
        groups = request.get("groupConfiguration", {}).get("groupsToOverride") or []
        if isinstance(groups, list):
            for item in groups:
                if not isinstance(item, str):
                    continue
                item = item.strip()
                if item and item not in seen:
                    seen.add(item)
                    values.append(item)

    return values


def _normalize_group_values(raw: Any) -> list[str]:
    if raw is None:
        return []
    if isinstance(raw, list):
        out: list[str] = []
        for item in raw:
            if isinstance(item, str) and item.strip():
                out.append(item.strip())
        return out
    if not isinstance(raw, str):
        return []
    text = raw.strip()
    if not text:
        return []
    if text.startswith("["):
        try:
            parsed = json.loads(text)
        except json.JSONDecodeError:
            parsed = None
        if isinstance(parsed, list):
            return _normalize_group_values(parsed)
    delimiter = _env("GROUPS_DELIMITER", DEFAULT_GROUPS_DELIMITER) or DEFAULT_GROUPS_DELIMITER
    return [part.strip() for part in text.split(delimiter) if part.strip()]


def _format_claim_value(values: list[str]) -> str:
    delimiter = _env("GROUPS_DELIMITER", DEFAULT_GROUPS_DELIMITER) or DEFAULT_GROUPS_DELIMITER
    return delimiter.join(values)


def handler(event: dict[str, Any], _context: Any) -> dict[str, Any]:
    request = event.setdefault("request", {})
    response = event.setdefault("response", {})
    details = response.setdefault("claimsAndScopesOverrideDetails", {})
    access = details.setdefault("accessTokenGeneration", {})
    claims = access.setdefault("claimsToAddOrOverride", {})

    values = _collect_source_values(request)
    if not values:
        return event

    target = _env("TARGET_ACCESS_TOKEN_CLAIM", DEFAULT_TARGET_CLAIM) or DEFAULT_TARGET_CLAIM
    claims[target] = _format_claim_value(values)
    return event
