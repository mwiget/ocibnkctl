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
#   pod        an in-cluster client for any cluster: a netshoot pod attached
#              to a NAD that reaches TMM's data-plane interface, with static
#              routes for the VIPs via a TMM's address on it. Created on first
#              use and kept (sh kit/lib/edge-pod.sh down removes it):
#                EDGE_POD_NAD     the NAD, namespace/name (default/bnk-bgp)
#                EDGE_POD_ROUTES  VIP CIDRs to route (203.0.113.0/24)
#                EDGE_POD_VIA     next hop; default: the first TMM's address
#                                 on EDGE_POD_NAD
#                EDGE_POD_NODE_LABEL  where the NAD exists (app=f5-tmm)
#              No BGP peer: BGP checks need EDGE_VTYSH (as for external).
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
# Extra `run` arguments for that container (e.g. "-v /path/grpcurl:/grpcurl:ro"
# to bring a tool along); word-split on purpose.
EDGE_RUN_ARGS=${EDGE_RUN_ARGS:-}

EDGE_POD_NS=${EDGE_POD_NS:-bnk-edge-client}
EDGE_POD_NAME=${EDGE_POD_NAME:-edge-client}
EDGE_POD_NAD=${EDGE_POD_NAD:-default/bnk-bgp}
EDGE_POD_ROUTES=${EDGE_POD_ROUTES:-203.0.113.0/24}
EDGE_POD_VIA=${EDGE_POD_VIA:-}
EDGE_POD_NODE_LABEL=${EDGE_POD_NODE_LABEL:-app=f5-tmm}

case $EDGE_DRIVER in
none | external | pod) ;;
ocibnkctl) [ -n "$EDGE_FRR" ] || kit_die "EDGE_DRIVER=ocibnkctl needs EDGE_FRR (the FRR container name)" ;;
*) kit_die "EDGE_DRIVER=$EDGE_DRIVER: want none, ocibnkctl, pod or external" ;;
esac

# edge_enabled — true when a data-plane client is available.
edge_enabled() { [ "$EDGE_DRIVER" != none ]; }

# edge_has_bgp — true when the BGP peer's CLI is reachable.
edge_has_bgp() {
	case $EDGE_DRIVER in
	ocibnkctl) return 0 ;;
	external | pod) [ -n "$EDGE_VTYSH" ] ;;
	*) return 1 ;;
	esac
}

