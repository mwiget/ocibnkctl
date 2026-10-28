#!/bin/sh
# Remove the tenants and the template. The probe veth lives in the node
# netns, not in a container, so it is removed explicitly.
. "$(dirname "$0")/../../lib/common.sh"

# The GlobalRoutingConfig first: it holds the template through a finalizer.
kubectl -n "$BNK_NAMESPACE" delete globalroutingconfig scn-grc-vlans --ignore-not-found >&2
kubectl -n "$BNK_NAMESPACE" delete routingtemplate scn-rt-vlans --ignore-not-found >&2
kubectl -n "$BNK_NAMESPACE" delete f5-spk-vlans tenant-red tenant-green --ignore-not-found >&2
kubectl -n "$BNK_NAMESPACE" delete vrf red green --ignore-not-found >&2
for node in $(kubectl -n "$BNK_NAMESPACE" get pod -l app=f5-tmm -o jsonpath='{range .items[*]}{.spec.nodeName}{"\n"}{end}' | sort -u); do
	bnk_node_netns "$node" sh -c 'ip link del scn-vlp 2>/dev/null || true' >/dev/null 2>&1
done
