package deploy

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRenderLicenseCR_Defaults(t *testing.T) {
	got, err := RenderLicenseCR(LicenseInputs{JWT: "abc.def.ghi"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"apiVersion: k8s.f5net.com/v1",
		"kind: License",
		"name: " + LicenseCRName,
		"namespace: " + SharedComponentNamespace,
		`operationMode: "connected"`,
		`jwt: "abc.def.ghi"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestRenderLicenseCR_Overrides(t *testing.T) {
	got, err := RenderLicenseCR(LicenseInputs{
		Name:          "lab-license",
		Namespace:     "f5-utils",
		OperationMode: "disconnected",
		JWT:           "header.body.sig",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"name: lab-license",
		"namespace: f5-utils",
		`operationMode: "disconnected"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderLicenseCR_RejectsMultilineJWT(t *testing.T) {
	_, err := RenderLicenseCR(LicenseInputs{JWT: "abc\ndef\nghi"})
	if err == nil {
		t.Fatal("expected error for multi-line JWT")
	}
	if !strings.Contains(err.Error(), "newlines") {
		t.Errorf("error should mention newlines, got %v", err)
	}
}

func TestRenderLicenseCR_NoJWKSRefByDefault(t *testing.T) {
	got, err := RenderLicenseCR(LicenseInputs{JWT: "abc.def.ghi"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "jwksConfigMapRef") {
		t.Errorf("unexpected jwksConfigMapRef:\n%s", got)
	}
}

func TestRenderLicenseCR_JWKSRef(t *testing.T) {
	got, err := RenderLicenseCR(LicenseInputs{JWT: "abc.def.ghi", JWKSConfigMap: JWKSConfigMapName})
	if err != nil {
		t.Fatal(err)
	}
	want := "  jwksConfigMapRef:\n    name: " + JWKSConfigMapName + "\n    key: " + JWKSConfigMapKey + "\n"
	if !strings.Contains(got, want) {
		t.Errorf("missing %q in:\n%s", want, got)
	}
}

func TestRenderJWKSConfigMap(t *testing.T) {
	got, err := RenderJWKSConfigMap("")
	if err != nil {
		t.Fatal(err)
	}
	var cm struct {
		Metadata struct{ Name, Namespace string }
		Data     map[string]string
	}
	if err := yaml.Unmarshal([]byte(got), &cm); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, got)
	}
	if cm.Metadata.Name != JWKSConfigMapName || cm.Metadata.Namespace != SharedComponentNamespace {
		t.Errorf("metadata = %+v", cm.Metadata)
	}
	var jwks struct {
		Keys []struct {
			Kid string   `json:"kid"`
			Alg string   `json:"alg"`
			X5c []string `json:"x5c"`
		} `json:"keys"`
	}
	if err := json.Unmarshal([]byte(cm.Data[JWKSConfigMapKey]), &jwks); err != nil {
		t.Fatalf("data.%s is not JWKS JSON: %v", JWKSConfigMapKey, err)
	}
	if len(jwks.Keys) != 1 || jwks.Keys[0].Kid != "v1" || jwks.Keys[0].Alg != "RS512" || len(jwks.Keys[0].X5c) != 4 {
		t.Errorf("unexpected jwks: %+v", jwks)
	}
}
