package deploy

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// BGP-anycast deploy-path plumbing (tmm_dataplane_mode: anycast-bgp).
//
// The persistent deploy path builds on the same L2 shape the
// bgp-peer-frr scenario proved out: a bridge-CNI Multus NAD on net1,
// mapres FALSE so net1 keeps its kernel IP, and a ZeBOS BGP session per
// TMM pod. Where the scenario applies all of this as a runtime patch on
// top of a standby deploy, here it's baked into the deploy pipeline so
// the cluster comes up in anycast-bgp mode.
//
// FRR peer placement: bridge CNI is per-node, so a single upstream peer
// can only share L2 with TMM pods on its own node. The deploy path runs
// FRR as a DaemonSet on the app=f5-tmm nodes — one peer co-located with
// each TMM pod — pinned to a STATIC net1 IP (BGPPeerIP) so the
// cluster-wide ZeBOS ConfigMap has a single deterministic neighbor for
// every TMM pod. On a single host the per-node bridges are isolated, so
// each FRR sees only its node-local TMM (one next-hop), not all N — the
// honest limitation documented in README "Network topology".
const (
	// BGPPeerIP is the fixed net1 address every FRR peer takes (static
	// IPAM), so the cluster-wide ZeBOS template can name one neighbor for
	// all TMM pods. Below the bnk-bgp host-local range (.20+) so it never
	// collides with a TMM pod's assigned address.
	BGPPeerIP = "192.168.99.2"
	// BGPFRRNADName is the NAD FRR attaches (static IPAM on the same
	// br-bnk-bgp bridge as TMM's bnk-bgp NAD). A separate NAD keeps FRR's
	// fixed .2 out of TMM's host-local pool.
	BGPFRRNADName = "bnk-bgp-frr"
	// FRRImage is the FRRouting container the BGP peer runs (matches the
	// bgp-peer-frr scenario's pin).
	FRRImage = "quay.io/frrouting/frr:9.1.0"
)

// RenderFRRStaticNAD renders the bridge-CNI NAD FRR attaches: the same
// br-bnk-bgp bridge as TMM's bnk-bgp NAD, pinned to BGPPeerIP so the
// ZeBOS neighbor is deterministic across nodes.
//
// It uses host-local IPAM with a single-address range
// (rangeStart == rangeEnd == BGPPeerIP) rather than the `static` IPAM
// type: only the `bridge` plugin (and the k3s-bundled host-local) is
// seeded onto the nodes, so a `static`-IPAM NAD fails sandbox creation
// with "failed to find plugin static". A one-address host-local range
// hands every FRR pod the same .2 with no extra plugin — and host-local
// is per-node, so each node's lone FRR pod gets it independently.
func RenderFRRStaticNAD(namespace string) string {
	return renderBridgeNAD(BGPFRRNADName, namespace, BGPBridge, BGPSubnet, BGPPeerIP, BGPPeerIP)
}

