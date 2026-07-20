package deploy

import "testing"

// Real `kubectl get daemonset,deployment,statefulset -n f5-cne-core` output
// under workloadJSONPath, captured from a healthy 2.3.1 cluster.
const healthyWorkloads = `DaemonSet/f5-spk-csrc 1 1
Deployment/f5-crdconversion 1 1
Deployment/f5-ipam-ctlr 1 1
Deployment/f5-observer-operator 1 1
Deployment/f5-rabbit 1 1
Deployment/f5-spk-cwc 1 1
Deployment/otel-collector 1 1
StatefulSet/f5-observer 1 1
StatefulSet/f5-observer-receiver 1 1
`

func TestParseWorkloadStatus_Healthy(t *testing.T) {
	ws := parseWorkloadStatus(healthyWorkloads)
	if len(ws) != 9 {
		t.Fatalf("parsed %d workloads, want 9: %+v", len(ws), ws)
	}
	if bad := unreadyWorkloads(ws); len(bad) != 0 {
		t.Errorf("healthy cluster reported unready: %s", describeUnready(bad))
	}
}

// When a DaemonSet has no available pods, .status.numberAvailable is absent and
// jsonpath renders it as the empty string — the line has only two fields. This
// is the exact shape f5-spk-csrc produced while wedged in ContainerCreating on
// a missing /etc/iproute2/rt_tables hostPath.
func TestParseWorkloadStatus_MissingAvailableIsZero(t *testing.T) {
	ws := parseWorkloadStatus("DaemonSet/f5-spk-csrc 1 \nDeployment/f5-rabbit 1 1\n")
	if len(ws) != 2 {
		t.Fatalf("parsed %d workloads, want 2: %+v", len(ws), ws)
	}

	bad := unreadyWorkloads(ws)
	if len(bad) != 1 {
		t.Fatalf("unready = %d, want 1: %+v", len(bad), bad)
	}
	if bad[0].Name != "DaemonSet/f5-spk-csrc" || bad[0].Available != 0 || bad[0].Desired != 1 {
		t.Errorf("got %+v, want f5-spk-csrc 0/1", bad[0])
	}
	if got, want := describeUnready(bad), "DaemonSet/f5-spk-csrc (0/1)"; got != want {
		t.Errorf("describeUnready = %q, want %q", got, want)
	}
}

func TestParseWorkloadStatus_PartialAvailability(t *testing.T) {
	ws := parseWorkloadStatus("DaemonSet/f5-tmm 3 2\n")
	bad := unreadyWorkloads(ws)
	if len(bad) != 1 {
		t.Fatalf("unready = %d, want 1", len(bad))
	}
	if got, want := describeUnready(bad), "DaemonSet/f5-tmm (2/3)"; got != want {
		t.Errorf("describeUnready = %q, want %q", got, want)
	}
}

// A parse failure must not be able to fail a healthy deploy, so malformed
// lines are skipped rather than counted as unready.
func TestParseWorkloadStatus_SkipsMalformed(t *testing.T) {
	ws := parseWorkloadStatus("\ngarbage\nDeployment/ok 1 1\nDeployment/nan x y\n   \n")
	if len(ws) != 1 {
		t.Fatalf("parsed %d workloads, want 1: %+v", len(ws), ws)
	}
	if ws[0].Name != "Deployment/ok" {
		t.Errorf("got %q, want Deployment/ok", ws[0].Name)
	}
}

func TestParseWorkloadStatus_EmptyNamespace(t *testing.T) {
	if ws := parseWorkloadStatus(""); len(ws) != 0 {
		t.Errorf("empty output parsed %d workloads: %+v", len(ws), ws)
	}
}

// Over-provisioned (available > desired, e.g. mid-rollout surge) is ready.
func TestWorkloadReady_AvailableExceedsDesired(t *testing.T) {
	if !(workload{Name: "Deployment/x", Desired: 1, Available: 2}).ready() {
		t.Error("available > desired should be ready")
	}
}
