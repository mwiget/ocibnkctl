# ocibnkctl — demo/training video shooting script

**Working title:** *Deploy F5 BIG-IP Next for Kubernetes on plain Docker — driven by Claude Code*
**Target length:** ~5:00 (tight cut)
**Voiceover:** US English, local Piper TTS (`en_US-amy` or `en_US-ryan`, medium)
**Aspect/res:** 1920×1080, 30 fps
**Style:** terminal-forward. Claude Code drives; the host stays "just Docker."

> **Secret-safety rule (non-negotiable):** the JWT (TEEM token) and the FAR
> tarball are customer secrets. In scene 4 use an **expired/throwaway** token,
> never reveal contents on camera, and redact any accidental reveal in post.
> `keys/` is gitignored for a reason — keep it off the recording.

---

## Timing map (tight 5-min cut)

| Scene | Title | Target | Screen action | Editor note |
|------:|-------|:------:|---------------|-------------|
| 1 | Cold open | 0:00–0:25 | empty host, `docker ps` | logo sting + title card |
| 2 | Bootstrap + doctor self-heal | 0:25–1:10 | `make install`, `doctor` finds a gap, Claude offers the fix | zoom on the install hint |
| 3 | Init the PoC | 1:10–1:40 | `ocibnkctl init demo`, tour `poc.yaml`/`AGENTS.md` | split-screen file tree |
| 4 | The secrets beat | 1:40–2:30 | deploy blocks on empty `keys/`; Claude explains JWT + FAR, portal path, drops keys | **redact**; lower-third "license portal" |
| 5 | Deploy | 2:30–3:30 | `ocibnkctl e2e --yolo --confirm-cluster demo`; pods come up in k9s | fast phases real-speed; **time-lapse only the CNE wait** |
| 6 | Agentic troubleshoot | 3:30–4:05 | a Pending pod; Claude reads `doctor` + logs, names the cause | zoom on diagnosis line |
| 7 | Validate + browse | 4:05–4:40 | `scenario run --all` → 12/12 green; k9s tour via `~/.kube/config` | speed-ramp the scenario run |
| 8 | Teardown + outro | 4:40–5:00 | `ocibnkctl destroy` reverts cleanly | end card + repo URL |

---

## Full narration (Piper input)

Each block is one Piper render. Keep sentences short — TTS phrasing is cleaner
with periods than commas. `[PAUSE]` = insert 400 ms silence in the editor (or a
trailing `. . .`). Numbers in **bold** are on-screen callouts to sync to.

### Scene 1 — Cold open (0:00–0:25)
> This host has nothing installed but Docker. No Kubernetes. No F5 tooling.
> [PAUSE] In the next five minutes, Claude Code will deploy a full F5 BIG-IP
> Next for Kubernetes stack — and fix the problems it hits along the way.

*Screen:* clean prompt → `docker ps` (empty) → title card.

### Scene 2 — Bootstrap + doctor self-heal (0:25–1:10)
> One Go binary drives everything. We build and install it...
> [PAUSE] ...then ask it to check the host. `ocibnkctl doctor` inspects the
> runtime, kubectl, helm, and the CPU and memory floor. Here it finds a missing
> tool — and prints the exact, operating-system-aware command to install it.
> Claude offers to run that for us. This is the pattern for the whole demo:
> the tool reports the truth, the agent acts on it.

*Screen:* `make install` → `ocibnkctl doctor`; highlight a red check + its install hint; Claude offers to run it.

### Scene 3 — Init the PoC (1:10–1:40)
> Every deployment lives in a self-contained folder called a PoC.
> `ocibnkctl init demo` scaffolds it. Inside: `poc.yaml`, the single source of
> truth; `AGENTS.md`, the operating manual Claude reads first; and a gitignored
> `keys` folder for secrets. The agent always reads `poc.yaml` before it acts.

*Screen:* `ocibnkctl init demo`, then `ls`/tree; open `poc.yaml` and `AGENTS.md` briefly.

