#!/usr/bin/env bash
# build.sh — assemble the demo MP4 entirely on a headless host (lake1).
#
# Inputs:
#   casts/sceneN.cast   per-scene asciinema recordings (N = 1..)
#   vo/sceneN.wav        per-scene Piper voiceover  + vo/vo-full.wav
#   captions.srt         burn-in subtitles (Whisper, hand-corrected)
#
# Each scene video is retimed (setpts) to its narration length and given a
# 0.8 s freeze-frame gap — matching the gaps baked into vo-full.wav — so the
# concatenated video lines up with the voiceover with no GUI editor.
#
# Deps: agg, ffmpeg, ffprobe, python3 — all installable/headless.
# Usage: ./build.sh [castsDir] [voDir] [captions.srt] [out.mp4]
set -euo pipefail

CASTS=${1:-casts}
VO=${2:-vo}
SRT=${3:-captions.srt}
OUT=${4:-demo.mp4}
FONT_SIZE=${AGG_FONT_SIZE:-28}
THEME=${AGG_THEME:-monokai}
GAP=0.8
FPS=30
W=1920; H=1080

command -v agg >/dev/null || { echo "need agg (asciinema/agg release binary)"; exit 1; }
command -v ffmpeg >/dev/null || { echo "need ffmpeg"; exit 1; }

WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT
dur(){ ffprobe -v error -show_entries format=duration -of csv=p=0 "$1"; }
: > "$WORK/list.txt"

n=1
while [ -f "$CASTS/scene$n.cast" ]; do
  echo "── scene $n ──"
  # 1. cast -> gif -> raw mp4
  agg --font-size "$FONT_SIZE" --theme "$THEME" "$CASTS/scene$n.cast" "$WORK/s$n.gif" >/dev/null 2>&1
  ffmpeg -y -i "$WORK/s$n.gif" -vf "scale=trunc(iw/2)*2:trunc(ih/2)*2,format=yuv420p" \
    -c:v libx264 -preset veryfast "$WORK/s$n-raw.mp4" >/dev/null 2>&1
  # 2. retime video to narration length
  vo=$(dur "$VO/scene$n.wav"); vid=$(dur "$WORK/s$n-raw.mp4")
  f=$(python3 -c "print(round($vo/$vid,5))")
  # 3. freeze-gap on every scene except the last
  gapf=""; [ -f "$CASTS/scene$((n+1)).cast" ] && gapf=",tpad=stop_mode=clone:stop_duration=$GAP"
  # 4. fit + normalize to a uniform 1920x1080 letterboxed canvas (so concat -c copy is valid)
  ffmpeg -y -i "$WORK/s$n-raw.mp4" -r $FPS -an \
    -vf "setpts=$f*PTS,scale=$W:$H:force_original_aspect_ratio=decrease,pad=$W:$H:(ow-iw)/2:(oh-ih)/2${gapf},format=yuv420p" \
    -c:v libx264 -preset veryfast "$WORK/s$n-fit.mp4" >/dev/null 2>&1
  printf "file '%s'\n" "$WORK/s$n-fit.mp4" >> "$WORK/list.txt"
  n=$((n+1))
done
[ "$n" -gt 1 ] || { echo "no $CASTS/scene1.cast found"; exit 1; }

# 5. concat the uniform segments
ffmpeg -y -f concat -safe 0 -i "$WORK/list.txt" -c copy "$WORK/video.mp4" >/dev/null 2>&1

# 6. burn captions + mux the full voiceover
ffmpeg -y -i "$WORK/video.mp4" -i "$VO/vo-full.wav" \
  -vf "subtitles=$SRT:force_style='FontName=DejaVu Sans Mono,FontSize=20,Outline=1,Shadow=1'" \
  -map 0:v -map 1:a -c:v libx264 -preset medium -crf 20 -c:a aac -b:a 192k -shortest "$OUT" >/dev/null 2>&1

echo "✅ $OUT  ($(dur "$OUT")s, ${W}x${H})"
