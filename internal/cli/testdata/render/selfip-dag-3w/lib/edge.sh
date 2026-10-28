# edge.sh — the data-plane vantage point: a client outside the cluster that
# reaches Gateway VIPs over the routes TMM advertises, and (optionally) the
# BGP peer those routes are learned on. Sourced by common.sh.
#
# EDGE_DRIVER picks how that client is reached:
#
#   none       (default) no client: data-plane checks are reported as
#              skipped, control-plane checks still run. Use on any cluster.
#   ocibnkctl  the edge fabric `ocibnkctl cluster up` builds: the external FRR
#              container EDGE_FRR (BGP peer and client netns), via the
#              container runtime EDGE_RUNTIME (docker|podman). Set by
#              `ocibnkctl scenario run`.
#   external   your own client and router:
#                EDGE_EXEC   command prefix that runs a command on the client,
#                            e.g. "ssh lab-client" (empty: run locally)
#                EDGE_VTYSH  command that takes one router CLI command as its
#                            argument and prints the output, e.g.
#                            "ssh lab-frr vtysh -c" (empty: BGP checks skipped)
#
# EDGE_PREFIX (optional) is the next-hop prefix routes to the VIPs should
# point at, e.g. "192.168.99." — checked when set.

EDGE_DRIVER=${EDGE_DRIVER:-none}
EDGE_RUNTIME=${EDGE_RUNTIME:-docker}
EDGE_FRR=${EDGE_FRR:-}
EDGE_EXEC=${EDGE_EXEC:-}
EDGE_VTYSH=${EDGE_VTYSH:-}
EDGE_PREFIX=${EDGE_PREFIX:-}
# Throwaway image joined to the FRR netns for client commands (curl etc.).
EDGE_TOOLS_IMAGE=${EDGE_TOOLS_IMAGE:-nicolaka/netshoot}

case $EDGE_DRIVER in
none | external) ;;
ocibnkctl) [ -n "$EDGE_FRR" ] || kit_die "EDGE_DRIVER=ocibnkctl needs EDGE_FRR (the FRR container name)" ;;
*) kit_die "EDGE_DRIVER=$EDGE_DRIVER: want none, ocibnkctl or external" ;;
esac

# edge_enabled — true when a data-plane client is available.
edge_enabled() { [ "$EDGE_DRIVER" != none ]; }

# edge_has_bgp — true when the BGP peer's CLI is reachable.
edge_has_bgp() {
	case $EDGE_DRIVER in
	ocibnkctl) return 0 ;;
	external) [ -n "$EDGE_VTYSH" ] ;;
	*) return 1 ;;
	esac
}

# edge_client CMD... — run a command from the client's network vantage
# (curl, socat, ip route ...). For ocibnkctl that is a throwaway
# EDGE_TOOLS_IMAGE container joined to the FRR's network namespace, so it
# routes over the BGP-learned routes and has the tools the FRR image lacks.
edge_client() {
	case $EDGE_DRIVER in
	ocibnkctl) "$EDGE_RUNTIME" run --rm --network "container:$EDGE_FRR" "$EDGE_TOOLS_IMAGE" "$@" ;;
	# shellcheck disable=SC2086 # EDGE_EXEC is a command prefix, split on purpose
	external) $EDGE_EXEC "$@" ;;
	*) kit_die "edge_client: EDGE_DRIVER=none has no client" ;;
	esac
}

# edge_curl CURL_ARGS... — curl from the client (fails on HTTP errors).
edge_curl() { edge_client curl -sS --fail --max-time 8 "$@"; }

# edge_vtysh "CMD" — one router CLI command on the BGP peer.
edge_vtysh() {
	case $EDGE_DRIVER in
	ocibnkctl) "$EDGE_RUNTIME" exec "$EDGE_FRR" vtysh -c "$1" ;;
	# shellcheck disable=SC2086
	external) [ -n "$EDGE_VTYSH" ] && $EDGE_VTYSH "$1" ;;
	*) return 1 ;;
	esac
}

# --- checks built on the above; they emit assertions (common.sh) ---

EDGE_NONE_REASON="EDGE_DRIVER=none (no data-plane client)"

# edge_skip DESC... — report each check as skipped for lack of a client.
edge_skip() {
	for _es in "$@"; do kit_skip "$_es" "$EDGE_NONE_REASON"; done
}

# edge_check_vip DESC VIP [SECONDS] — pass once the BGP peer's table holds
# VIP/32, re-triggering OcNOS redistribute on every TMM while it waits (OcNOS
# only advertises a new Gateway VIP after that). Skipped without a client,
# or without EDGE_VTYSH for the peer.
edge_check_vip() {
	_ecv_vip=$2
	if ! edge_enabled; then
		kit_skip "$1" "$EDGE_NONE_REASON"
	elif ! edge_has_bgp; then
		bnk_ocnos_redistribute
		kit_skip "$1" "no EDGE_VTYSH for the BGP peer"
	elif kit_retry "${3:-120}" 5 _edge_vip_in_bgp; then
		kit_pass "$1" "$_ecv_table"
	else
		kit_fail "$1" "$_ecv_table"
	fi
}
_edge_vip_in_bgp() {
	bnk_ocnos_redistribute
	_ecv_table=$(edge_vtysh "show bgp ipv4 unicast" 2>&1)
	case $_ecv_table in *"$_ecv_vip/32"*) return 0 ;; esac
	return 1
}

# edge_curls N MARKER CURL_ARGS... — run N curls from the client. Sets
# EDGE_OK to how many returned a body containing MARKER and EDGE_LAST to
# the last body or error that did not. (Call it directly, not in $(...),
# or the variables are lost with the subshell.)
edge_curls() {
	_ecc_n=$1 _ecc_m=$2 _ecc_i=0 EDGE_OK=0 EDGE_LAST=
	shift 2
	while [ "$_ecc_i" -lt "$_ecc_n" ]; do
		_ecc_i=$((_ecc_i + 1))
		if _ecc_b=$(edge_curl "$@" 2>&1); then
			case $_ecc_b in *"$_ecc_m"*) EDGE_OK=$((EDGE_OK + 1)) ;; *) EDGE_LAST=$_ecc_b ;; esac
		else
			EDGE_LAST=$_ecc_b
		fi
	done
}
