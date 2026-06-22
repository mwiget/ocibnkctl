package cluster

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

// The "realistic" edge fabric (ported from tmmlitectl to keep the two tools'
// data-plane shape identical). Instead of a per-node, node-local Linux bridge
// (br-bnk-bgp auto-created in isolation by the Multus bridge plugin), the
// worker nodes are dual-homed onto a SHARED docker network `bnk-edge`, and the
// external BGP peer (FRR) and the external upstream (origin) run as their OWN
// containers on that same network. The control node is deliberately left OFF
// bnk-edge.
//
// To put each TMM's net1 onto that shared L2, EnsureEdge enslaves each worker's
// bnk-edge uplink interface into br-bnk-bgp (the very bridge the NAD
// references) and moves its IP onto the bridge. The Multus bridge plugin then
// reuses that bridge, so every TMM net1 veth, the worker's own edge IP, the
// other worker's TMM, FRR, and origin all share one broadcast domain — the
// bnk-edge docker bridge. This reuses the existing NAD + OcNOS + mapres
// machinery; only the bridge's uplink changes (isolated → real shared docker
// net), which is what lets a single external FRR be the BGP peer + curl vantage
// for every scenario (no per-scenario scn-frr pod).
//
// The 3rd octet (`o`) of every bnk-edge address is parameterized per cluster
// (poc.yaml cluster.edge_octet, default 99) so multiple clusters can run in
// parallel without docker subnet collisions. The octet-dependent values below
// are FUNCTIONS of `o`; the octet-independent ones (AS numbers, bridge name,
// worker IP base, off-subnet egress dests) stay consts.

// EdgeSubnet / EdgeGateway are the shared external L2 segment. The /24 matches
// the bnk-bgp NAD subnet so TMM net1 (whereabouts IPAM) and the external
// containers live in one space.
func EdgeSubnet(o int) string  { return fmt.Sprintf("192.168.%d.0/24", o) }
func EdgeGateway(o int) string { return fmt.Sprintf("192.168.%d.1", o) }

// edgeAutoRange keeps docker's own auto-assignment in a tiny block clear of
// every pinned/pooled address below (all containers here are pinned, so docker
// never actually auto-assigns — this is just belt-and-suspenders).
func edgeAutoRange(o int) string { return fmt.Sprintf("192.168.%d.252/30", o) }

// External BGP peer (FRR) and upstream (origin) addresses.
func EdgeFRRIP(o int) string    { return fmt.Sprintf("192.168.%d.41", o) }
func EdgeOriginIP(o int) string { return fmt.Sprintf("192.168.%d.50", o) }

const (
	// edgeBridge is the in-worker bridge the NAD references; we pre-create
	// it and enslave the bnk-edge uplink so net1 lands on the shared L2.
	edgeBridge = "br-bnk-bgp"

	// edgeWorkerIPBase: worker-i gets .60+i (so the worker uplink range
	// .60-.159 stays clear of FRR .41 / origin .50 below it and the TMM net1
	// whereabouts pool .160-.250 above it — supporting dozens of workers on the
	// /24 without collisions; widen the subnet for more).
	edgeWorkerIPBase = 60

	EdgeFRRAS      = 65001
	EdgeTMMAS      = 65000
	EdgeOriginDest = "198.51.106.50" // primary /32 alias off-subnet → reachable only via TMM
	// EdgeOriginMark is the body the origin returns on every dest (proves a curl
	// reached the upstream through TMM).
	EdgeOriginMark = "ocibnkctl-edge-origin-OK"

	// Image pins (FRR matches the bgp-peer-frr scenario; netshoot carries
	// python3 + ip for the origin).
	edgeFRRImage    = "quay.io/frrouting/frr:9.1.0"
	edgeOriginImage = "nicolaka/netshoot"
)

// EdgeOriginDests are the external origin's destination /32s, each off the
// bnk-edge subnet so it's reachable only via TMM (the "TMM provably in path"
// trick). EdgeOriginDest is the primary; add more here as egress-style
// scenarios are added.
var EdgeOriginDests = []string{
	EdgeOriginDest, // 198.51.106.50
}

// EdgeNetworkName / edgeFRRName / edgeOriginName are per-cluster so
// multiple PoCs can run side by side.
func EdgeNetworkName(cluster string) string { return "bnk-edge-" + cluster }
func edgeFRRName(cluster string) string     { return "bnk-edge-frr-" + cluster }
func edgeOriginName(cluster string) string  { return "bnk-edge-origin-" + cluster }

