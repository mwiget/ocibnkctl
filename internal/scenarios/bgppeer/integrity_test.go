package bgppeer

import (
	"strings"
	"testing"
)

// Sample external-FRR `show ip bgp summary`: two TMM neighbors on the bnk-edge
// prefix, one Established (Up/Down timer) and one Active (still coming up).
const frrSummary = `IPv4 Unicast Summary (VRF default):
BGP router identifier 192.168.99.41, local AS number 65001 vrf-id 0

Neighbor        V         AS   MsgRcvd   MsgSent   TblVer  InQ OutQ  Up/Down State/PfxRcd
*192.168.99.160 4      65000        12        14        0    0    0 00:02:13            3
*192.168.99.161 4      65000         0         0        0    0    0    never        Active

Total number of neighbors 2`

func TestCountEstablished(t *testing.T) {
	if got := countEstablished(frrSummary, "192.168.99."); got != 1 {
		t.Errorf("countEstablished = %d, want 1 (one Established, one Active)", got)
	}
	if got := countEstablished("garbage", "192.168.99."); got != 0 {
		t.Errorf("countEstablished(garbage) = %d, want 0", got)
	}
}

// `imish -f` starts already in config mode, so a leading `configure terminal`
// is rejected with `% Invalid input detected` — once per exec, 3 retries x N
// TMMs. The rest of the script applies anyway, so BGP still comes up and the
// scenario still passes; the only symptom is a wall of error banners in the
// output. That is exactly how it shipped, so pin it.
func TestRedistShCmdHasNoConfigureTerminal(t *testing.T) {
	if strings.Contains(redistShCmd, "configure terminal") {
		t.Error("redistShCmd opens with `configure terminal`: imish -f is already " +
			"in config mode and will reject it with `% Invalid input detected`")
	}
	if !strings.Contains(redistShCmd, "router bgp 65000") {
		t.Fatal("redistShCmd no longer enters router-bgp config context")
	}
	for _, want := range []string{"redistribute kernel route-map RMALL", "redistribute connected route-map RMALL"} {
		if !strings.Contains(redistShCmd, want) {
			t.Errorf("redistShCmd missing %q", want)
		}
	}
	// One exit leaves address-family, one leaves router-bgp. A third was only
	// needed to unwind the `configure terminal` that no longer happens.
	if got := strings.Count(redistShCmd, `\nexit`); got != 2 {
		t.Errorf("redistShCmd has %d exits, want 2 (address-family, router-bgp)", got)
	}
}
