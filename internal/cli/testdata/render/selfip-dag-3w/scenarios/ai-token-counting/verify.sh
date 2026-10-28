#!/bin/sh
# Verify the annotation reconciled and TMM's generated iRule counted tokens.
. "$(dirname "$0")/../../lib/common.sh"

kit_check "stub-llm Deployment Available" \
	kubectl -n "$NAMESPACE" wait --for=condition=Available deployment/stub-llm --timeout=2m
kit_check "Gateway Programmed=True" \
	kubectl -n "$NAMESPACE" wait --for=condition=Programmed gateway/scn-tokencount-gateway --timeout=3m
kit_eq "HTTPRoute Accepted=True" True "$(kubectl -n "$NAMESPACE" get httproute/scn-tokencount-route \
	-o jsonpath='{.status.parents[0].conditions[?(@.type=="Accepted")].status}')"

ann=$(kubectl -n "$NAMESPACE" get gateway/scn-tokencount-gateway \
	-o jsonpath='{.spec.infrastructure.annotations.k8s\.f5\.com/ai-token-counting}')
case $ann in
*token_counting=enabled*user_id_source=api_key* | *user_id_source=api_key*token_counting=enabled*)
	kit_pass "k8s.f5.com/ai-token-counting annotation present on Gateway" "$ann" ;;
*) kit_fail "k8s.f5.com/ai-token-counting annotation present on Gateway" "$ann" ;;
esac

bgp="FRR BGP table has $VIP/32 advertised by TMM"
curls="$CURLS/$CURLS curls through Gateway return stub-llm body"
fired="TMM token-counting iRule fired (TOKEN(...) cumulative log lines)"
if ! edge_enabled; then
	edge_skip "$bgp" "$curls" "$fired"
	kit_done "Gateway annotation reconciled"
fi
edge_check_vip "$bgp" "$VIP"

edge_curls "$CURLS" "$MARKER" -H "Authorization: Bearer ocibnkctl-test-user" -H "Host: $HOST" \
	-d '{"model":"gpt-stub","messages":[{"role":"user","content":"ping"}]}' \
	"http://$VIP:$PORT/v1/chat/completions"
kit_count "$curls" "$EDGE_OK" "$CURLS" "last: $EDGE_LAST"

# With CNEInstance.spec.telemetry.loggingSubsystem enabled (on with the AI
# gateway's metrics), TMM's iRule log lines do not show in `kubectl logs`
# (observed on BNK 2.4.1: only boot lines remain there), so the counters
# cannot be checked this way — report that rather than a false failure.
if bnk_logging_subsystem; then
	kit_skip "$fired" "telemetry.loggingSubsystem is enabled: TMM's iRule logs are not in kubectl logs"
	kit_done "Gateway annotation reconciled + curls answered (iRule log check skipped)"
fi

# With N TMMs behind anycast, any of them may have served a curl, so read
# every TMM's log. Per-request lines are recent; the INIT line is logged
# once when TMM compiles the iRule, so look for it in the whole log (a
# re-run with an unchanged Gateway does not recompile).
recent=$(bnk_tmm_logs 60s)
all=$(bnk_tmm_logs)
seen=$(printf '%s\n' "$recent" | grep -F 'TOKEN(' | head -4 | tr '\n' '|')
case $all in *"TOKEN COUNTING IRULE INIT"*) init=1 ;; *) init= ;; esac
case $recent in
*scn-tokencount-gateway-scn-tokencount-token-counting*) rule=1 ;; *) rule= ;; esac
case $recent in *cumulative*) cum=1 ;; *) cum= ;; esac
if [ -n "$init" ] && [ -n "$rule" ] && [ -n "$seen" ] && [ -n "$cum" ]; then
	kit_pass "$fired" "$seen"
else
	kit_fail "$fired" "init=${init:-0} rule=${rule:-0} cumulative=${cum:-0} ${seen:-no TOKEN( lines}"
fi

kit_done "Gateway annotation reconciled + TMM data-plane TOKEN(...) counters fired"
