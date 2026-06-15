package deploy

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/mwiget/ocibnkctl/internal/embedded"
	"github.com/mwiget/ocibnkctl/internal/poc"
	"github.com/mwiget/ocibnkctl/internal/version"
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
	// TMMReplicas is CNEInstance.spec.tmmReplicas — how many TMM pods FLO
	// runs as a Deployment (the demo-mode "TMM as N replicas" path). It
	// tracks cluster.tmm_nodes so each TMM lands on its own labelled node.
	TMMReplicas int
	// NetworkAttachments is CNEInstance.spec.networkAttachments — the
	// Multus NAD(s) FLO attaches to every TMM pod. Empty in the default
	// demo shape; selfip-dag sets it to the DAG bridge NAD so mapres can
	// grab net1 and the F5SPKVlan self-IPs can bind; anycast-bgp sets it
	// to the bnk-bgp NAD so ZeBOS can source BGP from net1.
	NetworkAttachments []string
	// MapresOnDP is the string value for the TMM_MAPRES_ADDL_VETHS_ON_DP
	// env var ("TRUE"/"FALSE"). standby + selfip-dag use TRUE (mapres
	// grabs net1 as interface 1.1); anycast-bgp uses FALSE so net1 keeps
	// its kernel IP for ZeBOS to bind BGP to.
	MapresOnDP string
}

// RenderCNEInstance builds the CNEInstance YAML for a PoC. Demo
// mode is mandatory in this build — TMM relies on virtio inside its
// pod netns; SR-IOV / DPU pathways do not exist in the demo shape.
func RenderCNEInstance(p *poc.PoC) (string, error) {
	k, v := p.BNK.TMMLabel()
	in := CNEInputs{
		InstanceName:    "bnk-instance",
		ManifestVersion: version.CNEManifestVersion,
		DeploymentSize:  "Small",
		TMMNodeLabelKey: k,
		TMMNodeLabelVal: v,
		MetricSubsystem: p.BNK.MetricSubsystemEnabled(),
		TMMReplicas:     p.Cluster.Workers(),
		MapresOnDP:      "TRUE",
	}
	// Dispatch NAD + mapres by data-plane mode:
	//   standby     — no NAD, mapres TRUE (BNK's stock HA shape).
	//   selfip-dag  — DAG NAD on net1, mapres TRUE (interface 1.1 for the
	//                 F5SPKVlan to bind).
	//   anycast-bgp — bnk-bgp NAD on net1, mapres FALSE (net1 keeps its
	//                 kernel IP so ZeBOS can source BGP from it).
	switch p.BNK.DataplaneMode() {
	case poc.DataplaneSelfIPDAG:
		in.NetworkAttachments = []string{DAGNADName}
	case poc.DataplaneAnycastBGP:
		in.NetworkAttachments = []string{BGPNADName}
		in.MapresOnDP = "FALSE"
	}
	return renderTemplate("templates/cne-instance.yaml.tmpl", in)
}

func renderTemplate(path string, data any) (string, error) {
	raw, err := embedded.Templates.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("load %s: %w", path, err)
	}
	tmpl, err := template.New(path).Parse(string(raw))
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("execute %s: %w", path, err)
	}
	return out.String(), nil
}
