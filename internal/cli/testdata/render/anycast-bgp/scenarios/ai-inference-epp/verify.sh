#!/bin/sh
# Verify the EPP reconciled, TMM consulted it for every request, and it sent
# every request to the idle pod.
. "$(dirname "$0")/../../lib/common.sh"

for d in mock-vllm-busy mock-vllm-idle; do
	kit_check "$d Deployment Available" \
		kubectl -n "$NAMESPACE" wait --for=condition=Available "deployment/$d" --timeout=3m
done
# The controller reconciles F5EPPs only after the eppNamespaces change rolls
# it, then pulls the f5-epp image: allow for both.
kubectl -n "$NAMESPACE" wait --for=condition=Available "f5-epps.inference.k8s.f5.com/$EPP" --timeout=8m >&2 ||
	kubectl -n "$NAMESPACE" get f5-epps.inference.k8s.f5.com "$EPP" \
		-o jsonpath='{range .status.conditions[*]}{.type}: {.message}{"\n"}{end}' >&2
conds=$(kubectl -n "$NAMESPACE" get f5-epps.inference.k8s.f5.com "$EPP" \
	-o jsonpath='{range .status.conditions[*]}{.type}={.status} {end}')
for c in Accepted ResolvedRefs Programmed Available; do
	kit_contains "F5EPP condition $c=True" "$c=True" "$conds"
done
# Controller naming contract: <pool>-epp Service, ext_proc on 9002.
ports=$(kubectl -n "$NAMESPACE" get service "$EPP" -o jsonpath='{.spec.ports[*].port}')
kit_contains "controller rendered Service $EPP with ext_proc port 9002" " 9002 " " $ports "
kit_contains "controller rendered EPP Role/RoleBinding in $NAMESPACE (eppNamespaces took effect)" epp \
	"$(kubectl -n "$NAMESPACE" get role,rolebinding -o name)"
kit_check "Gateway Programmed=True" \
	kubectl -n "$NAMESPACE" wait --for=condition=Programmed gateway/scn-epp-gateway --timeout=3m
parents() {
	kubectl -n "$NAMESPACE" get "$1" "$2" -o jsonpath='{range .status.parents[*].conditions[*]}{.type}={.status} {end}'
}
route=$(parents httproute scn-epp-route)
kit_contains "HTTPRoute Accepted=True" Accepted=True "$route"
kit_contains "HTTPRoute ResolvedRefs=True (InferencePool backendRef resolved)" ResolvedRefs=True "$route"
pool=$(parents inferencepool.inference.networking.k8s.io "$POOL")
kit_contains "InferencePool Accepted=True by the Gateway" Accepted=True "$pool"
kit_contains "InferencePool ResolvedRefs=True (endpointPickerRef resolved)" ResolvedRefs=True "$pool"

bgp="FRR BGP table has $VIP/32 advertised by TMM"
answered="$REQUESTS/$REQUESTS requests through the Gateway answered by a mock vLLM pod"
steered="EPP steered every request to the idle pod (a load-blind balancer would split ~50/50)"
tokenized="EPP tokenized every prompt with the $MODEL tokenizer (prefix-index lookups ≥ $REQUESTS, no encode failures)"
consulted="TMM consulted the EPP for every request (inference_epp_stats Δschedule_calls ≥ $REQUESTS)"
succeeded="every EPP consult succeeded, none fell back"
ivs_d="TMM profile_ext_proc_stat has the $NAMESPACE-$POOL-epp-ivs internal virtual server"
if ! edge_enabled; then
	edge_skip "$bgp" "$answered" "$steered" "$tokenized" "$consulted" "$succeeded"
	ivs=$NAMESPACE-$POOL-epp-ivs found=
	for p in $(bnk_tmm_pods); do
		kubectl -n "$BNK_NAMESPACE" exec "$p" -c debug -- tmctl -d blade profile_ext_proc_stat 2>/dev/null |
			grep -qF "$ivs" && found=1
	done
	if [ -n "$found" ]; then kit_pass "$ivs_d" found; else kit_fail "$ivs_d" "not found"; fi
	kit_done "F5 EPP reconciled"
fi
edge_check_vip "$bgp" "$VIP"

