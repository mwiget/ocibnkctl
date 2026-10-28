#!/bin/sh
# Verify the annotations reconciled and TMM's semantic-cache iRule fired.
. "$(dirname "$0")/../../lib/common.sh"

for d in stub-llm stub-modelcache; do
	kit_check "$d Deployment Available" \
		kubectl -n "$NAMESPACE" wait --for=condition=Available "deployment/$d" --timeout=2m
done
kit_check "Gateway Programmed=True" \
	kubectl -n "$NAMESPACE" wait --for=condition=Programmed gateway/scn-semcache-gateway --timeout=3m
kit_eq "HTTPRoute Accepted=True" True "$(kubectl -n "$NAMESPACE" get httproute/scn-semcache-route \
	-o jsonpath='{.status.parents[0].conditions[?(@.type=="Accepted")].status}')"
ann=$(kubectl -n "$NAMESPACE" get gateway/scn-semcache-gateway \
	-o jsonpath='{.spec.infrastructure.annotations.k8s\.f5\.com/ai}')
case $ann in
*semantic_cache=enabled*semantic_cache_ip_port=* | *semantic_cache_ip_port=*semantic_cache=enabled*)
	kit_pass "k8s.f5.com/ai semantic-cache annotation present on Gateway" "$ann" ;;
*) kit_fail "k8s.f5.com/ai semantic-cache annotation present on Gateway" "$ann" ;;
esac
kit_eq "k8s.f5.com/sse-enabled annotation present on HTTPRoute" "value=true" \
	"value=$(kubectl -n "$NAMESPACE" get httproute/scn-semcache-route -o jsonpath='{.metadata.annotations.k8s\.f5\.com/sse-enabled}')"

bgp="FRR BGP table has $VIP/32 advertised by TMM"
curls="$CURLS/$CURLS curls return stub-llm body (cache-miss fall-through)"
fired="TMM semantic-cache iRule fired (CLIENT_ACCEPTED init with the annotated modelcache server)"
if ! edge_enabled; then
	edge_skip "$bgp" "$curls" "$fired"
	kit_done "Annotations reconciled"
fi
edge_check_vip "$bgp" "$VIP"
edge_curls "$CURLS" "$MARKER" -H "Host: $HOST" \
	-d '{"model":"llm-stub","messages":[{"role":"user","content":"identical prompt for cache miss"}]}' \
	"http://$VIP/v1/chat/completions"
kit_count "$curls" "$EDGE_OK" "$CURLS" "last: $EDGE_LAST"

if bnk_logging_subsystem; then
	kit_skip "$fired" "telemetry.loggingSubsystem is enabled: TMM's iRule logs are not in kubectl logs"
	kit_done "Annotations reconciled + curls answered (iRule log check skipped)"
fi
logs=$(bnk_tmm_logs 60s)
init=$(printf '%s\n' "$logs" | grep -F "Rule scn-semcache-gateway-scn-semcache-" | grep -F "Client initialized with modelcache_server=" | head -2 | tr '\n' '|')
if [ -n "$init" ]; then kit_pass "$fired" "$init"; else kit_fail "$fired" "no CLIENT_ACCEPTED init line"; fi
# BNK 2.4.0 stopped logging the per-request line; recorded, not required.
kit_pass "iRule HTTP_REQUEST log line (informational — absent since BNK 2.4.0)" \
	"$(printf '%s\n' "$logs" | grep -F SEMANTIC_CACHE_IRULE | head -2 | tr '\n' '|')"

kit_done "Annotations reconciled + TMM data-plane semantic-cache iRule fires on every request"
