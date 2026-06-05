#!/usr/bin/env bash
# v3: chapter slides + per-beat prompt banners + amy "the agent" VO.
set -euo pipefail
CAST=$HOME/demo-rec/full2.cast
VODIR=$HOME/piper/vo3
SL=$HOME/demo-rec/slides
STILLS=$HOME/demo-rec/bnkforge-stills.mp4
OUT=${1:-$HOME/demo-rec/demo3-final.mp4}
FS=26; THEME=monokai; W=1920; H=1080; BDUR=4.0
WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT
dur(){ ffprobe -v error -show_entries format=duration -of csv=p=0 "$1"; }
FR=22050

# scene VO durations (vo3)
declare -A SD
for n in $(seq 1 12); do SD[$n]=$(python3 -c "import wave;w=wave.open('$VODIR/scene$n.wav');print(round(w.getnframes()/w.getframerate(),3))"); done

mkslide(){ # png dur -> $WORK/$3
  ffmpeg -y -loop 1 -t "$2" -i "$1" -vf "scale=$W:$H,format=yuv420p,fade=t=in:st=0:d=0.3,fade=t=out:st=$(python3 -c "print(round($2-0.3,2))"):d=0.3" \
    -r 30 -c:v libx264 -preset veryfast -an "$WORK/$3" >/dev/null 2>&1; }

mkregion(){ # a b target banner-png(or "") outname
  local a=$1 b=$2 tgt=$3 bn=$4 out=$5
  python3 - "$CAST" "$a" "$b" "$WORK/_r.cast" <<'PY'
import json,sys
cast,t0,t1,out=sys.argv[1],float(sys.argv[2]),float(sys.argv[3]),sys.argv[4]
L=[l for l in open(cast) if l.strip()]; res=[L[0].rstrip()]
for l in L[1:]:
    e=json.loads(l)
    if isinstance(e,list) and t0<=e[0]<t1: res.append(json.dumps([round(e[0]-t0,6),e[1],e[2]]))
open(out,'w').write('\n'.join(res)+'\n')
PY
  local A spd raw f
  A=$(python3 -c "print(round($b-$a,3))"); spd=$(python3 -c "print(round($A/$tgt,5))")
  agg --idle-time-limit 99 --speed "$spd" --font-size $FS --theme $THEME "$WORK/_r.cast" "$WORK/_r.gif" >/dev/null 2>&1
  # crop the bottom tmux status row out, then normalize dims
  ffmpeg -y -i "$WORK/_r.gif" -vf "crop=iw:ih-34:0:0,scale=trunc(iw/2)*2:trunc(ih/2)*2,format=yuv420p" -c:v libx264 -preset veryfast "$WORK/_rraw.mp4" >/dev/null 2>&1
  raw=$(dur "$WORK/_rraw.mp4"); f=$(python3 -c "print(round($tgt/$raw,6))")
  ffmpeg -y -i "$WORK/_rraw.mp4" -r 30 -an -vf "setpts=$f*PTS,scale=$W:$H:force_original_aspect_ratio=decrease,pad=$W:$H:(ow-iw)/2:(oh-ih)/2,format=yuv420p" \
    -c:v libx264 -preset veryfast "$WORK/_rfit.mp4" >/dev/null 2>&1
  if [ -n "$bn" ]; then
    ffmpeg -y -i "$WORK/_rfit.mp4" -i "$bn" -filter_complex "[1]scale=1480:-1[b];[0][b]overlay=(W-w)/2:42:enable='lt(t,$BDUR)',format=yuv420p" \
      -r 30 -c:v libx264 -preset veryfast -an "$out" >/dev/null 2>&1
  else cp "$WORK/_rfit.mp4" "$out"; fi
}

echo "── slides ──"
mkslide "$SL/title.png" "${SD[1]}" s_title.mp4   # narrated title (scene 1)
for c in 1 2 3 4 5 6; do mkslide "$SL/ch$c.png" 2.8 s_ch$c.mp4; done
mkslide "$SL/close.png" 4.5 s_close.mp4

