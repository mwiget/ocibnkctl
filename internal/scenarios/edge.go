package scenarios

import "fmt"

// The ocibnkctl edge fabric — the external FRR container `cluster up` builds
// on the shared bnk-edge network — is the scenarios' data-plane client under
// EDGE_DRIVER=ocibnkctl (kit/lib/edge.sh). shellEnv passes these to the
// scripts; the scripts do the rest.

// edgeToolsImage is the throwaway image joined to the FRR netns for client
// commands (curl, socat, grpcurl, ...).
const edgeToolsImage = "nicolaka/netshoot"

// EdgePrefix is "192.168.<octet>." — the bnk-edge address prefix, the next
// hop every TMM route to a VIP should point into.
func EdgePrefix(ctx *Context) string { return fmt.Sprintf("192.168.%d.", ctx.PoC.Cluster.EdgeNet()) }

// EdgeFRRName is this PoC's external FRR container.
func EdgeFRRName(ctx *Context) string { return "bnk-edge-frr-" + ctx.PoC.Cluster.Name }

// runtimeBin is the container runtime (docker/podman) from poc.yaml.
func runtimeBin(ctx *Context) string {
	if p := ctx.PoC.Cluster.Provider; p != "" {
		return p
	}
	return "docker"
}
