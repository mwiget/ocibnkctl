# bnk.sh — helpers for BNK's own pods. Sourced by common.sh.

# BNK_NAMESPACE is where the CNEInstance runs TMM; BNK_LOCAL_AS is TMM's
# BGP AS (kit/platform/cne/ocnos-template.yaml's LOCAL_AS).
BNK_NAMESPACE=${BNK_NAMESPACE:-default}
BNK_LOCAL_AS=${BNK_LOCAL_AS:-65000}

# bnk_tmm_pods — names of the Running TMM pods, one per line.
bnk_tmm_pods() {
	kubectl -n "$BNK_NAMESPACE" get pod -l app=f5-tmm --field-selector=status.phase=Running \
		-o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'
}

# bnk_ocnos_passwd — write OcNOS's passwd.conf (lib/ocnos-passwd.conf, the
# same line `ocibnkctl deploy cne` writes) into every TMM's routing
# container; imish needs it before it accepts commands. Retries for ~30s to
# ride out TMM rollouts.
bnk_ocnos_passwd() {
	kit_retry 30 5 _bnk_passwd_all
}
_bnk_passwd_all() {
	_bp_pods=$(bnk_tmm_pods) && [ -n "$_bp_pods" ] || return 1
	for _bp_pod in $_bp_pods; do
		kubectl -n "$BNK_NAMESPACE" exec -i "$_bp_pod" -c f5-tmm-routing -- \
			sh -c 'cat > /config/zebos/rd0/passwd.conf' <"$KIT_LIB/ocnos-passwd.conf" >&2 || return 1
	done
}

# bnk_ocnos_redistribute — re-issue the BGP redistribute statements on every
# TMM (lib/ocnos-redistribute.cfg, the same file `ocibnkctl deploy cne`
# sends). OcNOS only advertises a redistributed route (e.g. a new Gateway
# VIP) once the statement is re-issued at runtime, so call this inside the
# loop that waits for the route. Best effort: imish prints stray noise.
bnk_ocnos_redistribute() {
	_bor_cfg=$(LOCAL_AS=$BNK_LOCAL_AS sh "$KIT_LIB/render.sh" "$KIT_LIB/ocnos-redistribute.cfg") || return 1
	for _bor_pod in $(bnk_tmm_pods); do
		printf '%s\n' "$_bor_cfg" | kubectl -n "$BNK_NAMESPACE" exec -i "$_bor_pod" -c f5-tmm-routing -- \
			sh -c 'cat > /tmp/ocibnkctl-redist.cfg; imish -f /tmp/ocibnkctl-redist.cfg' >/dev/null 2>&1
	done
	return 0
}

# bnk_tmm_logs [SINCE] — the f5-tmm container logs of every Running TMM,
# concatenated (behind anycast any TMM may have served a request); SINCE is
# a kubectl --since value such as 60s, empty for the whole log.
bnk_tmm_logs() {
	for _btl_pod in $(bnk_tmm_pods); do
		kubectl -n "$BNK_NAMESPACE" logs "$_btl_pod" -c f5-tmm ${1:+--since="$1"} 2>/dev/null
	done
}

# bnk_logging_subsystem — true when the CNEInstance enables
# telemetry.loggingSubsystem (on with the AI gateway's metrics). TMM's iRule
# log lines are then not in `kubectl logs` (observed on BNK 2.4.1), so
# checks that read them must skip.
bnk_logging_subsystem() {
	[ "$(kubectl get cneinstances -A -o jsonpath='{.items[0].spec.telemetry.loggingSubsystem.enabled}')" = true ]
}

# bnk_tmm_pods_by_age — Running TMM pods, oldest first: DP-0, DP-1, ...
# (the order the controller binds a VLAN's selfip_v4s[i] to).
bnk_tmm_pods_by_age() {
	kubectl -n "$BNK_NAMESPACE" get pod -l app=f5-tmm --field-selector=status.phase=Running \
		--sort-by=.metadata.creationTimestamp -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'
}

# bnk_node_netns NODE CMD... — run CMD in a cluster node's network namespace,
# where the per-node Multus bridges live (with NET_ADMIN). Only possible when
# the nodes are containers on this host (EDGE_DRIVER=ocibnkctl: k3s nodes);
# returns 2 otherwise, so callers can skip.
bnk_node_netns() {
	[ "$EDGE_DRIVER" = ocibnkctl ] || return 2
	_bnn_node=$1
	shift
	"$EDGE_RUNTIME" run --rm --network "container:$_bnn_node" --cap-add NET_ADMIN "$EDGE_TOOLS_IMAGE" "$@"
}
