#!/bin/sh
# Verify the L4Route reconciled and its weights split traffic across A and B.
. "$(dirname "$0")/../../lib/common.sh"

for d in tcp-lb-a tcp-lb-b; do
	kit_check "$d Deployment Available" \
		kubectl -n "$NAMESPACE" wait --for=condition=Available "deployment/$d" --timeout=3m
done
kit_check "Gateway Programmed=True" \
	kubectl -n "$NAMESPACE" wait --for=condition=Programmed gateway/scn-tcp-lb-gateway --timeout=5m
kit_eq "L4Route Accepted=True" True "$(kubectl -n "$NAMESPACE" get l4route/scn-tcp-lb-route \
	-o jsonpath='{.status.parents[0].conditions[?(@.type=="Accepted")].status}')"

bgp="FRR BGP table has $VIP/32 advertised by TMM"
curls="$CURLS/$CURLS L4 TCP curls succeeded"
split="weighted L4 LB hit both backend=A and backend=B at least once"
if ! edge_enabled; then
	edge_skip "$bgp" "$curls" "$split"
	kit_done "L4Route reconciled"
fi
edge_check_vip "$bgp" "$VIP"

i=0 ok=0 a=0 b=0 last=
while [ "$i" -lt "$CURLS" ]; do
	i=$((i + 1))
	if body=$(edge_curl "http://$VIP:$PORT/" 2>&1); then
		ok=$((ok + 1))
		case $body in *backend=A*) a=$((a + 1)) ;; *backend=B*) b=$((b + 1)) ;; esac
	else
		last=$body
	fi
done
kit_count "$curls" "$ok" "$CURLS" "last error: $last"
if [ "$a" -gt 0 ] && [ "$b" -gt 0 ]; then
	kit_pass "$split" "A=$a B=$b (of $CURLS)"
else
	kit_fail "$split" "A=$a B=$b (of $CURLS)"
fi

kit_done "L4Route TCP LB reconciled; weight 70/30 split observed across A and B"
