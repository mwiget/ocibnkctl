#!/bin/sh
# Verify every TMM is an Established anycast next hop at the edge router, and
# that the router fans out across them (ECMP) for the TMM net1 subnet.
. "$(dirname "$0")/../../lib/common.sh"

want=$(bnk_tmm_pods | wc -l | tr -d ' ')
[ "$want" -gt 0 ] || want=1
peers="external FRR sees all $want TMM(s) Established (one anycast next-hop per worker)"
rib="external FRR learned routes from the TMM anycast group (BGP RIB non-empty)"
ecmp="external FRR has $want+ BGP path(s) for the TMM net1 subnet (ECMP fan-out across workers)"
if ! edge_has_bgp; then
	why=$EDGE_NONE_REASON
	edge_enabled && why="no EDGE_VTYSH for the BGP peer"
	kit_skip "$peers" "$why"
	kit_skip "$rib" "$why"
	kit_skip "$ecmp" "$why"
	kit_done "anycast BGP not checked"
fi

edge_bgp_wait "$want" "$BGP_WAIT"
kit_eq "$peers" "$want/$want established" "$EDGE_ESTABLISHED/$want established"
if [ -n "$EDGE_ROUTES" ]; then kit_pass "$rib" "$EDGE_RIB"; else kit_fail "$rib" "$EDGE_RIB"; fi

# With N workers the router should hold N paths for net1's /24, one per TMM.
if [ -z "$EDGE_PREFIX" ]; then
	kit_skip "$ecmp" "EDGE_PREFIX not set (the edge L2, e.g. 192.168.99.)"
elif [ "$want" -lt 2 ]; then
	kit_pass "$ecmp" "single TMM: nothing to fan out"
else
	# FRR states the count: "Paths: (2 available, best #1, ...)".
	mp=$(edge_vtysh "show bgp ipv4 unicast ${EDGE_PREFIX}0/24" 2>&1)
	paths=$(printf '%s\n' "$mp" | sed -n 's/.*Paths: (\([0-9]*\) available.*/\1/p' | head -1)
	if [ "${paths:-0}" -ge "$want" ]; then
		kit_pass "$ecmp" "$paths path(s) for ${EDGE_PREFIX}0/24"
	else
		kit_fail "$ecmp" "${paths:-0} path(s) for ${EDGE_PREFIX}0/24: $mp"
	fi
fi

kit_done "$want TMM(s) anycast-Established with the external FRR; routes learned + ECMP fan-out"
