#!/bin/sh
# Delete the scenario namespace, the F5EPP first so the controller tears down
# the EPP stack it rendered while it still has RBAC there. Left in place on
# purpose: NAMESPACE in CNEInstance.spec.eppNamespaces (removing it rolls the
# CNE controller again; naming an absent namespace is harmless) and the GAIE
# CRDs (cluster-scoped; deleting them deletes every InferencePool).
. "$(dirname "$0")/../../lib/common.sh"

kubectl -n "$NAMESPACE" delete "f5-epps.inference.k8s.f5.com" "$EPP" --ignore-not-found --timeout=2m >&2
kit_delete_namespace "$NAMESPACE"
