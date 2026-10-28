package deploy

import (
	"strconv"
	"strings"
)

// BGP-anycast deploy-path plumbing (tmm_dataplane_mode: anycast-bgp).
//
// The persistent deploy path builds on the same L2 shape the
// bgp-peer-frr scenario proved out: a bridge-CNI Multus NAD on net1,
// mapres FALSE so net1 keeps its kernel IP, and a ZeBOS BGP session per
// TMM pod. Where the scenario applies all of this as a runtime patch on
// top of a standby deploy, here it's baked into the deploy pipeline so
// the cluster comes up in anycast-bgp mode.
//
// FRR peer placement: bridge CNI is per-node, so a single upstream peer
// can only share L2 with TMM pods on its own node. The deploy path runs
// FRR as a DaemonSet on the app=f5-tmm nodes — one peer co-located with
// each TMM pod — pinned to a STATIC net1 IP (BGPPeerIP) so the
// cluster-wide ZeBOS ConfigMap has a single deterministic neighbor for
// every TMM pod. On a single host the per-node bridges are isolated, so
// each FRR sees only its node-local TMM (one next-hop), not all N — the
// honest limitation documented in README "Network topology".
const (
	// BGPPeerIP is the fixed net1 address every FRR peer takes (static
	// IPAM), so the cluster-wide ZeBOS template can name one neighbor for
	// all TMM pods. Below the bnk-bgp host-local range (.20+) so it never
	// collides with a TMM pod's assigned address.
	BGPPeerIP = "192.168.99.2"
	// BGPFRRNADName is the NAD FRR attaches (static IPAM on the same
	// br-bnk-bgp bridge as TMM's bnk-bgp NAD). A separate NAD keeps FRR's
	// fixed .2 out of TMM's host-local pool.
	BGPFRRNADName = "bnk-bgp-frr"
	// FRRImage is the FRRouting container the BGP peer runs (matches the
	// bgp-peer-frr scenario's pin).
	FRRImage = "quay.io/frrouting/frr:9.1.0"
)

// TriggerOcNOSRedistribute re-issues the redistribute statements on every
// Running TMM pod's OcNOS daemon. OcNOS XP-6.6.0 only injects redistributed
// routes into BGP when the statement is (re-)issued at runtime, so after the
// routing ConfigMap is applied + TMM rolled, this nudges every pod to actually
// advertise its connected net1 subnet + the Gateway VIP /32s to the external
// FRR. Best-effort per pod (returns the last error).
//
// The commands go through `imish -f <script>`, NOT repeated `imish -e` flags:
// each `-e` runs as an independent command in exec mode, so it never enters
// router-bgp config context and the redistribute is a no-op (imish prints
// "% Error in processing command"). A -f script keeps config-mode context
// across lines, so the statement is actually re-issued and OcNOS rescans the
// kernel RIB. Terminated with `exit`s (OcNOS rejects `end` in this path).
// OcNOSRedistributeScript is the `imish -f` script that re-issues the BGP
// redistribute statements on one TMM. Exported and shared so the deploy path
// and the scenario helpers cannot drift apart — three near-copies of this
// string is how the `configure terminal` defect below survived in two of them
// after being fixed in the third.
//
// It must NOT open with `configure terminal`: `imish -f` already starts in
// config mode, so that line comes back as `% Invalid input detected` — once
// per exec, per TMM. Every following line applies regardless, so BGP still
// comes up and nothing fails; the only symptom is a wall of error banners in
// the deploy and scenario output. Two trailing exits, not three: one leaves
// address-family, one leaves router-bgp.
//
// The statements themselves live in kit/lib/ocnos-redistribute.cfg, which the
// kit's shell helpers (kit/lib/bnk.sh) load the same way.
func OcNOSRedistributeScript() string {
	cfg := mustRender("lib/ocnos-redistribute.cfg", map[string]string{"LOCAL_AS": strconv.Itoa(BGPTMMAS)})
	return "printf '" + strings.ReplaceAll(cfg, "\n", `\n`) +
		"' > /tmp/ocibnkctl-redist.cfg; imish -f /tmp/ocibnkctl-redist.cfg"
}

// PasswdConfCmd writes the one-line passwd.conf imish needs, run in each TMM
// pod's f5-tmm-routing container. The line itself is
// kit/lib/ocnos-passwd.conf, which kit/lib/bnk.sh writes the same way.
var PasswdConfCmd = "echo '" + strings.TrimSpace(mustRender("lib/ocnos-passwd.conf", nil)) +
	"' > /config/zebos/rd0/passwd.conf"
