#!/bin/sh
# Apply a Gateway + HTTPRoute + nginx in a brand-new namespace.
. "$(dirname "$0")/../../lib/common.sh"

kit_require crd:cneinstances.k8s.f5.com "gatewayclass:$GATEWAYCLASS"
kit_apply manifests/01-namespace.yaml manifests/03-backend.yaml \
	manifests/04-gateway.yaml manifests/05-httproute.yaml
