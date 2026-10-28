package deploy

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestExampleLicenseTstJWT(t *testing.T) {
	raw, err := os.ReadFile("../../examples/license-tst-jwt.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	var docs []map[string]any
	for {
		var d map[string]any
		if err := dec.Decode(&d); err != nil {
			break
		}
		docs = append(docs, d)
	}
	if len(docs) != 2 {
		t.Fatalf("docs = %d", len(docs))
	}
	emb, _ := os.ReadFile("../embedded/templates/jwks-tst.json")
	var a, b any
	json.Unmarshal(emb, &a)
	json.Unmarshal([]byte(docs[0]["data"].(map[string]any)["key"].(string)), &b)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Error("ConfigMap JWKS differs from the embedded jwks-tst.json")
	}
	cm, _ := RenderJWKSConfigMap("")
	cr, _ := RenderLicenseCR(LicenseInputs{JWT: "<JWT>", JWKSConfigMap: JWKSConfigMapName})
	var gotCR, wantCR map[string]any
	yaml.Unmarshal([]byte(cr), &wantCR)
	gotCR = docs[1]
	gj, _ := json.Marshal(gotCR)
	wj, _ := json.Marshal(wantCR)
	if string(gj) != string(wj) {
		t.Errorf("License differs from RenderLicenseCR:\n got %s\nwant %s", gj, wj)
	}
	var gotCM, wantCM map[string]any
	yaml.Unmarshal([]byte(cm), &wantCM)
	gotCM = docs[0]
	if gotCM["metadata"].(map[string]any)["name"] != wantCM["metadata"].(map[string]any)["name"] ||
		gotCM["metadata"].(map[string]any)["namespace"] != wantCM["metadata"].(map[string]any)["namespace"] {
		t.Error("ConfigMap metadata differs")
	}
}
