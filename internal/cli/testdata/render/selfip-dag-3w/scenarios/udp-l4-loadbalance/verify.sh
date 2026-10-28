#!/bin/sh
# Verify the UDP L4Route reconciled and carries datagrams both ways.
. "$(dirname "$0")/../../lib/common.sh"

kit_check "udp-echo Deployment Available" \
	kubectl -n "$NAMESPACE" wait --for=condition=Available deployment/udp-echo --timeout=3m
kit_check "Gateway Programmed=True" \
	kubectl -n "$NAMESPACE" wait --for=condition=Programmed gateway/scn-udp-lb-gateway --timeout=5m
kit_eq "L4Route Accepted=True" True "$(kubectl -n "$NAMESPACE" get l4route/scn-udp-lb-route \
	-o jsonpath='{.status.parents[0].conditions[?(@.type=="Accepted")].status}')"

bgp="FRR BGP table has $VIP/32 advertised by TMM"
probes="$PROBES/$PROBES UDP probes carried the marker reply"
if ! edge_enabled; then
	edge_skip "$bgp" "$probes"
	kit_done "L4Route reconciled"
fi
edge_check_vip "$bgp" "$VIP"

# socat -t 3: wait up to 3s for the reply datagram.
i=0 ok=0 last=
while [ "$i" -lt "$PROBES" ]; do
	i=$((i + 1))
	body=$(edge_client sh -c "printf probe | socat -t 3 - UDP4:$VIP:$PORT" 2>&1)
	case $body in *"$MARKER"*) ok=$((ok + 1)) ;; *) last=$body ;; esac
done
kit_count "$probes" "$ok" "$PROBES" "last: $last"

kit_done "L4Route UDP LB reconciled; $PROBES/$PROBES datagrams echoed via Gateway"
