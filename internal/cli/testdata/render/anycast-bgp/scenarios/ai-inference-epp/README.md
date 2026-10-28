# ai-inference-epp

AI Inference Routing with the F5 Endpoint Picker — F5EPP + GAIE InferencePool, mock vLLM

- Rating: **green**
- Depends on: `bgp-peer-frr` (apply those first)

## What it does

BNK 2.4's F5 Endpoint Picker: TMM consults a per-InferencePool EPP over
gRPC ext_proc before forwarding each request, and the EPP picks the
backend pod from its vLLM metrics (queue depth, running requests, KV-cache
use, prefix affinity).

Setup (the F5 doc's prerequisites, done by Apply):
  - GAIE v1.4.0 CRDs (InferencePool etc. — not bundled with BNK)
  - scn-epp added to CNEInstance.spec.eppNamespaces
  - two mock vLLM pods in one pool: "busy" (40 queued, KV cache 97%)
    and "idle" (nothing queued, KV cache 2%)
  - F5EPP (engine: vllm) → InferencePool (FailClose) → HTTPRoute on a
    Gateway at 203.0.113.124

Verify: the F5EPP reaches Available, the controller-rendered EPP
Deployment/Service exist, InferencePool and HTTPRoute resolve, and
10 requests from the external FRR all land on the idle pod, with
TMM's inference_epp_stats showing one successful consult per request.

Not covered: real KV-cache events (the mock publishes no ZMQ stream, so
the picker uses speculative cache tracking), prefix-affinity scoring,
Dynamo/SGLang engines, InferenceObjective/InferenceModelRewrite.

Also checks the EPP tokenized every prompt (its prefix-index counters), since
a failed tokenizer load is otherwise silent. The requests name
Qwen/Qwen2.5-0.5B-Instruct, whose tokenizer the EPP downloads from HuggingFace, so
the cluster needs internet access.

## Manifests

- `01-namespace.yaml`
- `02-mock-vllm.yaml`
- `03-f5epp.yaml`
- `04-inferencepool.yaml`
- `05-gateway.yaml`
- `06-httproute.yaml`

Not every file is applied unconditionally by `ocibnkctl scenario run`; check the
description above for ordering and waits between steps.
