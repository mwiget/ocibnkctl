# ai-token-counting-dssm

AI Token Counting via custom iRule + DSSM/Redis — 4 LBs, streaming + non-streaming, persisted for tmm-stat-exporter

- Rating: **green**
- Depends on: `bgp-peer-frr` (apply those first)

## What it does

Deliver AI token counting with a custom iRule whose counters persist to the
built-in DSSM/Redis — the complement to the annotation-driven ai-token-counting
scenario, and the path that also counts STREAMING responses and persists durably
to Redis.

Four LBs (Gateways with an HTTP listener on 203.0.113.120-123) share one
llm-d-inference-sim backend. One F5BigCneIrule (04-irule.yaml), bound to each
HTTP listener via NetPolicy, fires on HTTP_RESPONSE / HTTP_RESPONSE_DATA,
parses the OpenAI usage block, detects streaming vs non-streaming from the
response Content-Type, and table-incr's cumulative total/prompt/completion
counters keyed by vs + mode + model (an L4Route would yield a FastL4 VS that
never runs the payload iRule). With DSSM up (SESSIONDB_EXTERNAL_STORAGE=true, default on full BNK) the
table persists to Redis, where tmm-stat-exporter can emit
f5tmm_token_*{vs,mode,model} for a dashboard.

Verify drives ~60s of mixed streaming/non-streaming traffic across all four LBs,
then reads the counters back out of DSSM and HARD-ASSERTS they equal the
backend-reported usage for every LB and both modes. Cleanup deletes the
namespace.

## Manifests

- `01-namespace.yaml`
- `03-backend.yaml`
- `04-irule.yaml`
- `05-lb-chat.yaml`
- `06-lb-code.yaml`
- `07-lb-embed.yaml`
- `08-lb-rag.yaml`

Not every file is applied unconditionally by `ocibnkctl scenario run`; check the
description above for ordering and waits between steps.