# requests N TIMEOUT TAG — N chat completions naming MODEL, one after
# another from ONE client process, so the EPP never sees two in flight and
# only the metrics the pods publish can tell them apart.
requests() {
	edge_client sh -c "for i in \$(seq $1); do curl -sS --max-time $2 -H 'Host: $HOST' \
-H 'Content-Type: application/json' -d '{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"$3 '\$i'\"}]}' \
http://$VIP/v1/chat/completions; echo; done" 2>&1
}
# epp_stats — inference_epp_stats summed over every TMM (anycast: any TMM may
# serve a request), as "column value" lines. tmctl wraps wide tables into
# stacked header / dashes / row blocks; each header line above a dashes
# line pairs with the line below it.
epp_stats() {
	for p in $(bnk_tmm_pods); do
		kubectl -n "$BNK_NAMESPACE" exec "$p" -c debug -- tmctl -d blade inference_epp_stats 2>/dev/null
		echo
	done | awk '{ l[NR] = $0 } END {
		for (i = 2; i < NR; i++) if (l[i] ~ /^[ -]+$/ && l[i] ~ /-/) {
			n = split(l[i-1], c); m = split(l[i+1], v)
			if (n == m) for (j = 1; j <= n; j++) if (v[j] ~ /^[0-9]+$/) s[c[j]] += v[j]
		}
		for (k in s) print k, s[k] }'
}
# epp_metrics — the EPP's tokenizer / prefix counters through the API
# server's service proxy, summed by their one distinguishing label (so the
# keys survive EPP pod restarts): name{result="miss"} value.
epp_metrics() {
	kubectl get --raw "/api/v1/namespaces/$NAMESPACE/services/$EPP:9090/proxy/metrics" 2>/dev/null | awk '
		match($0, /^(f5_epp_prefix_index_lookups_total|epp_tokenizer_guard_events_total)[{]/) {
			name = substr($0, 1, RLENGTH - 1); label = (name ~ /^f5_/) ? "result" : "event"
			o = index($0, "{"); c = length($0); while (substr($0, c, 1) != "}") c--
			n = split(substr($0, o + 1, c - o - 1), kv, ",")
			for (j = 1; j <= n; j++) if (index(kv[j], label "=") == 1) s[name "{" kv[j] "}"] += substr($0, c + 1)
		}
		END { for (k in s) print k, s[k] }'
}
# delta BEFORE AFTER KEY — AFTER's value minus BEFORE's for KEY, as an
# integer (0 when absent); in awk, since counters may not be integers.
delta() {
	awk -v k="$3" 'FILENAME == ARGV[1] { if ($1 == k) b = $2; next } $1 == k { a = $2 } END { printf "%d\n", a - b }' "$1" "$2"
}

work=$(mktemp -d) || kit_die "mktemp"
trap 'rm -rf "$work"' EXIT
# The first request for a model makes the EPP download its tokenizer, which
# can outlast the per-request timeout; that request is routed but unscored,
# so warm up once.
requests 1 30 warm-up >/dev/null
epp_stats >"$work/s0"
epp_metrics >"$work/m0"
out=$(requests "$REQUESTS" 8 request)
idle=$(printf '%s\n' "$out" | grep -o 'ocibnkctl-epp-role=idle' | wc -l | tr -d ' ')
busy=$(printf '%s\n' "$out" | grep -o 'ocibnkctl-epp-role=busy' | wc -l | tr -d ' ')
kit_count "$answered" $((idle + busy)) "$REQUESTS" "idle=$idle busy=$busy; $out"
if [ "$idle" -eq "$REQUESTS" ] && [ "$busy" -eq 0 ]; then kit_pass "$steered" "idle=$idle busy=$busy"; else kit_fail "$steered" "idle=$idle busy=$busy"; fi

# A failed tokenization is silent (the pick still happens, prefix affinity
# scores zero); the EPP's counters tell: a tokenized prompt makes a
# prefix-index lookup (hit or miss), an untokenized one an encode_failed.
epp_metrics >"$work/m1"
d() { delta "$work/m0" "$work/m1" "$1"; }
lookups=$(($(d 'f5_epp_prefix_index_lookups_total{result="hit"}') + $(d 'f5_epp_prefix_index_lookups_total{result="miss"}')))
failed=$(d 'epp_tokenizer_guard_events_total{event="encode_failed"}')
if [ "$lookups" -ge "$REQUESTS" ] && [ "$failed" -eq 0 ]; then
	kit_pass "$tokenized" "Δlookups(hit+miss)=$lookups Δencode_failed=$failed"
else
	kit_fail "$tokenized" "Δlookups(hit+miss)=$lookups Δencode_failed=$failed"
fi

epp_stats >"$work/s1"
s() { delta "$work/s0" "$work/s1" "$1"; }
calls=$(s schedule_calls) ok=$(s schedule_successes) fb=$(s schedule_fallbacks)
stats="Δschedule_calls=$calls Δschedule_successes=$ok Δschedule_fallbacks=$fb over $(bnk_tmm_pods | wc -l | tr -d ' ') TMM(s)"
if [ "$calls" -ge "$REQUESTS" ]; then kit_pass "$consulted" "$stats"; else kit_fail "$consulted" "$stats"; fi
if [ "$calls" -gt 0 ] && [ "$ok" -eq "$calls" ] && [ "$fb" -eq 0 ]; then kit_pass "$succeeded" "$stats"; else kit_fail "$succeeded" "$stats"; fi

ivs=$NAMESPACE-$POOL-epp-ivs found=
for p in $(bnk_tmm_pods); do
	kubectl -n "$BNK_NAMESPACE" exec "$p" -c debug -- tmctl -d blade profile_ext_proc_stat 2>/dev/null | grep -qF "$ivs" && found=1
done
if [ -n "$found" ]; then kit_pass "$ivs_d" found=true; else kit_fail "$ivs_d" found=false; fi

kit_done "F5 EPP reconciled; every request consulted the EPP and landed on the idle mock vLLM pod"
