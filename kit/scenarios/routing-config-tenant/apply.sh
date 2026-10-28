#!/bin/sh
# A tenant RoutingTemplate (reads .tenant) bound by a RoutingConfig for VRF.
. "$(dirname "$0")/../../lib/common.sh"

kit_require crd:routingconfigs.k8s.f5net.com legacy-spk
# Immutable: a re-run deletes and recreates them.
kubectl -n "$BNK_NAMESPACE" delete routingconfig scn-rc-tenant --ignore-not-found >&2
kubectl -n "$BNK_NAMESPACE" delete routingtemplate scn-rt-tenant --ignore-not-found >&2
kit_apply manifests/01-routing-template.yaml manifests/02-routing-config.yaml
