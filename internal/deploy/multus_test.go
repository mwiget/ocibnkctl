package deploy

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestMultusShimInstallPatch(t *testing.T) {
	var ops []struct {
		Op    string   `json:"op"`
		Path  string   `json:"path"`
		Value []string `json:"value"`
	}
	if err := json.Unmarshal([]byte(multusShimInstallPatch), &ops); err != nil {
		t.Fatalf("patch is not valid JSON: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("patch has %d ops, want 1", len(ops))
	}
	op := ops[0]
	if op.Op != "replace" || op.Path != "/spec/template/spec/initContainers/0/command" {
		t.Errorf("op = %s %s, want replace of the init container command", op.Op, op.Path)
	}
	if len(op.Value) != 3 || op.Value[0] != "/usr/bin/sh" || op.Value[1] != "-c" {
		t.Fatalf("command = %q, want [/usr/bin/sh -c <script>]", op.Value)
	}
	script := op.Value[2]
	// A plain cp onto the target fails with "Text file busy" while the old
	// shim runs; the install must go through a rename.
	if !strings.Contains(script, "multus-shim.tmp && mv -f") {
		t.Errorf("install script %q does not copy to a temp file and rename", script)
	}
	if out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
		t.Errorf("install script does not parse: %v\n%s", err, out)
	}
}
