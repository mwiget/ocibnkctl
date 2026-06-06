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
