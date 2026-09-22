#!/usr/bin/env python3
"""Assemble the v2 (BNK 2.4.0) demo video from one live take.

Inputs — none of them live in this repo:
  casts/full.cast   one continuous asciinema recording of the Claude Code session
                    (tmux-driven, local model), plus casts/k9s.cast
  vo/scene*.wav     narration in Marcel's cloned voice, from
                    tmm-lb-nico/docs/video/kit/gen-narration.py --part ocibnkctl
  slides/*.png      build_video_slides.py (F5 template, git-ignored)
  bnkscope.webm     tmm-lb-nico kit capture-bnkscope.js --part 2 --cluster demo

Every segment is timed to its own narration (LEAD + VO + GAP). A segment's
visual is one or more parts sharing that time by weight; a cast part plays its
region fast and then HOLDS its last frame (the kit's pacing rule: a beat's
payoff is its final output, so it must stay on screen long enough to read).

    ./build-video.py --rec ~/demo-rec/v2 \
        --vo ~/src/tmm-lb-nico/docs/video/kit/build/vo/partocibnkctl
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import tempfile
import wave

HERE = os.path.dirname(os.path.abspath(__file__))
W, H, FPS = 1920, 1080, 30
LEAD, GAP = 1.0, 0.9
FONT = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf"

# Region boundaries are cast timestamps (see casts/beats.log and the text
# search in the README). A part: (kind, spec, weight, hold, banner)
SEGMENTS = [
    dict(vo=["scene01"], parts=[("slide", "title", 1, 0, None)]),
    dict(vo=["scene02"], parts=[("slide", "one-host", 1, 0, None)]),
    dict(vo=["scene03"], parts=[
        ("cast", ("full", 0.0, 16.4), 1, 3.5,
         "$ ocibnkctl init demo   ·   $ claude   (local model: qwen3.8-27b on llama.cpp)")]),
    dict(vo=["scene04"], extra=5, parts=[
        ("cast", ("full", 16.5, 122.4), 1, 3.0,
         "YOU > Read AGENTS.md and poc.yaml, then run ocibnkctl doctor ..."),
        ("cast", ("full", 122.5, 282.9), 1, 3.0,
         "YOU > The two F5 license files are in ~/license ... never print their contents"),
    ]),
    dict(vo=["scene05"], extra=8, parts=[
        ("cast", ("full", 283.0, 400.0), 3, 2.5,
         "YOU > Go ahead and deploy BNK end to end. I confirm the cluster name: demo."),
        ("cast", ("full", 400.0, 530.0), 3, 2.5, None),
        ("cast", ("full", 530.0, 905.0), 2.5, 3.0, None),
        ("cast", ("k9s", 0.0, 1e9), 2.5, 2.5, "$ k9s -A"),
    ]),
    dict(vo=["scene06"], extra=7, parts=[
        ("slide", "scenarios", 10, 0, None),
        ("cast", ("full", 972.7, 1532.6), 9, 4.0, "YOU > Yes, run all the green scenarios."),
        ("cast", ("full", 1532.7, 1669.4), 8, 4.0,
         "YOU > Where do the reports live? Open the http-routing-e2e report ..."),
    ]),
    # the capture's video opens on a loading page; its two settled stills are the claim
    dict(vo=["scene07"], parts=[("slide", "@bnkscope/01-bnk-health.png", 1, 0, None),
                                ("slide", "@bnkscope/02-traffic-flow.png", 1.2, 0, None)]),
    dict(vo=["scene08"], extra=3, parts=[
        ("cast", ("full", 1669.5, 1718.0), 1, 3.5,
         "YOU > We're done. Tear it all down. I confirm the cluster name: demo.")]),
    dict(vo=["scene09"], parts=[("slide", "close", 1, 0, None)], gap=2.0),
]


def run(*cmd):
    subprocess.run(cmd, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def dur(path):
    out = subprocess.run(["ffprobe", "-v", "error", "-show_entries", "format=duration",
                          "-of", "csv=p=0", path], capture_output=True, text=True, check=True)
    return float(out.stdout.strip())


def wav_dur(path):
    with wave.open(path) as w:
        return w.getnframes() / w.getframerate()


FIT = f"scale={W}:{H}:force_original_aspect_ratio=decrease,pad={W}:{H}:(ow-iw)/2:(oh-ih)/2:color=0x272822,format=yuv420p"


def enc(inp_args, vf, out, t=None):
    cmd = ["ffmpeg", "-y", *inp_args, "-vf", vf, "-r", str(FPS), "-an",
           "-c:v", "libx264", "-preset", "veryfast", "-crf", "18"]
    if t:
        cmd += ["-t", f"{t:.3f}"]
    run(*cmd, out)


# Claude Code draws a few glyphs no installed monospace font carries; they
# render as tofu. Swap them for look-alikes the font does have.
GLYPHS = str.maketrans({"\u23bf": "\u2514", "\u23f5": "\u25b8", "\u23f8": "\u2016", "\u23fa": "\u25cf"})


def slice_cast(src, a, b, out):
    lines = open(src).read().translate(GLYPHS).splitlines()
    res = [lines[0]]
    for l in lines[1:]:
        e = json.loads(l)
        if a <= e[0] < b:
            res.append(json.dumps([round(e[0] - a, 6), e[1], e[2]]))
    # replay everything before `a` instantly, so the screen state is right
    pre = [json.loads(l) for l in lines[1:] if json.loads(l)[0] < a]
    if pre:
        res.insert(1, json.dumps([0.0, "o", "".join(e[2] for e in pre if e[1] == "o")]))
    open(out, "w").write("\n".join(res) + "\n")


def banner_filter(text, work, i):
    if not text:
        return ""
    tf = os.path.join(work, f"banner{i}.txt")
    open(tf, "w").write(text)
    return (f",drawtext=fontfile={FONT}:textfile={tf}:fontsize=30:fontcolor=white:"
            f"box=1:boxcolor=0x0F142A@0.92:boxborderw=18:x=(w-tw)/2:y=36:enable='lt(t,3.8)'")


def build_part(kind, spec, target, hold, banner, a, work, i):
    out = os.path.join(work, f"p{i:02d}.mp4")
    if kind == "slide":
        png = (os.path.join(a.rec, spec[1:]) if spec.startswith("@")
               else os.path.join(a.slides, f"{spec}.png"))
        fo = max(0.0, target - 0.35)
        enc(["-loop", "1", "-t", f"{target:.3f}", "-i", png],
            f"scale={W}:{H},format=yuv420p,fade=t=in:st=0:d=0.35,fade=t=out:st={fo:.3f}:d=0.35", out)
        return out
    if kind == "video":
        src = a.bnkscope
        f = target / dur(src)
        enc(["-i", src], f"setpts={f:.6f}*PTS,{FIT}", out, target)
        return out
    # cast region: agg -> gif -> exact retime to (target - hold) -> freeze `hold`
    name, t0, t1 = spec
    src = os.path.join(a.rec, "casts", f"{name}.cast")
    rc = os.path.join(work, f"r{i}.cast")
    slice_cast(src, t0, t1, rc)
    gif = os.path.join(work, f"r{i}.gif")
    font = "20" if name == "k9s" else "24"
    run("agg", "--idle-time-limit", "999", "--speed", "4", "--font-size", font,
        "--theme", "monokai", "--last-frame-duration", "0.1", rc, gif)
    raw = os.path.join(work, f"r{i}.mp4")
    enc(["-i", gif], "scale=trunc(iw/2)*2:trunc(ih/2)*2,format=yuv420p", raw)
    play = max(0.5, target - hold)
    f = play / dur(raw)
    vf = f"setpts={f:.6f}*PTS,{FIT},tpad=stop_mode=clone:stop_duration={hold + 1:.3f}"
    vf += banner_filter(banner, work, i)
    enc(["-i", raw], vf, out, target)
    return out


def srt_time(t):
    ms = int(round(t * 1000))
    return f"{ms // 3600000:02d}:{ms // 60000 % 60:02d}:{ms // 1000 % 60:02d},{ms % 1000:03d}"


# Captions show what is SAID, but written the way it is read, not respelled.
SPOKEN_TO_WRITTEN = [
    ("O-C-I bink cuttle", "ocibnkctl"), ("m-wiget", "mwiget"), ("T-M-M", "TMM"),
    ("C-N-E", "CNE"), ("k-nine-s", "k9s"), ("k-three-d", "k3d"), ("g-R-P-C", "gRPC"),
    ("kube control", "kubectl"), ("kube config", "kubeconfig"), ("llama dot cpp", "llama.cpp"),
    ("Flo chart", "FLO chart"),
]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--rec", default=os.path.expanduser("~/demo-rec/v2"))
    ap.add_argument("--vo", default=os.path.expanduser(
        "~/src/tmm-lb-nico/docs/video/kit/build/vo/partocibnkctl"))
    ap.add_argument("--narration", default=os.path.join(HERE, "narration"))
    ap.add_argument("--slides", default=os.path.join(HERE, "build", "slides"))
    ap.add_argument("--bnkscope", default=os.path.expanduser(
        "~/demo-rec/v2/bnkscope/walkthrough-part2.webm"))
    ap.add_argument("--out", default=os.path.join(HERE, "build", "ocibnkctl-bnk-2.4.0.mp4"))
    a = ap.parse_args()

    work = tempfile.mkdtemp(prefix="ocivid-")
    parts, audio_events, captions = [], [], []
    t = 0.0
    rate = None
    for si, seg in enumerate(SEGMENTS):
        vo = [os.path.join(a.vo, f"{s}.wav") for s in seg["vo"]]
        vdur = sum(wav_dur(v) for v in vo)
        total = LEAD + vdur + seg.get("gap", GAP) + seg.get("extra", 0)
        weights = sum(p[2] for p in seg["parts"])
        for pi, (kind, spec, wgt, hold, banner) in enumerate(seg["parts"]):
            pt = total * wgt / weights
            parts.append(build_part(kind, spec, pt, hold, banner, a, work, len(parts)))
            print(f"  seg{si + 1} part{pi + 1} {kind:5} {pt:6.2f}s")
        st = t + LEAD
        for v, s in zip(vo, seg["vo"]):
            audio_events.append((st, v))
            text = open(os.path.join(a.narration, f"{s}.txt")).read().strip()
            for k, rpl in SPOKEN_TO_WRITTEN:
                text = text.replace(k, rpl)
            sents = [x for x in re.split(r"(?<=[.!?])\s+", text) if x]
            d = wav_dur(v)
            chars = sum(len(x) for x in sents)
            c0 = st
            for x in sents:
                c1 = c0 + d * len(x) / chars
                captions.append((c0, c1, x))
                c0 = c1
            st += d
        t += total

    lst = os.path.join(work, "list.txt")
    open(lst, "w").write("".join(f"file '{p}'\n" for p in parts))
    video = os.path.join(work, "video.mp4")
    run("ffmpeg", "-y", "-f", "concat", "-safe", "0", "-i", lst, "-c", "copy", video)
    vlen = dur(video)

    # soundtrack: silence with each narration wav dropped in at its start time
    with wave.open(audio_events[0][1]) as w:
        rate, sw, ch = w.getframerate(), w.getsampwidth(), w.getnchannels()
    buf = bytearray(int(vlen * rate) * sw * ch)
    for st, v in audio_events:
        with wave.open(v) as w:
            assert (w.getframerate(), w.getsampwidth(), w.getnchannels()) == (rate, sw, ch), v
            data = w.readframes(w.getnframes())
        off = int(st * rate) * sw * ch
        buf[off:off + len(data)] = data[:max(0, len(buf) - off)]
    aud = os.path.join(work, "audio.wav")
    with wave.open(aud, "wb") as w:
        w.setnchannels(ch); w.setsampwidth(sw); w.setframerate(rate)
        w.writeframes(bytes(buf))

    os.makedirs(os.path.dirname(a.out), exist_ok=True)
    run("ffmpeg", "-y", "-i", video, "-i", aud, "-c:v", "copy", "-c:a", "aac", "-b:a", "160k",
        "-shortest", "-movflags", "+faststart", a.out)
    srt = os.path.splitext(a.out)[0] + ".srt"
    with open(srt, "w") as f:
        for n, (c0, c1, x) in enumerate(captions, 1):
            f.write(f"{n}\n{srt_time(c0)} --> {srt_time(c1)}\n{x}\n\n")
    shutil.rmtree(work)
    print(f"✅ {a.out}  ({vlen:.1f}s = {int(vlen // 60)}:{int(vlen % 60):02d})  + {os.path.basename(srt)}")


if __name__ == "__main__":
    main()
