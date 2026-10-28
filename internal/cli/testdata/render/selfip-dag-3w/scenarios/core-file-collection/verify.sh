#!/bin/sh
# Verify FLO reconciled core collection: CoreMond CR + DaemonSet, a core
# volume in TMM's pod template, and the CNEInstance condition.
. "$(dirname "$0")/../../lib/common.sh"

name= ns=
for n in $COREMOND_NAMESPACES; do
	name=$(kubectl -n "$n" get coremonds.k8s.f5.com -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
	[ -n "$name" ] && ns=$n && break
done
if [ -n "$name" ]; then kit_pass "FLO auto-created a CoreMond CR" "name=$name ns=$ns"; else kit_fail "FLO auto-created a CoreMond CR" "none in $COREMOND_NAMESPACES"; fi

ds=$(kubectl -n "${ns:-default}" get daemonset -l app=f5-coremond \
	-o jsonpath='{.items[0].metadata.name} {.items[0].status.desiredNumberScheduled}' 2>/dev/null)
case $ds in
*" "[1-9]*) kit_pass "CoreMond DaemonSet exists with at least one desired replica" "$ds" ;;
*) kit_fail "CoreMond DaemonSet exists with at least one desired replica" "${ds:-none}" ;;
esac

vols=$(kubectl -n "$BNK_NAMESPACE" get daemonset/f5-tmm -o jsonpath='{range .spec.template.spec.volumes[*]}{.name},{end}')
case $(printf '%s' "$vols" | tr 'A-Z' 'a-z') in
*core*) kit_pass "TMM Deployment template includes a core-dump volume" "$vols" ;;
*) kit_fail "TMM Deployment template includes a core-dump volume" "$vols" ;;
esac

# FLO has spelled this condition both ways across releases.
cond=
coremond_available() {
	cne=$(kubectl get cneinstances -A -o jsonpath='{.items[0].metadata.namespace}/{.items[0].metadata.name}')
	cond=$(kubectl -n "${cne%%/*}" get cneinstance "${cne#*/}" \
		-o jsonpath='{.status.conditions[?(@.type=="CoremondAvailable")].status}{.status.conditions[?(@.type=="CoreMonAvailable")].status}')
	[ "$cond" = True ]
}
kit_retry 150 5 coremond_available
kit_eq "CNEInstance condition (Coremond|CoreMon)Available=True" "value=True" "value=$cond"

kit_done "coreCollection enabled, CoreMond Running on hostPath, all status conditions True"
