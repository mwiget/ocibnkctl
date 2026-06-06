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
		block := got[idx:]
		if !strings.Contains(block[:strings.Index(block, "\n")+40], c.want) {
			t.Errorf("profile %q: want metricSubsystem %q\n--- got ---\n%s", c.profile, c.want, block[:80])
		}
	}
}
