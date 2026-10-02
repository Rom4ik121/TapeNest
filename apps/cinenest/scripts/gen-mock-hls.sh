#!/usr/bin/env bash
# DEV ONLY: regenerate the 60 s test HLS stream used by the MSW cinema mocks.
set -euo pipefail
out="$(cd "$(dirname "$0")/.." && pwd)/dev-assets/mock-hls"
mkdir -p "$out" && cd "$out" && rm -f ./*.ts index.m3u8
ffmpeg -loglevel error -y -f lavfi -i "testsrc2=size=854x480:rate=24" \
  -f lavfi -i "sine=frequency=330:sample_rate=44100" -t 60 \
  -vf "drawtext=text='CineNest dev stream %{pts\\:hms}':x=24:y=h-60:fontsize=28:fontcolor=0xE7E4DE:box=1:boxcolor=0x16141C@0.6:boxborderw=10" \
  -c:v libx264 -preset veryfast -profile:v main -pix_fmt yuv420p -b:v 350k -maxrate 400k -bufsize 800k -g 48 \
  -c:a aac -b:a 64k -hls_time 6 -hls_playlist_type vod -hls_segment_filename 'seg%02d.ts' index.m3u8
echo "mock HLS written to $out"
