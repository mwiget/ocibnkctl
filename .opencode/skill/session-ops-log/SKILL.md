---
name: session-ops-log
description: Use when debugging awsbnkctl EKS deployments, bnk-forge (the Forge platform backend), AWS SSO/kubectl access issues, or ocibnkctl local k3s PoCs — especially "x509 certificate signed by unknown authority" image pull failures, pods stuck Pending/CrashLoopBackOff, F5 registry (repo.f5.com/devrepo.f5.com) auth errors, "jwt_token is missing" in Forge, or AWS credential/kubeconfig access problems. Also use when asked to continue, retry, or build on a prior multi-day debugging session across these three related F5 BNK tools.
---

# Session Ops Log: awsbnkctl, bnk-forge, and ocibnkctl

Accumulated context from a multi-day debugging session spanning three related
F5 BNK (BIG-IP Next for Kubernetes) tools:

1. **awsbnkctl** (`~/workspace/awsbnkctl`) — provisions BNK on AWS EKS. Server
   copy at `root@10.145.42.2:/root/awsbnkctl`.
2. **bnk-forge** (`root@10.145.42.2:/root/bnk-forge` and `/root/new/bnk-forge`)
   — the Forge platform backend (Docker Compose stack: FastAPI backend,
   Postgres, Celery, MCP server, frontend) that `awsbnkctl forge register`
   talks to and that provides a UI for managing BNK deployments/blueprints.
3. **ocibnkctl** (`~/workspace/ocibnkctl`) — provisions BNK on a *local* k3s
   cluster (Docker containers on your Mac) for PoC/demo purposes. Sibling
   project to awsbnkctl (same org, same CLAUDE.md/Makefile/kit layout
   pattern), but a completely separate codebase and workflow.

Read this before re-diagnosing issues that may already be fixed, and before
re-deriving root causes that are already documented below.

## Part 1: awsbnkctl (AWS EKS) — environment & issues

### Environment topology
- **Target server**: `root@10.145.42.2` (hostname `bnk-forge`), passwordless
  SSH already works.
- **awsbnkctl checkout on server**: `/root/awsbnkctl`. Binary:
  `/root/awsbnkctl/awsbnkctl` (prebuilt, ~230MB). Always `./awsbnkctl` from
  that dir or full path — not on `$PATH`.
- **Local checkout**: `/Users/q.nguyen/workspace/awsbnkctl`, same git remote
  (`JLCode-tech/awsbnkctl`).
- **Clusters provisioned so far**: `bnk-agentcore-demo`, `bnk-agentcore-demo2`,
  `bnk-agentcore-demo3` (all `us-west-2`, all EKS, all via `awsbnkctl up`).
  Workspace state: `/root/awsbnkctl/.awsbnkctl/<cluster-name>/state.env` +
  `kubeconfig`. Corresponding `cluster*.yaml` files live under
  `/root/awsbnkctl/examples/agentcore-demo/`.
- **No bare `kubectl`/`helm` on the server** — use `./awsbnkctl k get|describe|
  logs|apply|delete|exec|port-forward`, or export `KUBECONFIG=<state-dir>/
  kubeconfig` and use real `kubectl` if preferred.
- **`awsbnkctl down` is all-or-nothing** (tears down the whole VPC/EKS
  cluster, confirmed via `--dry-run`, no phase-scoped teardown). For a single
  bad resource, manually fix via `aws` CLI + edit `state.env` to drop the
  stale key, then re-run `up` so just that phase recreates it.
- **`up` is fully idempotent** — safe to re-run after any fix; already-done
  work is skipped via `state.env` + tags.
- **Long `up` runs exceed default shell/tool timeouts** (10-20+ min for Helm
  waits / activation polling) — use large explicit timeouts, don't let it get
  killed mid-phase.

### Issue 1 (fixed): Forge MCP registration — `BNK_FORGE_TOKEN`/`USERNAME`/`PASSWORD`
Symptom: Phase 09 forge registration retries 4x then fails with "No token or
credentials configured. Set BNK_FORGE_TOKEN or BNK_FORGE_USERNAME/PASSWORD."

