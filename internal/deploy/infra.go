package deploy

import "fmt"

// BNK 2.4.0 replaced the per-namespace F5BnkGateway IP pools with a
// platform-level, singleton `Infra` CR (gateway.k8s.f5.com/v1alpha1) in the
// CNEInstance namespace, consumed by per-tenant GatewaySettings CRs. A
// Gateway with static spec.addresses needs neither; a Gateway WITHOUT
// addresses gets one allocated from an Infra IPAM pool via the
// GatewaySettings it references in spec.infrastructure.parametersRef.
//
// The demo shape has no VLAN underlay for TMM (anycast-bgp keeps net1 in the
// kernel for OcNOS), so the Infra CR here is IPAM-only: `networks` and
// `networkAttachments` are deliberately empty lists — the CRD marks them
// required but sets no minItems, and the 2.4.0 controller programs the CR
// fine that way (verified live: Accepted/ResolvedRefs/Programmed=True and a
// dynamic Gateway allocated 203.0.113.110). Reconciliation of Infra /
// GatewaySettings is gated by the cneController env USE_GATEWAY_SETTINGS=true
// (set in cne-instance.yaml.tmpl); without it the CR sits at
// "Pending: Waiting for controller".
const (
	// InfraName is the singleton's name (F5's docs use `infra`).
	InfraName = "infra"
	// DynamicVIPPool is the Infra ipams[] entry GatewaySettings reference for
	// dynamically allocated Gateway listener addresses.
	DynamicVIPPool = "bnk-dynamic-vips"
	// DynamicVIPRangeStart/End carve a slice of the 203.0.113.0/24 external
	// range for IPAM. Static-address scenarios use .100-.109 and .120+, so
	// the two never collide on the shared edge L2.
	DynamicVIPRangeStart = "203.0.113.110"
	DynamicVIPRangeEnd   = "203.0.113.119"
)

// RenderInfra returns the platform Infra CR for the CNEInstance namespace.
func RenderInfra(namespace string) string {
	return fmt.Sprintf(`apiVersion: gateway.k8s.f5.com/v1alpha1
kind: Infra
metadata:
  name: %s
  namespace: %s
spec:
  ipams:
  - name: %s
    ipPools:
    - rangeStart: %s
      rangeEnd: %s
  networkAttachments: []
  networks: []
`, InfraName, namespace, DynamicVIPPool, DynamicVIPRangeStart, DynamicVIPRangeEnd)
}
