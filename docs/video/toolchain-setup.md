# ocibnkctl demo video — toolchain setup (Ubuntu 24.04)

> **Capturing from an iPad / no local desktop?** You don't need this two-machine
> split. See **[`recording-on-lake1.md`](recording-on-lake1.md)** for the
> fully headless path: record the SSH terminal with `asciinema` on lake1 and
> assemble the MP4 with `agg` + `ffmpeg` + `build.sh` — captions and Piper VO
> included, no GUI editor. The OBS/Kdenlive flow below is the desktop
> alternative when you want pixel-level GUI control.

Two machines, by necessity:

- **lake1 (headless, 28 cores)** — runs `ocibnkctl`, the k3s cluster, and the
  **CLI audio tools** (Piper TTS, Whisper captions). No GUI here.
- **Your local desktop** — runs the **screen capture** (OBS) and **editor**
  (Kdenlive). You SSH into lake1; the terminal and k9s (a TUI) render locally,
  so capturing your local terminal *is* capturing the demo. The cluster being
  remote doesn't matter — nothing visual lives on lake1.

> Rule of thumb: anything with a window → local desktop. Anything headless and
> CPU-heavy (TTS render, Whisper transcription) → run on lake1.

---

## 1. Capture + edit (local desktop)

```bash
# Debian/Ubuntu desktop
sudo apt update
sudo apt install -y obs-studio kdenlive audacity ffmpeg
# (macOS desktop instead:  brew install --cask obs kdenlive ; brew install ffmpeg)
```

**OBS settings for crisp terminal video**
- Settings → Video: Base & Output both **1920×1080**, FPS **30**.
- Settings → Output → Recording: format **mkv** (crash-safe; remux to mp4
  after), encoder **x264**, rate control **CRF**, CRF **18**, preset
  `veryfast`, profile `high`.
- Scene: one **Window Capture** of your terminal, plus an optional **Audio
  Input Capture** only if you ever want a live take (we're using Piper, so you
  can leave the mic muted).
- Terminal: 16–18 pt, high-contrast theme, ~100 columns. Bump it before you
  record — re-recording for legibility is the #1 redo.

**Tip:** record one continuous take per scene into its own `.mkv`. Easier to
re-shoot a single scene than to scrub a 20-minute monolith.

---

## 2. Piper TTS — US-English voiceover (run on lake1)

Piper is a fast, local neural TTS. No cloud, no API key. Install the binary +
one US-English voice:

```bash
# on lake1
mkdir -p ~/piper && cd ~/piper
# binary (x86_64)
curl -L -o piper_amd64.tar.gz \
  https://github.com/rhasspy/piper/releases/download/2023.11.14-2/piper_linux_x86_64.tar.gz
tar xzf piper_amd64.tar.gz          # → ./piper/piper

# a US-English voice (medium quality; ryan = male, amy = female)
mkdir -p voices && cd voices
BASE=https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US
curl -L -O $BASE/ryan/medium/en_US-ryan-medium.onnx
curl -L -O $BASE/ryan/medium/en_US-ryan-medium.onnx.json
# (swap ryan→amy for the female voice)
```

**Render one narration block → WAV:**

```bash
cd ~/piper
echo "This host has nothing installed but Docker. No Kubernetes. No F5 tooling." \
 | ./piper/piper --model voices/en_US-ryan-medium.onnx \
                 --output_file vo-scene1.wav
```

**Render the whole script in one pass.** Put each scene's narration in its own
`.txt` (scene1.txt … scene8.txt — copy from `shooting-script.md`), then:

```bash
cd ~/piper
for f in scene*.txt; do
  ./piper/piper --model voices/en_US-ryan-medium.onnx \
                --output_file "vo-${f%.txt}.wav" < "$f"
  echo "rendered vo-${f%.txt}.wav"
done
```

**Pacing knobs** (Piper CLI flags): `--length_scale 1.1` slows speech ~10 %
(good for a deliberate training tone); `--sentence_silence 0.4` inserts the
`[PAUSE]` gaps automatically between sentences. Example deliberate render:

```bash
./piper/piper --model voices/en_US-ryan-medium.onnx \
  --length_scale 1.08 --sentence_silence 0.35 \
  --output_file vo-scene5.wav < scene5.txt
```

Pull the WAVs to your desktop for editing: `scp lake1:~/piper/vo-*.wav .`

---

## 3. Captions with Whisper (run on lake1)

Transcribe the final mixed VO (or each WAV) to an SRT for burn-in:

```bash
# on lake1 — openai-whisper (Python) is simplest
pipx install openai-whisper        # or: pip install -U openai-whisper
whisper vo-full.wav --language en --model small --output_format srt
# → vo-full.srt  (review timings; TTS is clean so accuracy is high)
```

Faster/leaner alternative: `whisper.cpp` with the `base.en` model if you prefer
a C++ build with no Python.

---

## 4. Assembly (Kdenlive, local desktop)

1. New 1080p30 project. Drop the scene `.mkv` clips on the video track in order.
2. Drop `vo-sceneN.wav` on an audio track; nudge each to its scene's first frame.
3. **Time-lapse scene 5:** select the deploy clip → Speed → e.g. 2000 % (15 min
   → ~45 s). Do the same lighter ramp (300–500 %) on the scenario run in scene 7.
4. Add zoom/keyframe pushes on the callout lines (doctor hint, root-cause line).
5. Lower-thirds for each `ocibnkctl` subcommand; title + end cards.
6. Add the `.srt` as a subtitle track; style and **render to burn in**.
7. Export → MP4 (H.264), 1080p30, ~12 Mbps. Audio AAC 192 kbps.

**One-liner remux** if you'd rather assemble with ffmpeg than re-encode:

```bash
ffmpeg -i scene.mkv -c copy scene.mp4                      # crash-safe remux
ffmpeg -i video.mp4 -i vo-full.wav -c:v copy -c:a aac \
       -shortest final.mp4                                  # mux VO onto video
```

---

## 5. Audio polish (optional, Audacity, local)

Piper output is clean, but for broadcast feel: **Effect → Loudness
Normalization → −16 LUFS**, then a gentle **Noise Gate** if any room tone leaked
in from a live take. Export as `vo-full.wav` for Whisper + the editor.

---

## Quick reference — who runs what

| Task | Machine | Tool |
|------|---------|------|
| Screen + k9s capture | local desktop | OBS Studio |
| Voiceover render | lake1 | Piper |
| Caption transcript | lake1 | Whisper |
| Edit / time-lapse / titles | local desktop | Kdenlive |
| Audio loudness polish | local desktop | Audacity |
| Remux / mux | either | ffmpeg |
