package deploy

import (
	"fmt"
	"strings"
	"testing"
)

// `imish -f` starts already in config mode, so a leading `configure terminal`
// is rejected with `% Invalid input detected` — once per exec, per TMM, on
// both the deploy path (TriggerOcNOSRedistribute, during `deploy cne`) and the
// scenario path (scenarios.EnsureEdge...). Every following line applies
// anyway, so BGP comes up, nothing fails, and the only symptom is a wall of
// error banners nobody reads. It shipped that way in three separate copies of
// this string; this pins the one they now share.
func TestOcNOSRedistributeScript(t *testing.T) {
	got := OcNOSRedistributeScript()

	if strings.Contains(got, "configure terminal") {
		t.Error("script opens with `configure terminal`: imish -f is already in " +
			"config mode and rejects it with `% Invalid input detected`")
	}
	if want := fmt.Sprintf("router bgp %d", BGPTMMAS); !strings.Contains(got, want) {
		t.Errorf("script does not enter %q — redistribute would apply to the wrong AS", want)
	}
	for _, want := range []string{
		"address-family ipv4 unicast",
		"redistribute kernel route-map RMALL",
		"redistribute connected route-map RMALL",
		"imish -f /tmp/ocibnkctl-redist.cfg",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("script missing %q", want)
		}
	}
	// One exit leaves address-family, one leaves router-bgp. A third was only
	// ever needed to unwind the `configure terminal` that no longer happens.
	if n := strings.Count(got, `\nexit`); n != 2 {
		t.Errorf("script has %d exits, want 2 (address-family, router-bgp)", n)
	}
}
