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

func TestRenderBGPNAD(t *testing.T) {
	got := RenderBGPNAD("default")
	for _, want := range []string{
		"kind: NetworkAttachmentDefinition",
		"name: " + BGPNADName,
		"namespace: default",
		`"type": "bridge"`,
		`"bridge": "` + BGPBridge + `"`,
		BGPSubnet,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderBGPNAD missing %q:\n%s", want, got)
		}
	}
}

func TestRenderAnycastZebosConfigMap(t *testing.T) {
	got := RenderAnycastZebosConfigMap("default", "192.168.99.20", "")
	for _, want := range []string{
		"kind: ConfigMap",
		"name: " + BGPRoutingTemplateCM,
		"router bgp 65000",
		// router-id must render as the per-pod token FLO expands, NOT a
		// fixed value — the linchpin for distinct per-pod sessions.
		"bgp router-id %%POD_IP%%",
		"redistribute kernel",
		"redistribute connected",
		"neighbor 192.168.99.20 remote-as 65001",
		"neighbor 192.168.99.20 update-source net1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderAnycastZebosConfigMap missing %q:\n%s", want, got)
		}
	}
	// Empty vip → no static network statement (rely on redistribute kernel).
	if strings.Contains(got, "network ") {
		t.Errorf("empty vip should emit no `network` statement:\n%s", got)
	}
	// Non-empty vip → a /32 network statement is added.
	withVIP := RenderAnycastZebosConfigMap("default", "192.168.99.20", "203.0.113.50")
	if !strings.Contains(withVIP, "network 203.0.113.50/32") {
		t.Errorf("non-empty vip should emit `network 203.0.113.50/32`:\n%s", withVIP)
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
