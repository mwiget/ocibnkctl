package cluster

import (
	"strings"
	"testing"
)

func TestRenderRegistriesYAML(t *testing.T) {
	got := RenderRegistriesYAML("host.docker.internal", 5000, "", "")
	for _, want := range []string{
		`"docker.io":`,
		`- "http://host.docker.internal:5000"`,
		`- "https://registry-1.docker.io"`, // dockerhub fallback uses the v2 host
		`"ghcr.io":`,
		`- "http://host.docker.internal:5001"`,
		`"quay.io":`,
		`- "http://host.docker.internal:5002"`,
		`"repo.f5.com":`,
		`- "http://host.docker.internal:5003"`,
		`- "https://repo.f5.com"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("registries.yaml missing %q\n---\n%s", want, got)
		}
	}
	// No creds passed → no configs block.
	if strings.Contains(got, "configs:") {
		t.Errorf("unexpected configs block when no F5 creds given:\n%s", got)
	}
}

func TestRenderRegistriesYAML_CustomHostPort(t *testing.T) {
	got := RenderRegistriesYAML("172.18.0.1", 6000, "", "")
	if !strings.Contains(got, `- "http://172.18.0.1:6003"`) {
		t.Errorf("custom host/port-base not honored:\n%s", got)
	}
}

func TestRenderRegistriesYAML_F5Configs(t *testing.T) {
	got := RenderRegistriesYAML("host.docker.internal", 5000, "_json_key_base64", "SECRET")
	for _, want := range []string{
		"configs:",
		`"host.docker.internal:5003":`, // keyed by the F5 cache's mirror host:port
		`username: "_json_key_base64"`,
		`password: "SECRET"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("registries.yaml missing %q\n---\n%s", want, got)
		}
	}
}

func TestMirrorArgs(t *testing.T) {
	k := &K3s{}
	if args := k.mirrorArgs(); args != nil {
		t.Errorf("disabled cache should yield no args, got %v", args)
	}
	k.RegistriesYAMLPath = "/abs/artifacts/registries.yaml"
	args := strings.Join(k.mirrorArgs(), " ")
	if !strings.Contains(args, "--add-host host.docker.internal:host-gateway") {
		t.Errorf("missing host-gateway alias: %q", args)
	}
	if !strings.Contains(args, "/abs/artifacts/registries.yaml:/etc/rancher/k3s/registries.yaml:ro") {
		t.Errorf("missing registries.yaml bind-mount: %q", args)
	}
}
