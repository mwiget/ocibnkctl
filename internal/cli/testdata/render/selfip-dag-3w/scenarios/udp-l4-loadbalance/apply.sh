#!/bin/sh
# A socat UDP echo backend behind an L4Route on a UDP listener.
. "$(dirname "$0")/../../lib/common.sh"

kit_require anycast-bgp gatewayclass:bnk-gatewayclass
kit_apply manifests/01-namespace.yaml manifests/03-backend.yaml \
	manifests/04-gateway.yaml.tmpl manifests/05-l4route.yaml
