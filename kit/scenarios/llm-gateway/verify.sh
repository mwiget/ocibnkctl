#!/bin/sh
# Verify, as a client outside the cluster, what the gateway enforces: the
# virtual key works, wrong / no keys and out-of-scope models are refused,
# and (with AI metrics) every request is metered.
. "$(dirname "$0")/../../lib/common.sh"

kit_require ai-gateway
[ -s "$KEY_FILE" ] || kit_die "no virtual key at $KEY_FILE: run apply.sh first"
key=$(cat "$KEY_FILE")

# 1. TMM has the route's virtual server (f5ingress names it
#    <ns>-<route>-<vip>-http-80-vs). f5ingress 2.4.1 can fail a new route's
#    Gateway apply with "AI profile ... is not prepared yet" and never retry;
#    restarting f5-cne-controller re-applies every Gateway with the profile
#    ready, so do that once.
vs="$FLO_NAMESPACE-$ROUTE-$VIP-http-80-vs"
has_vs() {
	pods=$(bnk_tmm_pods)
	[ -n "$pods" ] || return 1
	for p in $pods; do
		kubectl -n "$BNK_NAMESPACE" exec "$p" -c debug -- tmctl -d blade -w 200 virtual_server_stat -s name 2>/dev/null |
			grep -qF "$vs" || return 1
	done
}
profile() {
	kubectl -n "$FLO_NAMESPACE" get aigatewayprofiles.inference.k8s.f5.com "$ROUTE" \
		-o jsonpath='Programmed={.status.conditions[?(@.type=="Programmed")].status}: {.status.conditions[?(@.type=="Programmed")].message}' 2>&1
}
vs_d="TMM has the $ROUTE virtual server on $VIP:80"
if kit_retry 90 5 has_vs; then
	kit_pass "$vs_d" present
else
	kit_log "AIGatewayProfile $ROUTE $(profile); restarting f5-cne-controller once (f5ingress 2.4.1 AI profile race)"
	kubectl -n "$FLO_NAMESPACE" rollout restart deploy/f5-cne-controller >&2
	kubectl -n "$FLO_NAMESPACE" rollout status deploy/f5-cne-controller --timeout=5m >&2
	if kit_retry 180 5 has_vs; then
		kit_pass "$vs_d" "present after restarting f5-cne-controller (f5ingress 2.4.1 profile race)"
	else
		kit_fail "$vs_d" "missing — AIGatewayProfile $ROUTE $(profile)"
	fi
fi

ok200="virtual key + allowed model → 200 with a usage block"
bad="wrong key → 401"
nokey="no key → 401"
scope="model outside the key's scope → 403"
bgp="edge FRR learned $VIP/32 from TMM over BGP"
if ! edge_enabled; then
	edge_skip "$bgp" "$ok200" "$bad" "$nokey" "$scope"
	kit_done "AI gateway route reconciled"
fi
edge_check_vip "$bgp" "$VIP"

# chat KEY MODEL — one chat completion from the client; prints "CODE BODY".
# No --fail: the 401/403s are what is being checked.
chat() {
	out=$(edge_client curl -s --max-time 15 -o - -w '\nOCIBNKCTL_HTTP_STATUS=%{http_code}\n' \
		-H 'Content-Type: application/json' ${1:+-H "Authorization: Bearer $1"} \
		-d "{\"model\":\"$2\",\"messages\":[{\"role\":\"user\",\"content\":\"hello from ocibnkctl\"}]}" \
		"http://$VIP/v1/chat/completions" 2>&1)
	# the body is the line just before the status marker
	printf '%s\n' "$out" | awk '/^OCIBNKCTL_HTTP_STATUS=/ { sub(/^OCIBNKCTL_HTTP_STATUS=/, ""); print $0, prev; f = 1; exit } NF { prev = $0 }
		END { if (!f) print 0, prev }'
}
expect() { # DESC CODE NEEDLE KEY MODEL
	r=$(chat "$4" "$5")
	case "$r" in "$2 "*"$3"*) kit_pass "$1" "HTTP $r" ;; *) kit_fail "$1" "HTTP $r" ;; esac
}
expect "$ok200" 200 '"usage"' "$key" "$MODEL"
expect "$bad" 401 "Invalid virtual key" sk-not-a-virtual-key "$MODEL"
expect "$nokey" 401 Authorization "" "$MODEL"
expect "$scope" 403 "Model not allowed" "$key" gpt-5

# 2. Metering: one ClickHouse record per request, rejects included. The
#    gateway writes record_version 2 (OpenTelemetry gen_ai.* columns);
#    clickhouse-client in the pod authenticates from FLO's env there, so no
#    credential is read or passed here.
if [ "$(kubectl get cneinstances -A -o jsonpath='{.items[0].spec.aiGateway.aiMetrics}')" = true ]; then
	q="SELECT \`http.response.status_code\` AS s, count() AS n, sum(\`gen_ai.f5.usage.total_tokens\`) AS t FROM otel.aigw_requests WHERE tenant = '$TENANT' GROUP BY s ORDER BY s FORMAT TSV"
	metered() {
		rows=$(kubectl -n "$FLO_NAMESPACE" exec "$CLICKHOUSE_POD" -- clickhouse-client -q "$q" 2>&1) || return 1
		for c in 200 401 403; do printf '%s\n' "$rows" | grep -q "^$c	" || return 1; done
	}
	m="ClickHouse otel.aigw_requests has records for tenant $TENANT (200, 401, 403)"
	if kit_retry 90 5 metered; then
		kit_pass "$m" "$(printf '%s\n' "$rows" | awk 'NF == 3 { printf "%s%s×%s (%s tokens)", s, $1, $2, $3; s = ", " }')"
	else
		kit_fail "$m" "$rows"
	fi
fi

if [ "$KIT_FAILED" -eq 0 ]; then
	kit_log "
  Try it yourself
  ---------------
  Console (tenant $TENANT, user admin):
    kubectl -n $FLO_NAMESPACE get secret aitenant-$TENANT-admin -o jsonpath='{.data.password}' | base64 --decode; echo
    kubectl -n $FLO_NAMESPACE port-forward svc/f5-aigateway-console 3000:3000   # then open http://localhost:3000
    # sign-in page: replace the proposed tenant \"lab\" with $TENANT, then \"Sign in as tenant admin\"
  A request through TMM from the edge client:
    curl -s http://$VIP/v1/chat/completions -H \"Authorization: Bearer \$(cat $KEY_FILE)\" \\
      -H 'Content-Type: application/json' -d '{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}'"
fi

kit_done "LLM gateway enforced keys and model scope through TMM and metered every request"