// EnsureEdge builds the realistic edge fabric for `cluster`: the shared
// bnk-edge docker network, the worker dual-homing + br-bnk-bgp enslave,
// and the external FRR + origin containers. Idempotent end to end —
// every step checks-before-acting, so re-running `cluster up` is safe.
// The workers slice is the worker (agent) node container names, in node
// order, so worker[i] gets edge IP .60+i. The control node is NOT passed
// in and never joins bnk-edge.
func (d *DockerCLI) EnsureEdge(ctx context.Context, cluster string, octet int, workers []string) error {
	net := EdgeNetworkName(cluster)
	if err := d.createEdgeNetwork(ctx, net, octet); err != nil {
		return err
	}
	// The cluster-net gateway (k3s-<cluster>) must remain the node's default
	// route. Attaching bnk-edge makes docker hijack the default route to the
	// edge gateway, and the enslave flush then drops it — so we restore it
	// explicitly after each enslave.
	clusterGW, err := d.networkGateway(ctx, "k3s-"+cluster)
	if err != nil {
		return fmt.Errorf("cluster-net gateway: %w", err)
	}
	for i, w := range workers {
		ip := fmt.Sprintf("192.168.%d.%d", octet, edgeWorkerIPBase+i)
		if err := d.connectNetworkIP(ctx, net, w, ip); err != nil {
			return err
		}
		if err := d.enslaveEdgeUplink(ctx, w, clusterGW, octet); err != nil {
			return fmt.Errorf("enslave bnk-edge uplink on %s: %w", w, err)
		}
	}
	if err := d.ensureFRR(ctx, cluster, net, octet); err != nil {
		return err
	}
	if err := d.ensureOrigin(ctx, cluster, net, octet); err != nil {
		return err
	}
	return nil
}

// RemoveEdge tears down the external FRR + origin containers and the
// bnk-edge network. Idempotent. The worker nodes are removed with the
// cluster (DeleteCluster), so this only needs to clear the extras.
func (d *DockerCLI) RemoveEdge(ctx context.Context, cluster string) error {
	for _, name := range []string{edgeFRRName(cluster), edgeOriginName(cluster)} {
		if err := d.removeContainer(ctx, name); err != nil {
			fmt.Fprintf(d.Out, "  WARN: %v\n", err)
		}
	}
	return d.RemoveNetwork(ctx, EdgeNetworkName(cluster))
}

func (d *DockerCLI) createEdgeNetwork(ctx context.Context, net string, octet int) error {
	exists, err := d.NetworkExists(ctx, net)
	if err != nil {
		return err
	}
	if exists {
		fmt.Fprintf(d.Out, "  edge network %s already exists — leaving in place\n", net)
		return nil
	}
	c := d.cmd(ctx, "network", "create", "-d", "bridge",
		"--subnet", EdgeSubnet(octet), "--gateway", EdgeGateway(octet),
		"--ip-range", edgeAutoRange(octet), net)
	c.Stdout = d.Out
	c.Stderr = d.Out
	if err := c.Run(); err != nil {
		return fmt.Errorf("create edge network %s: %w", net, err)
	}
	return nil
}

// connectNetworkIP attaches `container` to `network` with a pinned IP.
// Idempotent (skips if already attached).
func (d *DockerCLI) connectNetworkIP(ctx context.Context, network, container, ip string) error {
	if attached, err := d.IsAttached(ctx, network, container); err != nil {
		return err
	} else if attached {
		fmt.Fprintf(d.Out, "  %s already on %s\n", container, network)
		return nil
	}
	c := d.cmd(ctx, "network", "connect", "--ip", ip, network, container)
	c.Stdout = d.Out
	c.Stderr = d.Out
	if err := c.Run(); err != nil {
		return fmt.Errorf("connect %s → %s (%s): %w", container, network, ip, err)
	}
	return nil
}

// enslaveEdgeUplink moves the worker's bnk-edge uplink (the interface
// holding an EdgeSubnet IP) into br-bnk-bgp and relocates its IP onto the
// bridge, so the node keeps an L3 handle on the segment while TMM net1 veths
// added to the same bridge become L2-adjacent to FRR/origin/the other worker.
// The awk excludes br-bnk-bgp itself, so a second run is a no-op (the uplink
// no longer carries an edge IP). Done before any TMM pod attaches net1.
func (d *DockerCLI) enslaveEdgeUplink(ctx context.Context, worker, clusterGW string, octet int) error {
	edgeMatch := fmt.Sprintf(`$4 ~ /^192\.168\.%d\./`, octet)
	script := `set -e
ip link add name ` + edgeBridge + ` type bridge 2>/dev/null || true
ip link set ` + edgeBridge + ` up
IF=$(ip -o -4 addr show | awk '` + edgeMatch + ` && $2 != "` + edgeBridge + `" {print $2; exit}')
if [ -n "$IF" ]; then
  CIDR=$(ip -o -4 addr show dev "$IF" | awk '{print $4; exit}')
  ip addr flush dev "$IF"
  ip link set "$IF" master ` + edgeBridge + `
  ip link set "$IF" up
  [ -n "$CIDR" ] && ip addr add "$CIDR" dev ` + edgeBridge + `
fi
# Keep the cluster net as the node's default route (attaching bnk-edge +
# the flush above otherwise leaves the node with no default → kubelet/CNI
# can't reach the API service IP).
ip route replace default via ` + clusterGW + ``
	return d.exec(ctx, worker, "sh", "-c", script)
}

