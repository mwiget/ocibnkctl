package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/mwiget/ocibnkctl/internal/poc"
)

// envProfile is the `init --profile` flag for argv+env runners.
const envProfile = "OCIBNKCTL_PROFILE"

// A profile seeds poc.yaml for one use case. It is applied right after
// poc.New, before the host auto-detect and the OCIBNKCTL_* overrides, so a
// tight host still gets host_profile=small and an explicit env value still
// wins. poc.yaml stays the source of truth: everything a profile sets is a
// field the operator can edit afterwards.
type profile struct {
	summary string
	apply   func(*poc.PoC)
	// scenarios are what e2e runs after deploy cne for this profile, in order.
	scenarios []string
	next      string // printed after "Next:"
}

var profiles = map[string]profile{
	"llm-gateway": {
		summary: "BNK 2.4.1 AI/LLM gateway demo: console, virtual keys, metering, a mock LLM",
		apply: func(p *poc.PoC) {
			p.BNK.AIGateway = poc.AIGateway{Enabled: true, AIMetrics: true}
			// Clients reach the Gateway VIP from the edge over BGP: the
			// anycast-bgp data plane, which is already the default. Pinned so
			// the profile keeps working if the default ever changes.
			p.BNK.TMMDataplaneMode = poc.DataplaneAnycastBGP
		},
		scenarios: []string{"bgp-peer-frr", "llm-gateway"},
		next:      "ocibnkctl e2e --yolo --confirm-cluster %s --with-scenarios",
	},
}

func profileNames() []string {
	var n []string
	for k := range profiles {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// applyProfile seeds p from the named profile ("" = none). The flag wins over
// OCIBNKCTL_PROFILE.
func applyProfile(p *poc.PoC, name string, out io.Writer) (*profile, error) {
	if name == "" {
		name, _ = lookupNonEmpty(envProfile)
	}
	if name == "" {
		return nil, nil
	}
	pr, ok := profiles[name]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q: one of %s", name, strings.Join(profileNames(), ", "))
	}
	pr.apply(p)
	p.Scenarios = append([]string(nil), pr.scenarios...)
	fmt.Fprintf(out, "profile %s: %s\n", name, pr.summary)
	return &pr, nil
}
