# Recording the demo on a headless server (lake1), driven from an iPad

The whole demo is a **terminal session** — Claude Code, `ocibnkctl`, and the
k9s TUI. So the right capture tool isn't a GUI screen-grabber (there's no
display on lake1) — it's **asciinema**, which records the terminal itself.
You type `asciinema rec` inside your iPad SSH session and it captures exactly
what you see: text, colors, and TUI redraws. Then `agg` + `ffmpeg` turn the
recording into a 1080p MP4 — time-lapsed, captioned, and voiced — entirely on
lake1. Nothing leaves the server, nothing needs a screen.

```
iPad (SSH) ──▶ asciinema rec ──▶ scene*.cast ──▶ agg ──▶ gif ──▶ ffmpeg ──▶ MP4
                  (lake1, headless)                          + captions.srt + Piper vo
```

## One-time setup (already done on lake1)

```bash
sudo apt-get install -y asciinema           # 2.4.0
curl -fsSL -o ~/.local/bin/agg \
  https://github.com/asciinema/agg/releases/download/v1.5.0/agg-x86_64-unknown-linux-gnu
chmod +x ~/.local/bin/agg                    # 1.5.0
# ffmpeg is already present (/usr/bin/ffmpeg)
```

## iPad side

Any SSH client with a real terminal works — **Blink Shell** or **Termius** are
the common picks. Connect to lake1, then everything below runs in that session.
Tip: set the client's font a touch larger and keep the window ~100 columns —
that's what the viewer sees, since asciinema records the terminal geometry.

## Record — one cast per scene

Recording per scene (not one long take) lets each scene be retimed to its
narration independently. Name them `scene1.cast … scene8.cast`.

```bash
mkdir -p ~/demo/casts && cd ~/demo
asciinema rec casts/scene2.cast --idle-time-limit 2    # records a subshell
#   ... run the scene's commands (ocibnkctl doctor, etc.) ...
exit                                                   # stops recording
```

- **`--idle-time-limit 2`** is the key flag: any pause longer than 2 s is
  compressed to 2 s in the recording. That's your **automatic time-lapse** for
  the long `deploy-cne` wait in scene 5 — no manual editing of dead air.
- **Interactive Claude Code** records fine: start `claude` *inside* the recorded
  subshell and your whole agentic session is captured. (It's a full-screen TUI,
  so expect busy redraws — that's authentic to the demo.)
- Re-do a scene by just re-recording its one cast. Casts are tiny text files.

For the secrets beat (scene 4), keep the JWT/FAR contents off-screen exactly as
you would on camera — asciinema records literal terminal bytes, so a `cat` of a
secret *would* be captured. Stage `keys/` before you start recording.

## Assemble — one command, fully headless

Put the Piper voiceover (`vo/scene1.wav … scene8.wav` + `vo/vo-full.wav`, from
`toolchain-setup.md`) and `captions.srt` alongside the casts, then:

```bash
cd ~/demo
~/git/ocibnkctl/docs/video/build.sh casts vo \
   ~/git/ocibnkctl/docs/video/captions.srt demo.mp4
# ✅ demo.mp4  (~5 min, 1920x1080)
```

`build.sh` does, per scene: render the cast to GIF (`agg`), retime the video to
its narration length (`setpts`), add a 0.8 s freeze-frame gap (matching the gaps
in `vo-full.wav`), normalize to a 1920×1080 letterboxed canvas, then concat all
scenes, **burn `captions.srt`**, and **mux `vo-full.wav`**. Because each scene is
stretched to its own VO duration, the result stays in sync with the voiceover
without any editor. This produces the **tight ~3 min cut** (video length == VO
length).

### Extended cut — `build-extended.sh` (~5–6 min)

`build.sh` compresses every scene to its narration. For a longer training cut
that lets the **deploy and scenario footage breathe**, use `build-extended.sh`:
it gives each scene an explicit **TARGET** duration (edit the `TARGETS=(...)`
array). Scenes 5 (deploy) and 7 (scenarios) run well past their VO — the
narration plays at the scene's start, real footage continues after, silence pads
the audio, and captions are shifted to stay in sync. The committed defaults
(`14.6 23.9 15.8 24.2 150 16.7 75 18`) yield a **5 m 38 s** video at 1920×1080.

```bash
cd ~/demo
~/git/ocibnkctl/docs/video/build-extended.sh demo.mp4
```

### Tuning knobs

| Want | How |
|------|-----|
| Bigger/smaller text | `AGG_FONT_SIZE=32 build.sh ...` (default 28) |
| Different palette | `AGG_THEME=dracula build.sh ...` (agg themes: monokai, dracula, solarized-dark, …) |
| Re-time one scene only | re-record its `sceneN.cast`, re-run `build.sh` |
| Preview a single cast | `agg casts/scene5.cast /tmp/s5.gif && ffmpeg -i /tmp/s5.gif /tmp/s5.mp4` |

Pull the final file to the iPad (or anywhere) with `scp lake1:~/demo/demo.mp4 .`,
or serve it: `python3 -m http.server -d ~/demo 8080` and open it from the iPad.

---

## Alternative: a real virtual screen (Xvfb + VNC) — only if you need GUI fidelity

asciinema is the right tool for a terminal demo. Reach for this only if you
specifically need pixel-exact GUI capture, mouse movement, or to **watch/drive
the session live from the iPad** via a VNC app:

```bash
sudo apt-get install -y xvfb x11vnc xterm ffmpeg
Xvfb :99 -screen 0 1920x1080x24 &           # virtual display, no monitor
DISPLAY=:99 xterm -fa 'Monospace' -fs 16 &  # a real terminal on it
x11vnc -display :99 -forever -nopw -rfbport 5900 &   # iPad VNC app connects here
# record the virtual screen straight to MP4:
ffmpeg -f x11grab -video_size 1920x1080 -framerate 30 -i :99 \
       -c:v libx264 -preset veryfast -pix_fmt yuv420p screen.mp4
```

Trade-offs: heavier setup, larger files, and you drive the demo through a VNC
client (a tablet over VNC is fiddly) instead of plain SSH. For this terminal-only
demo, asciinema gives a cleaner, smaller, re-timable result — prefer it.
