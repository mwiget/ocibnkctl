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
