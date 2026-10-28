# bgp-peer-frr

BGP peering: TMM OcNOS ⇄ external FRR (ToR)

- Rating: **green**

## What it does

Establishes BGP between every TMM's OcNOS (AS 65000) and the external FRR container (bnk-edge-frr-<cluster>, AS 65001) on the shared bnk-edge L2. The deploy path already configures each TMM (anycast-bgp mode) to peer the external FRR; this scenario (re)triggers BGP redistribution on every TMM so Gateway VIPs and kernel routes are advertised, then verifies the external FRR sees each TMM Established. Replaces the former in-cluster scn-frr pod — the external FRR is a real ToR/BGP peer, and the data-plane scenarios curl from inside its netns.

## Manifests

None — this scenario acts imperatively (e.g. exec into TMM or the BGP peer);
the steps are in the description above.
