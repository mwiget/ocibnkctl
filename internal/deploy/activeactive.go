package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Active/active multi-TMM data-plane constants.
//
// The demo shape makes every TMM active by giving each pod a net1 on a
// bridge-CNI Multus NAD; with TMM_MAPRES_ADDL_VETHS_ON_DP=TRUE (the
// default) mapres grabs net1 onto the data plane and names it interface
// "1.1", which the F5SPKVlan binds. The VLAN's selfip_v4s assign one
// self-IP per TMM (DP-0, DP-1, …) so each leaves standby, and pod_hash
// programs the stateless DAG.
//
// IMPORTANT: each TMM only serves the traffic that physically lands on
// its own node's bridge — the per-node software bridges are isolated, so
// a single VIP's traffic is NOT transparently fanned across TMM nodes.
// That (hardware DAG across nodes) is the DPU/SR-IOV value prop and is
// out of scope for demo mode. See README "Network topology".
const (
	DAGNADName   = "f5-tmm-dag"
	DAGBridge    = "br-f5-tmm-dag"
	DAGSubnet    = "192.0.2.0/24"
	DAGIPAMStart = "192.0.2.20"
	DAGIPAMEnd   = "192.0.2.250"
	DAGVlanName  = "f5-tmm-dag"
	DAGInterface = "1.1"
	DAGPodHash   = "SRC_ADDR"
	DAGSelfV4Len = 24

	// Multus thick-plugin pin (matches the bgp-peer-frr scenario). The
	// base demo cluster ships no Multus; active/active installs it.
	MultusManifestURL = "https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/v4.1.4/deployments/multus-daemonset-thick.yml"
	MultusManifestSHA = "33fef64fbb67ef5d68183bad5b2aec4163dad0ebb0b63abe25343155d0d8b4be"
)

// BGP-anycast multi-TMM data-plane constants (tmm_dataplane_mode:
// anycast-bgp). Every per-node TMM runs with mapres FALSE and peers
// with an FRR router over a bridge-CNI Multus NAD on net1, advertising
// its VIP /32 over BGP. Mirrors the bgp-peer-frr scenario's NAD so the
// deploy path and the scenario share one L2 shape — name/bridge/subnet
// match `internal/scenarios/bgppeer/manifests/02-nad.yaml`.
//
// On a single host the per-node bnk-bgp bridges are isolated L2
// segments, so real ECMP fan-out across nodes cannot be demonstrated;
// this validates the anycast MODEL (each TMM advertises its VIP /32 to
// a co-located peer), not multi-host fan-out. See README "Network
// topology" and the validate warning.
const (
	BGPNADName   = "bnk-bgp"
	BGPBridge    = "br-bnk-bgp"
	BGPSubnet    = "192.168.99.0/24"
	BGPIPAMStart = "192.168.99.20"
	BGPIPAMEnd   = "192.168.99.250"
	// BGPWhereaboutsStart is the start of the TMM net1 whereabouts pool on the
	// shared bnk-edge L2. It sits ABOVE the edge fabric's pinned addresses
	// (external FRR .41, origin .50, worker uplinks .60-.159) so a TMM net1
	// can never collide with them. The host-local BGPIPAMStart (.20) is for
	// the standby-mode bgp-peer-frr scenario's own isolated bridge, where
	// there's no edge fabric to dodge.
	BGPWhereaboutsStart = "192.168.99.160"
	// BGPRoutingTemplateCM is the cluster-wide ConfigMap FLO renders into
	// every TMM pod's ZeBOS config (one per CNEInstance, shared by all TMM
	// pods). A single shared template still yields a unique router-id per
	// pod because `bgp router-id %%POD_IP%%` is a per-pod token FLO expands
	// — the linchpin that lets N-pod anycast work from one ConfigMap.
	BGPRoutingTemplateCM = "f5-tmm-dynamic-routing-template"
	// BGP AS numbers mirror the bgp-peer-frr scenario: TMM/ZeBOS in 65000,
	// the FRR peer in 65001.
	BGPTMMAS  = 65000
	BGPPeerAS = 65001
)

