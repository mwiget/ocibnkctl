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