// RenderFRRPeer renders the FRR BGP-peer stack for the anycast-bgp
// deploy path: a frr.conf ConfigMap and a DaemonSet pinned (via
// nodeSelector) to the app=f5-tmm nodes so one FRR sits beside each TMM
// pod on the shared per-node br-bnk-bgp bridge. FRR accepts any TMM peer
// on the bridge via `bgp listen range` + peer-group, so no per-pod
// config is needed regardless of TMM count.
func RenderFRRPeer(namespace, tmmLabelKey, tmmLabelVal string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: bnk-frr-config
  namespace: %[1]s
data:
  daemons: |
    bgpd=yes
    ospfd=no
    ospf6d=no
    ripd=no
    ripngd=no
    isisd=no
    pimd=no
    ldpd=no
    nhrpd=no
    eigrpd=no
    babeld=no
    sharpd=no
    pbrd=no
    bfdd=no
    fabricd=no
    vrrpd=no
    pathd=no
    vtysh_enable=yes
    zebra_options="  -A 127.0.0.1 -s 90000000"
    bgpd_options="  -A 127.0.0.1"
  vtysh.conf: |
    service integrated-vtysh-config
  frr.conf: |
    frr defaults traditional
    hostname bnk-frr
    log stdout informational
    !
    ip forwarding
    !
    router bgp %[5]d
     bgp router-id %[4]s
     no bgp ebgp-requires-policy
     no bgp default ipv4-unicast
     bgp log-neighbor-changes
     ! Peer-group + remote-as MUST precede the listen-range that
     ! references them, or FRR silently drops the listen-range line.
     neighbor from-tmm peer-group
     neighbor from-tmm remote-as %[6]d
     ! Accept BGP from any TMM on the bnk-bgp bridge — TMM's net1 IP
     ! is assigned by host-local IPAM and varies, so listen-range is
     ! the robust shape and scales to N TMM pods with no per-pod config.
     bgp listen range %[7]s peer-group from-tmm
     !
     address-family ipv4 unicast
      neighbor from-tmm activate
      neighbor from-tmm soft-reconfiguration inbound
     exit-address-family
    !
    line vty
    !
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: bnk-frr
  namespace: %[1]s
  labels: {app: bnk-frr}
spec:
  selector:
    matchLabels: {app: bnk-frr}
  template:
    metadata:
      labels: {app: bnk-frr}
      annotations:
        # FRR pins to the static BGPPeerIP via the bnk-bgp-frr NAD so the
        # cluster-wide ZeBOS template names one neighbor for every TMM pod.
        k8s.v1.cni.cncf.io/networks: |
          [{"name":"%[2]s","interface":"net1"}]
    spec:
      # One FRR per TMM node (bridge CNI is per-node, so the peer must
      # share L2 with the TMM it serves).
      nodeSelector:
        %[3]s: %[8]s
      tolerations:
      - operator: Exists
      initContainers:
      - name: bootstrap
        image: %[9]s
        command:
        - sh
        - -c
        - |
          cp /tmp/frr-cm/* /etc/frr/ && \
          chown -R frr:frr /etc/frr && \
          chmod 640 /etc/frr/* && \
          ls -la /etc/frr/
        volumeMounts:
        - {name: frr-config, mountPath: /tmp/frr-cm}
        - {name: frr-etc,    mountPath: /etc/frr}
      containers:
      - name: frr
        image: %[9]s
        securityContext:
          capabilities:
            add: [NET_ADMIN, NET_RAW, SYS_ADMIN]
        volumeMounts:
        - {name: frr-etc, mountPath: /etc/frr}
        readinessProbe:
          exec:
            command: ["vtysh", "-c", "show bgp summary"]
          initialDelaySeconds: 5
          periodSeconds: 5
        resources:
          requests: {cpu: 20m, memory: 32Mi}
          limits:   {cpu: 200m, memory: 128Mi}
      volumes:
      - name: frr-config
        configMap: {name: bnk-frr-config}
      - name: frr-etc
        emptyDir: {}
`, namespace, BGPFRRNADName, tmmLabelKey, BGPPeerIP,
		BGPPeerAS, // %[5]d — FRR's own AS
		BGPTMMAS,  // %[6]d — remote-as (the TMM peers)
		BGPSubnet, tmmLabelVal, FRRImage)
}

// TriggerOcNOSRedistribute re-issues the redistribute statements on every
// Running TMM pod's OcNOS daemon. OcNOS XP-6.6.0 only injects redistributed
// routes into BGP when the statement is (re-)issued at runtime, so after the
// routing ConfigMap is applied + TMM rolled, this nudges every pod to actually
// advertise its connected net1 subnet + the Gateway VIP /32s to the external
// FRR. Best-effort per pod (returns the last error).
func TriggerOcNOSRedistribute(ctx context.Context, r *Runner) error {
	pods, err := RunningTMMPods(ctx, r)
	if err != nil {
		return err
	}
	var lastErr error
	for _, pod := range pods {
		if e := r.Kubectl(ctx, "-n", "default", "exec", pod,
			"-c", "f5-tmm-routing", "--",
			"imish",
			"-e", "configure terminal",
			"-e", fmt.Sprintf("router bgp %d", BGPTMMAS),
			"-e", "address-family ipv4 unicast",
			"-e", "redistribute kernel route-map RMALL",
			"-e", "redistribute connected route-map RMALL",
			"-e", "end"); e != nil {
			lastErr = e
		}
	}
	return lastErr
}

// RunningTMMPods returns the names of all Running TMM pods, sorted by
// creation time. The anycast-bgp path must act on every TMM pod (inject
// passwd.conf, verify a session) — not just the newest — because each
// pod runs its own ZeBOS/BGP session.
func RunningTMMPods(ctx context.Context, r *Runner) ([]string, error) {
	out, err := r.KubectlCapture(ctx, "-n", "default", "get", "pod",
		"-l", "app=f5-tmm",
		"--field-selector=status.phase=Running",
		"--sort-by=.metadata.creationTimestamp",
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`)
	if err != nil {
		return nil, fmt.Errorf("list Running f5-tmm pods: %w", err)
	}
	var pods []string
	for _, p := range strings.Split(strings.TrimSpace(out), "\n") {
		if p = strings.TrimSpace(p); p != "" {
			pods = append(pods, p)
		}
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("no Running f5-tmm pods")
	}
	return pods, nil
}

// InjectPasswdConfAll writes the one-line passwd.conf into every Running
// TMM pod's f5-tmm-routing container — the gate bfd_watcher needs to
// imish-load the ZeBOS config. The scenario only injects into the newest
// pod (it runs a single TMM); the anycast path runs N TMM pods, each with
// its own ZeBOS session, so all of them need it. Best-effort per pod with
// a bounded retry to ride out rolling-update churn.
func InjectPasswdConfAll(ctx context.Context, r *Runner) error {
	const attempts = 18
	var lastErr error
	for i := 0; i < attempts; i++ {
		pods, err := RunningTMMPods(ctx, r)
		if err != nil {
			lastErr = err
		} else {
			lastErr = nil
			for _, pod := range pods {
				if e := r.Kubectl(ctx, "-n", "default", "exec", pod,
					"-c", "f5-tmm-routing", "--",
					"sh", "-c", "echo 'enable password 0 zebos' > /config/zebos/rd0/passwd.conf"); e != nil {
					lastErr = e
				}
			}
			if lastErr == nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return lastErr
}
