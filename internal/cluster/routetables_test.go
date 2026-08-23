package cluster

import (
	"strings"
	"testing"
)

// rt_tables is whitespace-significant: iproute2 parses "<id><space/tab><name>".
// The obvious way to write this constant — a backquoted raw literal — silently
// emits a literal backslash-t, which compiles fine and produces a broken file
// on the node. Assert real tabs.
func TestRouteTablesUsesRealTabs(t *testing.T) {
	if strings.Contains(k3sRouteTables, `\t`) {
		t.Fatal("k3sRouteTables contains a literal backslash-t; use an interpreted string, not a raw literal")
	}

	want := map[string]string{
		"255": "local",
		"254": "main",
		"253": "default",
		"0":   "unspec",
	}
	got := map[string]string{}
	for _, line := range strings.Split(k3sRouteTables, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, name, ok := strings.Cut(line, "\t")
		if !ok {
			t.Errorf("entry %q is not tab-separated", line)
			continue
		}
		got[id] = name
	}

	for id, name := range want {
		if got[id] != name {
			t.Errorf("table %s = %q, want %q", id, got[id], name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("parsed %d entries, want %d: %v", len(got), len(want), got)
	}
}

// The content is streamed to the node over stdin, which `docker exec` only
// attaches when given -i. Without it cat reads EOF and writes a zero-byte
// file — which still passes the hostPath File type check, so the DaemonSet
// comes up and nothing looks wrong. v2.3.1-13 shipped exactly that.
func TestRouteTablesExecArgsAttachStdin(t *testing.T) {
	args := routeTablesExecArgs("k3s-poc-agent-0")

	if args[0] != "exec" {
		t.Fatalf("args[0] = %q, want exec", args[0])
	}
	idxI, idxContainer := -1, -1
	for i, a := range args {
		switch a {
		case "-i":
			idxI = i
		case "k3s-poc-agent-0":
			idxContainer = i
		}
	}
	if idxI == -1 {
		t.Fatal("missing -i: stdin will not attach and the file will be empty")
	}
	if idxContainer == -1 {
		t.Fatal("container name not in argv")
	}
	if idxI > idxContainer {
		t.Errorf("-i at %d must precede the container at %d", idxI, idxContainer)
	}
}
