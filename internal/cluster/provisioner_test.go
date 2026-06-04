package cluster

import (
	"strings"
	"testing"
)

func TestNewProvisionerK3s(t *testing.T) {
	p, err := NewProvisioner(BackendK3s, RuntimeDocker, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Backend() != BackendK3s || p.Tool() != "docker" {
		t.Fatalf("got backend %q tool %q", p.Backend(), p.Tool())
	}
	if got := p.WorkerNodeName("demo"); got != "k3s-demo-agent-0" {
		t.Errorf("k3s worker node = %q, want k3s-demo-agent-0", got)
	}
	if got := p.NodeContainerLabel("demo"); got != "ocibnk.cluster=demo" {
		t.Errorf("k3s node label = %q", got)
	}
	if p.ConfigArtifact() != "k3s.yaml" {
		t.Errorf("k3s config artifact = %q", p.ConfigArtifact())
	}
	if p.DefaultNodeImage() == "" {
		t.Error("k3s default node image must be set")
	}
}

func TestK3sToolFollowsRuntime(t *testing.T) {
	p, err := NewProvisioner(BackendK3s, RuntimePodman, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Tool() != "podman" {
		t.Errorf("k3s tool with podman runtime = %q, want podman", p.Tool())
	}
}

func TestNewProvisionerUnknown(t *testing.T) {
	if _, err := NewProvisioner(Backend("nope"), RuntimeDocker, nil); err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestRenderConfigK3s(t *testing.T) {
	p, err := NewProvisioner(BackendK3s, RuntimeDocker, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := p.RenderConfig("demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: demo", "flannel-backend=none", "servers: 1", "agents: 1", "k3s-demo-server-0", "k3s-demo-agent-0"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("k3s config missing %q:\n%s", want, cfg)
		}
	}
}
