from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any
from urllib import error, parse, request

from .env import Settings
from .signing import generate_key_token, sign_image, sign_original, sign_thumb


@dataclass
class Response:
    status: int
    headers: dict[str, str]
    body: Any


class VyluxAPI:
    def __init__(self, settings: Settings):
        self.settings = settings

    def _request(self, method: str, path: str, *, body: dict[str, Any] | None = None, headers: dict[str, str] | None = None, auth: bool = False) -> Response:
        url = self.settings.base_url.rstrip("/") + path
        data = None
        req_headers = {"Accept": "application/json"}
        if headers:
            req_headers.update(headers)
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            req_headers["Content-Type"] = "application/json"
        if auth:
            req_headers["X-API-Key"] = self.settings.api_key
        req = request.Request(url, data=data, method=method, headers=req_headers)
        try:
            with request.urlopen(req) as resp:
                raw = resp.read()
                return Response(resp.status, dict(resp.headers.items()), self._decode(raw, resp.headers.get("Content-Type", "")))
        except error.HTTPError as exc:
            raw = exc.read()
            return Response(exc.code, dict(exc.headers.items()), self._decode(raw, exc.headers.get("Content-Type", "")))

    @staticmethod
    def _decode(raw: bytes, content_type: str) -> Any:
        text = raw.decode("utf-8", errors="replace")
        if "application/json" in content_type:
            try:
                return json.loads(text)
            except json.JSONDecodeError:
                return text
        return text

    def create_audio_job(self, *, content_hash: str, source_key: str, hls: bool = True, encrypt: bool = False, mp3: bool = False, flac: bool = False, waveform: bool = False, waveform_bins: int | None = None, callback_url: str | None = None) -> Response:
        body: dict[str, Any] = {
            "source": {"hash": content_hash, "key": source_key},
            "pipeline": {},
        }
        pipeline = body["pipeline"]
        if hls or encrypt:
            hls_cfg: dict[str, Any] = {"enabled": True, "profile": "stream_aac_standard"}
            if encrypt:
                hls_cfg["encryption"] = {"enabled": True}
            pipeline["package"] = {"hls": hls_cfg}
        downloads = []
        if mp3:
            downloads.append({"profile": "download_mp3_high"})
        if flac:
            downloads.append({"profile": "download_flac_standard"})
        if downloads:
            pipeline["downloads"] = downloads
        if waveform:
            wf = {"enabled": True, "profile": "waveform_standard"}
            if waveform_bins:
                wf["bins"] = waveform_bins
            pipeline["waveform"] = wf
        if callback_url:
            body["delivery"] = {"callback_url": callback_url}
        return self._request("POST", "/api/audio/jobs", body=body, auth=True)

    def create_video_transcode_job(self, *, content_hash: str, source_key: str, encrypt: bool = False, callback_url: str | None = None) -> Response:
        body: dict[str, Any] = {
            "source": {"hash": content_hash, "key": source_key},
            "pipeline": {
                "package": {
                    "hls": {
                        "enabled": True,
                        "profile": "stream_video_standard",
                    }
                }
            },
        }
        if encrypt:
            body["pipeline"]["package"]["hls"]["encryption"] = {"enabled": True}
        if callback_url:
            body["delivery"] = {"callback_url": callback_url}
        return self._request("POST", "/api/video/jobs", body=body, auth=True)

    def create_video_full_job(self, *, content_hash: str, source_key: str, encrypt: bool = False, callback_url: str | None = None) -> Response:
        body: dict[str, Any] = {
            "source": {"hash": content_hash, "key": source_key},
            "pipeline": {
                "cover": {"enabled": True},
                "preview": {"enabled": True},
                "package": {
                    "hls": {
                        "enabled": True,
                        "profile": "stream_video_standard",
                    }
                },
            },
        }
        if encrypt:
            body["pipeline"]["package"]["hls"]["encryption"] = {"enabled": True}
        if callback_url:
            body["delivery"] = {"callback_url": callback_url}
        return self._request("POST", "/api/video/jobs", body=body, auth=True)

    def get_job(self, job_id: str) -> Response:
        return self._request("GET", f"/api/jobs/{parse.quote(job_id)}", auth=True)

    def retry_job(self, job_id: str) -> Response:
        return self._request("POST", f"/api/jobs/{parse.quote(job_id)}/retry", auth=True)

    def delete_media(self, content_hash: str) -> Response:
        return self._request("DELETE", f"/api/media/{parse.quote(content_hash)}", auth=True)

    def health(self) -> Response:
        return self._request("GET", "/healthz")

    def ready(self) -> Response:
        return self._request("GET", "/readyz")

    def build_image_url(self, *, options: str, source_key: str, output_format: str) -> str:
        source_path = parse.quote(source_key, safe="") + f".{output_format}"
        signature, canonical_options, _ = sign_image(self.settings.hmac_secret, options, source_path)
        return f"{self.settings.base_url.rstrip('/')}/img/{signature}/{canonical_options}/{source_path}"

    def build_original_url(self, *, source_key: str) -> str:
        encoded_key = parse.quote(source_key, safe="")
        signature, _ = sign_original(self.settings.hmac_secret, encoded_key)
        return f"{self.settings.base_url.rstrip('/')}/original/{signature}/{encoded_key}"

    def build_thumb_url(self, *, object_key: str) -> str:
        encoded_key = parse.quote(object_key, safe="")
        signature, _ = sign_thumb(self.settings.hmac_secret, encoded_key)
        return f"{self.settings.base_url.rstrip('/')}/thumb/{signature}/{encoded_key}"

    def build_key_url(self, *, key_id: str, content_hash: str, ttl_seconds: int = 3600) -> tuple[str, str]:
        token = generate_key_token(content_hash, self.settings.key_token_secret, ttl_seconds)
        return f"{self.settings.base_url.rstrip('/')}/api/key/{parse.quote(key_id)}", token
