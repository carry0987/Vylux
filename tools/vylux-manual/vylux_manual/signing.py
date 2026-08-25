from __future__ import annotations

import base64
import hashlib
import hmac
import json
import time
from typing import Tuple
from urllib.parse import unquote


def sign(secret: str, options: str, source: str) -> str:
    mac = hmac.new(secret.encode("utf-8"), digestmod=hashlib.sha256)
    mac.update(options.encode("utf-8"))
    mac.update(b"/")
    mac.update(source.encode("utf-8"))
    return mac.hexdigest()


def canonicalize_object_key(raw: str) -> str:
    raw = raw.lstrip("/")
    if not raw:
        raise ValueError("missing object key")
    key = unquote(raw)
    if not key:
        raise ValueError("missing object key")
    return key


def canonicalize_image_options(raw: str) -> str:
    raw = raw.strip()
    if not raw:
        return ""

    parsed: dict[str, int] = {}
    for token in raw.split("_"):
        if len(token) < 2:
            raise ValueError(f"invalid option token: {token!r}")
        prefix, value = token[0], token[1:]
        try:
            num = int(value)
        except ValueError as exc:
            raise ValueError(f"invalid {prefix} value: {value!r}") from exc
        if prefix in {"w", "h"} and num < 0:
            raise ValueError(f"invalid {prefix} value: {value!r}")
        if prefix == "q" and not (1 <= num <= 100):
            raise ValueError(f"invalid quality: {value!r} (must be 1-100)")
        if prefix not in {"w", "h", "q"}:
            raise ValueError(f"unknown option prefix: {prefix!r}")
        parsed[prefix] = num

    ordered = []
    for prefix in ("w", "h", "q"):
        if prefix in parsed:
            ordered.append(f"{prefix}{parsed[prefix]}")
    return "_".join(ordered)


def canonicalize_image_source_path(raw: str) -> str:
    raw = raw.lstrip("/")
    if not raw:
        raise ValueError("missing source path")
    dot = raw.rfind(".")
    if dot == -1:
        raise ValueError("missing output format")
    encoded_source = raw[:dot]
    ext = raw[dot + 1 :].lower()
    if ext == "jpeg":
        ext = "jpg"
    if not ext:
        raise ValueError("missing output format")
    source_key = unquote(encoded_source)
    if not source_key:
        raise ValueError("missing source key")
    return f"{source_key}.{ext}"


def sign_image(secret: str, raw_options: str, raw_source_path: str) -> Tuple[str, str, str]:
    canonical_options = canonicalize_image_options(raw_options)
    canonical_source = canonicalize_image_source_path(raw_source_path)
    signature = sign(secret, canonical_options, canonical_source)
    return signature, canonical_options, canonical_source


def sign_original(secret: str, raw_object_key: str) -> Tuple[str, str]:
    canonical_key = canonicalize_object_key(raw_object_key)
    return sign(secret, "", canonical_key), canonical_key


def sign_thumb(secret: str, raw_object_key: str) -> Tuple[str, str]:
    canonical_key = canonicalize_object_key(raw_object_key)
    return sign(secret, "thumb", canonical_key), canonical_key


def generate_key_token(content_hash: str, secret: str, ttl_seconds: int) -> str:
    payload = {
        "hash": content_hash,
        "exp": int(time.time()) + ttl_seconds,
    }
    payload_bytes = json.dumps(payload, separators=(",", ":")).encode("utf-8")
    payload_b64 = base64.urlsafe_b64encode(payload_bytes).decode("ascii").rstrip("=")
    mac = hmac.new(secret.encode("utf-8"), digestmod=hashlib.sha256)
    mac.update(payload_b64.encode("ascii"))
    sig_b64 = base64.urlsafe_b64encode(mac.digest()).decode("ascii").rstrip("=")
    return f"{payload_b64}.{sig_b64}"