# _edge_pod_ready — create the pod client if needed, wait for it, and route
# EDGE_POD_ROUTES via EDGE_POD_VIA (default: the first TMM's address on the
# NAD, read from its Multus network-status). A routed pod is labelled
# kit-routed=yes, so later calls return at once.
_edge_pod_ready() {
	[ "$(kubectl -n "$EDGE_POD_NS" get pod "$EDGE_POD_NAME" -o jsonpath='{.metadata.labels.kit-routed}' 2>/dev/null)" = yes ] &&
		return 0
	EDGE_POD_NODE_LABEL_KEY=${EDGE_POD_NODE_LABEL%%=*} EDGE_POD_NODE_LABEL_VALUE=${EDGE_POD_NODE_LABEL#*=} \
		EDGE_POD_NS=$EDGE_POD_NS EDGE_POD_NAME=$EDGE_POD_NAME EDGE_POD_NAD=$EDGE_POD_NAD EDGE_TOOLS_IMAGE=$EDGE_TOOLS_IMAGE \
		sh "$KIT_LIB/render.sh" "$KIT_LIB/edge-pod.yaml" | kubectl apply -f - >&2 || kit_die "create the edge client pod"
	kubectl -n "$EDGE_POD_NS" wait --for=condition=Ready "pod/$EDGE_POD_NAME" --timeout=3m >&2 ||
		kit_die "edge client pod $EDGE_POD_NS/$EDGE_POD_NAME not Ready"
	_epr_via=$EDGE_POD_VIA
	if [ -z "$_epr_via" ]; then
		_epr_tmm=$(bnk_tmm_pods | head -1)
		_epr_via=$(kubectl -n "$BNK_NAMESPACE" get pod "$_epr_tmm" \
			-o jsonpath='{.metadata.annotations.k8s\.v1\.cni\.cncf\.io/network-status}' 2>/dev/null |
			awk -v nad="\"$EDGE_POD_NAD\"" 'index($0, "\"name\"") && index($0, nad) { f = 1; next }
				f && match($0, /[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/) { print substr($0, RSTART, RLENGTH); exit }')
		[ -n "$_epr_via" ] || kit_die "no address of TMM $_epr_tmm on $EDGE_POD_NAD: set EDGE_POD_VIA"
	fi
	for _epr_r in $EDGE_POD_ROUTES; do
		kubectl -n "$EDGE_POD_NS" exec "$EDGE_POD_NAME" -- ip route replace "$_epr_r" via "$_epr_via" dev net1 >&2 ||
			kit_die "route $_epr_r via $_epr_via in the edge client pod"
	done
	kubectl -n "$EDGE_POD_NS" label pod "$EDGE_POD_NAME" kit-routed=yes --overwrite >/dev/null
	kit_log "edge client $EDGE_POD_NS/$EDGE_POD_NAME routes $EDGE_POD_ROUTES via $_epr_via"
}

# edge_client CMD... — run a command from the client's network vantage
# (curl, socat, ip route ...). For ocibnkctl that is a throwaway
# EDGE_TOOLS_IMAGE container joined to the FRR's network namespace, so it
# routes over the BGP-learned routes and has the tools the FRR image lacks.
edge_client() {
	case $EDGE_DRIVER in
	# shellcheck disable=SC2086 # EDGE_RUN_ARGS is a list of arguments
	ocibnkctl) "$EDGE_RUNTIME" run --rm --network "container:$EDGE_FRR" $EDGE_RUN_ARGS "$EDGE_TOOLS_IMAGE" "$@" ;;
	pod) _edge_pod_ready && kubectl -n "$EDGE_POD_NS" exec "$EDGE_POD_NAME" -- "$@" ;;
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
	external | pod) [ -n "$EDGE_VTYSH" ] && $EDGE_VTYSH "$1" ;;
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

# edge_bgp_wait WANT [SECONDS] — poll the BGP peer until WANT sessions on
# the edge L2 (EDGE_PREFIX; empty = any) are Established and its table holds
# routes from TMM. Sets EDGE_ESTABLISHED (count), EDGE_SUMMARY and EDGE_RIB
# (last `show ip bgp summary` / `show ip bgp`) and EDGE_ROUTES (1 when routes
# were seen). Needs edge_has_bgp.
#
# A session counts as Established when its State/PfxRcd column holds a prefix
# count (FRR prints the state name, e.g. Active, otherwise). Not the Up/Down
# column: that reads 00:02:13 only for a session's first day, 2d17h34m after.
edge_bgp_wait() {
	_ebw_want=$1
	kit_retry "${2:-240}" 5 _edge_bgp_converged
}
_edge_bgp_converged() {
	EDGE_SUMMARY=$(edge_vtysh "show ip bgp summary" 2>&1)
	EDGE_RIB=$(edge_vtysh "show ip bgp" 2>&1)
	EDGE_ESTABLISHED=$(printf '%s\n' "$EDGE_SUMMARY" | awk -v p="$EDGE_PREFIX" '
		$1 ~ /^\*?[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/ && (p == "" || index($1, p)) && $10 ~ /^[0-9]+$/ { n++ }
		END { print n + 0 }')
	case $EDGE_RIB in *192.168.* | *203.0.113.* | *"$EDGE_PREFIX"*) EDGE_ROUTES=1 ;; *) EDGE_ROUTES= ;; esac
	[ "$EDGE_ESTABLISHED" -ge "$_ebw_want" ] && [ -n "$EDGE_ROUTES" ]
}

# edge_tool PATH — make the local binary PATH runnable on the client; sets
# EDGE_TOOL to the name to call there. ocibnkctl: mounted into the client
# container; pod: copied into the pod; external: expected on the client's
# PATH under its own name. (Sets a variable: call it outside $(...).)
edge_tool() {
	case $EDGE_DRIVER in
	ocibnkctl)
		EDGE_RUN_ARGS="$EDGE_RUN_ARGS -v $1:/$(basename "$1"):ro"
		EDGE_TOOL=/$(basename "$1")
		;;
	pod)
		_edge_pod_ready && kubectl -n "$EDGE_POD_NS" cp "$1" "$EDGE_POD_NAME:/tmp/$(basename "$1")" >&2 || return 1
		EDGE_TOOL=/tmp/$(basename "$1")
		;;
	*) EDGE_TOOL=$(basename "$1") ;;
	esac
}
