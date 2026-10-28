#!/bin/sh
# An address-less Gateway whose GatewaySettings point at the platform Infra
# IPAM pool, so the controller allocates its address.
. "$(dirname "$0")/../../lib/common.sh"

kit_require gateway-settings gatewayclass:bnk-gatewayclass
kit_apply manifests/01-namespace.yaml manifests/02-gatewaysettings.yaml \
	manifests/03-backend.yaml manifests/04-gateway.yaml manifests/05-httproute.yaml
