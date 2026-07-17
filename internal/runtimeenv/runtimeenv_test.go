package runtimeenv

import "testing"

func TestInContainer_EnvOverride(t *testing.T) {
	for _, tc := range []struct {
		val  string
		want bool
	}{{"1", true}, {"true", true}, {"YES", true}, {"0", false}, {"false", false}, {"no", false}} {
		t.Setenv("OCIBNKCTL_IN_CONTAINER", tc.val)
		if got := InContainer(); got != tc.want {
			t.Errorf("OCIBNKCTL_IN_CONTAINER=%q: InContainer()=%v want %v", tc.val, got, tc.want)
		}
	}
}

func TestSelfContainerID_NonEmpty(t *testing.T) {
	id, err := SelfContainerID()
	if err != nil {
		t.Fatalf("SelfContainerID: %v", err)
	}
	if id == "" {
		t.Fatal("SelfContainerID returned empty")
	}
}
