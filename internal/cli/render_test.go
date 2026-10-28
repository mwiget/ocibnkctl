package cli

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mwiget/ocibnkctl/internal/poc"
)

func renderFor(t *testing.T, mode string) map[string]renderedFile {
	t.Helper()
	p := poc.New("rtest")
	p.BNK.TMMDataplaneMode = mode
	files, err := renderBundle(t.TempDir(), p, false)
	if err != nil {
		t.Fatalf("renderBundle(%s): %v", mode, err)
	}
	byPath := map[string]renderedFile{}
	for _, f := range files {
		if _, dup := byPath[f.Path]; dup {
			t.Fatalf("duplicate path %s", f.Path)
		}
		byPath[f.Path] = f
	}
	return byPath
}

func TestRenderBundleModes(t *testing.T) {
	cases := []struct {
		mode        string
		want, avoid []string
	}{
		{poc.DataplaneAnycastBGP,
			[]string{"20-cne/01-nad-bnk-bgp.yaml", "20-cne/02-ocnos-template.yaml", "20-cne/03-cne-instance.yaml", "20-cne/06-infra.yaml"},
			[]string{"20-cne/01-nad-dag.yaml"}},
		{poc.DataplaneSelfIPDAG,
			[]string{"20-cne/01-nad-dag.yaml", "20-cne/02-cne-instance.yaml", "20-cne/05-f5spkvlan.yaml"},
			[]string{"20-cne/05-infra.yaml"}},
		{poc.DataplaneStandby,
			[]string{"20-cne/01-cne-instance.yaml", "20-cne/04-infra.yaml"},
			[]string{"20-cne/01-nad-bnk-bgp.yaml"}},
	}
	common := []string{
		"README.md", "00-prereqs/01-namespaces.yaml", "10-flo/01-bnk-ca-issuer.yaml",
		"10-flo/02-flo-values.yaml", "15-shrink/01-kyverno-shrink-requests.yaml",
		"scenarios/http-routing-e2e/README.md", "scenarios/http-routing-e2e/manifests/06-httproute.yaml",
		"scenarios/http-routing-e2e/verify.sh", "lib/common.sh",
		"scenarios/bgp-peer-frr/README.md",
	}
	for _, c := range cases {
		t.Run(c.mode, func(t *testing.T) {
			files := renderFor(t, c.mode)
			for _, w := range append(common, c.want...) {
				if _, ok := files[w]; !ok {
					t.Errorf("missing %s", w)
				}
			}
			for _, a := range c.avoid {
				if _, ok := files[a]; ok {
					t.Errorf("unexpected %s", a)
				}
			}
		})
	}
}

// Every .yaml in the bundle must parse — a hand operator applies them as-is.
func TestRenderBundleYAMLParses(t *testing.T) {
	for _, f := range renderFor(t, poc.DataplaneAnycastBGP) {
		if !strings.HasSuffix(f.Path, ".yaml") {
			continue
		}
		dec := yaml.NewDecoder(strings.NewReader(f.Body))
		for {
			var v any
			err := dec.Decode(&v)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Errorf("%s: %v", f.Path, err)
				break
			}
		}
	}
}

// Without --include-secrets no file may carry the FAR secret or a JWT, and
// the License CR is a template the README fills via envsubst.
func TestRenderBundleOmitsSecrets(t *testing.T) {
	files := renderFor(t, poc.DataplaneAnycastBGP)
	for _, f := range files {
		if strings.Contains(f.Body, ".dockerconfigjson:") {
			t.Errorf("%s carries a pull secret", f.Path)
		}
		want := os.FileMode(0o644)
		if strings.HasSuffix(f.Path, ".sh") && !strings.Contains(f.Path, "/manifests/") {
			want = 0o755 // scenario scripts and lib helpers stay executable
		}
		if f.Mode != want {
			t.Errorf("%s mode %o, want %o", f.Path, f.Mode, want)
		}
	}
	lic, ok := files["20-cne/04-license.yaml.tmpl"]
	if !ok || !strings.Contains(lic.Body, "${BNK_JWT}") {
		t.Fatalf("license template missing or without ${BNK_JWT} placeholder")
	}
}

func TestRenderBundleIncludeSecretsNeedsKeys(t *testing.T) {
	if _, err := renderBundle(t.TempDir(), poc.New("rtest"), true); err == nil {
		t.Fatal("--include-secrets with no keys/ should fail")
	}
}

func TestPrepareRenderDir(t *testing.T) {
	dir := t.TempDir()
	if err := prepareRenderDir(filepath.Join(dir, "new"), false); err != nil {
		t.Fatalf("absent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "precious"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareRenderDir(dir, true); err == nil {
		t.Fatal("non-render dir must be refused even with --force")
	}
	if err := os.WriteFile(filepath.Join(dir, renderMarker), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareRenderDir(dir, false); err == nil {
		t.Fatal("previous render without --force must be refused")
	}
	if err := prepareRenderDir(dir, true); err != nil {
		t.Fatalf("previous render with --force: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("--force should have removed the previous render")
	}
}

func TestRunRenderWritesBundle(t *testing.T) {
	repo := t.TempDir()
	if err := poc.New("rtest").Save(repo); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "bundle")
	var buf bytes.Buffer
	if err := runRender(&buf, &renderFlags{pocDir: repo, outDir: out}); err != nil {
		t.Fatalf("runRender: %v", err)
	}
	for _, f := range []string{"README.md", renderMarker, "20-cne/03-cne-instance.yaml"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
}

// A test JWT in keys/ makes render do what `deploy cne` does: the License
// references the JWKS ConfigMap and the README applies it first. The token
// itself still stays out of the bundle.
func TestRenderBundleTstJWT(t *testing.T) {
	repo := t.TempDir()
	p := poc.New("rtest")
	enc := base64.RawURLEncoding.EncodeToString
	jwt := enc([]byte(`{"alg":"RS512","jku":"https://product-tst.apis.f5networks.net/ee/v1/keys/jwks"}`)) +
		"." + enc([]byte(`{"sub":"TST-secret-subject"}`)) + ".sig"
	if err := os.MkdirAll(filepath.Join(repo, "keys"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resolveRef(repo, p.BNK.JWTRef), []byte(jwt), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := renderBundle(repo, p, false)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, f := range files {
		if strings.Contains(f.Body, jwt) || strings.Contains(f.Body, "TST-secret-subject") {
			t.Errorf("%s leaks the JWT", f.Path)
		}
		byPath[f.Path] = f.Body
	}
	if !strings.Contains(byPath["20-cne/04-license.yaml.tmpl"], "jwksConfigMapRef:") {
		t.Error("License template lacks jwksConfigMapRef for a tst JWT")
	}
	if !strings.Contains(byPath["README.md"], "kubectl apply -f 20-cne/04-license-jwks-configmap.yaml") {
		t.Error("README does not apply the JWKS ConfigMap for a tst JWT")
	}
}
