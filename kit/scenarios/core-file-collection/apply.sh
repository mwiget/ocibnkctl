#!/bin/sh
# Turn on core collection in the live CNEInstance (cneinstance-patch.yaml)
# and roll TMM so its pods pick up the core-dump volume.
#
# This CHANGES THE PLATFORM and cleanup.sh does not revert it: see there.
. "$(dirname "$0")/../../lib/common.sh"

kit_require crd:cneinstances.k8s.f5.com
cne=$(kubectl get cneinstances -A -o jsonpath='{.items[0].metadata.namespace}/{.items[0].metadata.name}')
[ "$cne" != / ] || kit_die "no CNEInstance found"
kubectl -n "${cne%%/*}" patch cneinstance "${cne#*/}" --type=merge --patch-file cneinstance-patch.yaml >&2 ||
	kit_die "patch CNEInstance $cne"
kubectl -n "$BNK_NAMESPACE" rollout restart daemonset/f5-tmm >&2
kubectl -n "$BNK_NAMESPACE" rollout status daemonset/f5-tmm --timeout=3m >&2 ||
	kit_log "warning: f5-tmm rollout still in progress after 3m (verify reads the DaemonSet template directly)"
