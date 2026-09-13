package deploy

import (
	"strings"
	"testing"
)

func TestRenderInfra(t *testing.T) {
	out := RenderInfra("default")
	for _, want := range []string{
		"kind: Infra",
		"apiVersion: gateway.k8s.f5.com/v1alpha1",
		"name: infra",
		"namespace: default",
		"name: bnk-dynamic-vips",
		"rangeStart: 203.0.113.110",
		"rangeEnd: 203.0.113.119",
		"networkAttachments: []",
		"networks: []",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderInfra missing %q:\n%s", want, out)
		}
	}
	// The IPAM slice must stay clear of the static VIPs the scenarios pin.
	for _, static := range []string{"203.0.113.100", "203.0.113.109", "203.0.113.120"} {
		if strings.Contains(out, static) {
			t.Errorf("Infra pool must not include static VIP %s", static)
		}
	}
}
