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

# edge_exec CMD... — run a command on the client.
edge_exec() {
	case $EDGE_DRIVER in
	ocibnkctl) "$EDGE_RUNTIME" exec "$EDGE_FRR" "$@" ;;
	# shellcheck disable=SC2086 # EDGE_EXEC is a command prefix, split on purpose
	external) $EDGE_EXEC "$@" ;;
	*) kit_die "edge_exec: EDGE_DRIVER=none has no client" ;;
	esac
}

# edge_curl CURL_ARGS... — curl from the client (fails on HTTP errors).
edge_curl() {
	case $EDGE_DRIVER in
	ocibnkctl)
		"$EDGE_RUNTIME" run --rm --network "container:$EDGE_FRR" "$EDGE_TOOLS_IMAGE" \
			curl -sS --fail --max-time 8 "$@"
		;;
	*) edge_exec curl -sS --fail --max-time 8 "$@" ;;
	esac
}

# edge_vtysh "CMD" — one router CLI command on the BGP peer.
edge_vtysh() {
	case $EDGE_DRIVER in
	ocibnkctl) "$EDGE_RUNTIME" exec "$EDGE_FRR" vtysh -c "$1" ;;
	# shellcheck disable=SC2086
	external) [ -n "$EDGE_VTYSH" ] && $EDGE_VTYSH "$1" ;;
	*) return 1 ;;
	esac
}