### Scene 4 — The secrets beat (1:40–2:30)
> The deploy needs two customer secrets from F5's license portal. A TEEM
> activation token — the JWT — and a FAR image-pull tarball for the F5 registry.
> [PAUSE] Without them, the deploy stops and tells us exactly what's missing.
> Claude explains where each one comes from, and we drop them into the keys
> folder. We never print their contents — they stay out of the journal and out
> of every report.

*Screen:* attempt deploy → it blocks; Claude's explanation; **redacted** drag of `f5-far-auth-key.tgz` + `.jwt` into `keys/`; re-run `validate` → passes.

### Scene 5 — Deploy (2:30–3:30)
> Now the payload. One command runs the whole pipeline: validate, bring up a
> two-node k3s cluster directly as Docker containers, install cert-manager and
> the F5 FLO chart, then the CNE instance with TMM in demo mode.
> [PAUSE] The first four phases finish in about a minute. The last one — the F5
> control plane reconciling — takes a few minutes more, which we've sped up
> here. With a warm image cache the whole deploy is about five and a half
> minutes; the first run, pulling images, is closer to fifteen. Watch the pods
> schedule and turn ready in k9s — thirty of them, across two nodes. No kind,
> no k3d, no third-party orchestrator. Just Docker.

*Screen:* `ocibnkctl e2e --yolo --confirm-cluster demo` — let phases 1–4 play at
real speed (~75 s combined), then cut to k9s and **time-lapse only the
`deploy-cne` reconcile** (~4 min → ~30 s). End on `f5-cne-core` all-Running.

> **Measured (warm cache, lake1):** validate 0s · cluster-up 32s · prereqs 19s ·
> flo 23s · **cne 4m8s** · total **5m24s** → 30 pods, 29 Running, 2 nodes Ready.

### Scene 6 — Agentic troubleshoot (3:30–4:05)
> Real deployments hit snags. Here a pod is stuck Pending. Instead of guessing,
> Claude runs `doctor` and reads the pod's events and logs, then names the
> cause in plain language and proposes the fix. The agent isn't just typing
> commands — it's diagnosing.

*Screen:* a Pending pod; Claude runs `doctor` + `kubectl describe`/logs; highlight the one-line root cause.

### Scene 7 — Validate + browse (4:05–4:40)
> To prove it works, we run the scenario suite — each one maps to an F5 how-to
> article. Twelve of twelve pass green. And because `cluster up` installed the
> kubeconfig for us, kubectl and k9s talk to the cluster directly — browse the
> whole F5 stack live.

*Screen:* `ocibnkctl scenario run --all` (speed-ramped) → 12/12 green; quick k9s tour of namespaces.

### Scene 8 — Teardown + outro (4:40–5:00)
> One command tears it all down — containers, network, and it even restores your
> original kubeconfig from backup. [PAUSE] A full F5 BIG-IP Next for Kubernetes
> deployment, on plain Docker, driven end to end by Claude Code. The tool is on
> GitHub at mwiget slash ocibnkctl.

*Screen:* `ocibnkctl destroy --yolo --confirm-cluster demo`; end card with repo URL.

---

## Per-scene shot checklist (for the recording session)

- [ ] Terminal at 16–18 pt, high-contrast theme, 1080p-safe width (~100 cols).
- [ ] Hide secrets: pre-stage `keys/` off-camera; use an expired JWT for scene 4.
- [ ] Capture k9s in its own take so you can time-lapse it independently.
- [ ] Record the real e2e once for B-roll; keep the timestamped log for pacing.
- [ ] Grab a clean `doctor` "all green" take *and* a "one missing tool" take.
- [ ] For scene 6, reproduce one real, explainable failure (e.g. a Pending pod
      before storage settles) — don't fake the diagnosis.

## Asset list to hand the editor

- `screen-*.mkv` — OBS captures per scene (or one long capture + cut points).
- `vo-sceneN.wav` — Piper renders, one per narration block.
- `captions.srt` — Whisper transcript of the final VO mix.
- Title card + end card (1920×1080 PNG, repo URL + F5/Claude marks).
- Optional: low-volume royalty-free music bed (−24 dB under VO).
