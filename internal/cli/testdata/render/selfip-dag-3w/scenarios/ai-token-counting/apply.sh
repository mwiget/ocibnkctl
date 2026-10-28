#!/bin/sh
# A stub LLM behind a Gateway carrying the k8s.f5.com/ai-token-counting
# annotation, which makes FLO generate the token-counting iRule.
. "$(dirname "$0")/../../lib/common.sh"

kit_require anycast-bgp gatewayclass:bnk-gatewayclass
kit_apply manifests/01-namespace.yaml manifests/03-backend.yaml \
	manifests/04-gateway.yaml.tmpl manifests/05-httproute.yaml
