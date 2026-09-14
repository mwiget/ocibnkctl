package cluster

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The boot script is every node's entrypoint: a syntax error there is a node
// container that exits at once, so parse it with a real sh. (It is never
// executed here — it remounts / and replaces /var/run.)
func TestNodeBootScriptParses(t *testing.T) {
	if out, err := exec.Command("sh", "-n", "-c", k3sNodeBootScript()).CombinedOutput(); err != nil {
		t.Fatalf("boot script does not parse: %v\n%s", err, out)
	}
}

func TestNodeBootScriptContents(t *testing.T) {
	s := k3sNodeBootScript()
	for _, want := range []string{
		"mount --make-rshared /",
		"ln -s /run /var/run",
		"> /etc/iproute2/rt_tables",
		"> /proc/sys/kernel/core_pattern",
		"for hook in " + k3sBootHookDir + "/*.sh",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("boot script lacks %q", want)
		}
	}
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if last := lines[len(lines)-1]; last != `exec /bin/k3s "$@"` {
		t.Errorf("boot script must end by exec'ing k3s with its argv; last line is %q", last)
	}
}

// The boot script embeds these values in single quotes.
func TestNodeBootScriptQuotedValues(t *testing.T) {
	for name, v := range map[string]string{"k3sRouteTables": k3sRouteTables, "k3sCorePattern": k3sCorePattern} {
		if strings.Contains(v, "'") {
			t.Errorf("%s contains a single quote, which breaks the boot script's quoting", name)
		}
	}
}

func TestBootEntrypointArgs(t *testing.T) {
	got := bootEntrypointArgs("rancher/k3s:test", "agent", "--node-name", "n0")
	want := []string{"--entrypoint", "/bin/sh", "rancher/k3s:test", "-c", k3sNodeBootScript(),
		"ocibnk-boot", "agent", "--node-name", "n0"}
	if !slices.Equal(got, want) {
		t.Errorf("bootEntrypointArgs =\n%q\nwant\n%q", got, want)
	}
}

// The apiserver host port is chosen once at creation and must be bindable.
func TestFreeLoopbackPort(t *testing.T) {
	port, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("port %d out of range", port)
	}
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("returned port %d is not free: %v", port, err)
	}
	l.Close()
}

// The hook is written through a quoted heredoc: the body must land verbatim
// ($4, $IF, ${p##*/} unexpanded) or the replayed hook differs from the one
// cluster up ran. Run the install against a temp dir, minus the final run.
func TestEdgeHookInstallWritesScriptVerbatim(t *testing.T) {
	install := edgeHookInstallScript("172.19.0.1", 99)
	if n := strings.Count(install, "OCIBNK_HOOK"); n != 2 {
		t.Fatalf("heredoc terminator appears %d times, want 2 (it must not occur in the hook body)", n)
	}
	runHook := "sh " + edgeBootHook
	if !strings.HasSuffix(install, runHook) {
		t.Fatalf("install script must end by running the hook (%q)", runHook)
	}

	dir := t.TempDir()
	script := strings.ReplaceAll(strings.TrimSuffix(install, runHook)+"true", k3sBootHookDir, dir)
	if out, err := exec.Command("sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("install script failed: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(dir, filepath.Base(edgeBootHook)))
	if err != nil {
		t.Fatal(err)
	}
	if want := edgeUplinkScript("172.19.0.1", 99) + "\n"; string(got) != want {
		t.Errorf("installed hook differs from edgeUplinkScript:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(string(got), "ip route replace default via 172.19.0.1") {
		t.Error("hook does not restore the cluster-net default route")
	}
}
