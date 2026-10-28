#!/bin/sh
# Verify the Gateway got an address from the pool, and it carries traffic.
. "$(dirname "$0")/../../lib/common.sh"

kit_eq "GatewaySettings ocibnkctl-fic Accepted=True + ResolvedRefs=True" True/True \
	"$(kubectl -n "$NAMESPACE" get gatewaysettings.gateway.k8s.f5.com/ocibnkctl-fic \
		-o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}/{.status.conditions[?(@.type=="ResolvedRefs")].status}')"
kit_check "nginx Deployment Available" \
	kubectl -n "$NAMESPACE" wait --for=condition=Available deployment/nginx --timeout=3m
kit_check "Gateway Programmed=True (address allocated by the controller)" \
	kubectl -n "$NAMESPACE" wait --for=condition=Programmed gateway/scn-fic-gateway --timeout=3m

vip=$(kubectl -n "$NAMESPACE" get gateway/scn-fic-gateway -o jsonpath='{.status.addresses[0].value}')
# in_pool IP START END — IPv4 START <= IP <= END.
in_pool() {
	awk -v ip="$1" -v s="$2" -v e="$3" 'function n(a,  p) { split(a, p, "."); return ((p[1] * 256 + p[2]) * 256 + p[3]) * 256 + p[4] }
		BEGIN { exit !(ip ~ /^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/ && n(ip) >= n(s) && n(ip) <= n(e)) }'
}
pooled="gateway.status.addresses populated from the $POOL pool ($POOL_START-$POOL_END)"
if in_pool "$vip" "$POOL_START" "$POOL_END"; then kit_pass "$pooled" "$vip"; else kit_fail "$pooled" "${vip:-none}"; fi
kit_eq "HTTPRoute Accepted=True" True "$(kubectl -n "$NAMESPACE" get httproute/scn-fic-route \
	-o jsonpath='{.status.parents[0].conditions[?(@.type=="Accepted")].status}')"

[ -n "$vip" ] || kit_done "no address allocated"
bgp="external FRR BGP table has $vip/32 advertised by TMM"
curls="$CURLS/$CURLS curls to the allocated VIP $vip reach nginx"
if ! edge_enabled; then
	edge_skip "$bgp" "$curls"
	kit_done "GatewaySettings resolved; Gateway got a dynamic VIP from the Infra pool"
fi
edge_check_vip "$bgp" "$vip"
edge_curls "$CURLS" "$MARKER" -H "Host: $HOST" "http://$vip/"
kit_count "$curls" "$EDGE_OK" "$CURLS" "last: $EDGE_LAST"

kit_done "GatewaySettings resolved; Gateway got a dynamic VIP from the Infra pool, FRR learned it, $CURLS/$CURLS curls succeeded"
