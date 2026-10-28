#!/bin/sh
# Verify the single cluster-wide controller reconciled the new namespace.
. "$(dirname "$0")/../../lib/common.sh"

whole=$(kubectl get cneinstances -A -o jsonpath='{.items[0].spec.wholeCluster}')
watch=$(kubectl get cneinstances -A -o jsonpath='{.items[0].spec.watchNamespaces}')
got="wholeCluster=$whole watchNamespaces=$watch"
case "$whole|$watch" in
true\|* | *All*) kit_pass 'CNEInstance watches the whole cluster (wholeCluster=true or watchNamespaces=["All"])' "$got" ;;
*) kit_fail 'CNEInstance watches the whole cluster (wholeCluster=true or watchNamespaces=["All"])' "$got" ;;
esac

replicas=$(kubectl -n "$BNK_NAMESPACE" get deployment/f5-cne-controller -o jsonpath='{.spec.replicas}')
kit_eq "f5-cne-controller Deployment is a single replica" "spec.replicas=1" "spec.replicas=$replicas"

kit_check "nginx Deployment in $NAMESPACE Available" \
	kubectl -n "$NAMESPACE" wait --for=condition=Available deployment/nginx --timeout=2m
kit_check "Gateway in $NAMESPACE Programmed=True (cross-namespace reconcile)" \
	kubectl -n "$NAMESPACE" wait --for=condition=Programmed gateway/scn-cwatch-gateway --timeout=3m
kit_eq "HTTPRoute in $NAMESPACE Accepted=True" True "$(kubectl -n "$NAMESPACE" get httproute/scn-cwatch-route \
	-o jsonpath='{.status.parents[0].conditions[?(@.type=="Accepted")].status}')"

kit_done "Single cluster-wide controller reconciled a Gateway+HTTPRoute in a brand-new namespace"
