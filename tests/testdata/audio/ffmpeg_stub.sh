#!/bin/sh
set -eu

last=''
for arg in "$@"; do
  last="$arg"
done

prev=''
waveform=0
for arg in "$@"; do
  if [ "$prev" = '-f' ] && [ "$arg" = 'f32le' ]; then
    waveform=1
  fi
  prev="$arg"
done

if [ "$waveform" -eq 1 ] && [ "$last" = '-' ]; then
  cat "$VYLUX_WAVEFORM_DATA"
  exit 0
fi

case "$last" in
  *.mp3|*.flac|*.mp4)
    mkdir -p "$(dirname "$last")"
    printf 'stub-media' > "$last"
    ;;
esac
