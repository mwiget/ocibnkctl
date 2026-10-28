package deploy

import (
	"fmt"
	"strings"

	"github.com/mwiget/ocibnkctl/kit"
)

// LicenseCRName is the canonical name of the cluster-wide license CR
// per F5's 2.3 docs ("f5-cne-cluster-license"). The IBM ROKS terraform
// uses "bnk-license" — they're functionally equivalent. We pick the
// public-docs name so operators following along can `kubectl get
// license.k8s.f5net.com f5-cne-cluster-license` straight from the docs
// without renaming anything.
const LicenseCRName = "f5-cne-cluster-license"

// LicenseInputs are substituted into license-cr.yaml.tmpl.
type LicenseInputs struct {
	Name          string // defaults to LicenseCRName
	Namespace     string // defaults to SharedComponentNamespace
	OperationMode string // "connected" | "disconnected"
	JWT           string // raw JWT (single line, no quotes)
	// JWKSConfigMap, when set, adds spec.jwksConfigMapRef so CWC verifies
	// the JWT against that ConfigMap's JWKS instead of its built-in keys.
	// See RenderJWKSConfigMap.
	JWKSConfigMap string
	JWKSKey       string // key inside JWKSConfigMap; defaults to JWKSConfigMapKey
}

// RenderLicenseCR substitutes the embedded license-cr template. The JWT
// goes onto a single YAML scalar line — gen_cert.sh / kubectl-apply
// pipelines balk on multi-line / wrapped JWTs.
func RenderLicenseCR(in LicenseInputs) (string, error) {
	if in.Name == "" {
		in.Name = LicenseCRName
	}
	if in.Namespace == "" {
		in.Namespace = SharedComponentNamespace
	}
	if in.OperationMode == "" {
		in.OperationMode = "connected"
	}
	if in.JWKSConfigMap != "" && in.JWKSKey == "" {
		in.JWKSKey = JWKSConfigMapKey
	}
	if strings.ContainsAny(in.JWT, "\r\n") {
		return "", fmt.Errorf("JWT contains newlines (got %d bytes); paste as a single line", len(in.JWT))
	}
	return kit.Render("platform/cne/license.yaml", map[string]string{
		"NAME":           in.Name,
		"NAMESPACE":      in.Namespace,
		"OPERATION_MODE": in.OperationMode,
		"JWT":            in.JWT,
		"JWKS_CONFIGMAP": in.JWKSConfigMap,
		"JWKS_KEY":       in.JWKSKey,
	})
}

// JWKSConfigMapName / JWKSConfigMapKey name the ConfigMap that carries the
// TEEM test-environment JWKS, per the TEEM team's recipe.
const (
	JWKSConfigMapName = "cwc-jwks-cm"
	JWKSConfigMapKey  = "key"
)

// RenderJWKSConfigMap returns the ConfigMap that lets CWC verify a tst JWT.
//
// CWC's built-in tst signing certs expired, so tst JWTs failed verification
// server-side and only prod JWTs worked. TEEM published fresh tst JWKS certs
// (2026-10-12) at https://product-tst.apis.f5networks.net/ee/v1/keys/jwks;
// a License CR that points spec.jwksConfigMapRef at this ConfigMap makes CWC
// use them. CWC reads the ConfigMap from its own POD_NAMESPACE, so namespace
// must be the CWC namespace (SharedComponentNamespace). The JWKS is embedded
// as kit/platform/cne/jwks-tst.json — refresh that file when TEEM rotates keys.
func RenderJWKSConfigMap(namespace string) (string, error) {
	if namespace == "" {
		namespace = SharedComponentNamespace
	}
	raw, err := kit.FS.ReadFile("platform/cne/jwks-tst.json")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\n  namespace: %s\ndata:\n  %s: |\n",
		JWKSConfigMapName, namespace, JWKSConfigMapKey)
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		b.WriteString("    " + line + "\n")
	}
	return b.String(), nil
}

// ErrLicensePendingVerification signals the disconnected-mode case
// where the License CR exists, was accepted by CWC, but is awaiting
// the operator's manual licensing-server registration call. Callers
// should typically log + continue rather than fail the deploy.
var ErrLicensePendingVerification = fmt.Errorf("license stuck at PendingVerification (disconnected-mode operator action required)")