This error and these env vars belong to `/root/bnk-forge` (the MCP server
project), NOT awsbnkctl's own `AWSBNKCTL_FORGE_USERNAME`/`PASSWORD` (client-
side, different purpose entirely — see `docs/FORGE_INTEGRATION.md`).

Root cause: `/root/bnk-forge/.env` didn't exist (only `.env.example`).
Without `MCP_SERVICE_PASSWORD` set there, the backend never seeds the `mcp`
service account, and `bnk-forge-mcp`'s `BNK_FORGE_PASSWORD` resolves empty
(`docker-compose.yml`: `BNK_FORGE_PASSWORD: ${MCP_SERVICE_PASSWORD:-${MCP_PASSWORD:-}}`).

Fix:
```bash
ssh root@10.145.42.2
SECRET=$(openssl rand -hex 24)
cat > /root/bnk-forge/.env <<EOF
MCP_SERVICE_PASSWORD=${SECRET}
EOF
chmod 600 /root/bnk-forge/.env
cd /root/bnk-forge && docker compose up -d --force-recreate backend mcp
# verify: docker logs bnk-forge-backend | grep -i mcp  -> "Created service account: mcp"
#         docker exec bnk-forge-mcp env | grep BNK_FORGE_PASSWORD  -> non-empty
#         docker ps | grep bnk-forge-mcp  -> "(healthy)"
```
"Generate a secret" = a random string for Docker Compose env config, NOT a
Kubernetes Secret — this stack is plain Docker Compose, not k8s.

### Issue 2 (fixed): AZ mismatch — ENI attach fails "not in the same availability zone"
Symptom (Phase 17): `ec2:AttachNetworkInterface ... InvalidParameterCombination:
You may not attach a network interface to an instance if they are not in the
same availability zone`.

Root cause: `cluster.yaml`'s `network.dataPath.external.az` and `internal.az`
were DIFFERENT. Schema validation only checks each AZ individually against
`network.azs` — never cross-validates `external.az == internal.az`. Phase 10
pins the EKS node group's AZ using ONLY `external.az`; Phase 03 creates the
internal subnet in whatever `internal.az` says. Mismatch → Phase 17's ENI
attach fails. No self-healing; hard fail.

Fix: make `external.az` and `internal.az` identical in `cluster.yaml`, then
clean up the orphaned wrong-AZ ENI + subnet:
```bash
# verify nothing else depends on it first
aws ec2 describe-network-interfaces --network-interface-ids <bad-eni-id> \
  --query 'NetworkInterfaces[0].[Status,Attachment]'
aws ec2 delete-network-interface --network-interface-id <bad-eni-id>
aws ec2 delete-subnet --subnet-id <bad-subnet-id>
# remove the stale BNK_INT_SUBNET=... line from state.env (back it up first)
# re-run `up` — phase 03 recreates subnet in correct AZ, phase 17 re-attaches
```
Same exposure class exists for `testing.jumphost.mgmtSubnetIndex` and
`bigipVE.mgmtSubnetIndex` if those are used.

### Issue 3 (fixed, upstreamed as PR): F5 registry host mismatch — `repo.f5.com` vs `devrepo.f5.com`
This was the big one. F5 FAR credentials can be scoped to F5's **dev/staging**
GCP project (`devrepo.f5.com`) instead of **production** (`repo.f5.com`) —
awsbnkctl hardcoded `repo.f5.com` in FOUR places, causing a chain of
increasingly-confusing errors as each got fixed in turn:

1. Phase 14 Helm chart pull → `401` (FLO chart OCI ref + registry login host)
2. FLO pod `ImagePullBackOff` → `403` (`image.repository` in FLO values +
   far-secret dockerconfigjson `auths` key)
3. FLO's own Lifecycle Operator binary fails fetching the release manifest →
   `403 registry.downloadArtifacts denied` (`CNEInstance.spec.registry.uri`)
   — Phase 21's `f5-cne-controller` deployment never gets created until fixed
