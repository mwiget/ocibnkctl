# core-file-collection

Core file collection (how-to #4) — CNEInstance.spec.coreCollection.enabled + CoreMond

- Rating: **green**

## What it does

Demonstrates BNK's core-dump collection feature. Setup is a
single CNEInstance.spec.coreCollection.enabled=true field flip;
FLO reconciles a CoreMond CR (auto-created, no operator-
authored manifest needed), the coremond Deployment, and
hostPath mounts on TMM pods for /var/crash.

What the scenario verifies (control-plane only):
  - CoreMond CR exists after the flip
  - coremond Deployment becomes Available
  - TMM pod spec now has a /var/crash volumeMount + matching
    hostPath volume

What the scenario does NOT verify (documented as a manual
follow-up, hence the amber rating):
  - That a forced TMM crash actually deposits a core file at
    the expected host path. F5's how-to suggests
    'kubectl exec -n default <tmm-pod> -c f5-tmm -- kill -11
    <tmm-pid>' to trigger this. We don't automate it because
    crashing TMM mid-scenario leaves the cluster in a state
    that can confuse other scenarios + the runtime cluster's
    own monitoring loops. The reconciled infrastructure is
    what we assert; if the operator wants to confirm the
    capture path, they can run the kill manually after the
    scenario completes and inspect /var/crash on the kind
    worker node container.

Cleanup reverts spec.coreCollection.enabled to false. TMM
restarts a second time during cleanup to drop the hostPath
mounts — slow but symmetric.

## Manifests

- `01-cneinstance-patch.yaml`

Not every file is applied unconditionally by `ocibnkctl scenario run`; check the
description above for ordering and waits between steps.
