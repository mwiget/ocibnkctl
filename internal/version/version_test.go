package version

import "testing"

func TestBaselineFor(t *testing.T) {
	if got := BaselineFor("small"); got.Cores != 4 {
		t.Errorf("small profile: want 4 cores, got %d", got.Cores)
	}
	for _, p := range []string{"", "standard", "bogus"} {
		if got := BaselineFor(p); got.Cores != MinBaseline.Cores {
			t.Errorf("profile %q: want standard floor %d cores, got %d", p, MinBaseline.Cores, got.Cores)
		}
	}
}

// TestMemoryBelowFloor pins the memory half of the auto-shrink decision: a
// 16 GB Docker Desktop VM (reports ~15.6 Gi) is tight, the floor itself is
// enough, and an unknown reading never engages shrink on its own.
func TestMemoryBelowFloor(t *testing.T) {
	const gib = int64(1) << 30
	floor := int64(MinBaseline.MemoryGB) * gib
	cases := []struct {
		name string
		mem  int64
		want bool
	}{
		{"unknown", 0, false},
		{"negative", -1, false},
		{"16 GB Docker Desktop", 16745824256, true},
		{"just under", floor - 1, true},
		{"at floor", floor, false},
		{"roomy", 188 * gib, false},
	}
	for _, c := range cases {
		if got := MemoryBelowFloor(c.mem); got != c.want {
			t.Errorf("%s: MemoryBelowFloor(%d) = %v, want %v (floor=%d GiB)",
				c.name, c.mem, got, c.want, MinBaseline.MemoryGB)
		}
	}
}
