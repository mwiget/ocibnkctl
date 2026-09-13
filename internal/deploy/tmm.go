package deploy

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// tmmDaemonSetJSONPath emits "desired updated available" for the f5-tmm
// DaemonSet. A count that is still zero is absent from .status and renders as
// the empty string, which parseTMMRollout reads as 0.
const tmmDaemonSetJSONPath = `{.status.desiredNumberScheduled} ` +
	`{.status.updatedNumberScheduled} {.status.numberAvailable}`

// tmmPodsJSONPath emits one "name<TAB>phase<TAB>PodScheduled message" line per
// TMM pod, so a timeout can say why a pod never started.
const tmmPodsJSONPath = `{range .items[*]}{.metadata.name}{"\t"}{.status.phase}{"\t"}` +
	`{.status.conditions[?(@.type=="PodScheduled")].message}{"\n"}{end}`

// tmmRollout is the f5-tmm DaemonSet's pod counts.
type tmmRollout struct {
	Desired   int
	Updated   int
	Available int
}

// parseTMMRollout reads the tmmDaemonSetJSONPath output. Missing or malformed
// counts are 0 — unlike the shared-namespace gate, an unreadable TMM status must
// not pass as ready, since TMM is the one component a deploy exists to deliver.
func parseTMMRollout(raw string) tmmRollout {
	var n [3]int
	for i, f := range strings.Fields(raw) {
		if i == len(n) {
			break
		}
		if v, err := strconv.Atoi(f); err == nil {
			n[i] = v
		}
	}
	return tmmRollout{Desired: n[0], Updated: n[1], Available: n[2]}
}

// tmmNotReady returns why TMM is not yet serving on every worker, or "" when it
// is. workers is cluster.tmm_nodes — one TMM per app=f5-tmm node.
func tmmNotReady(st tmmRollout, workers int) string {
	if workers < 1 {
		workers = 1
	}
	switch {
	case st.Desired < workers:
		return fmt.Sprintf("f5-tmm scheduled on %d of %d app=f5-tmm worker(s)", st.Desired, workers)
	case st.Updated < st.Desired:
		return fmt.Sprintf("%d of %d TMM pod(s) updated", st.Updated, st.Desired)
	case st.Available < st.Desired:
		return fmt.Sprintf("%d of %d TMM pod(s) available", st.Available, st.Desired)
	}
	return ""
}

// describeTMMPods renders the tmmPodsJSONPath output as
// "f5-tmm-abc Pending: 0/2 nodes are available: 1 Insufficient memory, ...".
func describeTMMPods(raw string) string {
	var parts []string
	for _, line := range strings.Split(raw, "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) < 2 || strings.TrimSpace(f[0]) == "" {
			continue
		}
		s := strings.TrimSpace(f[0]) + " " + strings.TrimSpace(f[1])
		if len(f) == 3 && strings.TrimSpace(f[2]) != "" {
			s += ": " + strings.TrimSpace(f[2])
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

// WaitTMMReady blocks until the f5-tmm DaemonSet runs an updated, available TMM
// on each of the workers.
//
// TMM lives in the default namespace, outside WaitWorkloadsAvailable's
// shared-namespace gate, and every step that touches it during deploy cne only
// warns. A TMM pod stuck Pending on Insufficient memory (a 16 GB Docker Desktop
// VM without deploy shrink) therefore still ended in DONE, and the failure only
// surfaced later as unrelated-looking scenario errors.
func WaitTMMReady(ctx context.Context, r *Runner, workers int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var reason string

	for {
		raw, err := r.KubectlCapture(ctx, "-n", "default", "get", "daemonset", "f5-tmm",
			"-o", "jsonpath="+tmmDaemonSetJSONPath)
		if err != nil {
			reason = fmt.Sprintf("f5-tmm DaemonSet not readable: %v", err)
		} else if reason = tmmNotReady(parseTMMRollout(raw), workers); reason == "" {
			return nil
		}

		if time.Now().After(deadline) {
			msg := fmt.Sprintf("TMM not ready after %s: %s", timeout, reason)
			if pods, perr := r.KubectlCapture(ctx, "-n", "default", "get", "pods",
				"-l", "app=f5-tmm", "-o", "jsonpath="+tmmPodsJSONPath); perr == nil {
				if d := describeTMMPods(pods); d != "" {
					msg += " — pods: " + d
				}
			}
			return fmt.Errorf("%s", msg)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}
