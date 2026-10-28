#!/bin/sh
# The F5 Endpoint Picker in front of a busy and an idle mock vLLM pod.
#
# Changes two cluster-wide things cleanup.sh leaves in place: the GAIE CRDs,
# and NAMESPACE in CNEInstance.spec.eppNamespaces (adding it rolls the CNE
# controller).
. "$(dirname "$0")/../../lib/common.sh"

kit_require anycast-bgp gatewayclass:bnk-gatewayclass
# The F5EPP CRD ships with BNK 2.4+.
kubectl get crd f5-epps.inference.k8s.f5.com >/dev/null 2>&1 ||
	kit_not_applicable "F5EPP CRD not installed: the F5 Endpoint Picker needs BNK 2.4+"

kit_log "installing GAIE $GAIE_VERSION CRDs"
kit_fetch "$GAIE_URL" "$GAIE_SHA" "$KIT_CACHE/gaie-$GAIE_VERSION/manifests.yaml"
kubectl apply -f "$KIT_CACHE/gaie-$GAIE_VERSION/manifests.yaml" >&2 || kit_die "apply GAIE CRDs"
kubectl wait --for=condition=Established crd/inferencepools.inference.networking.k8s.io --timeout=60s >&2 ||
	kit_die "InferencePool CRD not Established"

# Add NAMESPACE to CNEInstance.spec.eppNamespaces, keeping what is listed
# (a merge patch replaces the whole list). Skipped when NAMESPACE or "All"
# is already there: the patch rolls the CNE controller.
cne=$(kubectl get cneinstances -A -o jsonpath='{.items[0].metadata.namespace}/{.items[0].metadata.name}')
list=$(kubectl -n "${cne%%/*}" get cneinstance "${cne#*/}" -o jsonpath='{.spec.eppNamespaces}' |
	tr -d '[]" ' | tr ',' ' ')
case " $list " in
*" $NAMESPACE "* | *" All "*) kit_log "CNEInstance.spec.eppNamespaces already covers $NAMESPACE: [$list]" ;;
*)
	json=$(for n in $list $NAMESPACE; do printf '"%s",' "$n"; done)
	kit_log "CNEInstance.spec.eppNamespaces → [${json%,}]"
	kubectl -n "${cne%%/*}" patch cneinstance "${cne#*/}" --type=merge \
		-p "{\"spec\":{\"eppNamespaces\":[${json%,}]}}" >&2 || kit_die "patch eppNamespaces"
	;;
esac

kit_apply manifests/01-namespace.yaml manifests/02-mock-vllm.yaml manifests/03-f5epp.yaml \
	manifests/04-inferencepool.yaml manifests/05-gateway.yaml.tmpl manifests/06-httproute.yaml
