#!/bin/sh
# Two tenants (Vrf + tagged F5SPKVlan each, on interface 1.1, one self-IP per
# TMM), then a RoutingTemplate that reads .vlans, bound by a
# GlobalRoutingConfig.
. "$(dirname "$0")/../../lib/common.sh"

kit_require crd:f5-spk-vlans.k8s.f5net.com legacy-spk

# GlobalRoutingConfig is a cluster singleton and, like RoutingTemplate,
# immutable: a re-run deletes and recreates them.
kubectl -n "$BNK_NAMESPACE" delete globalroutingconfig scn-grc-vlans --ignore-not-found >&2
kubectl -n "$BNK_NAMESPACE" delete routingtemplate scn-rt-vlans --ignore-not-found >&2

# One self-IP per TMM: selfip_v4s[i] binds device DP-i.
n=$(kubectl -n "$BNK_NAMESPACE" get daemonset f5-tmm -o jsonpath='{.status.desiredNumberScheduled}')
[ "${n:-0}" -ge 1 ] 2>/dev/null || n=1
vlans=$KIT_STATE/routing-template-multitenant/02-vlans.yaml
mkdir -p "$(dirname "$vlans")"
{
	for t in "tenant-red 2001 red $RED_SUBNET 1" "tenant-green 2002 green $GREEN_SUBNET 2"; do
		set -- $t
		[ "$1" = tenant-green ] && echo ---
		printf 'apiVersion: k8s.f5net.com/v1\nkind: F5SPKVlan\nmetadata:\n  name: %s\n  namespace: default\nspec:\n  name: %s\n  interfaces: ["%s"]\n  tag: %s\n  vrf: %s\n  mtu: 1500\n  prefixlen_v4: 24\n  prefixlen_v6: 112\n  selfip_v4s:\n' \
			"$1" "$1" "$VLAN_INTERFACE" "$2" "$3"
		i=0; while [ "$i" -lt "$n" ]; do printf '    - "%s.%d"\n' "$4" $((SELFIP_BASE + i)); i=$((i + 1)); done
		echo '  selfip_v6s:'
		i=0; while [ "$i" -lt "$n" ]; do printf '    - "2001:db8:%s::%d"\n' "$5" $((SELFIP_BASE + i)); i=$((i + 1)); done
	done
} >"$vlans"

# The Vrfs first (a VLAN naming an unknown vrf is rejected), and the VLANs
# programmed before the template renders, or .vlans is partial.
kit_apply manifests/01-vrfs.yaml "$vlans"
programmed() {
	for v in tenant-red tenant-green; do
		[ "$(kubectl -n "$BNK_NAMESPACE" get f5-spk-vlans "$v" -o jsonpath='{.status.conditions[?(@.type=="Programmed")].status}')" = True ] || return 1
	done
}
kit_retry 120 5 programmed || kit_log "warning: the tenant VLANs are not Programmed yet"
kit_apply manifests/03-routing-template.yaml manifests/04-global-routing-config.yaml
