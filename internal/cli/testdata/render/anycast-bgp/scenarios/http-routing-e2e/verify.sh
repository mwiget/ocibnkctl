#!/bin/sh
# Verify http-routing-e2e: control plane, then BGP + curls from the edge.
. "$(dirname "$0")/../../lib/common.sh"

kit_check "nginx Deployment Available" \
	kubectl -n "$NAMESPACE" wait --for=condition=Available deployment/nginx --timeout=3m
kit_check "Gateway Programmed=True" \
	kubectl -n "$NAMESPACE" wait --for=condition=Programmed gateway/scn-gateway --timeout=5m
kit_eq "HTTPRoute Accepted=True" True "$(kubectl -n "$NAMESPACE" get httproute/scn-route \
	-o jsonpath='{.status.parents[0].conditions[?(@.type=="Accepted")].status}')"

if ! edge_enabled; then
	why="EDGE_DRIVER=none (no data-plane client)"
	kit_skip "FRR BGP table has $VIP/32 advertised by TMM" "$why"
	kit_skip "FRR kernel route $VIP/32 installed via the bnk-edge nexthop" "$why"
	kit_skip "$CURLS/$CURLS end-to-end curls via Gateway return nginx marker body" "$why"
	kit_done "control plane reconciled"
fi

# TMM advertises the VIP once OcNOS re-runs redistribute, so retrigger it
# inside the wait.
vip_in_bgp() {
	bnk_ocnos_redistribute
	table=$(edge_vtysh "show bgp ipv4 unicast" 2>&1)
	case $table in *"$VIP"*) return 0 ;; esac
	return 1
}
if edge_has_bgp; then
	if kit_retry 120 5 vip_in_bgp; then
		kit_pass "FRR BGP table has $VIP/32 advertised by TMM" "$table"
	else
		kit_fail "FRR BGP table has $VIP/32 advertised by TMM" "$table"
	fi
else
	bnk_ocnos_redistribute
	kit_skip "FRR BGP table has $VIP/32 advertised by TMM" "no EDGE_VTYSH for the BGP peer"
fi

route=$(edge_exec ip route show "$VIP" 2>&1)
if [ -n "$route" ] && { [ -z "$EDGE_PREFIX" ] || case $route in *"$EDGE_PREFIX"*) true ;; *) false ;; esac; }; then
	kit_pass "FRR kernel route $VIP/32 installed via the bnk-edge nexthop" "$route"
else
	kit_fail "FRR kernel route $VIP/32 installed via the bnk-edge nexthop" "${route:-no route}"
fi

ok=0 i=0 last=
while [ "$i" -lt "$CURLS" ]; do
	i=$((i + 1))
	if body=$(edge_curl -H "Host: $HOST" "http://$VIP/" 2>&1); then
		case $body in *"$MARKER"*) ok=$((ok + 1)) ;; *) last=$body ;; esac
	else
		last=$body
	fi
done
got="$ok/$CURLS curls returned marker"
[ "$ok" -eq "$CURLS" ] || got="$got — last: $last"
kit_eq "$CURLS/$CURLS end-to-end curls via Gateway return nginx marker body" "$CURLS/$CURLS curls returned marker" "$got"

kit_done "control-plane reconciled + $CURLS/$CURLS end-to-end curls succeeded via NAD"
