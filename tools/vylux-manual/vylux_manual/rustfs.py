from __future__ import annotations

import mimetypes
from pathlib import Path

from .env import Settings


class RustFSClient:
    def __init__(self, settings: Settings):
        try:
            import boto3  # type: ignore
        except ImportError as exc:  # pragma: no cover
            raise RuntimeError("boto3 is required for RustFS operations. Install it with: python3 -m pip install boto3") from exc

        self._boto3 = boto3
        self.settings = settings
        self._source = self._new_client(
            endpoint=settings.source_s3_endpoint,
            access_key=settings.source_s3_access_key,
            secret_key=settings.source_s3_secret_key,
            region=settings.source_s3_region,
        )
        self._media = self._new_client(
            endpoint=settings.media_s3_endpoint,
            access_key=settings.media_s3_access_key,
            secret_key=settings.media_s3_secret_key,
            region=settings.media_s3_region,
        )

    def _new_client(self, *, endpoint: str, access_key: str, secret_key: str, region: str):
        return self._boto3.client(
            "s3",
            endpoint_url=endpoint,
            aws_access_key_id=access_key,
            aws_secret_access_key=secret_key,
            region_name=region,
        )

    def _select(self, bucket_kind: str):
        kind = bucket_kind.lower()
        if kind == "source":
            return self._source, self.settings.source_bucket
        if kind == "media":
            return self._media, self.settings.media_bucket
        raise ValueError("bucket_kind must be 'source' or 'media'")

    def upload_file(self, *, bucket_kind: str, key: str, file_path: str, content_type: str | None = None) -> dict:
        client, bucket = self._select(bucket_kind)
        path = Path(file_path)
        if not path.exists():
            raise FileNotFoundError(path)
        guessed = content_type or mimetypes.guess_type(path.name)[0] or "application/octet-stream"
        client.upload_file(str(path), bucket, key, ExtraArgs={"ContentType": guessed})
        return {"bucket": bucket, "key": key, "content_type": guessed, "size": path.stat().st_size}

    def list_objects(self, *, bucket_kind: str, prefix: str = "") -> list[str]:
        client, bucket = self._select(bucket_kind)
        paginator = client.get_paginator("list_objects_v2")
        keys: list[str] = []
        for page in paginator.paginate(Bucket=bucket, Prefix=prefix):
            for item in page.get("Contents", []):
                keys.append(item["Key"])
        return keys

    def delete_object(self, *, bucket_kind: str, key: str) -> dict:
        client, bucket = self._select(bucket_kind)
        client.delete_object(Bucket=bucket, Key=key)
        return {"bucket": bucket, "key": key, "deleted": True}

    def head_object(self, *, bucket_kind: str, key: str) -> dict:
        client, bucket = self._select(bucket_kind)
        response = client.head_object(Bucket=bucket, Key=key)
        return {
            "bucket": bucket,
            "key": key,
            "size": response.get("ContentLength"),
            "content_type": response.get("ContentType"),
            "etag": response.get("ETag"),
            "last_modified": response.get("LastModified").isoformat() if response.get("LastModified") else None,
        }
