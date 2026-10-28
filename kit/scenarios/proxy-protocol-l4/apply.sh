#!/bin/sh
# An iRule that prepends a PROXY v1 header, attached by a NetPolicy to an
# L4Route whose nginx backend parses it. In numbered order: the NetPolicy
# references both the iRule and the L4Route.
. "$(dirname "$0")/../../lib/common.sh"

kit_require anycast-bgp gatewayclass:bnk-gatewayclass
kit_apply manifests/01-namespace.yaml manifests/03-backend.yaml manifests/04-gateway.yaml.tmpl \
	manifests/05-irule.yaml manifests/06-l4route.yaml manifests/07-bnknetpolicy.yaml
