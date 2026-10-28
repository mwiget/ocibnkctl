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

# established PREFIX — count neighbor rows of `show ip bgp summary` on the
# edge L2 (PREFIX, e.g. 192.168.99.; empty = any) that are Established: FRR
# shows an Up/Down timer (00:02:13) there, a state name otherwise.
established() {
	printf '%s\n' "$summary" | awk -v p="$1" '
		(p == "" || index($0, p)) && $0 !~ /Idle|Connect|Active|OpenSent|OpenConfirm/ {
			for (i = 2; i <= NF; i++) if ($i ~ /^[0-9]+:[0-9]+:[0-9]+$/) { n++; break }
		}
		END { print n + 0 }'
}
converged() {
	summary=$(edge_vtysh "show ip bgp summary" 2>&1)
	table=$(edge_vtysh "show ip bgp" 2>&1)
	got=$(established "$EDGE_PREFIX")
	case $table in *192.168.* | *203.0.113.*) routes=1 ;; *) routes= ;; esac
	[ "$got" -ge "$want" ] && [ -n "$routes" ]
}
kit_retry "$BGP_WAIT" 5 converged
kit_eq "$peers" "$want/$want established" "$got/$want established"
if [ -n "$routes" ]; then kit_pass "$rib" "$table"; else kit_fail "$rib" "$table"; fi

kit_done "$want TMM(s) Established with external FRR; routes learned"
