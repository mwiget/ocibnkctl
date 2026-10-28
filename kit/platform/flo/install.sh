#!/bin/sh
# Phase flo: resolve the release manifest, install the F5 Lifecycle Operator,
# generate the CWC API certificates. Idempotent. Variables: ../platform.env.
# Records release.env (FLO_CHART_VERSION, CERT_GEN_CHART_VERSION), the
# release manifest and the rendered values under KIT_STATE.
. "$(dirname "$0")/../../lib/common.sh"
. "$KIT_SCENARIO_DIR/../platform.env"
. "$KIT_SCENARIO_DIR/../lib.sh"

kit_log "[1/6] release manifest $MANIFEST_VERSION"
registry_login
plat_work
helm pull "$MANIFEST_CHART" --version "$MANIFEST_VERSION" -d "$PLAT_WORK" >&2 || kit_die "helm pull $MANIFEST_CHART $MANIFEST_VERSION"
tar -xzf "$PLAT_WORK/f5-bigip-k8s-manifest-$MANIFEST_VERSION.tgz" -C "$PLAT_WORK" || kit_die "unpack the release manifest"
manifest=$PLAT_WORK/f5-bigip-k8s-manifest-$MANIFEST_VERSION/bigip-k8s-manifest-$MANIFEST_VERSION.yaml
[ -f "$manifest" ] || kit_die "release manifest missing $manifest"
mkdir -p "$KIT_STATE/release-manifest" && cp "$manifest" "$KIT_STATE/release-manifest/manifest.yaml"
# chart NAME — releases[0].helm_charts[name=NAME].version
chart() {
	awk -v want="$1" '
		/^releases:/ { rel = 1 } rel && /^  - / { n++ } n > 1 { exit }
		/helm_charts:/ { charts = 1; next } /docker_images:/ { charts = 0 }
		charts && /name:/ { name = $NF } charts && /version:/ && name == want { print $NF; exit }' "$manifest" | tr -d '"'"'"
}
FLO_CHART_VERSION=$(chart charts/f5-lifecycle-operator)
CERT_GEN_CHART_VERSION=$(chart utils/f5-cert-gen)
[ -n "$FLO_CHART_VERSION" ] || kit_die "release manifest $MANIFEST_VERSION has no charts/f5-lifecycle-operator"
[ -n "$CERT_GEN_CHART_VERSION" ] || kit_die "release manifest $MANIFEST_VERSION has no utils/f5-cert-gen"
printf 'FLO_CHART_VERSION=%s\nCERT_GEN_CHART_VERSION=%s\n' "$FLO_CHART_VERSION" "$CERT_GEN_CHART_VERSION" >"$(plat_state release.env)"
kit_log "      FLO chart $FLO_CHART_VERSION, f5-cert-gen $CERT_GEN_CHART_VERSION"

kit_log "[2/6] JWT ($JWT_FILE): $(jwt_type) JWT"
[ -s "$(plat_abs "$JWT_FILE")" ] || kit_die "JWT not found at $JWT_FILE (JWT_FILE)"

kit_log "[3/6] bnk-ca certificate issuer chain"
kubectl apply -f bnk-ca-issuer.yaml >&2 || kit_die "apply bnk-ca-issuer.yaml"
kubectl -n cert-manager wait --for=condition=Ready certificate/bnk-ca --timeout=3m >&2 || kit_die "bnk-ca certificate not Ready"

kit_log "[4/6] FLO $FLO_CHART_VERSION"
values=$(plat_state flo-values-rendered.yaml)
plat_render flo-values.yaml OPERATOR_NAMESPACE="$OPERATOR_NAMESPACE" SHARED_NAMESPACE="$SHARED_NAMESPACE" \
	CLUSTER_ISSUER=bnk-ca-cluster-issuer >"$values" || kit_die "render flo-values.yaml"
helm upgrade --install flo "$FLO_CHART" --version "$FLO_CHART_VERSION" --namespace "$OPERATOR_NAMESPACE" \
	--create-namespace -f "$values" >&2 || kit_die "helm install FLO"

kit_log "[5/6] waiting for the FLO controller"
kubectl -n "$OPERATOR_NAMESPACE" wait --for=condition=Available deployment \
	-l app.kubernetes.io/name=f5-lifecycle-operator --timeout=5m >&2 ||
	kit_log "warning: FLO not Available within 5m (later phases wait on what they need)"

kit_log "[6/6] CWC API certificates (f5-cert-gen $CERT_GEN_CHART_VERSION)"
certs=$PLAT_WORK/cwc-certs
mkdir -p "$certs" && helm pull "$CERT_GEN_CHART" --version "$CERT_GEN_CHART_VERSION" -d "$certs" >&2 &&
	tar -xzf "$certs/f5-cert-gen-$CERT_GEN_CHART_VERSION.tgz" -C "$certs" || kit_die "fetch f5-cert-gen"
san=f5-spk-cwc.$SHARED_NAMESPACE.svc.cluster.local
gen="cd \"\$W\" && sh cert-gen/gen_cert.sh -s=api-server -a=$san -n=1"
# gen_cert.sh needs make, openssl and python3: use them when installed,
# otherwise K8S_TOOLS_IMAGE (which adds them at run time).
if command -v make >/dev/null && command -v openssl >/dev/null && command -v python3 >/dev/null; then
	W=$certs sh -c "$gen" >&2
else
	docker run --rm -v "$certs:/work" -e W=/work --network=host "$K8S_TOOLS_IMAGE" \
		sh -c "apk add --no-cache make openssl python3 >/dev/null && $gen" >&2
fi || kit_die "gen_cert.sh"
for f in cwc-license-certs.yaml cwc-license-client-certs.yaml; do
	[ -f "$certs/$f" ] || kit_die "gen_cert.sh did not produce $f"
	# gen_cert.sh's manifests carry no namespace: pass it to kubectl.
	kubectl -n "$SHARED_NAMESPACE" apply -f "$certs/$f" >&2 || kit_die "apply $f"
done
kit_log "flo done"
