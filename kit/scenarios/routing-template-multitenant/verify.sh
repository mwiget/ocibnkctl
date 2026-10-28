#!/bin/sh
# Verify .vlans: both tenants reconciled and bound, every TMM renders all six
# fields with its OWN self-IP, tenants stay distinct, and each self-IP is a
# real next hop on its own VLAN only (ARP from a peer in the node netns).
. "$(dirname "$0")/../../lib/common.sh"

cond() {
	kubectl -n "$BNK_NAMESPACE" get "$1" "$2" -o jsonpath='{.status.conditions[?(@.type=="Programmed")].status}' 2>/dev/null
}
for v in red green; do kit_eq "Vrf \"$v\" Programmed=True" True "$(cond vrf "$v")"; done
for v in tenant-red tenant-green; do
	kit_eq "F5SPKVlan \"$v\" Programmed=True (config sent to every TMM)" True "$(cond f5-spk-vlans "$v")"
done

pods=$(bnk_tmm_pods_by_age)
n=$(printf '%s\n' "$pods" | grep -c .)
[ "$n" -gt 0 ] || { kit_fail "list Running f5-tmm pods" "none"; kit_done "no TMM pods"; }

i=0
for p in $pods; do
	i=$((i + 1))
	vm=$(kubectl -n "$BNK_NAMESPACE" exec "$p" -c debug -- tmctl -d blade vlan_member_stat 2>&1)
	case $vm in *tenant-red*"$VLAN_INTERFACE"* | *"$VLAN_INTERFACE"*tenant-red*)
		case $vm in *tenant-green*) ok=1 ;; *) ok= ;; esac ;;
	*) ok= ;;
	esac
	if [ -n "$ok" ]; then kit_pass "TMM $i/$n bound both VLANs to interface $VLAN_INTERFACE" "$vm"; else kit_fail "TMM $i/$n bound both VLANs to interface $VLAN_INTERFACE" "$vm"; fi
done

# The rendered config per TMM — the only place per-device rendering shows.
render() {
	r=$(kubectl -n "$BNK_NAMESPACE" exec "$1" -c f5-tmm-routing -- sh -c \
		'cat /tmm-routing-config/conf-create/default_scn-grc-vlans_* 2>/dev/null')
	case $r in *"! OCIBNKCTL-CTX "*) return 0 ;; esac
	return 1
}
# context_keys — top-level keys of the {{ printf "%v" . }} dump (the
# outermost level of the map[k:v ...] Go prints).
context_keys() {
	awk '{ i = index($0, "map["); if (!i) next; s = substr($0, i + 4); depth = 0; key = ""
		for (j = 1; j <= length(s); j++) {
			c = substr(s, j, 1)
			if (c == "[") depth++
			else if (c == "]") { if (depth == 0) break; depth-- }
			else if (depth > 0) continue
			else if (c == ":") { if (key != "") printf "%s ", key; key = "" }
			else if (c == " ") key = ""
			else key = key c
		}
		exit }'
}
work=$(mktemp -d) || kit_die "mktemp"
trap 'rm -rf "$work"' EXIT
for p in $pods; do
	kit_retry "${ROUTING_WAIT:-120}" 5 render "$p"
	printf '%s\n' "$r" >"$work/$p"
