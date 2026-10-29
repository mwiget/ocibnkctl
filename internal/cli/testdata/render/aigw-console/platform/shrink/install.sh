#!/bin/sh
# Phase shrink (tight hosts only): cap container requests at admission with
# Kyverno, since FLO reasserts every workload spec; cap kube-system's
# Calico/Multus DaemonSets directly; recycle the F5 pods so the cap applies
# now. Must run before cne creates the TMM/DSSM pods. Variables:
# ../platform.env (SHRINK_CPU, SHRINK_MEMORY, NO_RECYCLE).
. "$(dirname "$0")/../../lib/common.sh"
. "$KIT_SCENARIO_DIR/../platform.env"
. "$KIT_SCENARIO_DIR/../lib.sh"

kit_log "[1/4] Kyverno $KYVERNO_VERSION (admission controller only)"
helm upgrade --install kyverno "$KYVERNO_CHART" --repo "$KYVERNO_REPO" --version "$KYVERNO_VERSION" \
	--namespace kyverno --create-namespace \
	--set backgroundController.enabled=false --set cleanupController.enabled=false \
	--set reportsController.enabled=false --set admissionController.replicas=1 >&2 || kit_die "helm install kyverno"
kubectl -n kyverno wait --for=condition=Available deployment/kyverno-admission-controller --timeout=5m >&2 ||
	kit_log "warning: kyverno admission controller not Available yet"

kit_log "[2/4] shrink policy (cpu=$SHRINK_CPU mem=$SHRINK_MEMORY per container, f5-tmm excluded)"
policy=$(plat_state kyverno-shrink-requests-rendered.yaml)
render_shrink_policy >"$policy" || kit_die "render the shrink policy"
kubectl apply -f "$policy" >&2 || kit_die "apply the shrink policy"

# Kyverno leaves system namespaces alone and these DaemonSets have no
# operator to revert a patch; patching the template rolls their pods.
kit_log "[3/4] capping kube-system DaemonSets (calico-node, kube-multus)"
for t in calico-node:calico-node kube-multus-ds:kube-multus; do
	if ! kubectl -n kube-system get "daemonset/${t%%:*}" >/dev/null 2>&1; then
		kit_log "      ${t%%:*} not present — skipping (cne caps Multus when it installs it)"
		continue
	fi
	kubectl -n kube-system set resources "daemonset/${t%%:*}" --containers="${t#*:}" \
		--requests="cpu=$SHRINK_CPU,memory=$SHRINK_MEMORY" >&2 || kit_log "warning: cap ${t%%:*}"
done

if [ -n "$NO_RECYCLE" ]; then
	kit_log "[4/4] not recycling F5 pods (NO_RECYCLE): the cap applies as pods restart"
else
	# The policy mutates pods at admission only; f5-tmm is left alone.
	kit_log "[4/4] recycling F5 pods (f5-tmm excluded)"
	for ns in "$OPERATOR_NAMESPACE" "$SHARED_NAMESPACE"; do
		kubectl -n "$ns" delete pods --all --wait=false >&2 || kit_log "warning: delete pods -n $ns"
	done
	for p in $(kubectl -n "$BNK_NAMESPACE" get pods -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'); do
		case $p in f5-tmm-*) ;; f5-*) kubectl -n "$BNK_NAMESPACE" delete pod "$p" --wait=false >&2 ;; esac
	done
fi
kit_log "shrink done"
