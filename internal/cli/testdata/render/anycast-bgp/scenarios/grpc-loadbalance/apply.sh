#!/bin/sh
# kong/grpcbin behind a GRPCRoute Gateway and, for the data plane, a plain
# TCP L4Route Gateway.
. "$(dirname "$0")/../../lib/common.sh"

kit_require anycast-bgp gatewayclass:bnk-gatewayclass
kit_apply manifests/01-namespace.yaml manifests/03-backend.yaml manifests/04-gateway.yaml.tmpl \
	manifests/05-grpcroute.yaml manifests/06-l4-backend-svc.yaml manifests/07-l4-gateway.yaml.tmpl \
	manifests/08-l4route.yaml
