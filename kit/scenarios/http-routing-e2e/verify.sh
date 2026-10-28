#!/bin/sh
# Verify http-routing-e2e: control plane, then BGP + curls from the edge.
. "$(dirname "$0")/../../lib/common.sh"

kit_check "nginx Deployment Available" \
	kubectl -n "$NAMESPACE" wait --for=condition=Available deployment/nginx --timeout=3m
kit_check "Gateway Programmed=True" \
	kubectl -n "$NAMESPACE" wait --for=condition=Programmed gateway/scn-gateway --timeout=5m
kit_eq "HTTPRoute Accepted=True" True "$(kubectl -n "$NAMESPACE" get httproute/scn-route \
	-o jsonpath='{.status.parents[0].conditions[?(@.type=="Accepted")].status}')"

bgp="FRR BGP table has $VIP/32 advertised by TMM"
route="FRR kernel route $VIP/32 installed via the bnk-edge nexthop"
curls="$CURLS/$CURLS end-to-end curls via Gateway return nginx marker body"
if ! edge_enabled; then
	edge_skip "$bgp" "$route" "$curls"
	kit_done "control plane reconciled"
fi

edge_check_vip "$bgp" "$VIP"

r=$(edge_client ip route show "$VIP" 2>&1)
if [ -z "$r" ]; then
	kit_fail "$route" "no route"
elif [ -n "$EDGE_PREFIX" ]; then
	kit_contains "$route" "$EDGE_PREFIX" "$r" # next hop on the edge L2
else
	kit_pass "$route" "$r"
fi

edge_curls "$CURLS" "$MARKER" -H "Host: $HOST" "http://$VIP/"
kit_count "$curls" "$EDGE_OK" "$CURLS" "last: $EDGE_LAST"

kit_done "control-plane reconciled + $CURLS/$CURLS end-to-end curls succeeded via NAD"
