# ai-semantic-cache

Semantic AI Model Caching (how-to #7) — k8s.f5.com/ai semantic_cache + sse-enabled

- Rating: **green**
- Depends on: `bgp-peer-frr` (apply those first)

## What it does

Demonstrates BNK's semantic-cache annotation pattern. On the
Gateway:

    k8s.f5.com/ai: |
      semantic_cache=enabled,
      semantic_cache_ip_port=<IP>:<port>,
      semantic_cache_recv_timeout=1000

…and on the HTTPRoute:

    k8s.f5.com/sse-enabled: "true"

(SSE is paired with semantic caching because LLM completions
commonly stream via Server-Sent Events.)

Rated AMBER. Real CodeFuse-ModelCache is out of scope on kind —
it needs persistent vector storage, an embedding model, and a
working ML stack. The scenario uses a TCP-listener stub at port
5050 to stand in for the ModelCache endpoint. TMM will dial it
on each request, get a clean TCP accept but no useful response,
fall through to the stub LLM as a "cache miss". The control-
plane wiring (Gateway + HTTPRoute with both annotations) is
what the verify step actually exercises.

Lifting this to green would require:
  - deploying real CodeFuse-ModelCache + its vector backend
  - deploying a real LLM (NIM/vLLM) as the cache-miss path
  - sending two identical prompts and observing one HIT, one
    MISS in TMM's data-plane telemetry

Cleanup deletes the scn-semcache namespace.

## Manifests

- `01-namespace.yaml`
- `03-stubs.yaml`
- `04-gateway.yaml.tmpl`
- `05-httproute.yaml`

Not every file is applied unconditionally by `ocibnkctl scenario run`; check the
description above for ordering and waits between steps.

`04-gateway.yaml.tmpl` holds `{{.Field}}` placeholders that `ocibnkctl` fills from live cluster
state at apply time (e.g. a Service ClusterIP); substitute them by hand. `kubectl apply -f`
on the directory skips `.tmpl` files.
