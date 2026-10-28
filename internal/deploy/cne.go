package deploy

import (
	"strconv"

	"github.com/mwiget/ocibnkctl/internal/poc"
	"github.com/mwiget/ocibnkctl/internal/version"
	"github.com/mwiget/ocibnkctl/kit"
)

// CNEInputs is the flat shape passed to cne-instance.yaml.tmpl. Much
// smaller than dpubnkctl's because the demo path runs TMM in demo mode
// — no DPU, no SR-IOV, no NetworkAttachments, no dynamicRouting / ACL.
type CNEInputs struct {
	InstanceName    string
	ManifestVersion string
	DeploymentSize  string
	TMMNodeLabelKey string
	TMMNodeLabelVal string
	// MetricSubsystem is CNEInstance.spec.telemetry.metricSubsystem.enabled.
	// The small-host profile sets it false to shed TMM's observer sidecar
	// (see poc.BNK.MetricSubsystemEnabled).
	MetricSubsystem bool
	// LoggingSubsystem is CNEInstance.spec.telemetry.loggingSubsystem.enabled.
	// Off unless the AI gateway stores metrics, whose HSL receiver runs in
	// the fluentd it deploys (see poc.BNK.LoggingSubsystemEnabled).
	LoggingSubsystem bool
	// AIGateway is CNEInstance.spec.aiGateway; the block is only rendered
	// when AIGateway.Enabled.
	AIGateway poc.AIGateway
	// NetworkAttachment is the one entry of
	// CNEInstance.spec.networkAttachments — the Multus NAD FLO attaches to
	// every TMM pod. Empty (no list at all) in the standby shape; selfip-dag
	// sets the DAG bridge NAD so mapres can grab net1 and the F5SPKVlan
	// self-IPs can bind; anycast-bgp sets the bnk-bgp NAD so ZeBOS can
	// source BGP from net1.
	NetworkAttachment string
	// MapresOnDP is the string value for the TMM_MAPRES_ADDL_VETHS_ON_DP
	// env var ("TRUE"/"FALSE"). standby + selfip-dag use TRUE (mapres
	// grabs net1 as interface 1.1); anycast-bgp uses FALSE so net1 keeps
	// its kernel IP for ZeBOS to bind BGP to.
	MapresOnDP string
	// UseGatewaySettings is the cneController env USE_GATEWAY_SETTINGS
	// ("true"/"false"). It selects which of BNK 2.4's two mutually
	// exclusive network models the controller reconciles:
	//
	//   true  — Infra + GatewaySettings. Needed for the platform Infra IPAM
	//           pool (address-less Gateways, the fic-dynamic-ip scenario).
	//           Legacy F5SPKVlan/Vrf CRs are accepted and then ignored.
	//   false — legacy SPK. F5SPKVlan/Vrf reconcile and program TMM, and the
	//           RoutingTemplate `.vlans` variable is populated per TMM.
	//
	// selfip-dag drives TMM through an F5SPKVlan, so it MUST have false;
	// every other mode wants true. See the long comment in
	// templates/cne-instance.yaml.tmpl.
	UseGatewaySettings string
}

// RenderCNEInstance builds the CNEInstance YAML for a PoC. Demo
// mode is mandatory in this build — TMM relies on virtio inside its
// pod netns; SR-IOV / DPU pathways do not exist in the demo shape.
func RenderCNEInstance(p *poc.PoC) (string, error) {
	k, v := p.BNK.TMMLabel()
	in := CNEInputs{
		InstanceName:     "bnk-instance",
		ManifestVersion:  version.CNEManifestVersion,
		DeploymentSize:   p.BNK.DeploymentSizeOrDefault(),
		TMMNodeLabelKey:  k,
		TMMNodeLabelVal:  v,
		MetricSubsystem:  p.BNK.MetricSubsystemEnabled(),
		LoggingSubsystem: p.BNK.LoggingSubsystemEnabled(),
		AIGateway:        p.BNK.AIGateway,
		MapresOnDP:       "TRUE",
		// Infra/GatewaySettings is the default; selfip-dag flips it below.
		UseGatewaySettings: "true",
	}
	// Dispatch NAD + mapres by data-plane mode:
	//   standby     — no NAD, mapres TRUE (BNK's stock HA shape).
	//   selfip-dag  — DAG NAD on net1, mapres TRUE (interface 1.1 for the
	//                 F5SPKVlan to bind).
	//   anycast-bgp — bnk-bgp NAD on net1, mapres FALSE (net1 keeps its
	//                 kernel IP so ZeBOS can source BGP from it).
	switch p.BNK.DataplaneMode() {
	case poc.DataplaneSelfIPDAG:
		in.NetworkAttachment = DAGNADName
		// The F5SPKVlan this mode renders is only reconciled in the legacy
		// SPK model; with USE_GATEWAY_SETTINGS=true the controller accepts
		// the CR and silently never programs it, leaving TMM with a bare
		// interface 1.1 and no self-IP.
		in.UseGatewaySettings = "false"
	case poc.DataplaneAnycastBGP:
		in.NetworkAttachment = BGPNADName
		in.MapresOnDP = "FALSE"
	}
	return kit.Render("platform/cne/cne-instance.yaml", in.vars())
}

// vars maps the inputs onto cne-instance.yaml's variables.
func (in CNEInputs) vars() map[string]string {
	v := map[string]string{
		"INSTANCE_NAME":        in.InstanceName,
		"MANIFEST_VERSION":     in.ManifestVersion,
		"DEPLOYMENT_SIZE":      in.DeploymentSize,
		"LOGGING_SUBSYSTEM":    strconv.FormatBool(in.LoggingSubsystem),
		"METRIC_SUBSYSTEM":     strconv.FormatBool(in.MetricSubsystem),
		"MAPRES_ON_DP":         in.MapresOnDP,
		"USE_GATEWAY_SETTINGS": in.UseGatewaySettings,
		"NAD_NAME":             in.NetworkAttachment,
		"AIGW_ENABLED":         "",
		"AIGW_METRICS":         strconv.FormatBool(in.AIGateway.AIMetrics),
		"AIGW_CONSOLE_URL":     in.AIGateway.ConsoleURL,
	}
	if in.AIGateway.Enabled {
		v["AIGW_ENABLED"] = "true"
	}
	return v
}
