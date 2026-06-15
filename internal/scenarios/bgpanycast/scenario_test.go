package bgpanycast

import "testing"

// Sample ZeBOS `show ip bgp summary` output (from a live anycast-bgp TMM).
const zebosSummary = `BGP router identifier 192.168.12.105, local AS number 65000
BGP table version is 2

Neighbor                 V   AS   MsgRcv    MsgSen TblVer   InQ   OutQ    Up/Down   State/PfxRcd
192.168.99.2             4 65001    3          2       1      0      0  00:00:15               0

Total number of neighbors 1

Total number of Established sessions 1`

const zebosIdle = `BGP router identifier 192.168.94.144, local AS number 65000

Total number of Established sessions 0`

func TestParseZebosRouterID(t *testing.T) {
	if got := parseZebosRouterID(zebosSummary); got != "192.168.12.105" {
		t.Errorf("router-id = %q, want 192.168.12.105", got)
	}
	if got := parseZebosRouterID("garbage"); got != "" {
		t.Errorf("router-id of garbage = %q, want empty", got)
	}
}

func TestZebosEstablished(t *testing.T) {
	if !zebosEstablished(zebosSummary) {
		t.Error("expected Established for a summary with 1 session")
	}
	if zebosEstablished(zebosIdle) {
		t.Error("expected NOT Established for 0 sessions")
	}
}

func TestReadyAll(t *testing.T) {
	cases := map[string]bool{
		"2/2": true, "1/1": true, "1/2": false, "0/0": false, "0/2": false, "": false, "x": false,
	}
	for in, want := range cases {
		if got := readyAll(in); got != want {
			t.Errorf("readyAll(%q) = %v, want %v", in, got, want)
		}
	}
}

// FRR `show bgp summary` with one dynamic neighbor (the "*" prefix).
const frrSummary = `IPv4 Unicast Summary (VRF default):
BGP router identifier 192.168.99.2, local AS number 65001 vrf-id 0

Neighbor        V         AS   MsgRcvd   MsgSent   TblVer  InQ OutQ  Up/Down State/PfxRcd   PfxSnt Desc
*192.168.99.21  4      65000         2         3        0    0    0 00:00:21            0        0 N/A

Total number of neighbors 1
* - dynamic neighbor
1 dynamic neighbor(s), limit 100`

func TestCountFRRNeighbors(t *testing.T) {
	if got := countFRRNeighbors(frrSummary); got != 1 {
		t.Errorf("countFRRNeighbors = %d, want 1", got)
	}
}

func TestCountDynamicNeighbors(t *testing.T) {
	if got := countDynamicNeighbors(frrSummary); got != 1 {
		t.Errorf("countDynamicNeighbors = %d, want 1", got)
	}
	if got := countDynamicNeighbors("no such phrase"); got != 0 {
		t.Errorf("countDynamicNeighbors(none) = %d, want 0", got)
	}
}

func TestSplitNonEmpty(t *testing.T) {
	got := splitNonEmpty("a\n\n  b \nc\n")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("splitNonEmpty = %v, want [a b c]", got)
	}
}
