#!/bin/sh
# Verify the Pool backendRef reconciled and serves traffic through TMM.
. "$(dirname "$0")/../../lib/common.sh"

kit_check "ext-backend Deployment Available" \
	kubectl -n "$NAMESPACE" wait --for=condition=Available deployment/ext-backend --timeout=2m
kit_check "Gateway Programmed=True" \
	kubectl -n "$NAMESPACE" wait --for=condition=Programmed gateway/scn-extres-gateway --timeout=5m
kit_eq "HTTPRoute Accepted=True" True "$(kubectl -n "$NAMESPACE" get httproute/scn-extres-route \
	-o jsonpath='{.status.parents[0].conditions[?(@.type=="Accepted")].status}')"
kit_contains "Pool CR has at least one member entry on :80" ":80" \
	"$(kubectl -n "$NAMESPACE" get pool.k8s.f5net.com/ext-backend-pool \
		-o jsonpath='{range .spec.members[*]}{.address}:{.port}{" "}{end}')"

bgp="FRR BGP table has $VIP/32 advertised by TMM"
curls="$CURLS/$CURLS curls via Gateway return ext-backend marker body"
if ! edge_enabled; then
	edge_skip "$bgp" "$curls"
	kit_done "Pool CR + HTTPRoute backendRef reconciled"
fi
edge_check_vip "$bgp" "$VIP"
edge_curls "$CURLS" "$MARKER" -H "Host: $HOST" "http://$VIP/"
kit_count "$curls" "$EDGE_OK" "$CURLS" "last: $EDGE_LAST"

kit_done "Pool reconciled, BGP route present, $CURLS/$CURLS curls to ext-backend via Pool succeeded"
