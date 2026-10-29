package deploy

// Names and pins shared with the kit (kit/platform), for the Go code that
// reports on or points at what the install scripts create.

const (
	// GatewayClassName is the GatewayClass the cne phase creates.
	GatewayClassName = "bnk-gatewayclass"
	// LicenseCRName is the License the cne phase applies.
	LicenseCRName = "f5-cne-cluster-license"
	// JWKSConfigMapName / JWKSConfigMapKey hold TEEM's test JWKS for a test JWT.
	JWKSConfigMapName = "cwc-jwks-cm"
	JWKSConfigMapKey  = "key"
	// BGPNADName is anycast-bgp's NAD, which gives each TMM its net1.
	BGPNADName = "bnk-bgp"
	// BGPTMMAS / BGPPeerAS: TMM's AS and the edge router's.
	BGPTMMAS  = 65000
	BGPPeerAS = 65001
	// `deploy shrink`'s default request ceilings: platform.env's SHRINK_CPU
	// and SHRINK_MEMORY defaults (TestPlatformEnvMatchesGo).
	DefaultShrinkCPURequest    = "25m"
	DefaultShrinkMemoryRequest = "128Mi"
	// Multus (thick) as the cne phase installs it.
	MultusManifestURL = "https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/v4.1.4/deployments/multus-daemonset-thick.yml"
	MultusManifestSHA = "33fef64fbb67ef5d68183bad5b2aec4163dad0ebb0b63abe25343155d0d8b4be"
)
