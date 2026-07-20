package deploy

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// workloadJSONPath emits one "kind/name desired available" line per workload.
// DaemonSets report desiredNumberScheduled/numberAvailable; Deployments and
// StatefulSets report spec.replicas/status.availableReplicas. A field that is
// absent (no pods available yet) renders as the empty string, which
// parseWorkloadStatus reads as 0.
const workloadJSONPath = `{range .items[*]}` +
	`{.kind}/{.metadata.name} ` +
	`{.status.desiredNumberScheduled}{.spec.replicas} ` +
	`{.status.numberAvailable}{.status.availableReplicas}` +
	"\n" +
	`{end}`

// workload is one DaemonSet/Deployment/StatefulSet and its availability.
type workload struct {
	Name      string
	Desired   int
	Available int
}

func (w workload) ready() bool { return w.Available >= w.Desired }

// parseWorkloadStatus reads the workloadJSONPath output. Malformed lines are
// skipped rather than failing the gate — a parse bug must not be able to block
// a deploy that is actually healthy.
func parseWorkloadStatus(raw string) []workload {
	var out []workload
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		desired, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		available := 0
		if len(fields) > 2 {
			if n, err := strconv.Atoi(fields[2]); err == nil {
				available = n
			}
		}
		out = append(out, workload{Name: fields[0], Desired: desired, Available: available})
	}
	return out
}

// unreadyWorkloads returns the not-fully-available entries, name-sorted.
func unreadyWorkloads(ws []workload) []workload {
	var bad []workload
	for _, w := range ws {
		if !w.ready() {
			bad = append(bad, w)
		}
	}
	sort.Slice(bad, func(i, j int) bool { return bad[i].Name < bad[j].Name })
	return bad
}

// describeUnready renders "kind/name (0/1)" for an error message.
func describeUnready(bad []workload) string {
	parts := make([]string, 0, len(bad))
	for _, w := range bad {
		parts = append(parts, fmt.Sprintf("%s (%d/%d)", w.Name, w.Available, w.Desired))
	}
	return strings.Join(parts, ", ")
}

// WaitWorkloadsAvailable blocks until every DaemonSet, Deployment and
// StatefulSet in namespace has all its replicas available.
//
// This exists because a workload can be wedged in a way nothing else notices.
// f5-spk-csrc hostPath-mounts /etc/iproute2/rt_tables, absent on a stock
// rancher/k3s node; the mount failed forever, the pod never left
// ContainerCreating, and its DaemonSet sat at 0 available for 29 minutes while
// every other component came up healthy and the deploy reported success. A
// green deploy that is not green hides the next failure too, so the phase must
// not declare DONE while anything in the shared namespace is unavailable.
//
// Only workloads that exist are checked, so this is safe to call before
// optional components are installed.
func WaitWorkloadsAvailable(ctx context.Context, r *Runner, namespace string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last []workload

	for {
		raw, err := r.KubectlCapture(ctx, "get", "daemonset,deployment,statefulset",
			"-n", namespace, "-o", "jsonpath="+workloadJSONPath)
		if err == nil {
			last = unreadyWorkloads(parseWorkloadStatus(raw))
			if len(last) == 0 {
				return nil
			}
		}

		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("could not read workload status in %s: %w", namespace, err)
			}
			return fmt.Errorf("workload(s) in %s not available after %s: %s",
				namespace, timeout, describeUnready(last))
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}
