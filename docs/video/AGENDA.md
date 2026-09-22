# ocibnkctl demo video v2 — agenda & shooting plan (BNK 2.4.0)

> **Status: outline approved 2026-09-22** — local model, ~3:00, no TMM scale-out, Pi as a slide mention, amber scenarios out, published unlisted.
>
> **Published:** [youtu.be/iZQ2l1kQ1UQ](https://youtu.be/iZQ2l1kQ1UQ) (unlisted, 4:05).
>
> Replaces the June 2026 cut ([youtu.be/uUyO17K6r5M](https://youtu.be/uUyO17K6r5M),
> ~3 min, BNK 2.3.0, Kokoro `af_heart`, bnk-forge). That cut stays live and
> gets listed as *superseded* in the README, the way `tmm-lb-nico/docs/video/AGENDA.md`
> keeps its older cuts.

**Purpose.** Show that a full F5 BIG-IP Next for Kubernetes 2.4.0 stack runs on
plain Docker on one host, is deployed and exercised by an AI agent through one
CLI, and proves itself against F5's own how-to articles.

**What changed since the June cut, and has to show on screen**

| June cut (2.3.0) | This cut (2.4.0) |
|---|---|
| BNK 2.3.0, F5BnkGateway model | BNK 2.4.0, Gateway API network model |
| BNK pods also scheduled on the control node | TMM on a dedicated, labelled agent node; `scale --tmm N` adds more |
| 12 green scenarios | **15 green** scenarios, plus 2 amber (tenant VLAN routing templates) |
| bnk-forge auto-registration | **bnkscope** — reads `~/.kube/config` and picks the cluster up by itself |
| — | automatic `deploy shrink` on tight hosts (10 CPU / 16 GB Docker VM, Raspberry Pi 5) |
| Kokoro `af_heart` voice | **Marcel's own voice**, F5-TTS cloned from `tmm-lb-nico/docs/video/kit/voice/marcel.wav` |
| HTML chapter cards | slides built on the **F5 FY26 template**, in the style of `tmm-lb-nico/docs/nico-per-tenant-lb-teaser.pptx` |

**Format.** One chapter, about 3:00. A real, live agent session driven over
`tmux send-keys` and recorded with asciinema, the same way as the June cut. Every chapter
opens on a deck slide, so the live terminal always lands against a claim that has already been stated.
No captions: they cover the terminal and slide content.

---

## Running order (~3:00)

| # | Chapter | Slide | On screen | ~sec |
|---|---------|-------|-----------|------|
| — | **Title** | template **Title Slide: Black Solid** — "F5 BIG-IP Next for Kubernetes 2.4.0 · on plain Docker, driven by an agent on a local model" | — | 6 |
| 1 | **Why this exists** | *NEW* "BNK on one host" — one Go binary, k3s nodes as Docker/podman containers, no kind/k3d, no DPU/SR-IOV; floor 10 CPU / 16 GB, and down to a 4-core Raspberry Pi 5 via automatic `deploy shrink` (**Pi is a slide mention only**) | — | 18 |
| 2 | **Scaffold & launch the agent** | — | `ocibnkctl init demo`, glance at `poc.yaml` / `AGENTS.md` / `keys/`; launch Claude Code on local `qwen3-coder` via `ollama launch claude`; one goal prompt | 18 |
| 3 | **Doctor & secrets** | — | agent runs `doctor`; JWT + FAR copied into `keys/` without printing them; `validate` passes | 22 |
| 4 | **Deploy 2.4.0** | — | `ocibnkctl e2e --yolo --confirm-cluster demo`: phases 1–4 real speed, **time-lapse only the CNE reconcile**, land on k9s with TMM on its dedicated agent node; timings re-measured on 2.4.0 | 38 |
| 5 | **Prove it — 15 how-tos** | *NEW* "What the scenarios prove" — the 15 green scenarios mapped to F5 how-tos, grouped: Gateway API routing · BGP · AI · operations (**amber scenarios left out**) | `scenario run --all` speed-ramped → **15/15 green**; agent opens one report | 38 |
| 6 | **One pane of glass** | — | **bnkscope**: auto-detected from kubeconfig → BNK Health → Traffic Flow populated by the scenario run | 22 |
| 7 | **Tear it down** | — | `destroy` → containers + network gone, `~/.kube/config` restored from backup | 14 |
| — | **Close** | template close slide: `github.com/mwiget/ocibnkctl` · release download | — | 6 |

**Narration spine.** "This host has nothing on it but Docker. One binary and
one agent take it to a full F5 BIG-IP Next 2.4.0 stack. It then proves the stack
against fifteen of F5's own how-to articles, and hands it back clean."

**Dropped from the June cut**
- *bnk-forge integration.* bnk-forge is retired, and bnkscope replaces it.
- *The staged "pod stuck Pending" diagnosis.* It stays out unless a **real** fault shows up during recording.
  If one does, it goes in between chapters 5 and 6 and is not faked
  (shooting-script rule).

---

## Production — reuse the tmm-lb-nico kit, don't fork it

| Piece | Source | Notes |
|---|---|---|
| Voice | `tmm-lb-nico/docs/video/kit/gen-narration.py --engine f5`, `voice/marcel.{wav,txt}` (the 2026-09-02 recording) | GPU on lake1 (RTX PRO 5000), `~/.venvs/f5tts` exists. Fixed seed, trimmed edges, and `LEAD`/`GAP` pacing, because pacing feedback on the part-1 slide lectures was "too fast" |
| Pronunciation | `cues.txt` respellings: `O C I bink cuttle`, `pock`, `Flo`, `F. Five`, `big I P`, `T M M`, `k three s` | check with a Whisper pass, as in June |
| Slides | *NEW* `docs/video/build_video_slides.py`, reusing the teaser deck's helpers (`_slide_title`, `_chip`, `_card`, `_code`) → LibreOffice → pdftoppm 1920×1080 | **The template is internal and this repo is public.** The script reads `--template ~/git/F5-Template-Starter-FY26-Q2.potx`, and both the `.pptx` and the PNGs land in a git-ignored `build/` |
| Terminal | tmux + asciinema + agg, prompt banners (`YOU ❯` / `COMMAND $`) | the `fast` pacing rule: hold finished output ≥4 s or half the line, whichever is longer |
| UI | `capture-bnkscope.js` adapted from the tmm-lb-nico kit | captured **after** the scenario run, so Traffic Flow is populated |
| Assembly | `build-session3.sh`-style retime + concat, sidecar `.srt` | |

## Decisions (2026-09-22)

- Agent: Claude Code on local `qwen3-coder` via ollama — nothing leaves the machine.
- Length ~4:00 after review (slower voice, more room per beat); TMM scale-out dropped.
- Raspberry Pi 5: mentioned on slide 1 only (no re-run).
- Amber tenant-VLAN scenarios: left out.
- Publish unlisted; README switches to the new link and lists the June cut as superseded.
