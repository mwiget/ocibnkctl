package cluster

import "testing"

func TestInContainer_EnvOverride(t *testing.T) {
	for _, tc := range []struct {
		val  string
		want bool
	}{
		{"1", true}, {"true", true}, {"YES", true},
		{"0", false}, {"false", false}, {"no", false},
	} {
		t.Setenv("OCIBNKCTL_IN_CONTAINER", tc.val)
		if got := InContainer(); got != tc.want {
			t.Errorf("OCIBNKCTL_IN_CONTAINER=%q: InContainer()=%v want %v", tc.val, got, tc.want)
		}
	}
}

func TestInContainer_OverrideBeatsFilesystem(t *testing.T) {
	// Explicit override wins even if /.dockerenv or cgroups would say otherwise —
	// a runner that needs to force host semantics can, and vice versa.
	t.Setenv("OCIBNKCTL_IN_CONTAINER", "false")
	if InContainer() {
		t.Fatal("explicit false override must return false regardless of environment")
	}
}

func TestSelfContainerID_NonEmpty(t *testing.T) {
	// os.Hostname() is always set in the test environment; the helper must
	// return it non-empty so a real in-container run has a reference to connect.
	id, err := selfContainerID()
	if err != nil {
		t.Fatalf("selfContainerID: %v", err)
	}
	if id == "" {
		t.Fatal("selfContainerID returned empty")
	}
}
