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
// gives each TMM pod a net1 on a per-node software bridge.
func RenderDAGNAD(namespace string) string {
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
`, DAGNADName, namespace, DAGNADName, DAGBridge, DAGSubnet, DAGIPAMStart, DAGIPAMEnd)
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
