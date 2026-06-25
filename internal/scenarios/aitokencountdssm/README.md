# ai-token-counting-dssm

AI token counting delivered by a **custom iRule** whose counters persist to the
cluster's built-in **DSSM/Redis**, where `tmm-stat-exporter` (or any scraper) can
read them for Prometheus/Grafana (the *TMM AI Token Usage* dashboard in
[tmmscope](https://github.com/mwiget/tmmscope)).

This is the **custom-iRule complement** to the annotation-driven
[`ai-token-counting`](../aitokencount) scenario (how-to #6). That one drives the
built-in `k8s.f5.com/ai-token-counting` Gateway annotation; this one keeps
explicit cumulative counters in an iRule `table` subtable instead. Two reasons it
earns its own scenario:

- **Durable, queryable counters.** With DSSM deployed
  (`SESSIONDB_EXTERNAL_STORAGE=true` — default on a full BNK 2.3 install) the
  iRule `table` command **persists to Redis**, so the counter is the durable
  source of truth a dashboard's day/week/month/year tiles read — no HSL receiver
  required.
- **Streaming.** It detects and counts **streaming (SSE)** responses, parsing the
  usage block from the final `usage` chunk — a path the request-scoped annotation
  doesn't surface as a persisted counter.

## Shape

- **Backend** — one shared `stub-llm` Deployment running
  [`llm-d-inference-sim`](https://github.com/llm-d/llm-d-inference-sim)
  (`ghcr.io/llm-d/llm-d-inference-sim:v0.10.0`), a **GPU-free vLLM/OpenAI API
  simulator**. A dummy `--model=gpt-stub` makes it return deterministic but
  varying `usage` blocks, and it emits real SSE when a request sets
  `"stream":true` with `stream_options.include_usage` — so both the streaming
  and non-streaming token paths are exercised without a real model. Pulled via
  `ghcr.io` (a mirrored regcache upstream).
- **4 LBs** — Gateways on `203.0.113.120-123`, each a TCP listener bound by an
  `L4Route` with `pvaAccelerationMode: disabled` (keeps the data path in TMM's
  slow/TCL path so the iRule can read raw payload). All four share the one
  `stub-llm` backend above.
- **One iRule** (`04-irule.yaml`), bound to every listener via `BNKNetPolicy`,
  parses the OpenAI usage block, detects **streaming vs non-streaming** from the
  response `Content-Type` (`text/event-stream` vs `application/json`), and
  `table incr`s cumulative `total`/`prompt`/`completion` counters keyed by
  `vs` + `mode` + `model` in the `TMMTOK` subtable (indefinite TTL, so Redis is
  the durable source the dashboard's day/week/month/year tiles read).
- **Exporter** — `tmm-stat-exporter` scans `TMMTOK` and emits
  `f5tmm_token_{total,prompt,completion}{vs,mode,model}`.

## What Verify asserts (green)

1. `stub-llm` Available; all 4 Gateways Programmed; the iRule exists.
2. FRR learns all 4 VIPs via BGP (re-triggering the OcNOS redistribute each poll
   — full BNK only injects a redistributed kernel route once the statement is
   re-issued at runtime after the VIP exists).
3. ~60s of mixed streaming/non-streaming traffic across all 4 LBs; every request
   returns a usage block.
4. DSSM is readable; all 4 LBs have counters; both modes present (mode detection
   works).
5. **Load-bearing:** the DSSM counter *delta* over the run equals the
   backend-reported usage exactly, for every LB and both modes — i.e. the iRule
   counted accurately, **streaming included**. (The delta is baselined before
   traffic, so re-runs are idempotent.)

## Notes

- The iRule maps the listener VIP to a friendly `vs` name for clean dashboard
  legends; a generic deployment can use `[virtual name]` instead.
- DSSM is part of the standard BNK install (the `f5-dssm-db` pods in `default`),
  so there's no extra deploy step. The scenario reads the counters back via
  `redis-cli` in `f5-dssm-db-0` over the DSSM mTLS certs at `/tls/dssm/mds/svr/`.
  Were DSSM ever absent, the "DSSM readable" assertion fails with a clear message.
