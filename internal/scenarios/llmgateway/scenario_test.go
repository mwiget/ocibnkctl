package llmgateway

import (
	"strings"
	"testing"
)

func TestRenderManifests(t *testing.T) {
	for _, name := range []string{"01-mock-llm.yaml", "02-setup-job.yaml"} {
		got, err := render(name, defaults())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(got, "{{") {
			t.Errorf("%s: unrendered placeholder:\n%s", name, got)
		}
	}
	job, _ := render("02-setup-job.yaml", defaults())
	for _, want := range []string{`value: "demo"`, `value: "203.0.113.140"`, `value: "gpt-4o-mini"`,
		`value: "http://mock-llm.llm-demo.svc:8080/v1"`, "name: llm-admin", "name: llm-provider"} {
		if !strings.Contains(job, want) {
			t.Errorf("setup Job missing %q:\n%s", want, job)
		}
	}
	mock, _ := render("01-mock-llm.yaml", defaults())
	if !strings.Contains(mock, `"--model=gpt-4o-mini"`) {
		t.Errorf("mock-llm must serve the demo model:\n%s", mock)
	}
}

// The setup script is shell, copied verbatim: Go template syntax in it would
// be a bug, and its route name must match the one Verify looks for in TMM.
func TestSetupScriptVerbatim(t *testing.T) {
	raw, _ := manifestFS.ReadFile("manifests/" + setupScript)
	got, err := render(setupScript, defaults())
	if err != nil || got != string(raw) {
		t.Fatalf("setup script must be copied as is (err=%v)", err)
	}
	if !strings.Contains(got, "ROUTE=$TENANT-llm") || Route != Tenant+"-llm" {
		t.Errorf("route naming drifted between llm-setup.sh and Route=%q", Route)
	}
}

func TestVirtualKeyAndRedaction(t *testing.T) {
	logs := "== AI gateway API\n  virtual key: created\nVKEY=vk_abc123XYZ\n"
	if got := virtualKey(logs); got != "vk_abc123XYZ" {
		t.Fatalf("virtualKey = %q", got)
	}
	red := redactKey(logs)
	if strings.Contains(red, "vk_abc123XYZ") {
		t.Fatalf("token leaked into redacted log: %q", red)
	}
	if virtualKey("no key here\n") != "" {
		t.Error("expected no key")
	}
}

func TestParseChat(t *testing.T) {
	cases := []struct {
		out  string
		code int
		body string
	}{
		{`{"id":"x","usage":{}}` + "\n" + statusMark + "200\n", 200, `{"id":"x","usage":{}}`},
		// The runtime's pull progress lands in the same stream.
		{"Unable to find image locally\nPull complete\n" + `{"error":"denied"}` + "\n" + statusMark + "401\n", 401, `{"error":"denied"}`},
		{"curl: (28) timed out\n" + statusMark + "000\n", 0, "curl: (28) timed out"},
		{"docker: cannot connect", 0, "docker: cannot connect"},
	}
	for _, c := range cases {
		code, body := parseChat(c.out)
		if code != c.code || body != c.body {
			t.Errorf("parseChat(%q) = %d, %q; want %d, %q", c.out, code, body, c.code, c.body)
		}
	}
}

func TestConfigMapIndentsScript(t *testing.T) {
	cm := configMap("llm-setup", setupScript, "#!/bin/sh\necho hi\n")
	if !strings.Contains(cm, "  llm-setup.sh: |\n    #!/bin/sh\n    echo hi\n") {
		t.Errorf("unexpected ConfigMap:\n%s", cm)
	}
	if !strings.Contains(cm, "namespace: llm-demo") {
		t.Errorf("ConfigMap must target llm-demo:\n%s", cm)
	}
}
