#!/bin/sh
# Phase cne: the data-plane prerequisites for DATAPLANE_MODE, the
# CNEInstance, the License, the GatewayClass and Infra, then the mode's
# finishing steps and the readiness gates. Idempotent. Variables:
# ../platform.env. Records the rendered CNEInstance under KIT_STATE.
. "$(dirname "$0")/../../lib/common.sh"
. "$KIT_SCENARIO_DIR/../platform.env"
. "$KIT_SCENARIO_DIR/../lib.sh"

nodes=$(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')

# Multus (thick) unless it is already running, then two fixes to the
# upstream DaemonSet: install the shim by rename (a plain cp hits "Text file
# busy" after a node restart and deadlocks every sandbox), and 500Mi memory
# (50Mi OOMKills under CNI churn), with the shrink policy's requests when it
# is applied (shrink runs before cne, so it could not cap Multus itself).
ensure_multus() {
	st=$(kubectl -n kube-system get daemonset/kube-multus-ds -o jsonpath='{.status.numberReady}/{.status.desiredNumberScheduled}' 2>/dev/null)
	case $st in */* ) [ "${st%%/*}" -gt 0 ] 2>/dev/null && running=1 || running= ;; *) running= ;; esac
	if [ -z "$running" ]; then
		kit_fetch "$MULTUS_URL" "$MULTUS_SHA" "$KIT_CACHE/multus/multus-daemonset-thick.yml"
		kubectl apply -f "$KIT_CACHE/multus/multus-daemonset-thick.yml" >&2 || kit_die "apply Multus"
	fi
	kubectl -n kube-system patch daemonset/kube-multus-ds --type=json -p \
		'[{"op":"replace","path":"/spec/template/spec/initContainers/0/command","value":["/usr/bin/sh","-c","cp -f /usr/src/multus-cni/bin/multus-shim /host/opt/cni/bin/multus-shim.tmp && mv -f /host/opt/cni/bin/multus-shim.tmp /host/opt/cni/bin/multus-shim"]}]' >&2 ||
		kit_die "patch the Multus shim install"
	cpu=100m mem=200Mi
	set -- $(kubectl get clusterpolicy f5-bnk-shrink-requests -o jsonpath='{.spec.rules[?(@.name=="shrink-f5-requests")].mutate.foreach[0].patchStrategicMerge.spec.containers[0].resources.requests.cpu} {.spec.rules[?(@.name=="shrink-f5-requests")].mutate.foreach[0].patchStrategicMerge.spec.containers[0].resources.requests.memory}' 2>/dev/null)
	[ $# -eq 2 ] && cpu=$1 mem=$2
	kubectl -n kube-system patch daemonset/kube-multus-ds --type=json -p \
		"[{\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/resources/limits/memory\",\"value\":\"500Mi\"},{\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/resources/requests/cpu\",\"value\":\"$cpu\"},{\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/resources/requests/memory\",\"value\":\"$mem\"}]" >&2
	kubectl -n kube-system rollout status daemonset/kube-multus-ds --timeout=3m >&2 || kit_die "Multus not rolled out"
}

# Whereabouts: cluster-wide IPAM, so N TMM pods on one edge L2 get unique
# net1 addresses. Upstream ships :latest; pin the release tag.
ensure_whereabouts() {
	for c in IPPOOL RES; do
		eval "url=\$WHEREABOUTS_${c}_URL sha=\$WHEREABOUTS_${c}_SHA"
		kit_fetch "$url" "$sha" "$KIT_CACHE/whereabouts/$(basename "$url")"
		kubectl apply -f "$KIT_CACHE/whereabouts/$(basename "$url")" >&2 || kit_die "apply whereabouts CRD"
	done
	kit_fetch "$WHEREABOUTS_DS_URL" "$WHEREABOUTS_DS_SHA" "$KIT_CACHE/whereabouts/daemonset-install.yaml"
	sed "s|ghcr.io/k8snetworkplumbingwg/whereabouts:latest|ghcr.io/k8snetworkplumbingwg/whereabouts:$WHEREABOUTS_TAG|g" \
		"$KIT_CACHE/whereabouts/daemonset-install.yaml" | kubectl apply -f - >&2 || kit_die "apply whereabouts"
	kubectl -n kube-system rollout status daemonset/whereabouts --timeout=3m >&2 || kit_die "whereabouts not rolled out"
}

# Node prep, through NODE_EXEC: the bridge CNI plugin where Multus looks for
# it (k3s ships it as /bin/bridge only), and anycast-bgp's NAD bridge
# bypassing iptables (Calico's natOutgoing MASQUERADE breaks it otherwise).
seed_bridge_plugin() {
	[ -n "$NODE_EXEC" ] || { kit_log "      NODE_EXEC unset: make sure /opt/cni/bin/bridge exists on every node"; return 0; }
	for n in $nodes; do
		node_exec "$n" sh -c 'test -x /opt/cni/bin/bridge || (cp -f /bin/bridge /opt/cni/bin/bridge && chmod 0755 /opt/cni/bin/bridge)' >&2 ||
			kit_log "warning: seed bridge CNI plugin on $n"
	done
}
bridge_nf_off() {
	[ -n "$NODE_EXEC" ] || { kit_log "      NODE_EXEC unset: set net.bridge.bridge-nf-call-iptables=0 on the TMM nodes"; return 0; }
	for n in $nodes; do
		node_exec "$n" modprobe br_netfilter >/dev/null 2>&1
		node_exec "$n" sh -c 'f=/proc/sys/net/bridge/bridge-nf-call-iptables; if [ -e $f ]; then echo 0 >$f; fi' >&2 ||
			kit_log "warning: bridge-nf-call-iptables on $n"
	done
}

ocnos() {
	plat_render ocnos-template.yaml CONFIGMAP_NAME=f5-tmm-dynamic-routing-template NAMESPACE="$BNK_NAMESPACE" \
		LOCAL_AS="$LOCAL_AS" PEER_IP="$EDGE_PEER_IP" PEER_AS="$PEER_AS" VIP=
}

# 0. Data-plane prep: Multus, the bridge plugin and the NAD must all exist
#    before the CNEInstance attaches the NAD to TMM, or TMM sandboxes fail
#    ("failed to find plugin bridge").
case $DATAPLANE_MODE in
selfip-dag)
	kit_log "[0/4] selfip-dag prep: Multus + bridge CNI + DAG NAD"
	ensure_multus
	seed_bridge_plugin
	plat_render nad-bridge-host-local.yaml NAD_NAME=f5-tmm-dag NAMESPACE="$BNK_NAMESPACE" BRIDGE=br-f5-tmm-dag \
		SUBNET=192.0.2.0/24 RANGE_START=192.0.2.20 RANGE_END=192.0.2.250 | kubectl apply -f - >&2 || kit_die "apply the DAG NAD"
	;;
anycast-bgp)
	kit_log "[0/4] anycast-bgp prep: Multus + whereabouts + bridge CNI + bnk-bgp NAD + OcNOS template"
	ensure_multus
	ensure_whereabouts
	seed_bridge_plugin
	bridge_nf_off
	plat_render nad-bridge-whereabouts.yaml NAD_NAME=bnk-bgp NAMESPACE="$BNK_NAMESPACE" BRIDGE=br-bnk-bgp \
		SUBNET="192.168.$EDGE_OCTET.0/24" RANGE_START="192.168.$EDGE_OCTET.160" RANGE_END="192.168.$EDGE_OCTET.250" |
		kubectl apply -f - >&2 || kit_die "apply the bnk-bgp NAD"
	# Pre-seed the routing template before the CNEInstance exists, so TMM
	# mounts the final config on its first boot instead of rolling again.
	ocnos | kubectl apply -f - >&2 || kit_log "warning: pre-seed the OcNOS template (falling back to a TMM re-roll later)"
	;;
standby) ;;
*) kit_die "DATAPLANE_MODE=$DATAPLANE_MODE: want anycast-bgp, selfip-dag or standby" ;;
esac

# 1. CNEInstance.
kit_log "[1/4] CNEInstance (demoMode, $DATAPLANE_MODE)"
cne=$(plat_state cne-instance-rendered.yaml)
render_cne >"$cne" || kit_die "render the CNEInstance"
kubectl apply -f "$cne" >&2 || kit_die "apply the CNEInstance"

# 2-3. License. Skip when already Active: re-applying makes CWC register the
#      device again, which fails on an already-registered asset.
kit_log "[2/4] License"
lic=f5-cne-cluster-license
state=$(kubectl -n "$SHARED_NAMESPACE" get license.k8s.f5net.com "$lic" -o jsonpath='{.status.state}' 2>/dev/null)
if [ "$state" = Active ]; then
	kit_log "      license already Active — skipping re-apply + registration"
else
	# FLO's crd-installer creates the License CRD in response to the
	# CNEInstance; the first pull of a release manifest can take minutes.
	kubectl wait --for=create crd/licenses.k8s.f5net.com --timeout=8m >&2 ||
		kit_die "License CRD never created (FLO did not reconcile?)"
	kubectl wait --for=condition=Established crd/licenses.k8s.f5net.com --timeout=3m >&2 ||
		kit_die "License CRD not Established"
	jwt=$(plat_abs "$JWT_FILE")
	[ -s "$jwt" ] || kit_die "JWT not found at $JWT_FILE (JWT_FILE)"
	jwks=
	# A test JWT needs TEEM's current test JWKS: CWC's built-in test certs
	# expired. Production JWTs keep CWC's defaults.
	if [ "$(jwt_type)" = tst ]; then
		{ printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cwc-jwks-cm\n  namespace: %s\ndata:\n  key: |\n' "$SHARED_NAMESPACE"
			sed 's/^/    /' jwks-tst.json; } | kubectl apply -f - >&2 || kit_die "apply the JWKS ConfigMap"
		jwks=cwc-jwks-cm
		kit_log "      test JWT — applied $SHARED_NAMESPACE/cwc-jwks-cm, the License references it"
	fi
	plat_work
	# The JWT reaches the renderer through its environment, not its argv.
	JWT=$(tr -d '\n\r ' <"$jwt")
	export JWT
	plat_render license.yaml NAME="$lic" NAMESPACE="$SHARED_NAMESPACE" \
		OPERATION_MODE=connected JWKS_CONFIGMAP="$jwks" JWKS_KEY=key >"$PLAT_WORK/license.yaml" || kit_die "render the License"
	unset JWT
	# CWC creates its ResourceQuota as it reconciles: retry for ~100s.
	apply_quota_retry "$PLAT_WORK/license.yaml" f5-single-license-quota 100 || kit_die "apply the License"
	kit_log "[3/4] waiting for the License to be Active"
	active() {
		state=$(kubectl -n "$SHARED_NAMESPACE" get license.k8s.f5net.com "$lic" -o jsonpath='{.status.state}' 2>/dev/null)
		case $state in Active) return 0 ;; '' | Activating | Registering | PendingVerification) return 1 ;; esac
		return 0 # an unexpected state: stop waiting, report below
	}
	kit_retry 1200 5 active
	case $state in
	Active) kit_log "      License Active" ;;
	PendingVerification) kit_log "warning: License stuck at PendingVerification (disconnected mode? register it manually per F5 docs)" ;;
	*) kit_log "warning: License state ${state:-unknown} (kubectl -n $SHARED_NAMESPACE describe license $lic)" ;;
	esac
fi

# 4. GatewayClass: wait until the CNE controller accepts it, or the next
#    Gateway races it and sticks at "Waiting for controller".
kit_log "[4/4] GatewayClass $GATEWAYCLASS_NAME"
plat_render gatewayclass.yaml GATEWAYCLASS_NAME="$GATEWAYCLASS_NAME" | kubectl apply -f - >&2 || kit_die "apply the GatewayClass"
wait_condition "gatewayclass/$GATEWAYCLASS_NAME" "" Accepted 480 ||
	kit_die "$GATEWAYCLASS_NAME not Accepted (kubectl describe gatewayclass $GATEWAYCLASS_NAME)"

# The platform Infra CR (IPAM for address-less Gateways). Not in selfip-dag:
# the legacy SPK model never reconciles it (docs/bnk-2.4.0-doc-errata.md #26).
if [ "$DATAPLANE_MODE" = selfip-dag ]; then
	kit_log "      no Infra in selfip-dag (legacy SPK network model; address-less Gateways unavailable)"
else
	kit_log "      Infra infra (IPAM pool bnk-dynamic-vips)"
	plat_work
	plat_render infra.yaml INFRA_NAME=infra NAMESPACE="$BNK_NAMESPACE" VIP_POOL=bnk-dynamic-vips \
		VIP_RANGE_START=203.0.113.110 VIP_RANGE_END=203.0.113.119 >"$PLAT_WORK/infra.yaml"
	apply_quota_retry "$PLAT_WORK/infra.yaml" f5-single-infra-quota 360 || kit_die "apply Infra"
	wait_condition infra/infra "$BNK_NAMESPACE" Programmed 180 ||
		kit_die "Infra never Programmed (USE_GATEWAY_SETTINGS=true on the CNE controller? kubectl -n $BNK_NAMESPACE describe infra infra)"
fi

# Under wholeCluster TMM is a DaemonSet whose default RollingUpdate
# (maxUnavailable=1) is already the rollout wanted.

# 5. The mode's finishing steps, once TMM has net1.
case $DATAPLANE_MODE in
selfip-dag)
	# One self-IP per TMM: 192.0.2.10, .11, ...
	ips= i=0
	while [ "$i" -lt "$TMM_WORKERS" ] || [ "$i" -eq 0 ]; do
		ips=${ips:+$ips,}192.0.2.$((10 + i))
		i=$((i + 1))
	done
	kit_log "[5/5] F5SPKVlan with self-IP(s) $ips + pod_hash DAG"
	wait_tmm_net1 f5-tmm-dag 180
	if plat_render f5spkvlan.yaml VLAN_NAME=f5-tmm-dag NAMESPACE="$BNK_NAMESPACE" INTERFACE=1.1 SELF_IPS="$ips" \
		PREFIX_LEN=24 POD_HASH=SRC_ADDR | kubectl apply -f - >&2; then
		# Applying is not programming: BNK 2.4 reconciles F5SPKVlan only in
		# the legacy SPK model, and says nothing otherwise.
		wait_condition f5-spk-vlans/f5-tmm-dag "$BNK_NAMESPACE" Programmed 120 ||
			kit_log "warning: F5SPKVlan f5-tmm-dag not Programmed; TMMs stay standby (USE_GATEWAY_SETTINGS must be false)"
	else
		kit_log "warning: apply the F5SPKVlan (TMMs stay standby)"
	fi
	;;
anycast-bgp)
	kit_log "[5/5] anycast-bgp: OcNOS template (neighbor $EDGE_PEER_IP), passwd.conf, redistribute on $TMM_WORKERS TMM(s)"
	wait_tmm_net1 bnk-bgp 180
	# Pre-seeded above, so TMM normally booted with it; roll TMM only when
	# this apply changed it (FLO reasserted its own copy meanwhile).
	if out=$(ocnos | kubectl apply -f - 2>&1); then
		case $out in
		*unchanged*) kit_log "      OcNOS template already current — no TMM re-roll" ;;
		*) kubectl -n "$BNK_NAMESPACE" rollout restart daemonset/f5-tmm >&2 || kit_log "warning: restart f5-tmm" ;;
		esac
		# TMM pods roll one at a time and each takes minutes.
		kubectl -n "$BNK_NAMESPACE" rollout status daemonset/f5-tmm --timeout="$((4 * (TMM_WORKERS + 1)))m" >&2 ||
			kit_log "warning: f5-tmm rollout not complete"
		wait_tmm_net1 bnk-bgp 180
		bnk_ocnos_passwd || kit_log "warning: passwd.conf (OcNOS may not load until it is written)"
		bnk_ocnos_redistribute || kit_log "warning: redistribute (routes may not advertise until re-issued)"
	else
		kit_log "warning: apply the OcNOS template: $out (TMMs won't peer)"
	fi
	;;
esac

# Gates: the finishing steps only warn, so check what matters. 10 min covers
# a cold TMM image pull.
kit_log "verifying f5-tmm is ready on $TMM_WORKERS worker(s)"
wait_tmm_ready "$TMM_WORKERS" 600 || kit_die "f5-tmm not ready"
kit_log "verifying all $SHARED_NAMESPACE workloads are available"
wait_workloads "$SHARED_NAMESPACE" 300 || kit_die "$SHARED_NAMESPACE workloads not available"
kit_log "cne done"
