#!/bin/sh
# Apply the http-routing-e2e workload: namespace, nginx, Gateway on VIP,
# HTTPRoute for HOST. The GatewayClass is normally already applied by
# `ocibnkctl deploy cne`; re-applying it here is idempotent.
. "$(dirname "$0")/../../lib/common.sh"

# The data-plane checks reach the VIP over the routes TMM advertises with
# BGP, which only the anycast-bgp data plane does.
kit_require crd:gateways.gateway.networking.k8s.io anycast-bgp
kit_apply manifests/01-gatewayclass.yaml
# A GatewayClass that was just created needs the CNE controller to accept it.
kubectl wait --for=condition=Accepted "gatewayclass/$GATEWAYCLASS" --timeout=3m >&2
kit_require "gatewayclass:$GATEWAYCLASS"
kit_apply manifests/02-namespace.yaml manifests/04-backend.yaml \
	manifests/05-gateway.yaml.tmpl manifests/06-httproute.yaml
