package deploy

import (
	"strings"
	"testing"
)

func TestDAGSelfIPs(t *testing.T) {
	cases := []struct {
		n    int
		want []string
	}{
		{0, []string{"192.0.2.10"}}, // defaults to 1
		{1, []string{"192.0.2.10"}},
		{3, []string{"192.0.2.10", "192.0.2.11", "192.0.2.12"}},
	}
	for _, c := range cases {
		got := DAGSelfIPs(c.n)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("DAGSelfIPs(%d) = %v, want %v", c.n, got, c.want)
		}
	}
}

// MaxSelfIPsForTest mirrors poc.MaxTMMNodes without importing poc (avoids
// an import cycle in the deploy package tests).
const MaxSelfIPsForTest = 8

func TestDAGSelfIPsBelowIPAMRange(t *testing.T) {
	// The last self-IP for the max node count must still be < .20.
	ips := DAGSelfIPs(MaxSelfIPsForTest)
	last := ips[len(ips)-1]
	if last != "192.0.2.17" {
		t.Errorf("last self-IP for %d nodes = %q, want 192.0.2.17 (must stay below IPAM start .20)",
			MaxSelfIPsForTest, last)
	}
}

func TestRenderDAGNAD(t *testing.T) {
	got := RenderDAGNAD("default")
	for _, want := range []string{
		"kind: NetworkAttachmentDefinition",
		"name: " + DAGNADName,
		"namespace: default",
		`"type": "bridge"`,
		`"bridge": "` + DAGBridge + `"`,
		DAGSubnet,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderDAGNAD missing %q:\n%s", want, got)
		}
	}
}

func TestRenderTMMVlan(t *testing.T) {
	got := RenderTMMVlan("default", DAGSelfIPs(2))
	for _, want := range []string{
		"kind: F5SPKVlan",
		"name: " + DAGVlanName,
		`interfaces: ["` + DAGInterface + `"]`,
		"selfip_v4s: [192.0.2.10,192.0.2.11]",
		"pod_hash: " + DAGPodHash,
		"cmp_hash: " + DAGPodHash,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderTMMVlan missing %q:\n%s", want, got)
		}
	}
}
