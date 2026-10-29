#!/bin/sh
# Phase prereqs: namespaces, the FAR image-pull secret, cert-manager.
# Idempotent. Variables: ../platform.env.
. "$(dirname "$0")/../../lib/common.sh"
. "$KIT_SCENARIO_DIR/../platform.env"
. "$KIT_SCENARIO_DIR/../lib.sh"

kit_log "[1/5] tools: kubectl, helm, cluster reachable"
command -v kubectl >/dev/null || kit_die "kubectl not found on PATH"
command -v helm >/dev/null || kit_die "helm not found on PATH"
kubectl version >/dev/null 2>&1 || kit_die "cannot reach the cluster (KUBECONFIG=${KUBECONFIG:-unset})"

kit_log "[2/5] FAR credential from $FAR_KEY"
far_init

kit_log "[3/5] namespaces $OPERATOR_NAMESPACE, $SHARED_NAMESPACE"
render_namespaces | kubectl apply -f - >&2 || kit_die "namespaces"

kit_log "[4/5] far-secret in $OPERATOR_NAMESPACE, $SHARED_NAMESPACE, $BNK_NAMESPACE"
render_far_secrets >"$PLAT_WORK/far-secrets.yaml" || kit_die "render far-secret"
kubectl apply -f "$PLAT_WORK/far-secrets.yaml" >&2 || kit_die "apply far-secret"

kit_log "[5/5] cert-manager $CERT_MANAGER_VERSION"
helm upgrade --install cert-manager cert-manager --repo "$CERT_MANAGER_REPO" --version "$CERT_MANAGER_VERSION" \
	--namespace cert-manager --create-namespace --set installCRDs=true >&2 || kit_die "helm install cert-manager"
kubectl -n cert-manager wait --for=condition=Available deployment/cert-manager-webhook --timeout=5m >&2 ||
	kit_die "cert-manager-webhook not Available"
kit_log "prereqs done"
