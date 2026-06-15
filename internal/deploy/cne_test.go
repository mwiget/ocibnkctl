package deploy

import (
	"strings"
	"testing"

	"github.com/mwiget/ocibnkctl/internal/poc"
)

func TestRenderCNEInstance_TMMReplicasTracksNodes(t *testing.T) {
	cases := []struct {
		nodes int
		want  string
	}{
		{0, "tmmReplicas: 1"}, // unset → defaults to 1
		{1, "tmmReplicas: 1"},
		{3, "tmmReplicas: 3"},
	}
	for _, c := range cases {
		p := &poc.PoC{Cluster: poc.Cluster{TMMNodes: c.nodes}}
		got, err := RenderCNEInstance(p)
		if err != nil {
			t.Fatalf("nodes %d: %v", c.nodes, err)
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("nodes %d: want %q in render", c.nodes, c.want)
		}
	}
}

func TestRenderCNEInstance_ActiveActiveNetworkAttachments(t *testing.T) {
	// Default (active/active off): no networkAttachments block at all.
	off, err := RenderCNEInstance(&poc.PoC{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(off, "networkAttachments") {
		t.Errorf("default render should not contain networkAttachments:\n%s", off)
	}
	// On: the DAG NAD is injected.
	on, err := RenderCNEInstance(&poc.PoC{BNK: poc.BNK{ActiveActive: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(on, "networkAttachments:") || !strings.Contains(on, "- "+DAGNADName) {
		t.Errorf("active/active render should attach %q:\n%s", DAGNADName, on)
	}
}

// TestRenderCNEInstance_DataplaneMode pins the NAD + mapres dispatch for
// each tmm_dataplane_mode: standby (no NAD, mapres TRUE), selfip-dag (DAG
// NAD, mapres TRUE), anycast-bgp (bnk-bgp NAD, mapres FALSE). The legacy
// tmm_active_active bool must render identically to selfip-dag.
func TestRenderCNEInstance_DataplaneMode(t *testing.T) {
	cases := []struct {
		name       string
		bnk        poc.BNK
		wantNAD    string // "" → no networkAttachments block
		wantMapres string
	}{
		{"standby default", poc.BNK{}, "", `value: "TRUE"`},
		{"explicit standby", poc.BNK{TMMDataplaneMode: poc.DataplaneStandby}, "", `value: "TRUE"`},
		{"selfip-dag", poc.BNK{TMMDataplaneMode: poc.DataplaneSelfIPDAG}, DAGNADName, `value: "TRUE"`},
		{"legacy bool == selfip-dag", poc.BNK{ActiveActive: true}, DAGNADName, `value: "TRUE"`},
		{"anycast-bgp", poc.BNK{TMMDataplaneMode: poc.DataplaneAnycastBGP}, BGPNADName, `value: "FALSE"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := RenderCNEInstance(&poc.PoC{BNK: c.bnk})
			if err != nil {
				t.Fatal(err)
			}
			if c.wantNAD == "" {
				if strings.Contains(got, "networkAttachments") {
					t.Errorf("want no networkAttachments block:\n%s", got)
				}
			} else if !strings.Contains(got, "- "+c.wantNAD) {
				t.Errorf("want NAD %q attached:\n%s", c.wantNAD, got)
			}
			if !strings.Contains(got, c.wantMapres) {
				t.Errorf("want mapres %s:\n%s", c.wantMapres, got)
			}
		})
	}
}

func TestRenderCNEInstance_MetricSubsystemByProfile(t *testing.T) {
	cases := []struct {
		profile string
		want    string
	}{
		{"", "enabled: true"},                      // default = standard
		{poc.HostProfileStandard, "enabled: true"}, // explicit standard
		{poc.HostProfileSmall, "enabled: false"},   // small host sheds the observer sidecar
	}
	for _, c := range cases {
		p := &poc.PoC{BNK: poc.BNK{HostProfile: c.profile}}
		got, err := RenderCNEInstance(p)
		if err != nil {
			t.Fatalf("profile %q: %v", c.profile, err)
		}
		// The metricSubsystem block is the only telemetry toggle that flips;
		// assert the rendered enabled line under it.
		idx := strings.Index(got, "metricSubsystem:")
		if idx < 0 {
			t.Fatalf("profile %q: no metricSubsystem block:\n%s", c.profile, got)
		}
		// Look only at a short window right after "metricSubsystem:" — its
		// `enabled:` line sits there (the only other `enabled:` is under
		// loggingSubsystem, which is above this index). Cap the window so a
		// short render can never slice out of range.
		block := got[idx:]
		end := 80
		if end > len(block) {
			end = len(block)
		}
		window := block[:end]
		if !strings.Contains(window, c.want) {
			t.Errorf("profile %q: want metricSubsystem %q\n--- got ---\n%s", c.profile, c.want, window)
		}
	}
}
