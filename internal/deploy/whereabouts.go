package deploy

import (
	"context"
	"fmt"
	"strings"

	"github.com/mwiget/ocibnkctl/internal/version"
)

// EnsureWhereabouts installs the whereabouts cluster-wide IPAM plugin
// (idempotent): its two CRDs (the cluster-wide IP allocation store) and the
// install DaemonSet, which seeds the whereabouts CNI binary into /opt/cni/bin
// and writes its CNI conf on every node. A NAD's `ipam: {type: whereabouts}`
// then hands each attached pod a UNIQUE address from one shared pool — so a
// single bnk-bgp NAD + a TMM DaemonSet (one pod per worker) scales to many
// workers without per-node net1 IP collisions on the shared edge L2. The
// DaemonSet image is pinned to the release tag (upstream ships :latest). All
// three manifests are SHA-verified before apply (see version.Whereabouts*).
func EnsureWhereabouts(ctx context.Context, r *Runner) error {
	for _, crd := range []struct{ url, sha string }{
		{version.WhereaboutsIPPoolCRDURL, version.WhereaboutsIPPoolCRDSHA},
		{version.WhereaboutsReservationCRDURL, version.WhereaboutsReservationCRDSHA},
	} {
		body, err := downloadAndVerify(ctx, crd.url, crd.sha)
		if err != nil {
			return fmt.Errorf("whereabouts CRD: %w", err)
		}
		if err := r.Apply(ctx, string(body)); err != nil {
			return fmt.Errorf("apply whereabouts CRD: %w", err)
		}
	}
	ds, err := downloadAndVerify(ctx, version.WhereaboutsDaemonSetURL, version.WhereaboutsDaemonSetSHA)
	if err != nil {
		return fmt.Errorf("whereabouts daemonset: %w", err)
	}
	pinned := strings.ReplaceAll(string(ds),
		"ghcr.io/k8snetworkplumbingwg/whereabouts:latest",
		"ghcr.io/k8snetworkplumbingwg/whereabouts:"+version.WhereaboutsTag)
	if err := r.Apply(ctx, pinned); err != nil {
		return fmt.Errorf("apply whereabouts daemonset: %w", err)
	}
	return r.Kubectl(ctx, "-n", "kube-system", "rollout", "status",
		"daemonset/whereabouts", "--timeout=3m")
}

// RenderBGPNADWhereabouts renders the bnk-bgp bridge NAD using whereabouts
// cluster-wide IPAM instead of per-node host-local. Under the wholeCluster
// DaemonSet shape every TMM pod attaches this one NAD; whereabouts hands each
// pod a UNIQUE net1 from the shared pool, so N TMMs across N workers never
// collide on the shared bnk-edge L2. Same name/bridge/subnet as RenderBGPNAD
// (the L2 shape is unchanged) — only the IPAM backend differs.
func RenderBGPNADWhereabouts(namespace string) string {
	return renderWhereaboutsBridgeNAD(BGPNADName, namespace, BGPBridge, BGPSubnet, BGPWhereaboutsStart, BGPIPAMEnd)
}

// renderWhereaboutsBridgeNAD renders a bridge-CNI NetworkAttachmentDefinition
// whose IPAM is whereabouts (cluster-wide) rather than host-local (per-node).
// host-local allocates independently on each node, which is fine only while
// the per-node bridges are isolated L2; whereabouts allocates from one shared
// store, which is what a DaemonSet of TMM pods sharing a single edge L2 needs.
func renderWhereaboutsBridgeNAD(name, namespace, bridge, subnet, rangeStart, rangeEnd string) string {
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
        "type": "whereabouts",
        "range": "%s",
        "range_start": "%s",
        "range_end": "%s"
      }
    }
`, name, namespace, name, bridge, subnet, rangeStart, rangeEnd)
}
