#!/usr/bin/env bash
# Assemble a 5+ min cut: each scene plays for a chosen TARGET duration (not just
# its narration length). The action scenes (5 deploy, 7 scenarios) run long so
# the real footage breathes; VO sits at each scene's start, silence pads the
# rest, and captions are shifted to match.
set -euo pipefail
CASTS=casts; VO=vo
SRT_IN=$HOME/git/ocibnkctl/docs/video/captions.srt
OUT=${1:-demo5.mp4}
FS=28; THEME=monokai; W=1920; H=1080
#          s1   s2   s3   s4   s5    s6   s7   s8
TARGETS=( 14.6 23.9 15.8 24.2 150  16.7 75   18.0 )
WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT
dur(){ ffprobe -v error -show_entries format=duration -of csv=p=0 "$1"; }

echo "── rendering + retiming scenes ──"
: > "$WORK/list.txt"
for i in 0 1 2 3 4 5 6 7; do
  n=$((i+1)); t=${TARGETS[$i]}
  agg --font-size $FS --theme $THEME "$CASTS/scene$n.cast" "$WORK/s$n.gif" >/dev/null 2>&1
  ffmpeg -y -i "$WORK/s$n.gif" -vf "scale=trunc(iw/2)*2:trunc(ih/2)*2,format=yuv420p" \
    -c:v libx264 -preset veryfast "$WORK/s$n-raw.mp4" >/dev/null 2>&1
  vid=$(dur "$WORK/s$n-raw.mp4"); f=$(python3 -c "print(round($t/$vid,5))")
  ffmpeg -y -i "$WORK/s$n-raw.mp4" -r 30 -an \
    -vf "setpts=$f*PTS,scale=$W:$H:force_original_aspect_ratio=decrease,pad=$W:$H:(ow-iw)/2:(oh-ih)/2,format=yuv420p" \
    -c:v libx264 -preset veryfast "$WORK/s$n-fit.mp4" >/dev/null 2>&1
  printf "file '%s'\n" "$WORK/s$n-fit.mp4" >> "$WORK/list.txt"
  echo "  scene$n -> ${t}s"
done
ffmpeg -y -f concat -safe 0 -i "$WORK/list.txt" -c copy "$WORK/video.mp4" >/dev/null 2>&1

echo "── building padded audio + shifted captions ──"
python3 - "$VO" "$SRT_IN" "$WORK/audio.wav" "$WORK/shift.srt" "${TARGETS[@]}" <<'PY'
import sys, wave
vo_dir, srt_in, audio_out, srt_out = sys.argv[1:5]
targets=[float(x) for x in sys.argv[5:]]; N=len(targets); GAP=0.8; FR=22050
def wdur(p):
    w=wave.open(p,'rb'); d=w.getnframes()/w.getframerate(); w.close(); return d
vo=[wdur(f"{vo_dir}/scene{i+1}.wav") for i in range(N)]
orig=[0.0]; new=[0.0]
for i in range(1,N):
    orig.append(orig[-1]+vo[i-1]+GAP)
    new.append(new[-1]+targets[i-1])
# audio: each scene's VO then silence to fill its target
out=wave.open(audio_out,'wb'); out.setnchannels(1); out.setsampwidth(2); out.setframerate(FR)
for i in range(N):
    w=wave.open(f"{vo_dir}/scene{i+1}.wav",'rb'); out.writeframes(w.readframes(w.getnframes())); w.close()
    pad=targets[i]-vo[i]
    if pad>0: out.writeframes(b'\x00\x00'*int(pad*FR))
out.close()
# captions: shift each cue by (new_start - orig_start) of its scene
def ts2s(t):
    h,m,r=t.split(':'); s,ms=r.split(','); return int(h)*3600+int(m)*60+int(s)+int(ms)/1000
def s2ts(x):
    ms=round((x-int(x))*1000); s=int(x); return f"{s//3600:02d}:{(s%3600)//60:02d}:{s%60:02d},{ms:03d}"
def scene_of(t):
    idx=0
    for i in range(N):
        if orig[i]<=t+1e-6: idx=i
    return idx
res=[]
for b in open(srt_in).read().strip().split('\n\n'):
    L=b.split('\n'); a,bb=[x.strip() for x in L[1].split('-->')]
    sa,sb=ts2s(a),ts2s(bb); sh=new[scene_of(sa)]-orig[scene_of(sa)]
    res.append(f"{L[0]}\n{s2ts(sa+sh)} --> {s2ts(sb+sh)}\n"+'\n'.join(L[2:]))
open(srt_out,'w').write('\n\n'.join(res)+'\n')
print(f"  total {sum(targets):.0f}s, {len(res)} caption cues")
PY

echo "── burning captions + muxing audio ──"
ffmpeg -y -i "$WORK/video.mp4" -i "$WORK/audio.wav" \
  -vf "subtitles=$WORK/shift.srt:force_style='FontName=DejaVu Sans Mono,FontSize=20,Outline=1,Shadow=1'" \
  -map 0:v -map 1:a -c:v libx264 -preset medium -crf 20 -c:a aac -b:a 192k -shortest "$OUT" >/dev/null 2>&1
echo "✅ $OUT  ($(dur "$OUT")s, ${W}x${H})"
