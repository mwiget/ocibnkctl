package cluster

import (
	"context"
	"fmt"
	"net"
	"strings"
)

// CoreDNSUpstreams returns the DNS server IPs the cluster's in-cluster CoreDNS
// should forward external queries to, discovered from the k3s server node's
// /etc/resolv.conf.
//
// Why not just leave CoreDNS on its default `forward . /etc/resolv.conf`: on
// docker/k3s-in-docker the node's only nameserver is docker's embedded resolver
// (127.0.0.11), and CoreDNS forwarding through it is unreliable under load — it
// intermittently returns SERVFAIL ("server misbehaving") for external names,
// which stalls image pulls and, fatally, makes F5 CNE license device
// registration fail on its one-shot attempt. Pointing CoreDNS straight at the
// real host uplinks bypasses that proxy. Docker records those uplinks in the
// "# ExtServers: [a b]" comment it writes into the container resolv.conf; a host
// with real (non-loopback) nameservers exposes them directly instead.
//
// Returns the usable upstreams (routable IPv4, no loopback, no Tailscale
// MagicDNS — see usableNodeResolver), or empty when none can be discovered, in
// which case the caller should leave CoreDNS untouched.
func CoreDNSUpstreams(ctx context.Context, rt Runtime, clusterName string) ([]string, error) {
	server := "k3s-" + clusterName + "-server-0"
	raw, err := runtimeOut(ctx, rt, "exec", server, "cat", "/etc/resolv.conf")
	if err != nil {
		return nil, fmt.Errorf("read /etc/resolv.conf from %s: %w", server, err)
	}
	return parseResolvUpstreams(raw), nil
}

// parseResolvUpstreams extracts usable upstream resolver IPs from a resolv.conf.
// It prefers real `nameserver` lines; when those are only loopback (the docker
// embedded-DNS case) it falls back to the IPs in docker's
// "# ExtServers: [a b ...]" comment. All candidates are filtered through
// usableNodeResolver, preserving file order and de-duplicating.
func parseResolvUpstreams(resolv string) []string {
	var nameservers, extServers []string
	for _, line := range strings.Split(resolv, "\n") {
		trimmed := strings.TrimSpace(line)
		if fields := strings.Fields(trimmed); len(fields) >= 2 && fields[0] == "nameserver" {
			if usableNodeResolver(net.ParseIP(fields[1])) {
				nameservers = append(nameservers, fields[1])
			}
			continue
		}
		if strings.HasPrefix(trimmed, "# ExtServers:") {
			extServers = append(extServers, parseExtServers(trimmed)...)
		}
	}
	if len(nameservers) > 0 {
		return dedupe(nameservers)
	}
	return dedupe(extServers)
}

// parseExtServers pulls usable IPs out of a docker
// "# ExtServers: [185.12.64.1 185.12.64.2]" comment line.
func parseExtServers(line string) []string {
	open := strings.Index(line, "[")
	close := strings.LastIndex(line, "]")
	if open < 0 || close < 0 || close <= open {
		return nil
	}
	var out []string
	for _, tok := range strings.Fields(line[open+1 : close]) {
		// entries can be bare IPs or host:port ("1.2.3.4:53") — take the IP.
		ip := tok
		if h, _, err := net.SplitHostPort(tok); err == nil {
			ip = h
		}
		if usableNodeResolver(net.ParseIP(ip)) {
			out = append(out, ip)
		}
	}
	return out
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	var out []string
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
