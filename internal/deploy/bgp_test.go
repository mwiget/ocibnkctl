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

// The neighbor-restore script runs through the same `imish -f` path as the
// redistribute one, so it inherits the same two traps: a leading `configure
// terminal` is rejected, and `no bgp default ipv4-unicast` comes back as
// `% Invalid input detected` from a -f script (observed on OcNOS XP-6.6.0
// while restoring a TMM after a GlobalRoutingConfig delete).
func TestOcNOSAnycastNeighborScript(t *testing.T) {
	const peer = "192.168.99.41"
	got := OcNOSAnycastNeighborScript(peer)

	if strings.Contains(got, "configure terminal") {
		t.Error("script opens with `configure terminal`: imish -f is already in " +
			"config mode and rejects it with `% Invalid input detected`")
	}
	if strings.Contains(got, "no bgp default ipv4-unicast") {
		t.Error("script contains `no bgp default ipv4-unicast`: OcNOS XP-6.6.0 " +
			"rejects it from an imish -f script")
	}
	for _, want := range []string{
		fmt.Sprintf("router bgp %d", BGPTMMAS),
		fmt.Sprintf("neighbor %s remote-as %d", peer, BGPPeerAS),
		fmt.Sprintf("neighbor %s update-source net1", peer),
		fmt.Sprintf("neighbor %s activate", peer),
		"address-family ipv4 unicast",
		"redistribute kernel route-map RMALL",
		"redistribute connected route-map RMALL",
		"imish -f /tmp/ocibnkctl-neighbor.cfg",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("script missing %q", want)
		}
	}
	// One exit leaves address-family, one leaves router-bgp — same shape as
	// OcNOSRedistributeScript.
	if n := strings.Count(got, `\nexit`); n != 2 {
		t.Errorf("script has %d exits, want 2 (address-family, router-bgp)", n)
	}
}
