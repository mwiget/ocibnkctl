package poc

import (
	"strings"
	"testing"
	"time"

	"github.com/mwiget/ocibnkctl/internal/version"
)

// goodPoC returns a PoC that passes Validate clean. Tests then mutate
// one thing at a time to assert each rule fires individually. Same
// pattern as dpubnkctl/internal/poc/validate_test.go.
func goodPoC(t *testing.T) *PoC {
	t.Helper()
	p := New("smoke")
	// New() leaves Created at "now" — pin it for stable test output.
	p.Metadata.Created = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if r := p.Validate(); !r.Valid() {
		t.Fatalf("baseline goodPoC should be valid, got errors: %v", r.Errors)
	}
	return p
}

func TestValidateGood(t *testing.T) {
	p := goodPoC(t)
	r := p.Validate()
	if !r.Valid() {
		t.Fatalf("good PoC failed: %v", r.Errors)
	}
	// The default data-plane mode is now anycast-bgp (the wholeCluster
	// scale-out shape), which always carries the single-host ECMP caveat as
	// an informational warning. Assert that is the ONLY warning a clean PoC
	// produces — no other rule fired.
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "anycast-bgp") {
		t.Fatalf("good PoC warnings = %v, want exactly the anycast-bgp caveat", r.Warnings)
	}
}

func TestValidateRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*PoC)
		want   string // substring of one expected error line
	}{
		{"missing apiVersion",
			func(p *PoC) { p.APIVersion = "" },
			"apiVersion"},
		{"wrong apiVersion",
			func(p *PoC) { p.APIVersion = "dpubnkctl.f5.com/v1alpha1" },
			"apiVersion"},
		{"wrong kind",
			func(p *PoC) { p.Kind = "NotPoC" },
			"kind"},
		{"missing metadata.name",
			func(p *PoC) { p.Metadata.Name = "" },
			"metadata.name: required"},
		{"bad metadata.name with caps",
			func(p *PoC) { p.Metadata.Name = "ABC" },
			"must match"},
		{"bad metadata.name starts hyphen",
			func(p *PoC) { p.Metadata.Name = "-foo" },
			"must match"},
		{"missing bnk_version",
			func(p *PoC) { p.Metadata.BNKVersion = "" },
			"metadata.bnk_version: required"},
		{"missing cluster.name",
			func(p *PoC) { p.Cluster.Name = "" },
			"cluster.name: required"},
		{"missing cluster.provider",
			func(p *PoC) { p.Cluster.Provider = "" },
			"cluster.provider: required"},
		{"bad cluster.provider",
			func(p *PoC) { p.Cluster.Provider = "containerd" },
			"must be docker or podman"},
		{"missing versions.k8s",
			func(p *PoC) { p.Versions.K8s = "" },
			"versions.k8s"},
		{"missing node_image",
			func(p *PoC) { p.Versions.NodeImage = "" },
			"node_image"},
		{"missing cne_manifest",
			func(p *PoC) { p.Versions.CNEManifest = "" },
			"cne_manifest"},
		// Networks is now optional — cluster up doesn't create the
		// bnk-internal / bnk-external bridges anymore. We still
		// validate shape when fields ARE populated (CIDR parse +
		// duplicate-name check), so cover those cases.
		{"bad networks.internal.subnet",
			func(p *PoC) {
				p.Networks.Internal.Name = "x"
				p.Networks.Internal.Subnet = "not-a-cidr"
			},
			"networks.internal.subnet"},
		{"duplicate network names",
			func(p *PoC) {
				p.Networks.Internal.Name = "dup"
				p.Networks.External.Name = "dup"
			},
			"must differ"},
		{"missing bnk.far_key_ref",
			func(p *PoC) { p.BNK.FARKeyRef = "" },
			"bnk.far_key_ref"},
		{"missing bnk.jwt_ref",
			func(p *PoC) { p.BNK.JWTRef = "" },
			"bnk.jwt_ref"},
		{"bnk_forge bad url",
			func(p *PoC) {
				p.BNKForge.Enabled = true
				p.BNKForge.URL = "ftp://no"
			},
			"bnk_forge.url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := goodPoC(t)
			tc.mutate(p)
			r := p.Validate()
			if r.Valid() {
				t.Fatalf("expected error containing %q, got clean", tc.want)
			}
			joined := strings.Join(r.Errors, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("error %q not found in:\n%s", tc.want, joined)
			}
		})
	}
}

func TestValidateDemoModeOffEmitsWarning(t *testing.T) {
	p := goodPoC(t)
	p.BNK.DemoMode = false
	r := p.Validate()
	if !r.Valid() {
		t.Fatalf("demo_mode=false should be valid (just a warning), got errors: %v", r.Errors)
	}
	joined := strings.Join(r.Warnings, "\n")
	if !strings.Contains(joined, "demo_mode") {
		t.Fatalf("expected demo_mode warning, got: %v", r.Warnings)
	}
}

