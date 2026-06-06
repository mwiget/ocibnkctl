package deploy

import (
	"strings"
	"testing"

	"github.com/mwiget/ocibnkctl/internal/poc"
)

func TestRenderCNEInstance_MetricSubsystemByProfile(t *testing.T) {
	cases := []struct {
		profile string
		want    string
	}{
		{"", "enabled: true"},                      // default = standard
		{poc.HostProfileStandard, "enabled: true"}, // explicit standard
		{poc.HostProfileSmall, "enabled: false"},   // small host sheds the observer sidecar
	}
	for _, c := range cases {
		p := &poc.PoC{BNK: poc.BNK{HostProfile: c.profile}}
		got, err := RenderCNEInstance(p)
		if err != nil {
			t.Fatalf("profile %q: %v", c.profile, err)
		}
		// The metricSubsystem block is the only telemetry toggle that flips;
		// assert the rendered enabled line under it.
		idx := strings.Index(got, "metricSubsystem:")
		if idx < 0 {
			t.Fatalf("profile %q: no metricSubsystem block:\n%s", c.profile, got)
		}
		// Look only at a short window right after "metricSubsystem:" — its
		// `enabled:` line sits there (the only other `enabled:` is under
		// loggingSubsystem, which is above this index). Cap the window so a
		// short render can never slice out of range.
		block := got[idx:]
		end := 80
		if end > len(block) {
			end = len(block)
		}
		window := block[:end]
		if !strings.Contains(window, c.want) {
			t.Errorf("profile %q: want metricSubsystem %q\n--- got ---\n%s", c.profile, c.want, window)
		}
	}
}