// ensureFRR runs the external FRR container on the edge net and brings up
// bgpd peering every TMM (a listen-range over the whole /24, so all workers'
// dynamic net1 IPs are accepted). AS 65001, router-id .41.
func (d *DockerCLI) ensureFRR(ctx context.Context, cluster, net string, octet int) error {
	name := edgeFRRName(cluster)
	if exists, err := d.containerExists(ctx, name); err != nil {
		return err
	} else if exists {
		fmt.Fprintf(d.Out, "  external FRR %s already present — leaving in place\n", name)
		return nil
	}
	conf := fmt.Sprintf(`frr defaults traditional
hostname frr
no ipv6 forwarding
!
router bgp %d
 bgp router-id %s
 no bgp ebgp-requires-policy
 no bgp default ipv4-unicast
 bgp log-neighbor-changes
 neighbor from-tmm peer-group
 neighbor from-tmm remote-as %d
 bgp listen range %s peer-group from-tmm
 !
 address-family ipv4 unicast
  neighbor from-tmm activate
  neighbor from-tmm soft-reconfiguration inbound
 exit-address-family
!
`, EdgeFRRAS, EdgeFRRIP(octet), EdgeTMMAS, EdgeSubnet(octet))
	// Lay down frr.conf + enable bgpd BEFORE the normal FRR startup, then
	// exec it — so bgpd comes up with the config already loaded. (Restarting
	// FRR after the fact via frrinit.sh kills the container's PID-1
	// supervisor → exit 137.)
	start := "cat > /etc/frr/frr.conf <<'CONF'\n" + conf + "CONF\n" +
		"sed -i 's/^bgpd=no/bgpd=yes/' /etc/frr/daemons\n" +
		// vtysh.conf silences the harmless 'Can't open vtysh.conf' warning that
		// otherwise prefixes every `vtysh -c` output (scenarios parse that).
		"touch /etc/frr/vtysh.conf\n" +
		"exec /usr/lib/frr/docker-start"
	if err := d.run(ctx, "run", "-d", "--name", name, "--hostname", "frr",
		"--network", net, "--ip", EdgeFRRIP(octet), "--privileged",
		"--restart=unless-stopped", "--entrypoint", "sh", edgeFRRImage,
		"-c", start); err != nil {
		return fmt.Errorf("run external FRR: %w", err)
	}
	return nil
}

// ensureOrigin runs the external upstream container: a plain web server
// on .50 plus an off-subnet /32 alias (EdgeOriginDest) reachable only via
// TMM, the same "TMM provably in path" trick the egress scenarios use.
func (d *DockerCLI) ensureOrigin(ctx context.Context, cluster, net string, octet int) error {
	name := edgeOriginName(cluster)
	if exists, err := d.containerExists(ctx, name); err != nil {
		return err
	} else if exists {
		fmt.Fprintf(d.Out, "  external origin %s already present — leaving in place\n", name)
		return nil
	}
	// One upstream serving every destination: each dest /32 is an alias off the
	// bnk-edge subnet, reachable only via TMM, so the origin always sees TMM's
	// SNAT identity (not the client). Reachability is proven by the shared
	// marker; per-tenant SNAT separation from the origin's request log.
	var b strings.Builder
	for _, a := range EdgeOriginDests {
		fmt.Fprintf(&b, "ip addr add %s/32 dev eth0 || true; ", a)
	}
	cmd := b.String() + fmt.Sprintf("echo '%s' > /index.html; cd / && exec python3 -m http.server 80", EdgeOriginMark)
	if err := d.run(ctx, "run", "-d", "--name", name, "--hostname", "origin",
		"--network", net, "--ip", EdgeOriginIP(octet), "--cap-add", "NET_ADMIN",
		"--restart=unless-stopped", edgeOriginImage, "sh", "-c", cmd); err != nil {
		return fmt.Errorf("run external origin: %w", err)
	}
	return nil
}

// --- small runtime helpers (docker/podman) ---

func (d *DockerCLI) run(ctx context.Context, args ...string) error {
	c := d.cmd(ctx, args...)
	c.Stdout = d.Out
	c.Stderr = d.Out
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", d.Runtime, strings.Join(args, " "), err)
	}
	return nil
}

func (d *DockerCLI) exec(ctx context.Context, container string, argv ...string) error {
	return d.run(ctx, append([]string{"exec", container}, argv...)...)
}

// networkGateway returns the IPv4 gateway of a docker network.
func (d *DockerCLI) networkGateway(ctx context.Context, name string) (string, error) {
	c := d.cmd(ctx, "network", "inspect", name, "--format",
		"{{range .IPAM.Config}}{{.Gateway}}{{end}}")
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("network inspect %s: %w (%s)", name, err, strings.TrimSpace(stderr.String()))
	}
	gw := strings.TrimSpace(stdout.String())
	if gw == "" {
		return "", fmt.Errorf("network %s has no IPv4 gateway", name)
	}
	return gw, nil
}

func (d *DockerCLI) containerExists(ctx context.Context, name string) (bool, error) {
	c := d.cmd(ctx, "ps", "-a", "--filter", "name=^"+name+"$", "--format", "{{.Names}}")
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return false, fmt.Errorf("ps: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

func (d *DockerCLI) removeContainer(ctx context.Context, name string) error {
	exists, err := d.containerExists(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return d.run(ctx, "rm", "-f", name)
}