echo "── beats (with prompt banners) ──"
mkregion 0      39     "$(python3 -c "print(${SD[2]}+${SD[3]})")" "$SL/b0.png" "$WORK/R0.mp4"
mkregion 39     84.1   "${SD[4]}"  "$SL/b1.png" "$WORK/R1.mp4"
mkregion 84.1   128.3  "${SD[5]}"  "$SL/b2.png" "$WORK/R2.mp4"
mkregion 128.3  280    "${SD[6]}"  "$SL/b3.png" "$WORK/R3.mp4"
mkregion 1336.9 1392.2 "${SD[7]}"  "$SL/b4.png" "$WORK/R4.mp4"
mkregion 1392.2 1460.2 "${SD[8]}"  "$SL/b5.png" "$WORK/R5.mp4"
mkregion 1460.2 2110   "${SD[9]}"  "$SL/b6.png" "$WORK/R6.mp4"
mkregion 2127.6 2170.7 "${SD[10]}" "$SL/b7.png" "$WORK/R7.mp4"
# stills (R8) retimed to s11
sf=$(python3 -c "print(round(${SD[11]}/$(dur "$STILLS"),6))")
ffmpeg -y -i "$STILLS" -r 30 -an -vf "setpts=$sf*PTS,scale=$W:$H:force_original_aspect_ratio=decrease,pad=$W:$H:(ow-iw)/2:(oh-ih)/2,format=yuv420p" -c:v libx264 -preset veryfast "$WORK/R8.mp4" >/dev/null 2>&1
mkregion 2170.7 2222.8 "${SD[12]}" "$SL/b9.png" "$WORK/R9.mp4"

echo "── concat (sequence with slides) ──"
SEQ=(s_title.mp4 s_ch1.mp4 R0.mp4 s_ch2.mp4 R1.mp4 R2.mp4 R3.mp4 s_ch3.mp4 R4.mp4 R5.mp4 s_ch4.mp4 R6.mp4 R7.mp4 s_ch5.mp4 R8.mp4 s_ch6.mp4 R9.mp4 s_close.mp4)
: > "$WORK/list.txt"; for f in "${SEQ[@]}"; do echo "file '$WORK/$f'" >> "$WORK/list.txt"; done
ffmpeg -y -f concat -safe 0 -i "$WORK/list.txt" -c copy "$WORK/video.mp4" >/dev/null 2>&1

echo "── master audio (silence over slides, VO over beats) ──"
python3 - "$VODIR" "$WORK/audio.wav" <<'PY'
import wave,sys
vodir,out=sys.argv[1],sys.argv[2]
FR=22050
def sil(sec): return b'\x00\x00'*int(sec*FR)
def scene(n):
    w=wave.open(f"{vodir}/scene{n}.wav",'rb'); d=w.readframes(w.getnframes()); w.close(); return d
# sequence audio: (slide durations) silence; (region) scene wavs
seq=[('sc',[1]),('sil',2.8),('sc',[2,3]),('sil',2.8),('sc',[4]),('sc',[5]),('sc',[6]),
     ('sil',2.8),('sc',[7]),('sc',[8]),('sil',2.8),('sc',[9]),('sc',[10]),('sil',2.8),
     ('sc',[11]),('sil',2.8),('sc',[12]),('sil',4.5)]
o=wave.open(out,'wb'); o.setnchannels(1); o.setsampwidth(2); o.setframerate(FR)
for kind,v in seq:
    if kind=='sil': o.writeframes(sil(v))
    else:
        for n in v: o.writeframes(scene(n))
o.close()
print('  audio', round(wave.open(out,'rb').getnframes()/FR,1),'s')
PY

