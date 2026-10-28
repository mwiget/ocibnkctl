package deploy

import (
	"fmt"
)

// RenderBGPNADWhereabouts renders the bnk-bgp bridge NAD using whereabouts
// cluster-wide IPAM instead of per-node host-local. Under the wholeCluster
// DaemonSet shape every TMM pod attaches this one NAD; whereabouts hands each
// pod a UNIQUE net1 from the shared pool, so N TMMs across N workers never
// collide on the shared bnk-edge L2. Same name/bridge/subnet as RenderBGPNAD
// (the L2 shape is unchanged) — only the IPAM backend differs.
//
// octet is the cluster's edge_octet (Cluster.EdgeNet()): the TMM net1 pool
// MUST sit on the same 192.168.<octet>.0/24 L2 as the edge fabric's external
// FRR (.41) and origin (.50), or the TMM can't reach its BGP peer. Deriving
// the subnet/range from the octet (not the hardcoded .99 constants) is what
// makes a non-default edge_octet work — with the default octet 99 this yields
// exactly the old BGPSubnet/.160-.250 values.
func RenderBGPNADWhereabouts(namespace string, octet int) string {
	subnet := fmt.Sprintf("192.168.%d.0/24", octet)
	rangeStart := fmt.Sprintf("192.168.%d.160", octet)
	rangeEnd := fmt.Sprintf("192.168.%d.250", octet)
	return renderWhereaboutsBridgeNAD(BGPNADName, namespace, BGPBridge, subnet, rangeStart, rangeEnd)
}

// renderWhereaboutsBridgeNAD renders a bridge-CNI NetworkAttachmentDefinition
// whose IPAM is whereabouts (cluster-wide) rather than host-local (per-node).
// host-local allocates independently on each node, which is fine only while
// the per-node bridges are isolated L2; whereabouts allocates from one shared
// store, which is what a DaemonSet of TMM pods sharing a single edge L2 needs.
func renderWhereaboutsBridgeNAD(name, namespace, bridge, subnet, rangeStart, rangeEnd string) string {
	return mustRender("platform/cne/nad-bridge-whereabouts.yaml", bridgeNADVars(name, namespace, bridge, subnet, rangeStart, rangeEnd))
}