4. `bnk.manifestVersion` must match an ACTUAL tag on whichever registry you're
   using — NOT guaranteed to be the same string across `repo.f5.com` vs
   `devrepo.f5.com`. Discover the real tag by decoding the registry token's
   embedded `commands` claim (F5's GAR proxy returns a custom token format):
   ```bash
   FARB64=$(cat cne_pull_64.json | tr -d '\n')
   AUTH=$(printf '_json_key_base64:%s' "$FARB64" | base64 -w0)
   RESP=$(curl -sS -H "Authorization: Basic $AUTH" \
     "https://<HOST>/_token?service=<HOST>&scope=repository:release/f5-bigip-k8s-manifest:pull")
   echo "$RESP" | python3 -c "
  import json,sys,base64
  d=json.load(sys.stdin); tok=d['token']
  inner=json.loads(base64.b64decode(tok+'=='))
  print(json.dumps(inner.get('commands', {}), indent=2))"
   ```

**Fast diagnostic for ANY `repo.f5.com`/`devrepo.f5.com` 401/403/404** — test
the raw registry token endpoint BEFORE touching code or re-running `up`
(saves 10+ min per iteration):
```bash
FARB64=$(cat cne_pull_64.json | tr -d '\n')
AUTH=$(printf '_json_key_base64:%s' "$FARB64" | base64 -w0)
curl -sS -i -H "Authorization: Basic $AUTH" \
  "https://<HOST>/_token?service=<HOST>&scope=repository:<REPO_PATH>:pull"
# 200+token → host+scope OK; GET the manifest next, check 200 vs 404 (tag issue)
# 401/403 → wrong host or no permission for this key on this host/project
```
Also: to tell a cryptographically-broken key apart from a valid-key/wrong-host
problem, sign a Google OAuth2 JWT-bearer assertion with it directly against
Google's own token endpoint (bypasses F5 entirely):
```bash
python3 << 'EOF'
import json, base64, time, jwt as pyjwt, urllib.request, urllib.parse
d = base64.b64decode(open("cne_pull_64.json").read())
sa = json.loads(d)
print("client_email:", sa.get("client_email"), "project:", sa.get("project_id"))
now = int(time.time())
payload = {"iss": sa["client_email"], "scope": "https://www.googleapis.com/auth/cloud-platform",
           "aud": sa.get("token_uri","https://oauth2.googleapis.com/token"), "iat": now, "exp": now+3600}
assertion = pyjwt.encode(payload, sa["private_key"], algorithm="RS256")
data = urllib.parse.urlencode({"grant_type":"urn:ietf:params:oauth:grant-type:jwt-bearer","assertion":assertion}).encode()
req = urllib.request.Request(sa.get("token_uri","https://oauth2.googleapis.com/token"), data=data, method="POST")
try:
    resp = urllib.request.urlopen(req, timeout=15)
    print("STATUS:", resp.status, "-- key is cryptographically valid")
except urllib.error.HTTPError as e:
    print("HTTP ERROR:", e.code, e.read().decode())  # 400 invalid_grant = broken/revoked key
EOF
```

**license.jwt validation**: must be a real 3-segment JWT (2 dots). Decode
header/payload to check if it's production vs test/eval:
```python
import base64, json
parts = open("license.jwt").read().strip().split(".")
def seg(s): return json.loads(base64.urlsafe_b64decode(s + "="*(-len(s)%4)))
print(seg(parts[0])); print(seg(parts[1]))
```
- **Production**: `jku: https://product.apis.f5.com/...`, `sub: FNI-...`,
  `f5_order_type: paid`, has `f5_sat` (subscription-active-until timestamp).
- **Test/eval**: `jku: https://product-tst.apis.f5networks.net/...`,
  `sub: TST-...` — awsbnkctl's own Phase 00 preflight flags this combo with
  production endpoints as a non-fatal warning.
- A matched production pair (`cne_pull_64.json` scoped to
  `f5-gcs-7056-ptg-cne-prod` + `license.jwt` with `FNI-`/`paid`) should use
  `repo.f5.com` (the default) — do NOT set `registryHost: devrepo.f5.com`
  for a production credential pair, that combination will 403.

**Proper fix (not a hardcoded swap)**: added `bnk.registryHost` config field
to `cluster.yaml` (default `repo.f5.com`, via `manifest.FARRegistryHost` in
`applyDefaults` — existing clusters unaffected). Threaded through:
- `internal/aws/phases/phase14_flo_helm.go` — `floRegistryHostFor(cl)` /
  `floChartRefFor(cl)` replace old hardcoded constants.
- `internal/k8s/manifests/shared/flo-values.yaml.tmpl` — `{{ .RegistryHost }}`
  in all 4 `image.repository` lines.
- `internal/k8s/manifests/shared/cneinstance.yaml.tmpl` — `registry.uri:
  {{ .RegistryHost }}`.
- `internal/aws/phases/phase12_k8s_foundation.go` — `buildFARSecret(ns,
  farData, registryHost)` takes host as a param.
- `awsbnkctl bnk upgrade` path too (`migrate.UpgradeOptions.RegistryHost`,
  `phases.NewHelmInstaller(..., registryHost)`, `phases.FLOChartRefFor(cl)`).

Usage: `bnk: { registryHost: devrepo.f5.com }` in `cluster.yaml` (omit/default
= `repo.f5.com`, unchanged behavior).

**PR status**: committed locally on branch `feat/bnk-registry-host-override`
(off `main`, `JLCode-tech/awsbnkctl`), pushed to fork `quangnguyen101/
awsbnkctl`, **opened as PR #83** against `JLCode-tech/awsbnkctl:main`. CI is
gated on `action_required` (GitHub's standard first-time-fork-contributor
approval gate) — needs a repo maintainer to approve the workflow run. Check
`gh pr view 83 --repo JLCode-tech/awsbnkctl` for current status before
re-doing this work. Also merged into this branch: the GIE CRD fix (issue 4
below) from branch `fix/gie-crds-phase12`. If you land on a fresh `main`
clone and hit the `repo.f5.com` 401/403 chain again, check whether #83 has
merged first — don't re-hardcode `devrepo.f5.com` as a quick fix again (we
tried that first, it's the wrong approach, breaks prod users).

### Issue 4 (fixed, included in PR #83): missing Gateway API Inference Extension CRDs
Symptom: `f5-cne-controller` pod `CrashLoopBackOff` indefinitely (hundreds of
restarts over days). Container exits 0 (clean, not a panic) — `kubectl logs
--previous` shows:
```
failed to wait for f5epp caches to sync kind source: *v1.InferencePool:
timed out waiting for cache to be synced for Kind *v1.InferencePool
```

Root cause: `internal/k8s/manifests/gie/gateway-api-inference-extension-
v1.5.0.yaml` (upstream `InferencePool` CRDs) was embedded into the binary via
`embed.go` but **never applied by any phase** — a latent bug since the commit
that added the embed. BNK 2.4+ defaults EPP (F5's endpoint picker) to
enabled, and `f5-cne-controller`'s `f5epp` manager source watches
`*v1.InferencePool` at startup; without the CRD, cache-sync times out and the
manager exits, kubelet loops it forever.

Fix: apply the embedded `gie/` manifest in Phase 12 alongside Multus (same
`applyRawYAML`/`deleteRawYAML` pattern), wait for the CRD to establish before
returning. Unconditional — EPP defaults on for all BNK 2.4+ clusters
regardless of `cluster.yaml` settings.

**Gotcha hit while deploying this fix**: branched the GIE fix off `main`
instead of off `feat/bnk-registry-host-override`, and syncing just that one
file to the server silently REVERTED the registry-host plumbing in it
(`buildFARSecret` lost its `registryHost` param), breaking the FAR secret's
registry host back to `repo.f5.com`-only. Always check `git branch --show-
current` and diff against the RIGHT base before cherry-picking individual
files to a shared server — a single-file sync can silently regress unrelated
work if the files were edited on divergent branches.

**Also hit**: rebuilding the backend image from a stale checkout
(`/root/bnk-forge`) while a newer checkout (`/root/new/bnk-forge`, mid-
upgrade) had already migrated the shared Postgres DB to a newer Alembic
revision (`v2_163`) caused `Can't locate revision identified by 'v2_163'` —
a split-brain deployment, not a code bug. Always check `docker inspect
<container> --format '{{index .Config.Labels "com.docker.compose.project.
config_files"}}'` across ALL related containers before rebuilding one piece
of a multi-container stack — if they don't all point at the same checkout,
rebuilding from the wrong one will break DB migration compatibility.

**Rebuild after any Go/template source edit on the server**: `go build -o
awsbnkctl ./cmd/awsbnkctl` — embedded files require a rebuild, not just a
file copy.

## Part 2: bnk-forge backend bug — `project_secret` source never resolved (fixed)

Symptom: adding the "BNK 2.4.1 Platform (AWS)" blueprint release to an
existing project (`POST /api/stacks/projects/{id}/releases/{id}`) fails with
`Variable validation failed: Required variable 'jwt_token' is missing` —
even though a `jwt_token` **Project Secret** already existed for that project
in the Forge UI.

Root cause: `bnk/flo`'s `module.json` declares `jwt_token` with `"source":
"project_secret"`. This source type IS listed in `CONTEXT_RESOLVED_SOURCES`
in `backend/services/imported_blueprint_service.py` (used to correctly HIDE
the field from the user-facing required-inputs form, telling the UI "Forge
resolves this automatically") — but the actual variable-resolution function
`_apply_explicit_input_source_mappings` only implemented `source == "project"`
and `source == "credential_template"`. No code path ever looked up
`project_secret`-sourced inputs from the `project_secrets` table, even though
a correct reference implementation already existed in a DIFFERENT service
(`stack_deployment_service.py`, used by a different deploy path).

Confirmed by inspecting the cached module catalog
(`/var/lib/docker/volumes/bnk-forge_module_catalog/_data/bnk-forge-modules/`):
every `project_secret`-sourced input across the whole catalog (`bnk/flo`'s
`jwt_token`, `k8s/bnk-prerequisites`'s `cne_pull_secret`, `infra/ocp/connect`'s
kubeconfig/token) omits `source_field` entirely and matches by variable
**`name`** against `project_secrets.name` alone.

Fix: added a `source == "project_secret"` branch to
`_apply_explicit_input_source_mappings` in
`backend/services/imported_blueprint_service.py` (both `/root/bnk-forge` and
`/root/new/bnk-forge` checkouts — confirmed same bug in both), mirroring
`stack_deployment_service.py`'s pattern:
```python
if source == "project_secret":
    if secrets_service is None:  # None when project doesn't exist yet
        continue                 # (create_project_from_release path)
    project_secret = secrets_service.get_secret_by_name(project_id, name)
    if project_secret and project_secret.secret_type == "value":
        try:
            secret_value = secrets_service.get_decrypted_value(project_secret)
        except Exception:
            secret_value = None
        if secret_value:
            resolved[name] = secret_value
    continue
```
Also relaxed the loop guard (`if not name or not source or not source_field`
→ `if not name or not source`, with a separate `if not source_field: continue`
AFTER the new branch) since `project_secret` inputs never set `source_field`.

**Not committed to git** (this is a live-patched running container, not
pushed anywhere) — if `/root/bnk-forge` or `/root/new/bnk-forge` ever gets a
fresh `git pull`/reset, this fix is lost and needs re-applying. Consider
upstreaming to `JLCode-tech/bnk-forge` the same way PR #83 was done for
awsbnkctl, if this recurs.

To deploy this fix: edit the file, `docker compose build backend`, `docker
compose up -d --force-recreate backend` — but FIRST confirm which checkout
directory (`/root/bnk-forge` vs `/root/new/bnk-forge`) the REST of the stack
(celery-worker/beat/agent) is actually running from (see the split-brain
gotcha in Part 1, Issue 4) — rebuild from THAT one.

## Part 3: AWS SSO / kubectl access gotchas

### Setting up a brand-new machine for AWS + kubectl access
```bash
brew install awscli jq   # jq optional but handy
cat > ~/.aws/config <<'EOF'
[profile Users-948780579501]
sso_session = forge
sso_account_id = 948780579501
sso_role_name = Users
region = us-west-2

[sso-session forge]
sso_start_url = https://identitycenter.amazonaws.com/ssoins-72234c7bb73f26b6
sso_region = us-east-1
sso_registration_scopes = sso:account:access
EOF
aws sso login --profile Users-948780579501
aws eks update-kubeconfig --name <cluster-name> --region us-west-2 --profile Users-948780579501
kubectl get nodes
```
(Note: the server's own `~/.aws/config` has a stray `[default]` profile with
a bogus `login_session` key — not real SSO config, always resolves to
"session expired". Always use the explicit `Users-948780579501` profile /
`AWS_PROFILE=Users-948780579501` env var on that server, never rely on the
default profile.)

### Recurring SSO expiry
Sessions expire periodically (hours, not days). Fix is always:
```bash
aws sso login --profile Users-948780579501
```
No need to regenerate kubeconfig — its embedded `exec:` block picks up the
refreshed session automatically.

### "the server has asked for the client to provide credentials" / "Unauthorized" despite valid SSO
Two distinct root causes hit this session, in order of likelihood:

1. **Your IAM role isn't in the cluster's `aws-auth` ConfigMap.** EKS's
   implicit "cluster creator gets admin" grant is fragile and can stop
   applying. Check cluster auth mode first (`aws eks describe-cluster
   --query 'cluster.accessConfig.authenticationMode'`) — if `CONFIG_MAP`
   (not `API`/`API_AND_CONFIG_MAP`), you CANNOT use `aws eks create-access-
   entry` (fails with `InvalidRequestException`); must patch `aws-auth`
   directly:
   ```bash
   kubectl get configmap aws-auth -n kube-system -o yaml  # check mapRoles
   # if your role's rolearn is missing, patch it in (use --force on apply,
   # expect a field-manager conflict with "Kubernetes Java Client"):
   kubectl apply -f aws-auth-patch.yaml --force-conflicts --server-side
   ```
   The IAM role ARN for an AWS-SSO-derived identity is the UNDERLYING
   permission-set role, not the assumed-role session ARN — find it via:
   ```bash
   aws iam list-roles --path-prefix /aws-reserved/sso.amazonaws.com/ \
     --query "Roles[?contains(RoleName, 'AWSReservedSSO_Users')].Arn" --output text
   ```

2. **Stray static AWS credential env vars silently override `AWS_PROFILE`.**
   AWS's credential chain ALWAYS prioritizes explicit `AWS_ACCESS_KEY_ID`/
   `AWS_SECRET_ACCESS_KEY`/`AWS_SESSION_TOKEN` env vars over profile-based
   SSO credentials, even if `AWS_PROFILE` is also set. If these are present
   (e.g. injected by a corporate credential-vending tool — look for
   `AWS_CA_BUNDLE=/Library/corp-ca/...` as a tell), `kubectl`'s exec plugin
   silently authenticates as the WRONG identity (not mapped in `aws-auth`) →
   `Unauthorized`. Diagnose and fix:
   ```bash
   env | grep -i "^AWS_ACCESS_KEY_ID\|^AWS_SECRET_ACCESS_KEY\|^AWS_SESSION_TOKEN"
   unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
   ```
   This recurred across multiple terminal sessions — may be re-injected
   automatically by a corporate tool on new shells; re-check/unset whenever
   `kubectl`/`awsbnkctl` unexpectedly fail auth despite a confirmed-valid
   SSO session.

## Part 4: ocibnkctl (local k3s PoC) — corporate proxy + memory

### What it is
`~/workspace/ocibnkctl` — sibling tool to awsbnkctl, provisions BNK on a
LOCAL k3s cluster running as plain Docker containers (`rancher/k3s` image,
one `k3s-<poc-name>-server-0` + N `k3s-<poc-name>-agent-N` containers) —
no cloud provider, no VM. PoC state lives in a repo dir (e.g. `~/workspace/
ocibnkctl/demo/`) with `poc.yaml` as the single source of truth (destroy/
redeploy reads only from this file).

Commands: `cluster up` (destroys+recreates, `--yolo --confirm-cluster NAME`
required) → `deploy prereqs` → `deploy flo` → `deploy cne` (each individually
`--yolo --confirm-deploy NAME` gated) → optionally `deploy shrink` for tight
hosts. `destroy --yolo --confirm-cluster NAME` tears down symmetrically. `e2e`
chains validate→cluster up→deploy. Binary at `~/workspace/ocibnkctl/
ocibnkctl` — if downloaded via browser/tarball, macOS Gatekeeper quarantines
it (`com.apple.quarantine` xattr): fix with `xattr -d com.apple.quarantine
~/workspace/ocibnkctl/ocibnkctl` (or `-dr` on the whole dir for a fresh
tarball extraction).

### Issue A (fixed): `x509: certificate signed by unknown authority` on image pulls
Symptom: `kube-multus-ds` and `kyverno-admission-controller` (and potentially
any other pod pulling from `ghcr.io`/`reg.kyverno.io`/etc.) stuck in
`Init:ErrImagePull`/`Init:ImagePullBackOff` with kubelet events showing:
```
tls: failed to verify certificate: x509: certificate signed by unknown authority
```
while pulling from `pkg-containers.githubusercontent.com` (GHCR's actual blob
backend).

Root cause: host machine sits behind a corporate TLS-inspecting proxy
(re-signs all outbound HTTPS with a corporate CA — look for `/Library/
corp-ca/corp-ca-bundle.pem` on macOS, or similar on other platforms). The
k3s nodes are isolated Docker containers with their own containerd trust
store, which has no knowledge of this corporate CA — unrelated to Docker
Desktop's or the host's own trust store.

**ocibnkctl already has a purpose-built feature for this** — don't
reinvent it. `poc.yaml`'s `cluster.extra_ca_certs` (see `internal/poc/
schema.go` doc comment, explicitly mentions "TLS-inspecting proxy (Netskope,
Zscaler)"):
```yaml
cluster:
    name: demo
    provider: docker
    tmm_nodes: 1
    extra_ca_certs:
        - keys/corp-ca-bundle.pem   # path relative to the poc.yaml dir
```
```bash
cp /Library/corp-ca/corp-ca-bundle.pem ~/workspace/ocibnkctl/demo/keys/
# edit poc.yaml to add extra_ca_certs as above, then:
```
**Critical**: this bind-mounts the CA into `/etc/ssl/certs/ocibnkctl-extra-
ca.pem` on every node, but per the schema doc: "Applies to nodes created
after it is set; existing nodes keep their trust store." Running `cluster up`
again on an ALREADY-EXISTING cluster does NOT recreate the containers (logs
"cluster already exists — leaving in place") — the mount is NOT retroactively
applied. You MUST fully destroy first:
```bash
cd ~/workspace/ocibnkctl/demo
~/workspace/ocibnkctl/ocibnkctl destroy --yolo --confirm-cluster demo
~/workspace/ocibnkctl/ocibnkctl cluster up --yolo --confirm-cluster demo
# verify: docker inspect k3s-demo-server-0 --format '{{json .Mounts}}' | grep extra-ca
# then redeploy: deploy prereqs / deploy flo / deploy cne (all --yolo --confirm-deploy demo)
```
General diagnostic for this exact error class (any k3s/Docker-based tool,
not just ocibnkctl): check `docker inspect <node-container> --format '{{json
.Mounts}}'` for a `/etc/ssl/certs/...` bind mount — if absent and you're
behind a corporate proxy, this is almost certainly the cause.

