package cluster

import (
	"context"
	"fmt"
	"net"
	"strings"
)

// TEEMS egress relay — a host-side workaround for environments where FORWARDED
// pod egress is lossy while host-ORIGINATED egress is fine (observed on a
// Hetzner box: ~40% of NAT'd/forwarded TCP connections get no SYN-ACK, host
// curls + speedtest are 100%). The only thing that breaks under that loss is
// the CWC's license registration: it POSTs to F5's TEEMS/CPCL backend over a
// multi-RTT TLS connection that almost never completes.
//
// The CWC builds its own HTTP client and ignores HTTPS_PROXY, so an env-proxy
// can't catch it — it dials the TEEMS IP directly. So we intercept that dial
// transparently: a host-netns `socat` relay RE-ORIGINATES the connection from
// the host stack (reliable), and a DNAT rule redirects the cluster's forwarded
// traffic to TEEMS:443 onto the relay. TLS/SNI/cert pass through the raw TCP
// relay intact (it never terminates TLS), so the CWC still validates the real
// F5 certificate. Opt in via poc.yaml `cluster.teems_relay: true` on hosts that
// need it; healthy hosts leave it off and egress unchanged.
//
// All of this is done through docker (a privileged host-netns container runs
// iptables), so the tool needs no host `sudo` — same trust model as the rest of
// the runtime. Rules are tagged with a per-cluster comment so destroy can find
// and remove exactly its own.

// teemsHosts are F5's licensing endpoints the CWC POSTs to (GA + engineering /
// test variants). BNK 2.3.0 demo mode hits the -tst pair; all currently resolve
// to one GCP IP, but we relay every distinct IP they resolve to.
var teemsHosts = []string{
	"product.apis.f5networks.net",
	"product-s.apis.f5networks.net",
	"product-tst.apis.f5networks.net",
	"product-s-tst.apis.f5networks.net",
}

const (
	// relayImage carries socat + iptables (host-netns container).
	relayImage = "nicolaka/netshoot"
	// teemsRelayPortBase: relay i listens on host port base+i.
	teemsRelayPortBase = 9443
)

func teemsRelayName(cluster string, i int) string {
	return fmt.Sprintf("teems-relay-%s-%d", cluster, i)
}
func teemsRuleComment(cluster string) string { return "ocibnkctl-teems-" + cluster }

// EnsureTEEMSRelay stands up one host-netns socat relay per resolved TEEMS IP
// and DNATs the cluster's forwarded traffic to that IP:443 onto the relay, so
// the CWC's licensing POST is re-originated from the host stack. Idempotent.
func (d *DockerCLI) EnsureTEEMSRelay(ctx context.Context, cluster string) error {
	ips := resolveTEEMSIPs()
	if len(ips) == 0 {
		fmt.Fprintln(d.Out, "      teems-relay: no TEEMS IPs resolved (host DNS) — skipping")
		return nil
	}
	hostIP, err := hostPrimaryIP()
	if err != nil {
		return fmt.Errorf("teems-relay: detect host IP: %w", err)
	}
	comment := teemsRuleComment(cluster)
	for i, ip := range ips {
		name := teemsRelayName(cluster, i)
		port := teemsRelayPortBase + i
		if err := d.removeContainer(ctx, name); err != nil {
			return err
		}
		fmt.Fprintf(d.Out, "      teems-relay: %s re-originates %s:443; DNAT %s:443→%s:%d\n",
			name, ip, ip, hostIP, port)
		if err := d.run(ctx, "run", "-d", "--name", name, "--network", "host",
			"--restart=unless-stopped", relayImage,
			"socat", fmt.Sprintf("TCP-LISTEN:%d,fork,reuseaddr", port),
			fmt.Sprintf("TCP:%s:443", ip)); err != nil {
			return fmt.Errorf("teems-relay: run %s: %w", name, err)
		}
		// DNAT every non-host source (i.e. the forwarded pod traffic; the relay's
		// own host-sourced connection to TEEMS is excluded so it never loops).
		rule := fmt.Sprintf("! -s %s -d %s -p tcp --dport 443 -m comment --comment %s -j DNAT --to-destination %s:%d",
			hostIP, ip, comment, hostIP, port)
		if err := d.iptablesNAT(ctx, fmt.Sprintf(
			"iptables -t nat -C PREROUTING %s 2>/dev/null || iptables -t nat -A PREROUTING %s", rule, rule)); err != nil {
			return fmt.Errorf("teems-relay: DNAT for %s: %w", ip, err)
		}
	}
	return nil
}

// RemoveTEEMSRelay tears down this cluster's relays + DNAT rules. Best-effort:
// relays removed by name prefix, DNAT rules by their per-cluster comment (so it
// works even if the TEEMS IPs have since changed).
func (d *DockerCLI) RemoveTEEMSRelay(ctx context.Context, cluster string) error {
	comment := teemsRuleComment(cluster)
	// Delete every PREROUTING nat rule carrying our comment (turn each `-A` line
	// from `iptables -S` into a `-D`).
	_ = d.iptablesNAT(ctx, fmt.Sprintf(
		"iptables -t nat -S PREROUTING | grep -- '--comment %s' | sed 's/^-A /-D /' | "+
			"while read -r r; do iptables -t nat $r; done", comment))
	// Remove the relay containers (teems-relay-<cluster>-*).
	names, _ := d.psNames(ctx, "teems-relay-"+cluster+"-")
	for _, n := range names {
		if err := d.removeContainer(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// iptablesNAT runs an iptables script in a privileged host-netns container, so
// it edits the HOST nat table without the tool needing host sudo.
func (d *DockerCLI) iptablesNAT(ctx context.Context, script string) error {
	return d.run(ctx, "run", "--rm", "--network", "host", "--cap-add", "NET_ADMIN",
		relayImage, "sh", "-c", script)
}

// psNames returns container names starting with prefix.
func (d *DockerCLI) psNames(ctx context.Context, prefix string) ([]string, error) {
	out, err := d.capture(ctx, "ps", "-a", "--filter", "name=^"+prefix, "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			names = append(names, l)
		}
	}
	return names, nil
}

// resolveTEEMSIPs resolves the F5 licensing endpoints to their distinct IPv4
// addresses using the HOST resolver (host-originated DNS is reliable even when
// forwarded pod egress is not).
func resolveTEEMSIPs() []string {
	seen := map[string]bool{}
	var ips []string
	for _, h := range teemsHosts {
		addrs, err := net.LookupHost(h)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ip := net.ParseIP(a); ip != nil && ip.To4() != nil && !seen[a] {
				seen[a] = true
				ips = append(ips, a)
			}
		}
	}
	return ips
}

// hostPrimaryIP returns the host's primary source IPv4 (the address the kernel
// would use to reach the internet) — the DNAT target + the relay's identity. A
// UDP "dial" just resolves the route; no packet is sent.
func hostPrimaryIP() (string, error) {
	c, err := net.Dial("udp", "1.1.1.1:80")
	if err != nil {
		return "", err
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String(), nil
}
