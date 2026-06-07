package cli

import (
	"testing"

	"github.com/mwiget/ocibnkctl/internal/version"
)

// TestResolveProfile pins the doctor auto-detect: "auto" maps to the small
// floor below the standard core floor and to standard at or above it, while
// an explicit profile is passed through untouched.
func TestResolveProfile(t *testing.T) {
	floor := version.MinBaseline.Cores
	cases := []struct {
		profile string
		cores   int
		want    string
	}{
		{"auto", 4, "small"},        // Raspberry-Pi shape
		{"auto", floor - 1, "small"}, // just under
		{"auto", floor, "standard"},  // exactly the floor
		{"auto", 64, "standard"},     // roomy
		{"small", 64, "small"},       // explicit wins over core count
		{"standard", 4, "standard"},  // explicit wins over core count
	}
	for _, c := range cases {
		if got := resolveProfile(c.profile, c.cores); got != c.want {
			t.Errorf("resolveProfile(%q, %d) = %q, want %q (floor=%d)",
				c.profile, c.cores, got, c.want, floor)
		}
	}
}
