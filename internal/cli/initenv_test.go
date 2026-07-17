package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/mwiget/ocibnkctl/internal/poc"
)

func TestApplyEnvOverrides_UnsetKeepsDefaults(t *testing.T) {
	p := poc.New("demo")
	if err := applyEnvOverrides(p, io.Discard, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Cluster.Provider != "docker" || p.Cluster.TMMNodes != 1 {
		t.Fatalf("defaults mutated: provider=%q tmm_nodes=%d", p.Cluster.Provider, p.Cluster.TMMNodes)
	}
	if p.Cluster.EdgeOctet != 0 || p.Cluster.TEEMSRelay || p.BNK.HostProfile != "" {
		t.Fatalf("unset env must not write fields: %+v", p.Cluster)
	}
}

func TestApplyEnvOverrides_AppliesEveryKnob(t *testing.T) {
	t.Setenv(envCustomer, "Acme")
	t.Setenv(envProvider, "podman")
	t.Setenv(envTMMNodes, "3")
	t.Setenv(envEdgeOctet, "42")
	t.Setenv(envHostProfile, "small")
	t.Setenv(envTEEMSRelay, "true")

	p := poc.New("demo")
	var out strings.Builder
	if err := applyEnvOverrides(p, &out, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.Metadata.Customer != "Acme" {
		t.Errorf("customer = %q", p.Metadata.Customer)
	}
	if p.Cluster.Provider != "podman" {
		t.Errorf("provider = %q", p.Cluster.Provider)
	}
	if p.Cluster.TMMNodes != 3 {
		t.Errorf("tmm_nodes = %d", p.Cluster.TMMNodes)
	}
	if p.Cluster.EdgeOctet != 42 {
		t.Errorf("edge_octet = %d", p.Cluster.EdgeOctet)
	}
	if p.BNK.HostProfile != poc.HostProfileSmall {
		t.Errorf("host_profile = %q", p.BNK.HostProfile)
	}
	if !p.Cluster.TEEMSRelay {
		t.Errorf("teems_relay = false")
	}
	// Every applied override is echoed so the runner log shows the shape.
	if !strings.Contains(out.String(), "cluster.provider=podman") {
		t.Errorf("override not echoed: %q", out.String())
	}
}

func TestApplyEnvOverrides_CustomerFlagWinsOverEnv(t *testing.T) {
	t.Setenv(envCustomer, "FromEnv")
	p := poc.New("demo")
	p.Metadata.Customer = "FromFlag"
	if err := applyEnvOverrides(p, io.Discard, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Metadata.Customer != "FromFlag" {
		t.Fatalf("explicit --customer must win, got %q", p.Metadata.Customer)
	}
}

func TestApplyEnvOverrides_HostProfileOverridesAutoDetect(t *testing.T) {
	t.Setenv(envHostProfile, "standard")
	p := poc.New("demo")
	p.BNK.HostProfile = poc.HostProfileSmall // as if auto-detect fired on a tight host
	if err := applyEnvOverrides(p, io.Discard, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.BNK.HostProfile != poc.HostProfileStandard {
		t.Fatalf("explicit env must override auto-detect, got %q", p.BNK.HostProfile)
	}
}

func TestApplyEnvOverrides_InvalidValuesAreErrors(t *testing.T) {
	cases := []struct {
		name, key, value, wantSubstr string
	}{
		{"provider", envProvider, "containerd", "must be docker or podman"},
		{"tmm_nodes non-numeric", envTMMNodes, "tow", "positive integer"},
		{"tmm_nodes zero", envTMMNodes, "0", "positive integer"},
		{"edge_octet out of range", envEdgeOctet, "300", "1-254"},
		{"edge_octet non-numeric", envEdgeOctet, "x", "1-254"},
		{"host_profile", envHostProfile, "tiny", "HOST_PROFILE"},
		{"teems_relay", envTEEMSRelay, "yesplease", "boolean"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			err := applyEnvOverrides(poc.New("demo"), io.Discard, false)
			if err == nil {
				t.Fatalf("expected error for %s=%q", tc.key, tc.value)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("error %q missing %q", err, tc.wantSubstr)
			}
		})
	}
}

func TestApplyEnvOverrides_EmptyValueIsTreatedAsUnset(t *testing.T) {
	// A runner that templates an unset input emits KEY="" — that must mean
	// "use the default", not "empty provider" (which would fail validate).
	t.Setenv(envProvider, "   ")
	p := poc.New("demo")
	if err := applyEnvOverrides(p, io.Discard, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Cluster.Provider != "docker" {
		t.Fatalf("blank env must keep default, got %q", p.Cluster.Provider)
	}
}

func TestApplyEnvOverrides_ResultPassesValidate(t *testing.T) {
	t.Setenv(envProvider, "podman")
	t.Setenv(envTMMNodes, "2")
	t.Setenv(envEdgeOctet, "77")
	t.Setenv(envHostProfile, "small")

	p := poc.New("demo")
	if err := applyEnvOverrides(p, io.Discard, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The env-seeded PoC must satisfy the same schema validation an
	// operator-authored poc.yaml does.
	if r := p.Validate(); !r.Valid() {
		t.Fatalf("env-seeded poc.yaml fails validate: %v", r.Errors)
	}
}
