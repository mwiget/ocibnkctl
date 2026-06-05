# Live-session demo video — production kit

This is the pipeline behind the **~3 min training video** that shows
`ocibnkctl` deploying F5 BIG-IP Next for Kubernetes by driving a **real,
live Claude Code session on a local model** — recorded headless on a server,
no screen, no GUI editor.

▶ **Watch it on YouTube: https://youtu.be/uUyO17K6r5M**

The video is a real Claude Code session (Claude Code pointed at a local
`qwen3-coder` model via `ollama launch claude`), driven over `tmux send-keys`
and recorded with `asciinema`, then assembled entirely with `ffmpeg`/`agg` +
headless `playwright`. See [`../recording-on-lake1.md`](../recording-on-lake1.md)
for the base asciinema capture mechanism.

> Paths in these scripts are hard-coded to the original capture session
> (`~/demo-rec`, `~/piper`, `full2.cast`, the recorded beat timestamps). They
> document the exact pipeline used; adapt the paths/boundaries to re-run.

## Pieces

| File | What it does |
|------|--------------|
| `narration/scene{1..12}.txt` | TTS **input** for the voiceover. Voiced by **Kokoro-82M** (`af_heart`). Says **"the agent"** for the deploying agent; the Claude Code brand stays on the captions/slides/TUI. Phonetic spellings carried over from the Piper era and still read cleanly: `O C I bink cuttle`, `pock` (PoC), `Flo` (FLO), `F. Five` (F5), `big I P` (BIG-IP). |
| `gen-kokoro.py` | Kokoro TTS **generator**: reads `narration/scene*.txt`, synthesizes each on the GPU with `af_heart`, writes 24 kHz wavs (the build step resamples to 22050/mono/16-bit). Swap the voice via its first arg. |
| `render-slides.js` | playwright → 8 full-screen chapter slides (1920×1080 PNG): narrated **title** (F5 BNK 2.3.0 + agentic), 6 chapter cards, and a **closing** card pointing to `github.com/mwiget/ocibnkctl`. |
| `render-banners.js` | playwright → transparent **"prompt banner"** PNGs overlaid at the top of each beat, highlighting exactly what the user typed (`YOU ❯ …` / `COMMAND $ …`). |
| `bnkforge-capture.js` | playwright → screenshots of the local **bnk-forge** UI (auto-registered project, K8s dashboard, F5 BNK health) over its API-authenticated login. |
| `bnkforge-capture-trafficflow.js` | playwright → the **F5 BNK ▸ Insights ▸ Traffic Flow** view (captured *after* the scenario suite populates gateways/routes). |
| `build-session3.sh` | the assembler. Slices the continuous `full2.cast` into per-beat regions by their cast timestamps, time-compresses each to its narration length (`agg --speed` + `setpts`), overlays its prompt banner, interleaves the chapter slides, splices the bnk-forge UI as a crossfade still segment, builds the VO track (silence over slides), generates shifted burn-in captions, and muxes it all. |

## Flow

```
record live Claude session (tmux + asciinema)         → full2.cast
Kokoro af_heart narration      (narration/*.txt)      → vo-kokoro/scene*.wav
chapter slides + prompt banners (playwright)          → slides/*.png
bnk-forge UI screenshots        (playwright)           → uishot/*.png
                       │
                       ▼
              build-session3.sh   →  demo3-final.mp4  (1920×1080, h264+aac)
```

## Tools

Everything ran **locally and headless** — no GUI editor, no cloud TTS, no
cloud LLM (the model is local), no external services.

### Production (making the video)

| Tool | Version | Role |
|------|---------|------|
| **tmux** | 3.4 | Hosts the Claude Code TUI; driven non-interactively via `send-keys` |
| **asciinema** | 2.4.0 | Records the terminal session → `.cast` (text, deterministic) |
| **agg** | 1.5.0 | Renders the cast → GIF frames (`--speed` time-compression, monokai theme) |
| **ffmpeg** | 6.1.1 | Whole assembly: gif→mp4, `setpts` retiming, `concat`, banner `overlay`, slide `xfade` crossfades, `subtitles` caption burn-in, audio mux |
| **Kokoro-82M** | hexgrad/Kokoro (Apache-2.0) | Local neural TTS for the voiceover — runs on the GPU via torch+CUDA, far more natural than the earlier Piper track |
| **af_heart** | — | The Kokoro narrator voice model (US English, female) |
| **Whisper** | openai-whisper (small.en) | Pronunciation verification (the "F. Five", "big I-P" testing) |
| **Playwright + Chromium** | npm | Renders chapter slides + "YOU ❯" prompt banners (HTML→PNG) **and** screenshots the bnk-forge UI |
| **Node.js** | v24 | Runs Playwright |
| **Python 3** | 3.12 | Cast slicing, audio-track building (`wave`), caption SRT generation, amplitude checks |

### Content (what's shown *in* the video)

| Tool | Role |
|------|------|
| **ocibnkctl** | The CLI being demoed (init → deploy → scenarios → destroy) |
| **Claude Code** | 2.1.165 — the agent driving the deployment (the subject) |
| **ollama** | 0.24.0 — serves the local model + `ollama launch claude` wires Claude Code to it |
| **qwen3-coder** | The local model that drove Claude Code |
| **Docker / k3s / kubectl** | Container runtime, cluster, and the commands the agent ran |
| **bnk-forge** | The local app whose F5 BNK / Traffic Flow UI was screenshotted |
