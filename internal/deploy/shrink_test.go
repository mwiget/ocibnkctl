package deploy

import (
	"strings"
	"testing"
)

func TestRenderShrinkPolicy_Defaults(t *testing.T) {
	got, err := RenderShrinkPolicy(ShrinkInputs{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"kind: ClusterPolicy",
		"name: " + ShrinkPolicyName,
		"pod-policies.kyverno.io/autogen-controllers: none",
		"namespaces: [" + SharedComponentNamespace + ", f5-operators]",
		`cpu: "` + DefaultShrinkCPURequest + `"`,
		`memory: "` + DefaultShrinkMemoryRequest + `"`,
		// f5-tmm must stay excluded — its pod-manager fights mutation.
		"app: f5-tmm",
		// The Kyverno per-element variable must survive Go templating
		// verbatim, NOT be interpreted by text/template.
		"{{ element.name }}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, got)
		}
	}
	// The exclude block must sit under the rule, and the policy must only
	// touch requests (never limits).
	if strings.Contains(got, "limits:") {
		t.Errorf("policy must not set limits:\n%s", got)
	}
}

func TestRenderShrinkPolicy_Overrides(t *testing.T) {
	got, err := RenderShrinkPolicy(ShrinkInputs{
		SharedComponentNamespace: "f5-shared",
		OperatorNamespace:        "flo-ns",
		CPURequest:               "50m",
		MemoryRequest:            "200Mi",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"namespaces: [f5-shared, flo-ns]",
		`cpu: "50m"`,
		`memory: "200Mi"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
