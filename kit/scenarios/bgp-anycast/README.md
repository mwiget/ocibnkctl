# `bgp-anycast` — BGP anycast all-active data plane (how-to #3) — every TMM advertises over the shared edge FRR

Rating: 🟢 &nbsp;·&nbsp; Depends on: [`bgp-peer-frr`](../bgp-peer-frr)

Verification-only scenario for the anycast-bgp data-plane mode (the default).
It applies nothing — the deploy path installed the bnk-bgp NAD, mapres=FALSE,
and the cluster-wide OcNOS template, and the cluster backend runs the external
bnk-edge FRR (the ToR) every TMM peers. This scenario asserts the anycast model
against that one external FRR:

  - the external FRR is reachable (else the cluster isn't in the edge-fabric
    anycast shape — skips cleanly)
  - every Running TMM has its own BGP session Established to the external FRR
    (count of Established == TMM-pod count)
  - the external FRR learned the TMM-advertised routes (BGP RIB non-empty)
  - the FRR has MULTIPLE next-hops for the TMM net1 subnet — real ECMP fan-out
    across workers, now that all TMMs share one L2 with one FRR (the edge-fabric
    improvement over per-node-isolated bridges)

## How to run

```bash
ocibnkctl scenario run bgp-anycast --poc <pocdir>
```

Or by hand, on any cluster `kubectl` points at (plain `sh` + `kubectl`;
contract: [kit/lib/common.sh](../../lib/common.sh), edge client:
[kit/lib/edge.sh](../../lib/edge.sh)):

```sh
sh kit/scenarios/bgp-anycast/apply.sh
EDGE_DRIVER=none sh kit/scenarios/bgp-anycast/verify.sh    # control plane only; data-plane checks skipped
sh kit/scenarios/bgp-anycast/cleanup.sh
```

Variables and their defaults are in [`scenario.env`](scenario.env); set any
of them in the environment.

The ECMP check needs `EDGE_PREFIX` (the edge L2, e.g. `192.168.99.`) and
only applies with two or more TMM workers.
