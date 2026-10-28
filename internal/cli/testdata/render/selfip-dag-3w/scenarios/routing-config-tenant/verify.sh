#!/bin/sh
# Verify the tenant RoutingConfig programs, the controller survives it (the
# same CR panics it under the Infra/GatewaySettings model), and each TMM's
# tenant render carries .tenant.spec.vrf, a non-zero routeDomainID and .vlans.
. "$(dirname "$0")/../../lib/common.sh"

# Settles through "CR config deployment is in progress" before Programmed.
st=
programmed() {
	st=$(kubectl -n "$BNK_NAMESPACE" get routingconfig scn-rc-tenant -o jsonpath='{.status.conditions[?(@.type=="Programmed")].status}' 2>/dev/null)
	[ "$st" = True ]
}
kit_retry 120 5 programmed
kit_eq "RoutingConfig scn-rc-tenant Programmed=True" True "$st"

# Running is the check; the restart count is reported alongside.
set -- $(kubectl -n "$BNK_NAMESPACE" get pod -l app=f5-cne-controller \
	-o jsonpath='{.items[0].status.phase} {.items[0].status.containerStatuses[0].restartCount}')
ctl="f5-cne-controller still Running after the tenant RoutingConfig (no reconciler panic)"
if [ "${1:-}" = Running ]; then kit_pass "$ctl" "phase=$1 restarts=${2:-?}"; else kit_fail "$ctl" "phase=${1:-none} restarts=${2:-?}"; fi

pods=$(bnk_tmm_pods_by_age)
n=$(printf '%s\n' "$pods" | grep -c .)
[ "$n" -gt 0 ] || { kit_fail "list Running f5-tmm pods" none; kit_done "no TMM pods"; }
render() {
	body=$(kubectl -n "$BNK_NAMESPACE" exec "$1" -c f5-tmm-routing -- sh -c \
		'cat /tmm-routing-config/conf-create/tenant/default_scn-rc-tenant_* 2>/dev/null')
	case $body in *"! OCIBNKCTL-TENANT-CTX "*) return 0 ;; esac
	return 1
}
# after KEY LEN — LEN characters of the dump from the first KEY.
after() { printf '%s' "$dump" | awk -v k="$1" -v n="$2" '{ i = index($0, k); if (i) print substr($0, i, n) }'; }
i=0
for p in $pods; do
	i=$((i + 1))
	kit_retry 120 5 render "$p"
	dump=$(printf '%s\n' "$body" | grep -F "! OCIBNKCTL-TENANT-CTX " | head -1)
	if [ -z "$dump" ]; then
		kit_fail "TMM $i/$n has a tenant render under conf-create/tenant/" "$body"
		continue
	fi
	kit_pass "TMM $i/$n has a tenant render under conf-create/tenant/" "$(printf '%s' "$body" | head -c 120)"
	# .tenant exists only in tenant scope.
	case $dump in *"tenant:map[spec:map["*"vrf:$VRF"*) ok=1 ;; *) ok= ;; esac
	if [ -n "$ok" ]; then kit_pass "TMM $i/$n tenant context carries .tenant.spec.vrf=$VRF" "$(after tenant:map[ 90)"; else kit_fail "TMM $i/$n tenant context carries .tenant.spec.vrf=$VRF" "$(after tenant:map[ 90)"; fi
	rd=$(after routeDomainID: 24)
	case $rd in '' | routeDomainID:0*) kit_fail "TMM $i/$n tenant context has a NON-ZERO routeDomainID (global scope renders 0)" "${rd:-absent}" ;;
	*) kit_pass "TMM $i/$n tenant context has a NON-ZERO routeDomainID (global scope renders 0)" "$rd" ;;
	esac
	kit_contains "TMM $i/$n .vlans is available in tenant scope too" "vlans:map[" "$dump"
	kit_contains "TMM $i/$n rendered \`ip vrf $VRF\` (not \`<no value>\`)" "ip vrf $VRF" "$body"
done

kit_done "tenant RoutingConfig programmed; .tenant.spec.vrf, a per-tenant routeDomainID and .vlans all present in tenant scope"
