#!/bin/sh
# Verify the iRule + NetPolicy landed and nginx sees the PROXY header.
. "$(dirname "$0")/../../lib/common.sh"

kit_check "pp-backend Deployment Available" \
	kubectl -n "$NAMESPACE" wait --for=condition=Available deployment/pp-backend --timeout=3m
kit_check "Gateway Programmed=True" \
	kubectl -n "$NAMESPACE" wait --for=condition=Programmed gateway/scn-proxy-gateway --timeout=5m
kit_eq "L4Route Accepted=True" True "$(kubectl -n "$NAMESPACE" get l4route/scn-proxy-route \
	-o jsonpath='{.status.parents[0].conditions[?(@.type=="Accepted")].status}')"
kit_eq "F5BigCneIrule pp-prepend exists" pp-prepend \
	"$(kubectl -n "$NAMESPACE" get f5-big-cne-irule/pp-prepend -o jsonpath='{.metadata.name}')"
kit_eq "NetPolicy scn-proxy-attach exists" scn-proxy-attach \
	"$(kubectl -n "$NAMESPACE" get netpolicy.gateway.k8s.f5.com/scn-proxy-attach -o jsonpath='{.metadata.name}')"

bgp="FRR BGP table has $VIP/32 advertised by TMM"
curls="$CURLS/$CURLS L4 curls carry PROXY header parsed by nginx"
if ! edge_enabled; then
	edge_skip "$bgp" "$curls"
	kit_done "L4Route + iRule + NetPolicy reconciled"
fi
edge_check_vip "$bgp" "$VIP"

# Pass needs the marker and a real proxy_addr: 0.0.0.0 or empty would mean
# nginx saw no PROXY header.
i=0 ok=0 last=
while [ "$i" -lt "$CURLS" ]; do
	i=$((i + 1))
	body=$(edge_curl "http://$VIP:$PORT/" 2>&1) || { last=$body; continue; }
	last=$body
	case $body in *"$MARKER"*) ;; *) continue ;; esac
	addr=$(printf '%s\n' "$body" | sed -n 's/.*proxy_addr=\([^ ]*\).*/\1/p' | head -1)
	[ -n "$addr" ] && [ "$addr" != 0.0.0.0 ] && ok=$((ok + 1))
done
kit_count "$curls" "$ok" "$CURLS" "last body: $last"

kit_done "L4Route + iRule + NetPolicy reconciled; $CURLS/$CURLS PROXY-prefixed connections parsed by nginx"