### Issue B (fixed): insufficient memory for `f5-tmm` pod — stuck `Pending`
Symptom: after fixing Issue A, everything deploys except `f5-tmm-*`, stuck
`Pending` with `FailedScheduling: 0/2 nodes are available: 1 Insufficient
memory`.

Root cause investigation (dead ends first, for future reference — don't
repeat these):
- `deploy shrink` (Kyverno-based resource-request capping) explicitly
  EXCLUDES all `f5-tmm` pods by design — its bespoke pod-manager controller
  deletes/reverts any TMM pod whose live spec doesn't match what FLO
  rendered, so mutating it via admission webhook loops forever. Per the
  `deploy shrink --help` text: "Shrink TMM's main container via
  `CNEInstance.spec.advanced.tmm.resources` instead."
- `kubectl patch cneinstance ... spec.advanced.tmm.resources` DOES work (FLO
  re-renders through this field, doesn't revert it) but **only affects the
  single main `f5-tmm` container**, not its 5 sidecars
  (`f5-tmm-routing`/`f5-tmm-tmrouted`/`debug`/`blobd`/`observer`). `blobd`
  alone defaults to requesting 4Gi — the single biggest line item. No CRD
  field exists to size individual sidecars (`kubectl explain cneinstance.
  spec.advanced.tmm` confirms no per-sidecar schema).
- `spec.deploymentSize: Tiny` (down from `Small`) had **zero effect** on
  actual resource requests in this BNK release — don't rely on this enum
  for memory sizing even though the CRD description implies it should.

**Actual fix**: the default-size TMM pod's total requested memory across all
6 containers is ~9Gi — more than fits on a typical Docker Desktop default
VM allocation (often ~7.75Gi). This is a genuine host-sizing problem, not a
BNK/ocibnkctl config problem. Bump Docker Desktop's VM resources (check your
Mac's actual free RAM first with `sysctl hw.memsize` — most modern dev
laptops have 32-64GiB+ and can spare much more than the default):
```bash
# quit Docker Desktop, then edit:
# ~/Library/Group Containers/group.com.docker/settings-store.json
# add/edit: "MemoryMiB": 16384, "Cpus": 6  (adjust to taste)
open -a "Docker Desktop"
# wait for daemon: `docker info` succeeds
# k3s containers persist across a Docker Desktop restart (just stop/resume,
# not removed) — no need to recreate the cluster, just wait ~60-90s for
# everything to reconnect and reschedule.
docker info --format '{{.MemTotal}}'  # verify new allocation took effect
```
After the bump, `f5-tmm` scheduled and reached `6/6 Running` within a few
minutes, with zero further manual intervention (FLO/CNEInstance naturally
retried the Pending pod once node capacity freed up — no restart of
ocibnkctl commands needed).

### Quick triage checklist for a new "pods stuck Pending/ImagePullBackOff" report on ocibnkctl
1. `kubectl describe pod <name>` → check `Events` for the EXACT failure
   reason first (don't guess):
   - `x509: certificate signed by unknown authority` → Issue A (corp CA)
   - `Insufficient memory`/`Insufficient cpu` in `FailedScheduling` → Issue B
     (host resources) — check if it's `f5-tmm` specifically (shrink-excluded)
     or something else (shrink should have capped it already)
2. For CA errors: `docker inspect <node> --format '{{json .Mounts}}' | grep
   ssl` — present but still failing → bundle itself may be wrong/incomplete;
   absent → `extra_ca_certs` never configured or cluster never recreated
   after configuring it.
3. For memory errors: `kubectl describe node <node> | grep -A6 "Allocated
   resources"` to see current usage %, then `docker info --format
   '{{.MemTotal}}'` to check the VM's actual ceiling.
