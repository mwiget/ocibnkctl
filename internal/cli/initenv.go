package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/mwiget/ocibnkctl/internal/poc"
)

// Env overrides for `init`. Motivation: argv+env runners (the BNK Forge
// container-runner module, CI) scaffold a PoC non-interactively and cannot
// hand-edit poc.yaml afterwards — without these, every PoC would deploy the
// default shape regardless of what the operator asked for. `init` stays
// declarative: these only seed poc.yaml, which remains the source of truth.
//
// Unset env var → the existing default/auto-detected value is kept.
const (
	envCustomer    = "OCIBNKCTL_CUSTOMER"
	envProvider    = "OCIBNKCTL_PROVIDER"
	envTMMNodes    = "OCIBNKCTL_TMM_NODES"
	envEdgeOctet   = "OCIBNKCTL_EDGE_OCTET"
	envHostProfile = "OCIBNKCTL_HOST_PROFILE"
	envTEEMSRelay  = "OCIBNKCTL_TEEMS_RELAY"
)

// EnvOverrideNames lists every env var applyEnvOverrides reads, for docs/help.
var EnvOverrideNames = []string{
	envCustomer, envProvider, envTMMNodes, envEdgeOctet, envHostProfile, envTEEMSRelay,
}

// applyEnvOverrides seeds poc.yaml fields from OCIBNKCTL_* env vars.
//
// Invalid values are a hard error rather than a silent skip: a typo in a
// runner's env (TMM_NODES=tow) must not quietly deploy the default shape —
// the operator would only discover it after a 20-minute pipeline.
//
// Call AFTER the flag/auto-detect defaults are applied so an explicit env
// value wins over auto-detection (e.g. host_profile=standard on a tight host).
// The --customer flag, being explicit, wins over OCIBNKCTL_CUSTOMER.
func applyEnvOverrides(p *poc.PoC, out io.Writer, customerFlagSet bool) error {
	if v, ok := lookupNonEmpty(envCustomer); ok && !customerFlagSet {
		p.Metadata.Customer = v
		fmt.Fprintf(out, "env: metadata.customer=%s\n", v)
	}

	if v, ok := lookupNonEmpty(envProvider); ok {
		provider := strings.ToLower(v)
		if provider != "docker" && provider != "podman" {
			return fmt.Errorf("%s=%q: must be docker or podman", envProvider, v)
		}
		p.Cluster.Provider = provider
		fmt.Fprintf(out, "env: cluster.provider=%s\n", provider)
	}

	if v, ok := lookupNonEmpty(envTMMNodes); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fmt.Errorf("%s=%q: must be a positive integer", envTMMNodes, v)
		}
		p.Cluster.TMMNodes = n
		fmt.Fprintf(out, "env: cluster.tmm_nodes=%d\n", n)
	}

	if v, ok := lookupNonEmpty(envEdgeOctet); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 254 {
			return fmt.Errorf("%s=%q: must be an integer 1-254 (3rd octet of 192.168.<n>.0/24)", envEdgeOctet, v)
		}
		p.Cluster.EdgeOctet = n
		fmt.Fprintf(out, "env: cluster.edge_octet=%d\n", n)
	}

	if v, ok := lookupNonEmpty(envHostProfile); ok {
		profile := strings.ToLower(v)
		if profile != poc.HostProfileStandard && profile != poc.HostProfileSmall {
			return fmt.Errorf("%s=%q: must be %q or %q", envHostProfile, v, poc.HostProfileStandard, poc.HostProfileSmall)
		}
		p.BNK.HostProfile = profile
		fmt.Fprintf(out, "env: bnk.host_profile=%s (overrides host auto-detect)\n", profile)
	}

	if v, ok := lookupNonEmpty(envTEEMSRelay); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("%s=%q: must be a boolean (true/false)", envTEEMSRelay, v)
		}
		p.Cluster.TEEMSRelay = b
		fmt.Fprintf(out, "env: cluster.teems_relay=%t\n", b)
	}

	return nil
}

func lookupNonEmpty(key string) (string, bool) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return "", false
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return "", false
	}
	return v, true
}
