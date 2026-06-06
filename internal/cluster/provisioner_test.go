package cluster

import (
	"os"
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

func TestNonLoopbackNameservers(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := dir + "/" + name
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Loopback stub (systemd-resolved) → no usable upstreams.
	stub := write("stub", "nameserver 127.0.0.53\noptions edns0\n")
	if got := nonLoopbackNameservers(stub); got != nil {
		t.Errorf("loopback stub: got %v, want nil", got)
	}

	// Real upstreams, in file order, loopback filtered out.
	real := write("real", "nameserver 127.0.0.1\nnameserver 213.144.129.20\nnameserver 77.109.128.2\n")
	got := nonLoopbackNameservers(real)
	if strings.Join(got, ",") != "213.144.129.20,77.109.128.2" {
		t.Errorf("real upstreams: got %v", got)
	}

	// Missing file → nil, no error.
	if got := nonLoopbackNameservers(dir + "/does-not-exist"); got != nil {
		t.Errorf("missing file: got %v, want nil", got)
	}
}
