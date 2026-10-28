package deploy

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

// GatewayClassName is the cluster-scoped GatewayClass every BNK Gateway
// references; `deploy cne` applies it as platform infrastructure.
const GatewayClassName = "bnk-gatewayclass"

// RenderGatewayClass returns the GatewayClass bound to the F5 CNE controller.
func RenderGatewayClass() string {
	return mustRender("platform/cne/gatewayclass.yaml", map[string]string{"GATEWAYCLASS_NAME": GatewayClassName})
}

// RenderInfra returns the platform Infra CR for the CNEInstance namespace.
func RenderInfra(namespace string) string {
	return mustRender("platform/cne/infra.yaml", map[string]string{
		"INFRA_NAME":      InfraName,
		"NAMESPACE":       namespace,
		"VIP_POOL":        DynamicVIPPool,
		"VIP_RANGE_START": DynamicVIPRangeStart,
		"VIP_RANGE_END":   DynamicVIPRangeEnd,
	})
}
