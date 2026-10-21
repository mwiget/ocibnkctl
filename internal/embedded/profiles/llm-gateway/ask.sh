#!/usr/bin/env bash
# ask.sh: send one prompt through the LLM gateway, as a client outside the
# cluster, and print the reply.
#
#   ./ask.sh "what is BNK?"            # model gpt-4o-mini, the demo's virtual key
#   ./ask.sh "hi" gpt-5                # a model outside the key's scope: 403
#   KEY=sk-... ./ask.sh "hi"           # another principal's virtual key
#   RAW=1 ./ask.sh "hi"                # the full JSON response
#
# The request runs in a throwaway curl container on the edge FRR's network,
# where the gateway address is reachable over BGP. Needs docker and jq; run it
# from the PoC directory after `ocibnkctl e2e --with-scenarios` (the llm-gateway
# scenario creates tenant demo, principal demo-app and its virtual key).
set -euo pipefail
cd "$(dirname "$0")"
prompt=${1:?usage: ./ask.sh \"prompt\" [model]}
model=${2:-gpt-4o-mini}
name=$(awk '/^cluster:/{c=1} c && /^ +name:/{print $2; exit}' poc.yaml)
rt=$(awk '/^cluster:/{c=1} c && /^ +provider:/{print $2; exit}' poc.yaml)
rt=${rt:-docker}
vip=${VIP:-203.0.113.140}
key=${KEY:-$(cat artifacts/llm-gateway/virtual-key 2>/dev/null || true)}
[ -n "$key" ] || { echo "no virtual key: run the llm-gateway scenario, or set KEY" >&2; exit 1; }

body=$(jq -n --arg p "$prompt" --arg m "$model" '{model: $m, messages: [{role: "user", content: $p}]}')
resp=$("$rt" run --rm --network "container:bnk-edge-frr-$name" curlimages/curl:8.10.1 \
  -s -m 120 -w '\n%{http_code}' "http://$vip/v1/chat/completions" \
  -H "Authorization: Bearer $key" -H 'Content-Type: application/json' -d "$body")
code=${resp##*$'\n'}
json=${resp%$'\n'*}

if [ -n "${RAW:-}" ]; then
  echo "$json" | jq . 2>/dev/null || echo "$json"
elif [ "$code" = 200 ]; then
  echo "$json" | jq -r '.choices[0].message.content'
  echo "$json" | jq -r '"-- \(.model), \(.usage.total_tokens) tokens" ' >&2
else
  echo "HTTP $code: $(echo "$json" | jq -r '.error.message // .' 2>/dev/null || echo "$json")" >&2
  exit 1
fi
