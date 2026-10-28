#!/bin/sh
# A stub LLM and a stub model cache; the Gateway's k8s.f5.com/ai annotation
# points the generated semantic-cache iRule at the cache's ClusterIP.
. "$(dirname "$0")/../../lib/common.sh"

kit_require anycast-bgp gatewayclass:bnk-gatewayclass
kit_apply manifests/01-namespace.yaml manifests/03-stubs.yaml
CACHE_IP=$(kubectl -n "$NAMESPACE" get service/stub-modelcache -o jsonpath='{.spec.clusterIP}')
[ -n "$CACHE_IP" ] || kit_die "no ClusterIP for stub-modelcache"
export CACHE_IP
kit_log "stub-modelcache ClusterIP: $CACHE_IP"
kit_apply manifests/04-gateway.yaml.tmpl manifests/05-httproute.yaml
