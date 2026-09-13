package deploy

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseShrinkRequests(t *testing.T) {
	cases := []struct {
		raw     string
		cpu     string
		mem     string
		wantOK  bool
		comment string
	}{
		{"25m 128Mi", "25m", "128Mi", true, "live policy output"},
		{"  50m   256Mi\n", "50m", "256Mi", true, "whitespace"},
		{"", "", "", false, "policy without the rule"},
		{"25m", "", "", false, "memory missing"},
		{"25m 128Mi extra", "", "", false, "unexpected shape"},
	}
	for _, c := range cases {
		cpu, mem, ok := parseShrinkRequests(c.raw)
		if cpu != c.cpu || mem != c.mem || ok != c.wantOK {
			t.Errorf("%s: parseShrinkRequests(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.comment, c.raw, cpu, mem, ok, c.cpu, c.mem, c.wantOK)
		}
	}
}

// TestShrinkRequestsJSONPathMatchesPolicy pins shrinkRequestsJSONPath to the
// rendered policy: rule name, foreach[0], containers[0].resources.requests. If
// the template is restructured, ActiveShrinkRequests would silently stop
// finding the cap and Multus would go back to uncapped.
func TestShrinkRequestsJSONPathMatchesPolicy(t *testing.T) {
	rendered, err := RenderShrinkPolicy(ShrinkInputs{CPURequest: "33m", MemoryRequest: "77Mi"})
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Spec struct {
			Rules []struct {
				Name   string `yaml:"name"`
				Mutate struct {
					Foreach []struct {
						PatchStrategicMerge struct {
							Spec struct {
								Containers []struct {
									Resources struct {
										Requests map[string]string `yaml:"requests"`
									} `yaml:"resources"`
								} `yaml:"containers"`
							} `yaml:"spec"`
						} `yaml:"patchStrategicMerge"`
					} `yaml:"foreach"`
				} `yaml:"mutate"`
			} `yaml:"rules"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &policy); err != nil {
		t.Fatalf("unmarshal rendered policy: %v", err)
	}
	for _, rule := range policy.Spec.Rules {
		if rule.Name != "shrink-f5-requests" {
			continue
		}
		if len(rule.Mutate.Foreach) == 0 || len(rule.Mutate.Foreach[0].PatchStrategicMerge.Spec.Containers) == 0 {
			t.Fatal("shrink-f5-requests has no foreach[0] container patch")
		}
		req := rule.Mutate.Foreach[0].PatchStrategicMerge.Spec.Containers[0].Resources.Requests
		if req["cpu"] != "33m" || req["memory"] != "77Mi" {
			t.Errorf("foreach[0] container requests = %v, want cpu=33m memory=77Mi", req)
		}
		return
	}
	t.Fatal("rendered policy has no shrink-f5-requests rule")
}

func TestMultusResourcesPatch(t *testing.T) {
	var ops []struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal([]byte(multusResourcesPatch("25m", "128Mi")), &ops); err != nil {
		t.Fatalf("patch is not valid JSON: %v", err)
	}
	got := map[string]string{}
	for _, o := range ops {
		if o.Op != "replace" {
			t.Errorf("op %q on %s, want replace", o.Op, o.Path)
		}
		got[o.Path] = o.Value
	}
	const base = "/spec/template/spec/containers/0/resources/"
	want := map[string]string{
		base + "limits/memory":   "500Mi",
		base + "requests/cpu":    "25m",
		base + "requests/memory": "128Mi",
	}
	for path, v := range want {
		if got[path] != v {
			t.Errorf("%s = %q, want %q", path, got[path], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("patch has %d ops, want %d: %v", len(got), len(want), got)
	}
}
