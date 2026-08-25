#!/bin/sh
set -eu

descriptor="$1"
master=''
prev=''
for arg in "$@"; do
  if [ "$prev" = '--hls_master_playlist_output' ]; then
    master="$arg"
  fi
  prev="$arg"
done

init=$(printf '%s' "$descriptor" | tr ',' '\n' | sed -n 's/^init_segment=//p')
playlist=$(printf '%s' "$descriptor" | tr ',' '\n' | sed -n 's/^playlist_name=//p')
segment_template=$(printf '%s' "$descriptor" | tr ',' '\n' | sed -n 's/^segment_template=//p')

mkdir -p "$(dirname "$master")" "$(dirname "$init")" "$(dirname "$playlist")" "$(dirname "$segment_template")"
printf '#EXTM3U\n' > "$master"
: > "$init"
printf '#EXTM3U\n' > "$playlist"
segment=$(printf '%s' "$segment_template" | sed 's/\$Number\$/1/g')
: > "$segment"