echo "── captions ──"
python3 - "$WORK/caps.srt" <<'PY'
import sys,re,wave
out=sys.argv[1]; vodir="/home/mwiget/piper/vo3"
def sd(n): w=wave.open(f"{vodir}/scene{n}.wav");return w.getnframes()/w.getframerate()
SD={n:sd(n) for n in range(1,13)}
disp={
1:"This is F5 BIG-IP Next for Kubernetes, deployed on plain Docker. Driven entirely by Claude Code, running a local model. No cloud. No Kubernetes installed yet.",
2:"First, one command. The ocibnkctl binary scaffolds a self-contained PoC folder. It holds the deployment's state, an operator guide for the agent, and the license keys.",
3:"Now we launch Claude Code inside that folder, pointed at a local model through ollama. Nothing leaves the machine. Then we tell it what we want.",
4:"The agent reads the guide, then runs the doctor check. Docker, kubectl, helm, the CPU and memory floor. All verified. The host is ready; two license secrets are still needed.",
5:"We point Claude at the F5 license files: the activation token and the image-pull key. It installs them without printing their contents, and re-runs validate. The PoC is ready.",
6:"One prompt kicks off the whole pipeline. Claude runs the end-to-end deploy: a two-node k3s cluster as Docker containers, cert-manager, the F5 FLO chart, then the CNE instance with TMM in demo mode.",
7:"With the stack up, we ask Claude to list every pod across all namespaces. About thirty of them, the full F5 control plane and data plane, across two nodes.",
8:"When something goes wrong, here a pod stuck Pending, the agent doesn't guess. It inspects the events and logs, finds the cause, and explains it.",
9:"To prove it works, Claude runs the full scenario suite, each mapping to an F5 how-to. Twelve of twelve pass green.",
10:"Every run writes a detailed report. We ask Claude to open one. It walks through a scenario's results: the reconciled state and the data path it verified.",
11:"Because a local bnk-forge is running, the cluster auto-registers there. Its F5 BIG-IP dashboard shows the platform healthy, and the traffic flow from every scenario: gateways, routes, backends.",
12:"And when you're done, one more prompt tears it all down: cluster, containers, and your kubeconfig restored. A full F5 BIG-IP Next deployment on plain Docker, driven end to end by Claude Code on a local model.",
}
# absolute scene start times by walking the sequence
seq=[('sc',[1]),('sil',2.8),('sc',[2,3]),('sil',2.8),('sc',[4]),('sc',[5]),('sc',[6]),
     ('sil',2.8),('sc',[7]),('sc',[8]),('sil',2.8),('sc',[9]),('sc',[10]),('sil',2.8),
     ('sc',[11]),('sil',2.8),('sc',[12]),('sil',4.5)]
starts={}; t=0.0
for kind,v in seq:
    if kind=='sil': t+=v
    else:
        for n in v: starts[n]=t; t+=SD[n]
def chunks(tx):
    parts=re.split(r'(?<=[.;:,])\s+',tx); o=[]
    for p in parts:
        wds=p.split()
        while len(wds)>9: o.append(' '.join(wds[:9])); wds=wds[9:]
        if wds: o.append(' '.join(wds))
    return o
def ts(x):
    ms=round((x-int(x))*1000); s=int(x); return f"{s//3600:02d}:{(s%3600)//60:02d}:{s%60:02d},{ms:03d}"
srt=[]; idx=1
for n in range(1,13):
    if n==1: continue  # scene1 narrates the title slide; no burned caption
    cs=chunks(disp[n]); tot=sum(len(c) for c in cs); st=starts[n]
    for c in cs:
        d=SD[n]*len(c)/tot; srt.append(f"{idx}\n{ts(st)} --> {ts(st+d)}\n{c}"); idx+=1; st+=d
open(out,'w').write('\n\n'.join(srt)+'\n'); print('  ',idx-1,'cues')
PY

echo "── burn + mux ──"
ffmpeg -y -i "$WORK/video.mp4" -i "$WORK/audio.wav" \
  -vf "subtitles=$WORK/caps.srt:force_style='FontName=DejaVu Sans Mono,FontSize=18,Outline=1,Shadow=1,MarginV=18'" \
  -map 0:v -map 1:a -c:v libx264 -preset medium -crf 20 -c:a aac -b:a 192k -shortest "$OUT" >/dev/null 2>&1
echo "✅ $OUT  ($(dur "$OUT")s, ${W}x${H})"
