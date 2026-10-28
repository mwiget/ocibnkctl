#!/bin/sh
# Verify every TMM's session to the edge router is Established and that the
# router learned routes from them.
. "$(dirname "$0")/../../lib/common.sh"

want=$(bnk_tmm_pods | wc -l | tr -d ' ')
[ "$want" -gt 0 ] || want=1
peers="external FRR shows all $want TMM neighbor(s) Established"
rib="external FRR learned routes from TMM (BGP RIB non-empty)"
if ! edge_enabled || ! edge_has_bgp; then
	why=$EDGE_NONE_REASON
	edge_enabled && why="no EDGE_VTYSH for the BGP peer"
	kit_skip "$peers" "$why"
	kit_skip "$rib" "$why"
	kit_done "BGP peering not checked"
fi

edge_bgp_wait "$want" "$BGP_WAIT"
kit_eq "$peers" "$want/$want established" "$EDGE_ESTABLISHED/$want established"
if [ -n "$EDGE_ROUTES" ]; then kit_pass "$rib" "$EDGE_RIB"; else kit_fail "$rib" "$EDGE_RIB"; fi

kit_done "$want TMM(s) Established with external FRR; routes learned"
