# Live-session demo video — production kit

This is the pipeline behind the **~3.5 min training video** that shows
`ocibnkctl` deploying F5 BIG-IP Next for Kubernetes by driving a **real,
live Claude Code session on a local model** — recorded headless on a server,
no screen, no GUI editor.

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
| `narration/scene{1..12}.txt` | Piper TTS **input** for the voiceover. Voiced by `en_US-amy-medium`. Says **"the agent"** (Piper can't reliably pronounce "Claude" — it drifts to "cloud"); the brand stays on the captions/slides/TUI. Phonetic spellings: `O C I bink cuttle`, `pock` (PoC), `Flo` (FLO), `F. Five` (F5), `big I P` (BIG-IP). |
| `render-slides.js` | playwright → 8 full-screen chapter slides (1920×1080 PNG): narrated **title** (F5 BNK 2.3.0 + agentic), 6 chapter cards, and a **closing** card pointing to `github.com/mwiget/ocibnkctl`. |
| `render-banners.js` | playwright → transparent **"prompt banner"** PNGs overlaid at the top of each beat, highlighting exactly what the user typed (`YOU ❯ …` / `COMMAND $ …`). |
| `bnkforge-capture.js` | playwright → screenshots of the local **bnk-forge** UI (auto-registered project, K8s dashboard, F5 BNK health) over its API-authenticated login. |
| `bnkforge-capture-trafficflow.js` | playwright → the **F5 BNK ▸ Insights ▸ Traffic Flow** view (captured *after* the scenario suite populates gateways/routes). |
| `build-session3.sh` | the assembler. Slices the continuous `full2.cast` into per-beat regions by their cast timestamps, time-compresses each to its narration length (`agg --speed` + `setpts`), overlays its prompt banner, interleaves the chapter slides, splices the bnk-forge UI as a crossfade still segment, builds the VO track (silence over slides), generates shifted burn-in captions, and muxes it all. |

## Flow

```
record live Claude session (tmux + asciinema)         → full2.cast
Piper amy narration            (narration/*.txt)      → vo3/scene*.wav
chapter slides + prompt banners (playwright)          → slides/*.png
bnk-forge UI screenshots        (playwright)           → uishot/*.png
                       │
                       ▼
              build-session3.sh   →  demo3-final.mp4  (1920×1080, h264+aac)
```

## Dependencies

- `asciinema` + `agg` (terminal capture/render) — see `../recording-on-lake1.md`
- `ffmpeg` (assembly), `python3` (cast slicing, audio/caption generation)
- `piper` + `en_US-amy-medium` voice (TTS)
- `node` + `playwright` chromium (slides, banners, bnk-forge UI shots)
- a running local `ollama` with a tool-capable model (`qwen3-coder`) for the
  live Claude Code session
