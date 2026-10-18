package deploy

import (
	"strings"
	"testing"

	"github.com/mwiget/ocibnkctl/internal/poc"
)

func TestRenderCNEInstance_WholeClusterDaemonSet(t *testing.T) {
	// Under wholeCluster, FLO runs TMM as a DaemonSet — one pod per labelled
	// worker — so the render must declare wholeCluster: true and carry NO
	// tmmReplicas (the pod count tracks the node label, not a replica field),
	// regardless of cluster.tmm_nodes.
	for _, nodes := range []int{0, 1, 3} {
		p := &poc.PoC{Cluster: poc.Cluster{TMMNodes: nodes}}
		got, err := RenderCNEInstance(p)
		if err != nil {
			t.Fatalf("nodes %d: %v", nodes, err)
		}
		if !strings.Contains(got, "wholeCluster: true") {
			t.Errorf("nodes %d: want wholeCluster: true in render:\n%s", nodes, got)
		}
		if strings.Contains(got, "tmmReplicas:") {
			t.Errorf("nodes %d: render must not contain a tmmReplicas field under wholeCluster:\n%s", nodes, got)
		}
	}
}

func TestRenderCNEInstance_NetworkAttachmentsByMode(t *testing.T) {
	// Explicit standby: no networkAttachments block at all (mapres grabs the
	// pod's own net, no NAD).
	sb, err := RenderCNEInstance(&poc.PoC{BNK: poc.BNK{TMMDataplaneMode: poc.DataplaneStandby}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sb, "networkAttachments:") {
		t.Errorf("standby render should not contain a networkAttachments field:\n%s", sb)
	}
	// Legacy active/active bool aliases selfip-dag: the DAG NAD is injected.
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
		// USE_GATEWAY_SETTINGS picks which of BNK 2.4's two network models
		// the controller reconciles. selfip-dag drives TMM through an
		// F5SPKVlan, which is ONLY reconciled in the legacy SPK model — with
		// "true" the CR is accepted and silently never programmed, leaving
		// TMM with a bare interface 1.1. Every other mode wants the Infra +
		// GatewaySettings model (platform IPAM pool, fic-dynamic-ip).
		wantGWS string
	}{
		{"default is anycast-bgp", poc.BNK{}, BGPNADName, `value: "FALSE"`, `"true"`},
		{"explicit standby", poc.BNK{TMMDataplaneMode: poc.DataplaneStandby}, "", `value: "TRUE"`, `"true"`},
		{"selfip-dag", poc.BNK{TMMDataplaneMode: poc.DataplaneSelfIPDAG}, DAGNADName, `value: "TRUE"`, `"false"`},
		{"legacy bool == selfip-dag", poc.BNK{ActiveActive: true}, DAGNADName, `value: "TRUE"`, `"false"`},
		{"anycast-bgp", poc.BNK{TMMDataplaneMode: poc.DataplaneAnycastBGP}, BGPNADName, `value: "FALSE"`, `"true"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := RenderCNEInstance(&poc.PoC{BNK: c.bnk})
			if err != nil {
				t.Fatal(err)
			}
			if c.wantNAD == "" {
				if strings.Contains(got, "networkAttachments:") {
					t.Errorf("want no networkAttachments block:\n%s", got)
				}
			} else if !strings.Contains(got, "- "+c.wantNAD) {
				t.Errorf("want NAD %q attached:\n%s", c.wantNAD, got)
			}
			if !strings.Contains(got, c.wantMapres) {
				t.Errorf("want mapres %s:\n%s", c.wantMapres, got)
			}
			// The env block renders as: - name: "USE_GATEWAY_SETTINGS"
			//                             value: "<want>"
			i := strings.Index(got, `"USE_GATEWAY_SETTINGS"`)
			if i < 0 {
				t.Fatalf("USE_GATEWAY_SETTINGS not rendered:\n%s", got)
			}
			if tail := got[i:]; !strings.Contains(tail[:120], "value: "+c.wantGWS) {
				t.Errorf("want USE_GATEWAY_SETTINGS %s, got:\n%s", c.wantGWS, tail[:120])
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

func TestRenderCNEInstance_AIGateway(t *testing.T) {
	cases := []struct {
		name    string
		gw      poc.AIGateway
		want    []string
		notWant []string
	}{
		{
			name:    "off by default",
			want:    []string{"loggingSubsystem:\n      enabled: false"},
			notWant: []string{"aiGateway:"},
		},
		{
			name:    "console only",
			gw:      poc.AIGateway{Enabled: true},
			want:    []string{"aiGateway:\n    enabled: true\n    aiMetrics: false\n", "loggingSubsystem:\n      enabled: false"},
			notWant: []string{"consoleURL:"},
		},
		{
			// Metrics flow through the platform fluentd, so they need the
			// logging subsystem on.
			name: "metrics and console URL",
			gw:   poc.AIGateway{Enabled: true, AIMetrics: true, ConsoleURL: "http://192.168.99.10:30300"},
			want: []string{
				"aiMetrics: true",
				`consoleURL: "http://192.168.99.10:30300"`,
				"loggingSubsystem:\n      enabled: true",
			},
		},
		{
			// Settings without enabled render nothing, logging included.
			name:    "metrics without enabled",
			gw:      poc.AIGateway{AIMetrics: true},
			want:    []string{"loggingSubsystem:\n      enabled: false"},
			notWant: []string{"aiGateway:"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &poc.PoC{BNK: poc.BNK{AIGateway: c.gw}}
			got, err := RenderCNEInstance(p)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("want %q in render:\n%s", w, got)
				}
			}
			for _, w := range c.notWant {
				if strings.Contains(got, w) {
					t.Errorf("render must not contain %q:\n%s", w, got)
				}
			}
		})
	}
}
