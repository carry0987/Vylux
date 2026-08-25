from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path
from typing import Dict


@dataclass(frozen=True)
class Settings:
    repo_root: Path
    base_url: str
    api_key: str
    hmac_secret: str
    key_token_secret: str
    source_s3_endpoint: str
    source_s3_access_key: str
    source_s3_secret_key: str
    source_s3_region: str
    source_bucket: str
    media_s3_endpoint: str
    media_s3_access_key: str
    media_s3_secret_key: str
    media_s3_region: str
    media_bucket: str


def find_repo_root(start: Path | None = None) -> Path:
    current = (start or Path(__file__)).resolve()
    for candidate in [current, *current.parents]:
        if (candidate / "package.json").exists() and (candidate / "docs").exists():
            return candidate
    raise RuntimeError("Unable to locate gh-pages repo root from current path")


def parse_dotenv(path: Path) -> Dict[str, str]:
    values: Dict[str, str] = {}
    if not path.exists():
        return values

    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        value = value.strip()
        if value and value[0] == value[-1] and value[0] in {'"', "'"}:
            value = value[1:-1]
        values[key] = value
    return values


def load_settings() -> Settings:
    repo_root = find_repo_root()
    merged: Dict[str, str] = {}
    merged.update(parse_dotenv(repo_root / ".env"))
    merged.update(parse_dotenv(repo_root / ".env.local"))
    merged.update({key: value for key, value in os.environ.items() if key in merged or key.startswith(("BASE_URL", "API_KEY", "HMAC_SECRET", "KEY_TOKEN_SECRET", "SOURCE_", "MEDIA_"))})

    def need(key: str) -> str:
        value = merged.get(key, "").strip()
        if not value:
            raise RuntimeError(f"Missing required setting: {key}")
        return value

    return Settings(
        repo_root=repo_root,
        base_url=need("BASE_URL"),
        api_key=need("API_KEY"),
        hmac_secret=need("HMAC_SECRET"),
        key_token_secret=need("KEY_TOKEN_SECRET"),
        source_s3_endpoint=need("SOURCE_S3_ENDPOINT"),
        source_s3_access_key=need("SOURCE_S3_ACCESS_KEY"),
        source_s3_secret_key=need("SOURCE_S3_SECRET_KEY"),
        source_s3_region=need("SOURCE_S3_REGION"),
        source_bucket=need("SOURCE_BUCKET"),
        media_s3_endpoint=need("MEDIA_S3_ENDPOINT"),
        media_s3_access_key=need("MEDIA_S3_ACCESS_KEY"),
        media_s3_secret_key=need("MEDIA_S3_SECRET_KEY"),
        media_s3_region=need("MEDIA_S3_REGION"),
        media_bucket=need("MEDIA_BUCKET"),
    )
