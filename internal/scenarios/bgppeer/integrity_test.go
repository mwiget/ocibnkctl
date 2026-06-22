package bgppeer

import "testing"

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
