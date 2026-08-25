from __future__ import annotations

import argparse
import json
from typing import Any

from .api import VyluxAPI
from .env import load_settings
from .rustfs import RustFSClient


def print_output(value: Any) -> None:
    if isinstance(value, (dict, list)):
        print(json.dumps(value, indent=2, ensure_ascii=False))
    else:
        print(value)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Local manual testing helpers for Vylux")
    sub = parser.add_subparsers(dest="command", required=True)

    upload = sub.add_parser("upload", help="Upload a file to RustFS source/media bucket")
    upload.add_argument("bucket", choices=["source", "media"])
    upload.add_argument("key")
    upload.add_argument("file")
    upload.add_argument("--content-type")

    ls = sub.add_parser("list", help="List objects in source/media bucket")
    ls.add_argument("bucket", choices=["source", "media"])
    ls.add_argument("--prefix", default="")

    head = sub.add_parser("head", help="Head an object in source/media bucket")
    head.add_argument("bucket", choices=["source", "media"])
    head.add_argument("key")

    delete_object = sub.add_parser("delete-object", help="Delete an object from source/media bucket")
    delete_object.add_argument("bucket", choices=["source", "media"])
    delete_object.add_argument("key")

    audio = sub.add_parser("create-audio", help="Create an audio job")
    audio.add_argument("hash")
    audio.add_argument("key")
    audio.add_argument("--encrypt", action="store_true")
    audio.add_argument("--mp3", action="store_true")
    audio.add_argument("--flac", action="store_true")
    audio.add_argument("--waveform", action="store_true")
    audio.add_argument("--waveform-bins", type=int)
    audio.add_argument("--callback-url")

    video_tx = sub.add_parser("create-video-transcode", help="Create a video transcode job")
    video_tx.add_argument("hash")
    video_tx.add_argument("key")
    video_tx.add_argument("--encrypt", action="store_true")
    video_tx.add_argument("--callback-url")

    video_full = sub.add_parser("create-video-full", help="Create a full video workflow job")
    video_full.add_argument("hash")
    video_full.add_argument("key")
    video_full.add_argument("--encrypt", action="store_true")
    video_full.add_argument("--callback-url")

    status = sub.add_parser("job-status", help="Fetch job status")
    status.add_argument("job_id")

    retry = sub.add_parser("job-retry", help="Retry a failed job")
    retry.add_argument("job_id")

    delete_media = sub.add_parser("delete-media", help="Delete all derived media for a content hash")
    delete_media.add_argument("hash")

    health = sub.add_parser("health", help="Call /healthz")
    ready = sub.add_parser("ready", help="Call /readyz")

    img = sub.add_parser("image-url", help="Build a signed /img URL")
    img.add_argument("source_key")
    img.add_argument("output_format")
    img.add_argument("--options", default="")

    original = sub.add_parser("original-url", help="Build a signed /original URL")
    original.add_argument("source_key")

    thumb = sub.add_parser("thumb-url", help="Build a signed /thumb URL")
    thumb.add_argument("object_key")

    key = sub.add_parser("key-url", help="Build key endpoint URL and Bearer token")
    key.add_argument("key_id")
    key.add_argument("hash")
    key.add_argument("--ttl", type=int, default=3600)

    return parser


def main() -> None:
    parser = build_parser()
    args = parser.parse_args()
    settings = load_settings()
    api = VyluxAPI(settings)

    if args.command in {"upload", "list", "head", "delete-object"}:
        rustfs = RustFSClient(settings)
        if args.command == "upload":
            print_output(rustfs.upload_file(bucket_kind=args.bucket, key=args.key, file_path=args.file, content_type=args.content_type))
            return
        if args.command == "list":
            print_output(rustfs.list_objects(bucket_kind=args.bucket, prefix=args.prefix))
            return
        if args.command == "head":
            print_output(rustfs.head_object(bucket_kind=args.bucket, key=args.key))
            return
        if args.command == "delete-object":
            print_output(rustfs.delete_object(bucket_kind=args.bucket, key=args.key))
            return

    if args.command == "create-audio":
        print_output(api.create_audio_job(
            content_hash=args.hash,
            source_key=args.key,
            encrypt=args.encrypt,
            mp3=args.mp3,
            flac=args.flac,
            waveform=args.waveform,
            waveform_bins=args.waveform_bins,
            callback_url=args.callback_url,
        ).body)
        return
    if args.command == "create-video-transcode":
        print_output(api.create_video_transcode_job(content_hash=args.hash, source_key=args.key, encrypt=args.encrypt, callback_url=args.callback_url).body)
        return
    if args.command == "create-video-full":
        print_output(api.create_video_full_job(content_hash=args.hash, source_key=args.key, encrypt=args.encrypt, callback_url=args.callback_url).body)
        return
    if args.command == "job-status":
        print_output(api.get_job(args.job_id).body)
        return
    if args.command == "job-retry":
        print_output(api.retry_job(args.job_id).body)
        return
    if args.command == "delete-media":
        print_output(api.delete_media(args.hash).body)
        return
    if args.command == "health":
        print_output(api.health().body)
        return
    if args.command == "ready":
        print_output(api.ready().body)
        return
    if args.command == "image-url":
        print_output(api.build_image_url(source_key=args.source_key, output_format=args.output_format, options=args.options))
        return
    if args.command == "original-url":
        print_output(api.build_original_url(source_key=args.source_key))
        return
    if args.command == "thumb-url":
        print_output(api.build_thumb_url(object_key=args.object_key))
        return
    if args.command == "key-url":
        url, token = api.build_key_url(key_id=args.key_id, content_hash=args.hash, ttl_seconds=args.ttl)
        print_output({"url": url, "authorization": f"Bearer {token}"})
        return

    parser.error(f"unsupported command: {args.command}")


if __name__ == "__main__":
    main()
