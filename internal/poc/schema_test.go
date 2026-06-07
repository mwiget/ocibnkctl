package poc

import "testing"

// TestResolveHostProfile pins the auto-select rule: an explicit profile is
// always honored, while an unset profile resolves to small below stdFloor
// cores (autoSmall=true) and standard at or above it.
func TestResolveHostProfile(t *testing.T) {
	const floor = 10
	cases := []struct {
		name     string
		profile  string
		cores    int
		want     string
		wantAuto bool
	}{
		{"unset tight host", "", 4, HostProfileSmall, true},
		{"unset just under", "", floor - 1, HostProfileSmall, true},
		{"unset at floor", "", floor, HostProfileStandard, false},
		{"unset roomy", "", 64, HostProfileStandard, false},
		{"explicit small honored", HostProfileSmall, 64, HostProfileSmall, false},
		{"explicit standard honored on tight host", HostProfileStandard, 4, HostProfileStandard, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := BNK{HostProfile: c.profile}
			got, auto := b.ResolveHostProfile(c.cores, floor)
			if got != c.want || auto != c.wantAuto {
				t.Errorf("ResolveHostProfile(%q, cores=%d, floor=%d) = (%q, %v), want (%q, %v)",
					c.profile, c.cores, floor, got, auto, c.want, c.wantAuto)
			}
		})
	}
}
