# `ai-inference-epp` — F5 Endpoint Picker in front of a mock vLLM pool

F5 how-to: [AI Inference Routing with the F5 Endpoint Picker](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/ai-related-features/intelligent-ai-load-balancing/ai-inference-routing-with-f5-endpoint-picker/index.html)
&nbsp;·&nbsp; Rating: 🟢 (verified on BNK 2.4.0, lake1, 2026-09-30)
&nbsp;·&nbsp; Wall time: **~30s** (with bgp-peer-frr running already)
&nbsp;·&nbsp; Depends on: [`bgp-peer-frr`](../bgppeer)
&nbsp;·&nbsp; Needs: BNK 2.4+, `bnk.tmm_dataplane_mode: anycast-bgp`

BNK 2.4 adds the F5 Endpoint Picker (EPP), F5's implementation of the
Gateway API Inference Extension (GAIE) endpoint picker. When an HTTPRoute's
`backendRef` is an `InferencePool`, TMM asks that pool's EPP, over gRPC
`ext_proc`, which pod should get each request. The EPP answers from the
model servers' metrics. You declare one `F5EPP` CR per pool, and the CNE
controller renders the EPP Deployment, Service, ConfigMap and RBAC for it.

```
FRR netns ─► 203.0.113.124 (Gateway, TMM) ─► HTTPRoute ─► InferencePool llm-pool
                      │ ext_proc :9002
                      ▼
              llm-pool-epp (rendered from F5EPP) ◄── /metrics ── mock-vllm-busy
                      │ "send it to <idle pod IP>"                mock-vllm-idle
                      ▼
              TMM forwards to mock-vllm-idle
```

## The mock vLLM

No GPU and no model. `manifests/02-mock-vllm.yaml` runs a stdlib-only Python
server (`python:3.12-alpine`) that serves:

- vLLM's Prometheus series on `:8000/metrics`, which is the scrape port
  `engine: vllm` implies. It publishes the four the 2.4.0 EPP reads (per
  its logged effective config): `vllm:num_requests_waiting`,
  `vllm:num_requests_running`, `vllm:kv_cache_usage_perc` and
  `vllm:cache_config_info`.
- `/v1/chat/completions` and `/v1/completions`, whose answer names the pod
  and its role (`ocibnkctl-epp-role=idle pod=…`).

Two Deployments share `app=mock-vllm`, so the pool contains both:

| Pod | queued | running | KV cache |
|---|---|---|---|
| `mock-vllm-busy` | 40 | 64 | 97% |
| `mock-vllm-idle` | 0 | 0 | 2% |

A balancer that ignores load would split requests between them. The test
passes only if the EPP sends **all** of them to the idle pod.

Control run: swapping the two pods' `ROLE` values moved all 10 requests to
the other pod, so the pick follows the published load, not pod name or IP.

## How the EPP picks its tokenizer (observed on 2.4.0)

- **The model comes from each request**, the OpenAI body's `model` field
  (`incomingModelName` in the EPP log), not from the pods' metrics.
- **With `spec.tokenizer` unset**, the controller renders `tokenizer.models: []`
  and the EPP autoloads each model's `tokenizer.json` from HuggingFace on its
  first request. That download can outlast the per-request timeout
  (`prepare data plugin timed out`); the request is still routed, just
  without prefix scoring. Verify therefore sends one warm-up request first.
- **Failure is silent.** `facebook/opt-125m` has no `tokenizer.json` (HTTP
  404), so every prompt "tokenizes to nothing" and prefix affinity scores
  zero while traffic keeps flowing. The EPP's counters are the only signal:
  `f5_epp_prefix_index_lookups_total{result="no_query"}` and
  `epp_tokenizer_guard_events_total{event="encode_failed"}` go up instead of
  `result="hit|miss"`. The scenario uses `Qwen/Qwen2.5-0.5B-Instruct`
  (ungated, has `tokenizer.json`) and asserts on those counters.
- Setting `spec.tokenizer.name` preloads that tokenizer at startup instead.
  The EPP logs a warning recommending exactly that when nothing is preloaded.

## What Apply does

1. Checks the `f5-epps.inference.k8s.f5.com` CRD exists, which it does on
   BNK 2.4 and later.
2. Installs the GAIE v1.4.0 CRDs (SHA-pinned in `internal/version`). BNK
   doesn't bundle them.
3. Adds `scn-epp` to `CNEInstance.spec.eppNamespaces`, keeping any existing
   entries. The CNE controller ignores F5EPPs in namespaces not listed
   there. This change makes FLO restart the controller.
4. Applies the namespace, the mock pods, the `F5EPP` (`engine: vllm`), the
   `InferencePool` (`failureMode: FailClose`, so an EPP fault fails the
   request instead of hiding behind a fallback), the Gateway and the
   HTTPRoute.

## What Verify asserts

- Both mock Deployments are Available.
- The F5EPP reports Accepted, ResolvedRefs, Programmed and Available. The
  wait allows up to 8 minutes for the controller restart and the `f5-epp`
  image pull.
- The controller rendered the `llm-pool-epp` Service with port 9002, plus
  an EPP Role and RoleBinding in `scn-epp`.
- The Gateway is Programmed.
- The HTTPRoute and InferencePool both report Accepted and ResolvedRefs.
- The external FRR learned `203.0.113.124/32` over BGP.
- All 10 sequential requests from the FRR netns get an answer, and every
  one comes from the idle pod.
- The EPP tokenized every prompt: its prefix-index hit+miss lookups rose by
  at least 10 and `encode_failed` didn't move. The scrape goes through the
  API server's service proxy, so no port-forward is needed.
- On every TMM, `tmctl inference_epp_stats` increased
  `schedule_calls` by at least 10, with `schedule_successes` equal to
  `schedule_calls` and no `schedule_fallbacks`.
- `profile_ext_proc_stat` has a `scn-epp-llm-pool-epp-ivs` row, which is
  TMM's internal virtual server for the EPP.

## Not covered

- **KV-cache events.** The mock publishes no ZMQ stream, so the EPP uses
  speculative cache tracking. Prefix affinity isn't tested.
- **Air-gapped clusters.** The tokenizer comes from HuggingFace, so the
  tokenizer assertion needs internet access. Without it the routing
  assertions still pass.
- **Other engines and resources.** Dynamo (NATS), SGLang,
  `mode: disaggregated`, and `InferenceObjective` /
  `InferenceModelRewrite` aren't tested.

## Cleanup

Cleanup deletes the F5EPP first, while the controller still has RBAC in the
namespace, then deletes `scn-epp`. It leaves two changes in place on
purpose:

- **`eppNamespaces`.** Removing `scn-epp` would restart the controller
  again, and listing a namespace that doesn't exist does no harm.
- **The GAIE CRDs.** They're cluster-scoped, and deleting them would also
  delete every InferencePool in the cluster.
