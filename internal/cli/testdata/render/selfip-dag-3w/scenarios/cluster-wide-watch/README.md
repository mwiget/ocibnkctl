# cluster-wide-watch

Components needing cluster-wide access (how-to #2) — single controller reconciling a new namespace

- Rating: **green**

## What it does

Demonstrates BNK's "Whole Cluster"/cluster-wide-access pattern.
The F5 how-to itself is architectural prose with no YAML or
kubectl commands; it states two load-bearing claims:

  - "A single BIG-IP Next for Kubernetes Controller [...]
    watches multiple namespaces."
  - "All BIG-IP Next for Kubernetes pods run in the same
    namespace."

Our ocibnkctl deploy already runs FLO in that posture:
  - CNEInstance.spec.wholeCluster = true (watches every namespace)
  - f5-cne-controller is a single Deployment in the
    default namespace
  - f5-cne-core hosts the shared CWC + DSSM + RabbitMQ +
    cert-manager + Fluentd
  - the same controller reconciles Gateways/HTTPRoutes in
    every namespace (http-routing-e2e, external-resource-pool,
    ai-token-counting, etc. already prove this incidentally)

This scenario makes the claim explicit: applies a brand-new
namespace + Gateway + HTTPRoute + nginx backend, then asserts
that the existing single controller picks them up
(Gateway Programmed=True, HTTPRoute Accepted=True) without
any per-namespace controller install or configuration.

Cleanup deletes the scn-cwatch namespace.

## Manifests

- `01-namespace.yaml`
- `03-backend.yaml`
- `04-gateway.yaml`
- `05-httproute.yaml`

Not every file is applied unconditionally by `ocibnkctl scenario run`; check the
description above for ordering and waits between steps.