// DAGSelfIPs returns one self-IP per TMM node (192.0.2.10, .11, …), kept
// below the NAD IPAM range (.20+) so they never collide with addresses
// host-local hands to other pods on the bridge. selfip_v4s[i] binds to
// TMM device DP-i. Capped at the schema's MaxTMMNodes so it stays inside
// the final octet.
func DAGSelfIPs(n int) []string {
	if n < 1 {
		n = 1
	}
	ips := make([]string, n)
	for i := 0; i < n; i++ {
		ips[i] = fmt.Sprintf("192.0.2.%d", 10+i)
	}
	return ips
}

// RenderDAGNAD renders the bridge-CNI NetworkAttachmentDefinition that
// gives each TMM pod a net1 on a per-node software bridge for the
// self-IP + DAG all-active path.
func RenderDAGNAD(namespace string) string {
	return renderBridgeNAD(DAGNADName, namespace, DAGBridge, DAGSubnet, DAGIPAMStart, DAGIPAMEnd)
}

// RenderBGPNAD renders the bridge-CNI NetworkAttachmentDefinition for
// the anycast-bgp path. Same shape as the bgp-peer-frr scenario's NAD
// (name/bridge/subnet match) so the deploy path and the scenario peer
// over one L2 segment.
func RenderBGPNAD(namespace string) string {
	return renderBridgeNAD(BGPNADName, namespace, BGPBridge, BGPSubnet, BGPIPAMStart, BGPIPAMEnd)
}

// renderBridgeNAD renders a bridge-CNI NetworkAttachmentDefinition that
// gives each attached pod a net1 on a per-node software bridge with
// host-local IPAM. Both RenderDAGNAD and RenderBGPNAD call it.
func renderBridgeNAD(name, namespace, bridge, subnet, rangeStart, rangeEnd string) string {
	return fmt.Sprintf(`apiVersion: k8s.cni.cncf.io/v1
kind: NetworkAttachmentDefinition
metadata:
  name: %s
  namespace: %s
  annotations:
    # FLO's TMM-template propagation expects a resourceName on the NAD
    # (the SR-IOV convention). Bridge CNI has no hardware behind it, so
    # this is a documentary stand-in that silences a reconcile warning.
    k8s.v1.cni.cncf.io/resourceName: bridge.cni.io/%s
spec:
  config: |
    {
      "cniVersion": "0.3.1",
      "type": "bridge",
      "bridge": "%s",
      "ipMasq": false,
      "isGateway": false,
      "hairpinMode": true,
      "ipam": {
        "type": "host-local",
        "subnet": "%s",
        "rangeStart": "%s",
        "rangeEnd": "%s"
      }
    }
`, name, namespace, name, bridge, subnet, rangeStart, rangeEnd)
}