func TestNewPopulatesBinaryPins(t *testing.T) {
	p := New("demo")
	if p.Metadata.Name != "demo" {
		t.Errorf("name: got %q want demo", p.Metadata.Name)
	}
	if p.Metadata.BNKVersion != version.BNKVersion {
		t.Errorf("bnk_version: got %q want %q", p.Metadata.BNKVersion, version.BNKVersion)
	}
	if p.Versions.NodeImage != version.K3sNodeImage {
		t.Errorf("node_image: got %q want %q", p.Versions.NodeImage, version.K3sNodeImage)
	}
	if p.Versions.CNEManifest != version.CNEManifestVersion {
		t.Errorf("cne_manifest: got %q want %q", p.Versions.CNEManifest, version.CNEManifestVersion)
	}
	if !p.BNK.DemoMode {
		t.Errorf("demo_mode: want true (k3s demo shape requires demo TMM)")
	}
	if p.Cluster.Provider != "docker" {
		t.Errorf("cluster.provider: got %q want docker", p.Cluster.Provider)
	}
}

func TestTMMLabelDefault(t *testing.T) {
	var b BNK
	k, v := b.TMMLabel()
	if k != "app" || v != "f5-tmm" {
		t.Errorf("default TMM label: got %s=%s want app=f5-tmm", k, v)
	}
}

func TestTMMLabelOverride(t *testing.T) {
	b := BNK{TMMNodeLabelKey: "role", TMMNodeLabelValue: "edge"}
	k, v := b.TMMLabel()
	if k != "role" || v != "edge" {
		t.Errorf("override TMM label: got %s=%s want role=edge", k, v)
	}
}

// TestDataplaneMode pins the legacy-bool → enum folding and the three
// helpers. The explicit field always wins; the bool aliases to selfip-dag.
func TestDataplaneMode(t *testing.T) {
	cases := []struct {
		name       string
		active     bool
		mode       string
		want       string
		wantSelfIP bool
		wantBGP    bool
		wantAA     bool
	}{
		{"default is anycast-bgp", false, "", DataplaneAnycastBGP, false, true, true},
		{"explicit standby", false, DataplaneStandby, DataplaneStandby, false, false, false},
		{"legacy bool aliases selfip-dag", true, "", DataplaneSelfIPDAG, true, false, true},
		{"explicit selfip-dag", false, DataplaneSelfIPDAG, DataplaneSelfIPDAG, true, false, true},
		{"explicit anycast-bgp", false, DataplaneAnycastBGP, DataplaneAnycastBGP, false, true, true},
		{"field wins over absent bool", false, DataplaneAnycastBGP, DataplaneAnycastBGP, false, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := BNK{ActiveActive: c.active, TMMDataplaneMode: c.mode}
			if got := b.DataplaneMode(); got != c.want {
				t.Errorf("DataplaneMode() = %q, want %q", got, c.want)
			}
			if got := b.IsSelfIPDAG(); got != c.wantSelfIP {
				t.Errorf("IsSelfIPDAG() = %v, want %v", got, c.wantSelfIP)
			}
			if got := b.IsAnycastBGP(); got != c.wantBGP {
				t.Errorf("IsAnycastBGP() = %v, want %v", got, c.wantBGP)
			}
			if got := b.IsAllActive(); got != c.wantAA {
				t.Errorf("IsAllActive() = %v, want %v", got, c.wantAA)
			}
		})
	}
}

// TestValidateDataplaneMode covers the enum validation + the legacy-bool
// conflict rule.
func TestValidateDataplaneMode(t *testing.T) {
	t.Run("bad mode value", func(t *testing.T) {
		p := goodPoC(t)
		p.BNK.TMMDataplaneMode = "anycast"
		r := p.Validate()
		if r.Valid() {
			t.Fatal("expected error for bad mode value, got clean")
		}
		if !strings.Contains(strings.Join(r.Errors, "\n"), "tmm_dataplane_mode") {
			t.Fatalf("expected tmm_dataplane_mode error, got: %v", r.Errors)
		}
	})
	t.Run("legacy bool + conflicting mode errors", func(t *testing.T) {
		p := goodPoC(t)
		p.BNK.ActiveActive = true
		p.BNK.TMMDataplaneMode = DataplaneAnycastBGP
		r := p.Validate()
		if r.Valid() {
			t.Fatal("expected conflict error, got clean")
		}
		if !strings.Contains(strings.Join(r.Errors, "\n"), "conflicts") {
			t.Fatalf("expected conflict error, got: %v", r.Errors)
		}
	})
	t.Run("legacy bool + matching selfip-dag is fine", func(t *testing.T) {
		p := goodPoC(t)
		p.BNK.ActiveActive = true
		p.BNK.TMMDataplaneMode = DataplaneSelfIPDAG
		r := p.Validate()
		if !r.Valid() {
			t.Fatalf("bool + matching mode should be valid, got: %v", r.Errors)
		}
	})
	t.Run("anycast-bgp valid with single-host caveat warning", func(t *testing.T) {
		p := goodPoC(t)
		p.BNK.TMMDataplaneMode = DataplaneAnycastBGP
		r := p.Validate()
		if !r.Valid() {
			t.Fatalf("anycast-bgp should be valid, got: %v", r.Errors)
		}
		if !strings.Contains(strings.Join(r.Warnings, "\n"), "anycast-bgp") {
			t.Fatalf("expected single-host caveat warning, got: %v", r.Warnings)
		}
	})
	t.Run("legacy bool alone warns deprecation", func(t *testing.T) {
		p := goodPoC(t)
		p.BNK.ActiveActive = true
		r := p.Validate()
		if !r.Valid() {
			t.Fatalf("legacy bool alone should be valid, got: %v", r.Errors)
		}
		if !strings.Contains(strings.Join(r.Warnings, "\n"), "deprecated") {
			t.Fatalf("expected deprecation warning, got: %v", r.Warnings)
		}
	})
}
