#!/bin/sh
# A BNK Pool CR whose member is the backend's address (standing in for a
# resource outside the cluster), used as an HTTPRoute backendRef.
. "$(dirname "$0")/../../lib/common.sh"

kit_require anycast-bgp gatewayclass:bnk-gatewayclass
kit_apply manifests/01-namespace.yaml manifests/03-backend.yaml
kubectl -n "$NAMESPACE" wait --for=condition=Available deployment/ext-backend --timeout=2m >&2 ||
	kit_die "ext-backend Deployment not Available"
# The Pool member is the backend pod's own IP; TMM reaches it over plain
# pod-to-pod routing.
BACKEND_IP=$(kubectl -n "$NAMESPACE" get pod -l app=ext-backend --field-selector=status.phase=Running \
	-o jsonpath='{.items[0].status.podIP}')
[ -n "$BACKEND_IP" ] || kit_die "no podIP for ext-backend"
export BACKEND_IP
kit_log "ext-backend podIP: $BACKEND_IP"
kit_apply manifests/05-pool.yaml.tmpl manifests/04-gateway.yaml.tmpl manifests/06-httproute.yaml