done
keys=$(cat "$work"/* | grep -F "! OCIBNKCTL-CTX " | head -1 | context_keys)
case " $keys " in
*" vlans "*) kit_pass "RoutingTemplate context exposes .vlans (the feature under test)" "context keys = $keys" ;;
*)
	kit_fail "RoutingTemplate context exposes .vlans (the feature under test)" "context keys = $keys"
	kit_log ".vlans is empty: BNK 2.4 only reconciles F5SPKVlan in the legacy SPK network model (USE_GATEWAY_SETTINGS=false)"
	kit_done "no .vlans"
	;;
esac

# field FILE VLAN KEY — one field of a VLAN's marker line.
field() {
	grep -F "! OCIBNKCTL-VLAN $2 " "$1" | head -1 | tr ' ' '\n' | sed -n "s/^$3=//p"
}
i=0
for p in $pods; do
	f=$work/$p
	for t in "tenant-red red 2001 $RED_SUBNET 1 $GREEN_SUBNET tenant-green" "tenant-green green 2002 $GREEN_SUBNET 2 $RED_SUBNET tenant-red"; do
		set -- $t
		v4=$4.$((SELFIP_BASE + i)) v6=2001:db8:$5::$((SELFIP_BASE + i)) other=$6.$((SELFIP_BASE + i))
		got="selfIPv4=$(field "$f" "$1" selfIPv4) selfIPv6=$(field "$f" "$1" selfIPv6) prefixLenV4=$(field "$f" "$1" prefixLenV4) prefixLenV6=$(field "$f" "$1" prefixLenV6) tag=$(field "$f" "$1" tag) vrf=$(field "$f" "$1" vrf)"
		kit_eq "TMM $((i + 1))/$n renders all six .vlans fields for \"$1\" (own self-IP $v4)" \
			"selfIPv4=$v4 selfIPv6=$v6 prefixLenV4=24 prefixLenV6=112 tag=$3 vrf=$2" "$got"
		mine=$(field "$f" "$1" selfIPv4)
		if [ -n "$mine" ] && [ "$mine" != "$other" ]; then
			kit_pass "tenant \"$1\" render excludes \"$7\"'s self-IP ($other)" "$mine"
		else
			kit_fail "tenant \"$1\" render excludes \"$7\"'s self-IP ($other)" "${mine:-empty}"
		fi
	done
	i=$((i + 1))
done

# On the wire: a peer on each tenant VLAN (a veth into DAG_BRIDGE, the
# per-node bridge under TMM's 1.1, with one 802.1Q subinterface per tag).
# ARP, not ICMP: a self-IP must answer ARP to receive anything, while ICMP is
# subject to the VLAN's (empty) allowed_services. The other tenant's
# self-IP must stay SILENT on the wrong VLAN.
setup="set -e; ip link del scn-vlp 2>/dev/null || true
ip link add scn-vlp type veth peer name scn-vlp-br
ip link set scn-vlp-br master $DAG_BRIDGE up; ip link set scn-vlp up
ip link add link scn-vlp name scn-vlp.2001 type vlan id 2001
ip link add link scn-vlp name scn-vlp.2002 type vlan id 2002
ip addr add $RED_SUBNET.$PROBE_HOST/24 dev scn-vlp.2001
ip addr add $GREEN_SUBNET.$PROBE_HOST/24 dev scn-vlp.2002
ip link set scn-vlp.2001 up; ip link set scn-vlp.2002 up
ip -br addr show | grep scn-vlp"
# arp TAG TARGET — 1 when TARGET answered ARP on the VLAN with TAG, else 0.
# arping exits non-zero when nothing answers (the expected outcome of the
# isolation probe), so its output is the signal: a "Unicast reply from" line,
# or a non-zero count in "Received N response(s)" (iputils) or "N packets
# received" (other arpings).
arp() {
	out=$(bnk_node_netns "$node" sh -c "arping -I scn-vlp.$1 -c 3 -w 5 $2 2>&1 || true")
	printf '%s\n' "$out" | awk '
		/Unicast reply from/ { r = 1 }
		/^Received [0-9]+ response/ { if ($2 > 0) r = 1 }
		{ for (k = 1; k < NF; k++) if ($(k+1) == "packets" && $(k+2) ~ /^received/ && $k > 0) r = 1 }
		END { print r + 0 }'
}
i=0
for p in $pods; do
	i=$((i + 1))
	node=$(kubectl -n "$BNK_NAMESPACE" get pod "$p" -o jsonpath='{.spec.nodeName}')
	peer="TMM $i/$n: tenant VLAN peer built on $node (scn-vlp.2001 + scn-vlp.2002 on $DAG_BRIDGE)"
	red="TMM $i/$n: tenant-red self-IP $RED_SUBNET.$((SELFIP_BASE + i - 1)) answers ARP on VLAN 2001 (it is a real next hop)"
	green="TMM $i/$n: tenant-green self-IP $GREEN_SUBNET.$((SELFIP_BASE + i - 1)) answers ARP on VLAN 2002 (it is a real next hop)"
	iso="TMM $i/$n: tenant-green self-IP $GREEN_SUBNET.$((SELFIP_BASE + i - 1)) is SILENT on tenant-red's VLAN 2001 (tenant isolation)"
	out=$(bnk_node_netns "$node" sh -c "$setup" 2>&1)
	case $? in
	2) for d in "$peer" "$red" "$green" "$iso"; do kit_skip "$d" "no access to the node netns (EDGE_DRIVER=ocibnkctl only)"; done; continue ;;
	0) kit_contains "$peer" "$RED_SUBNET" "$out" || continue ;;
	*) kit_fail "$peer" "$out"; continue ;;
	esac
	kit_eq "$red" 1 "$(arp 2001 "$RED_SUBNET.$((SELFIP_BASE + i - 1))")"
	kit_eq "$green" 1 "$(arp 2002 "$GREEN_SUBNET.$((SELFIP_BASE + i - 1))")"
	kit_eq "$iso" 0 "$(arp 2001 "$GREEN_SUBNET.$((SELFIP_BASE + i - 1))")"
done

kit_done "two tenants (red/green) on interface $VLAN_INTERFACE; .vlans renders all six fields per TMM with each device's own self-IP"