// RenderAnycastZebosConfigMap renders the cluster-wide OcNOS routing
// template ConfigMap for the anycast-bgp path. Every TMM pod peers the
// external bnk-edge FRR at peerIP over net1 and advertises its routes
// (the connected net1 /24 + the Gateway VIP /32s FLO installs as kernel
// routes) over BGP.
//
// router-id is the literal token %%POD_IP%% — FLO expands it per pod, so
// this single shared ConfigMap yields a UNIQUE router-id per TMM pod (the
// linchpin that lets N pods form distinct sessions for anycast).
//
// OcNOS XP-6.6.0 (ZEBOS_STATE=ocnos / ocnos-img) refuses to inject
// redistributed routes into BGP unless the redistribute statement is
// qualified by a route-map; RMALL is an unconditional permit (no match =
// permit all). Without it OcNOS silently advertises 0 prefixes. We
// redistribute kernel (Gateway VIP /32s), connected (net1's subnet) and
// static, all at router-bgp scope.
//
// vip is optional. When non-empty a `network <vip>/32` statement is added
// (advertised only if the route is in the RIB); when empty the path relies
// on `redistribute kernel` to advertise the Gateway VIPs FLO installs.
func RenderAnycastZebosConfigMap(namespace, peerIP, vip string) string {
	var networkStmt string
	if vip != "" {
		networkStmt = fmt.Sprintf("      network %s/32\n", vip)
	}
	return fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  ZebOS.conf: |
    route-map RMALL permit 10
    !
    router bgp %d
      bgp router-id %%%%POD_IP%%%%
      bgp log-neighbor-changes
      bgp graceful-restart restart-time 120
      no bgp default ipv4-unicast
      redistribute kernel route-map RMALL
      redistribute connected route-map RMALL
      redistribute static route-map RMALL
%s      !
      neighbor %s remote-as %d
      neighbor %s update-source net1
      !
      address-family ipv4
        neighbor %s activate
        neighbor %s soft-reconfiguration inbound
      exit-address-family
    !
`, BGPRoutingTemplateCM, namespace, BGPTMMAS, networkStmt,
		peerIP, BGPPeerAS, peerIP, peerIP, peerIP)
}

// RenderTMMVlan renders the F5SPKVlan that turns the mapres-grabbed net1
// (interface "1.1") into a TMM VLAN with one self-IP per TMM node and a
// pod_hash stateless DAG. selfIPs must have one entry per TMM replica.
func RenderTMMVlan(namespace string, selfIPs []string) string {
	return fmt.Sprintf(`apiVersion: k8s.f5net.com/v1
kind: F5SPKVlan
metadata:
  name: %s
  namespace: %s
spec:
  name: %s
  category: external
  interfaces: ["%s"]
  selfip_v4s: [%s]
  prefixlen_v4: %d
  pod_hash: %s
  cmp_hash: %s
  mtu: 1500
`, DAGVlanName, namespace, DAGVlanName, DAGInterface,
		strings.Join(selfIPs, ","), DAGSelfV4Len, DAGPodHash, DAGPodHash)
}

// EnsureMultus installs the Multus thick plugin (idempotent) and bumps
// its memory limit so it survives CNI churn. Required before any NAD can
// attach a net1 interface to a pod.
func EnsureMultus(ctx context.Context, r *Runner) error {
	status, _ := r.KubectlCapture(ctx, "-n", "kube-system", "get",
		"daemonset/kube-multus-ds",
		"-o", "jsonpath={.status.numberReady}/{.status.desiredNumberScheduled}")
	installed := strings.Contains(status, "/") && !strings.HasPrefix(strings.TrimSpace(status), "0/")
	if !installed {
		body, err := downloadAndVerify(ctx, MultusManifestURL, MultusManifestSHA)
		if err != nil {
			return fmt.Errorf("multus manifest: %w", err)
		}
		if err := r.Apply(ctx, string(body)); err != nil {
			return fmt.Errorf("apply multus: %w", err)
		}
	}
	// Upstream's 50Mi limit OOMKills under CNI churn; 500Mi holds.
	patch := `[{"op":"replace","path":"/spec/template/spec/containers/0/resources/limits/memory","value":"500Mi"},` +
		`{"op":"replace","path":"/spec/template/spec/containers/0/resources/requests/memory","value":"200Mi"}]`
	_ = r.Kubectl(ctx, "-n", "kube-system", "patch", "daemonset/kube-multus-ds", "--type=json", "-p", patch)
	return r.Kubectl(ctx, "-n", "kube-system", "rollout", "status",
		"daemonset/kube-multus-ds", "--timeout=3m")
}

// downloadAndVerify fetches url and refuses to return the body unless its
// sha256 matches wantHex (pin guard against upstream tampering / drift).
func downloadAndVerify(ctx context.Context, url, wantHex string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if got := hex.EncodeToString(sha256Sum(body)); got != wantHex {
		return nil, fmt.Errorf("integrity check failed for %s: got sha256=%s want %s "+
			"— refusing to apply (upstream tampered or the version pin needs an update)", url, got, wantHex)
	}
	return body, nil
}

func sha256Sum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}
