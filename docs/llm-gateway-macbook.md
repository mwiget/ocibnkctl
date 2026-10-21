# LLM gateway on a MacBook Pro

How to run the BNK 2.4.1 AI/LLM gateway demo on one Apple-silicon MacBook
Pro with Docker Desktop: a k3s cluster with BNK and TMM, F5's console, a mock
LLM, virtual keys and request metering. No GPU or LLM API key needed.

Measured on 2026-10-21 on a **MacBook Pro, Apple M4 Pro, 12 cores, 24 GB,
macOS 26.7**, Docker Desktop (engine 29.8), on the F5 network behind
Netskope, with ocibnkctl v2.4.1-3 (the scripts: v2.4.1-4). The general guide, with every option and
the API, is [llm-gateway-howto.md](llm-gateway-howto.md).

## Contents

- [What it needs](#what-it-needs)
- [The scripts in the PoC](#the-scripts-in-the-poc)
- [What runs in the cluster](#what-runs-in-the-cluster)
- [1. Docker Desktop settings](#1-docker-desktop-settings)
- [2. Install ocibnkctl](#2-install-ocibnkctl)
- [3. Create the PoC](#3-create-the-poc)
- [4. Deploy](#4-deploy)
- [5. Open the console (port forwarding)](#5-open-the-console-port-forwarding)
- [6. Sign in as tenant demo](#6-sign-in-as-tenant-demo)
- [7. Send prompts with ask.sh](#7-send-prompts-with-asksh)
- [8. Sign in as platform admin](#8-sign-in-as-platform-admin)
- [Troubleshooting on a Mac](#troubleshooting-on-a-mac)
- [Tear down](#tear-down)

## What it needs

| | Needed | Measured on the M4 Pro |
|---|---|---|
| CPU | 10 cores for Docker (`doctor` enforces it) | 12 cores, the Docker VM sees all 12 |
| Docker Desktop memory | **18 GB** (default is half of RAM; raise it, below) | 17.6 GiB in the VM; the cluster uses about **9 GiB** (TMM worker 7.0, server 1.9, edge routers 0.1) |
| Mac RAM | 24 GB or more (macOS keeps about 6 GB) | 24 GB |
| Disk | about 45 GB free for images and volumes | 184 GB free; Docker held 23 GB of images and 20 GB of volumes |
| Time | about 8 minutes for the first deploy, with a warm image cache | deploy 7m 30s, then the two scenarios |
| Other clusters | none: stop every other k3s/kind cluster first | — |

BNK's full footprint asks for 24 GiB of memory on one node. Under that,
`ocibnkctl e2e` adds **`deploy shrink`** by itself: a Kyverno policy that
lowers the pods' resource requests so they schedule. Nothing to configure;
the run says `deploy-shrink (auto: … 17.6 GiB runtime memory < 24 GiB floor
… engaging shrink)`. Real usage stays near 9 GiB either way.

With less than 18 GB for Docker, set `ai_metrics: false` in `poc.yaml`: that
drops ClickHouse and request metering, and the gateway, console and keys
still work.

## The scripts in the PoC

`ocibnkctl init --profile llm-gateway` puts four scripts in the PoC
directory. Each works from any directory and reads the PoC's kubeconfig
itself.

| Script | What it does | Step |
|---|---|---|
| `./console.sh` | Forwards the console to `http://localhost:3000` and prints the sign-in hints. Runs until Ctrl-C | [5](#5-open-the-console-port-forwarding) |
| `./demo-password.sh [tenant]` | Prints a tenant's admin password, `demo` by default | [6](#6-sign-in-as-tenant-demo) |
| `./ask.sh "prompt" [model]` | Sends a prompt as `demo-app` through TMM and prints the reply | [7](#7-send-prompts-with-asksh) |
| `./admin-password.sh` | Prints the platform admin's password | [8](#8-sign-in-as-platform-admin) |

The password scripts print the secret on your terminal: don't run them where
others can see the screen, and don't paste the output into chats.

They ship with ocibnkctl v2.4.1-4 and later. For a PoC created with an
earlier release, copy them from
[`internal/embedded/profiles/llm-gateway/`](../internal/embedded/profiles/llm-gateway/)
into the PoC directory and make them executable (`chmod +x *.sh`).

### Launching them

`console.sh` keeps running (it *is* the port forward), so give it its own
terminal tab. Everything else runs in a second tab and returns right away.

**Terminal 1: the console's port forward** (leave it open):

```console
$ cd ~/lab/llm
$ ./console.sh
Console: http://localhost:3000   (use localhost:3000 exactly)
  tenant demo:    tenant "demo" (not "lab"), Sign in as tenant admin, user admin, ./demo-password.sh
  platform admin: Sign in as platform admin, user admin, ./admin-password.sh
Ctrl-C stops the forward.
Forwarding from 127.0.0.1:3000 -> 3000
Forwarding from [::1]:3000 -> 3000
Handling connection for 3000
```

Each browser request logs a `Handling connection for 3000` line. Ctrl-C ends
the forward, and the console goes away until you start it again.

**Terminal 2: passwords and prompts:**

```console
$ cd ~/lab/llm
$ ./demo-password.sh           # copy it into the console's tenant sign-in
<password>
$ ./ask.sh "what is BNK?"
Alas, poor Yorick! I knew him, Horatio: ...
-- gpt-4o-mini, 75 tokens
```

Then open **http://localhost:3000** in the browser.

To keep the forward in the background instead, with no tab of its own:

```bash
nohup ./console.sh > console.log 2>&1 &    # start
pkill -f 'port-forward svc/f5-aigateway-console'   # stop
```

A second `./console.sh` while one already runs stops with `localhost:3000 is
already in use` and shows which process holds the port.

## What runs in the cluster

Two k3s nodes run as Docker containers: `k3s-llm-server-0` (the Kubernetes
control plane, kept free of workloads) and `k3s-llm-agent-0` (the TMM worker,
where every BNK pod runs). Next to them, outside the cluster, run
`bnk-edge-frr-llm` (the edge router TMM peers with over BGP, and where
`ask.sh`'s client runs) and `bnk-edge-origin-llm`.

After a green run, `kubectl get pods -A` shows 39 pods: 36 Running and 3
Completed setup jobs.

| Namespace | Pods | What they are |
|---|---|---|
| `default` | `f5-tmm-…` (7/7) | **TMM**, the data plane: the gateway runs inside it. Containers `f5-tmm`, `f5-tmm-routing` (BGP), `f5-tmm-tmrouted`, `debug`, `blobd`, `f5-fluentbit`, `observer` |
| `default` | `f5-cne-controller-…` (3/3) | The control plane (f5ingress) and the AI gateway API on :8443 |
| `default` | `f5-aigateway-console-…` | The web console ("Polaris AI"), on :3000 |
| `default` | `f5-aigateway-clickhouse-0`, `…-schema-…` (Completed) | Request records for usage and spend |
| `default` | `f5-dssm-db-0..2`, `f5-dssm-sentinel-0..2` | DSSM: session state (virtual keys, budgets) |
| `default` | `f5-afm-…` | Firewall policy (AFM) |
| `f5-cne-core` | `f5-spk-cwc-…` | CWC: the License (`kubectl -n f5-cne-core get license` → Active) |
| `f5-cne-core` | `f5-toda-fluentd-…`, `otel-collector-…`, `f5-observer-…` (3) | Logging and telemetry; fluentd carries the gateway's records to ClickHouse |
| `f5-cne-core` | `f5-ipam-ctlr-…`, `f5-crdconversion-…`, `f5-rabbit-…`, `f5-spk-csrc-…`, `crd-installer-…` (Completed) | IP pools, CRD versions, the message bus, egress |
| `f5-operators` | `flo-f5-lifecycle-operator-…` | FLO: installs and reconciles all of the above |
| `llm-demo` | `mock-llm-…`, `llm-setup-…` (Completed) | The mock LLM, and the job that configured tenant `demo` |
| `kyverno` | `kyverno-admission-controller-…` | Applies `deploy shrink`'s lower resource requests |
| `cert-manager` | 3 pods | Certificates for BNK's internal TLS |
| `kube-system` | `calico-*`, `kube-multus-ds-*`, `whereabouts-*`, `coredns-…`, `local-path-provisioner-…` | Pod network, TMM's second interface (`net1`) and its IPAM, DNS, storage |

### Watch it with k9s

[k9s](https://k9scli.io/) is a terminal UI for the cluster (`brew install
k9s`). Point it at the PoC's kubeconfig:

```bash
cd ~/lab/llm
KUBECONFIG=$PWD/artifacts/kubeconfig k9s -A      # -A: all namespaces
```

What it shows on the MacBook after `e2e --with-scenarios` (captured from
k9s 0.51 on this cluster):

```text
                                                   K9s v0.51.0 default:default:v1.30.14+k3s1 0%::0%
┌────────────────────────────────────────────────────────────────── pods(all)[39] ───────────────────────────────────────────────────────────────────┐
│ NAMESPACE↑     NAME                                            PF   READY   STATUS         RESTARTS IP                 NODE                AGE     │
│ cert-manager   cert-manager-974688dfd-rps75                    ●    1/1     Running               0 192.168.130.134    k3s-llm-agent-0     37m     │
│ cert-manager   cert-manager-cainjector-6687cc685b-zvrmt        ●    1/1     Running               0 192.168.130.132    k3s-llm-agent-0     37m     │
│ cert-manager   cert-manager-webhook-b8cdf84f-jfztm             ●    1/1     Running               0 192.168.130.133    k3s-llm-agent-0     37m     │
│ default        f5-afm-66f998cb9d-bc49t                         ●    2/2     Running               0 192.168.130.156    k3s-llm-agent-0     34m     │
│ default        f5-aigateway-clickhouse-0                       ●    1/1     Running               0 192.168.130.147    k3s-llm-agent-0     34m     │
│ default        f5-aigateway-clickhouse-schema-15991cd6-hqwbn   ●    0/1     Completed             1 192.168.130.143    k3s-llm-agent-0     34m     │
│ default        f5-aigateway-console-7bb9f564cb-pw4sn           ●    1/1     Running               0 192.168.130.140    k3s-llm-agent-0     34m     │
│ default        f5-cne-controller-67868bf9bc-r25vv              ●    3/3     Running               0 192.168.130.172    k3s-llm-agent-0     28m     │
│ default        f5-dssm-db-0                                    ●    3/3     Running               0 192.168.130.160    k3s-llm-agent-0     34m     │
│ default        f5-dssm-db-1                                    ●    3/3     Running               0 192.168.130.166    k3s-llm-agent-0     32m     │
│ default        f5-dssm-db-2                                    ●    3/3     Running               0 192.168.130.169    k3s-llm-agent-0     31m     │
│ default        f5-dssm-sentinel-0                              ●    3/3     Running               0 192.168.130.157    k3s-llm-agent-0     34m     │
│ default        f5-dssm-sentinel-1                              ●    3/3     Running               0 192.168.130.165    k3s-llm-agent-0     32m     │
│ default        f5-dssm-sentinel-2                              ●    3/3     Running               0 192.168.130.167    k3s-llm-agent-0     31m     │
│ default        f5-tmm-nwgcc                                    ●    7/7     Running               0 192.168.130.163    k3s-llm-agent-0     34m     │
│ f5-cne-core    crd-installer-7n82w                             ●    0/2     Completed             0 192.168.130.139    k3s-llm-agent-0     35m     │
│ f5-cne-core    f5-crdconversion-776b9d77b4-vqzqs               ●    2/2     Running               0 192.168.130.154    k3s-llm-agent-0     34m     │
│ f5-cne-core    f5-ipam-ctlr-56c8c8c876-6h8bb                   ●    2/2     Running               0 192.168.130.152    k3s-llm-agent-0     34m     │
│ f5-cne-core    f5-observer-0                                   ●    2/2     Running               0 192.168.130.150    k3s-llm-agent-0     34m     │
│ f5-cne-core    f5-observer-operator-78c49ff6f-hh9rs            ●    2/2     Running               0 192.168.130.155    k3s-llm-agent-0     34m     │
│ f5-cne-core    f5-observer-receiver-0                          ●    2/2     Running               0 192.168.130.161    k3s-llm-agent-0     34m     │
│ f5-cne-core    f5-rabbit-85676fcb8b-8dd9f                      ●    2/2     Running               0 192.168.130.149    k3s-llm-agent-0     34m     │
│ f5-cne-core    f5-spk-csrc-d5568                               ●    2/2     Running               0 172.20.0.3         k3s-llm-agent-0     34m     │
│ f5-cne-core    f5-spk-cwc-6cfcbbd8b6-6lwtn                     ●    3/3     Running               0 192.168.130.162    k3s-llm-agent-0     34m     │
│ f5-cne-core    f5-toda-fluentd-fff645569-hxrc6                 ●    1/1     Running               0 192.168.130.158    k3s-llm-agent-0     34m     │
│ f5-cne-core    otel-collector-85d768c6b7-6xf8c                 ●    1/1     Running               0 192.168.130.153    k3s-llm-agent-0     34m     │
│ f5-operators   flo-f5-lifecycle-operator-55546b74b7-nr4pj      ●    1/1     Running               0 192.168.130.138    k3s-llm-agent-0     36m     │
│ kube-system    calico-kube-controllers-65dcc554ff-9wt94        ●    1/1     Running               0 192.168.130.130    k3s-llm-agent-0     38m     │
│ kube-system    calico-node-7hr9q                               ●    1/1     Running               0 172.20.0.3         k3s-llm-agent-0     36m     │
│ kube-system    calico-node-l9l8h                               ●    1/1     Running               0 172.20.0.2         k3s-llm-server-0    36m     │
│ kube-system    coredns-645bdb8675-tj7jw                        ●    1/1     Running               0 192.168.130.131    k3s-llm-agent-0     38m     │
│ kube-system    kube-multus-ds-nvhhk                            ●    1/1     Running               0 172.20.0.3         k3s-llm-agent-0     36m     │
│ kube-system    kube-multus-ds-stvzz                            ●    1/1     Running               0 172.20.0.2         k3s-llm-server-0    36m     │
│ kube-system    local-path-provisioner-ffbcc4db4-pfm6t          ●    1/1     Running               0 192.168.130.129    k3s-llm-agent-0     38m     │
│ kube-system    whereabouts-6klhw                               ●    1/1     Running               0 172.20.0.2         k3s-llm-server-0    35m     │
│ kube-system    whereabouts-7c742                               ●    1/1     Running               0 172.20.0.3         k3s-llm-agent-0     35m     │
│ kyverno        kyverno-admission-controller-d9f7fb997-48mvj    ●    1/1     Running               0 192.168.130.137    k3s-llm-agent-0     37m     │
│ llm-demo       llm-setup-wkd5n                                 ●    0/1     Completed             0 192.168.130.171    k3s-llm-agent-0     30m     │
│ llm-demo       mock-llm-7fd858c7-nwlr4                         ●    1/1     Running               0 192.168.130.170    k3s-llm-agent-0     30m     │
└────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
  <pod>
                                                         😎 Viewing v1/pods in namespace all
```

Useful keys: `:pods` lists pods, `/tmm` filters them, `l` shows logs, `s`
opens a shell, `d` describes, `0` shows all namespaces, `:svc` lists services,
`Shift-F` sets up a port forward, and `:q` quits. Things to watch:

- **During `e2e`**: pods appear namespace by namespace; `f5-tmm-…` reaching
  7/7 is the last big step.
- **TMM's logs**: select `f5-tmm-…`, press `l`, then pick the `f5-tmm`
  container.
- **Requests from `ask.sh`**: the mock doesn't log requests. Each one lands in
  ClickHouse, including the 401s and 403s TMM answers itself. Select
  `f5-aigateway-clickhouse-0`, press `s`, and count them by status:

  ```bash
  clickhouse-client -q "SELECT \`http.response.status_code\` AS status, count()
    FROM otel.aigw_requests WHERE tenant = 'demo' GROUP BY status ORDER BY status"
  # 200  12
  # 401   3
  # 403   3
  ```

Without k9s:

```bash
export KUBECONFIG=~/lab/llm/artifacts/kubeconfig
kubectl get pods -A
kubectl get pods -A --field-selector=status.phase!=Running,status.phase!=Succeeded   # anything unhealthy
kubectl -n f5-cne-core get license
```

## 1. Docker Desktop settings

Docker Desktop → **Settings → Resources → Advanced**:

- **CPU limit**: all cores (12 here).
- **Memory limit**: **18 GB**. Apply & restart.
- **Disk usage limit**: leave the default, at least 64 GB.

Check what the VM got:

```bash
docker info --format 'cpus={{.NCPU}} memory={{.MemTotal}}'
# cpus=12 memory=18863607808   (17.6 GiB)
```

Stop or destroy any other cluster on Docker first: two clusters don't fit in
18 GB.

## 2. Install ocibnkctl

v2.4.1-4 or later. Releases before v2.4.1-3 fail on Docker Desktop (see
[Troubleshooting](#troubleshooting-on-a-mac)), and v2.4.1-3 doesn't ship the
scripts.

```bash
V=2.4.1-4
curl -sSL "https://github.com/mwiget/ocibnkctl/releases/download/v$V/ocibnkctl_${V}_darwin_arm64.tar.gz" | tar xz ocibnkctl
install -m 0755 ocibnkctl ~/.local/bin/
ocibnkctl version                       # ocibnkctl 2.4.1-4 ...
brew install kubectl helm jq            # if missing; ocibnkctl doctor checks them
```

## 3. Create the PoC

```bash
mkdir -p ~/lab && cd ~/lab
ocibnkctl init llm --profile llm-gateway
cd llm
cp /path/to/f5-far-auth-key.tgz keys/   # FAR image-pull key from F5
cp ~/.jwt keys/.jwt                     # BNK license token (a test token works)
```

**Behind Netskope (the F5 network), trust its CA on the cluster nodes.**
Netskope re-signs HTTPS. The Mac trusts its CA; the k3s nodes don't, and image
pulls fail with `x509: certificate signed by unknown authority`. Export the
CA from the keychain and list it in `poc.yaml`:

```bash
security find-certificate -a -c goskope -p > keys/netskope-ca.pem
```

```yaml
cluster:
    extra_ca_certs:
        - keys/netskope-ca.pem
```

Optional, if [regcachectl](https://github.com/mwiget/regcachectl) runs on the
Mac: repeat deploys pull images from the local cache instead of the internet.

```yaml
cluster:
    registry_cache:
        enabled: true
```

Check:

```bash
ocibnkctl doctor        # resources: 12 cores ≥ 10; memory note: e2e auto-runs deploy shrink
ocibnkctl validate      # OK (1 warning about anycast-bgp on one host: expected)
```

## 4. Deploy

```bash
ocibnkctl e2e --yolo --confirm-cluster llm --with-scenarios
```

The run on the M4 Pro:

```
[1/6] validate         OK  (0s)
[2/6] cluster-up       OK  (36s)    extra CA → 2 cert(s) trusted on every node
[3/6] deploy-prereqs   OK  (16s)
[4/6] deploy-flo       OK  (18s)
[5/6] deploy-shrink    OK  (44s)    auto: 17.6 GiB < 24 GiB floor
[6/6] deploy-cne       OK  (5m9s)   License Active
bgp-peer-frr           OK — 1 TMM(s) Established with external FRR; routes learned
llm-gateway            OK — LLM gateway enforced keys and model scope through TMM and metered every request
```

`llm-gateway` may print *"AIGatewayProfile demo-llm not Programmed yet …
restarting f5-cne-controller once"*. That is a known 2.4.1 race, and the
scenario handles it.

What the scenario sets up: tenant **`demo`**, a mock OpenAI-compatible LLM,
model **`gpt-4o-mini`**, principal **`demo-app`**, route `demo-llm` on the
gateway address **`203.0.113.140`**, and a virtual key for `demo-app` in
`artifacts/llm-gateway/virtual-key`.

## 5. Open the console (port forwarding)

The console runs in the cluster. `kubectl port-forward` makes it reachable on
the Mac's `localhost:3000`. The profile puts `console.sh` in the PoC for that
(and `demo-password.sh`, `admin-password.sh` for steps 6 and 8). Run it in
its own terminal tab, since it keeps running ([Launching them](#launching-them)):

```bash
cd ~/lab/llm
./console.sh            # prints the sign-in hints, then forwards until Ctrl-C
```

By hand, it is:

```bash
export KUBECONFIG=$PWD/artifacts/kubeconfig
kubectl port-forward svc/f5-aigateway-console 3000:3000
```

Leave it running in its own terminal, and open **http://localhost:3000**. Use
`localhost:3000` exactly: the console's sign-in redirects go there. The
forward stops when the terminal closes or the console pod restarts; start it
again then.

## 6. Sign in as tenant demo

Each tenant has a local admin, user `admin`, whose password is in Secret
`aitenant-<tenant>-admin`. For the demo tenant:

```bash
./demo-password.sh             # another tenant: ./demo-password.sh acme
# by hand:
kubectl get secret aitenant-demo-admin -o jsonpath='{.data.password}' | base64 -d; echo
```

On the sign-in page:

1. **Tenant**: the page proposes **`lab`**. Replace it with **`demo`**: `lab`
   doesn't exist on this cluster, and signing in there fails with *"wrong
   username or password"* even with the right password.
2. **Sign in as tenant admin**.
3. User **`admin`**, the password from above.

What to look at: the **dashboard** (requests, errors, tokens, cost, latency),
**Spend** by model, **Providers** (the mock), **Models**, **Routes**
(`demo-llm`), **Keys**, and **Users** (`demo-app`).

## 7. Send prompts with ask.sh

`ask.sh` ([the scripts](#the-scripts-in-the-poc)) sends one prompt as `demo-app`, with its virtual key, through
TMM, from a throwaway curl container on the edge router's network, which is
outside the cluster the way a real client is. It needs `docker` and `jq`.

```bash
cd ~/lab/llm
./ask.sh "what is BNK?"
# Alas, poor Yorick! I knew him, Horatio: ...
# -- gpt-4o-mini, 75 tokens

./ask.sh "hi" gpt-5           # a model outside the key's scope
# HTTP 403: Model not allowed for this virtual key

KEY=nope ./ask.sh "hi"        # a wrong key
# HTTP 401: Invalid virtual key

RAW=1 ./ask.sh "hi"           # the full OpenAI-style JSON
KEY=sk-... ./ask.sh "hi"      # as another principal (a key issued in the console)
```

The mock answers with canned text: the gateway, keys, scope and metering are
real, the model isn't. Refresh the console: each request shows in usage and
spend within seconds. For real answers, add a real provider and model in the
console (as tenant admin): [Use a real provider](llm-gateway-howto.md#use-a-real-provider).

To prompt as someone else: in the console as tenant admin, add a user under
**Users**, issue a virtual key for them under **Keys**, and run
`KEY=<that key> ./ask.sh "…"`.

## 8. Sign in as platform admin

The **platform admin** sits above the tenants: it creates and deletes
tenants and sets their SSO. Its password is in Secret `f5-aigateway-admin`:

```bash
./admin-password.sh
# by hand:
kubectl get secret f5-aigateway-admin -o jsonpath='{.data.password}' | base64 -d; echo
```

On the sign-in page: **Sign in as platform admin**, user **`admin`**, that
password. No tenant field is involved.

The two passwords don't cross over:

| Sign-in | Secret | Accepted on |
|---|---|---|
| Platform admin | `f5-aigateway-admin` | the platform sign-in only (refused on a tenant's, 401) |
| Tenant `demo` admin | `aitenant-demo-admin` | tenant `demo`'s sign-in only (refused on the platform's, 401) |

To give someone their own tenant: as platform admin, create tenant `acme`,
read its admin password with `./demo-password.sh acme`, and hand them that. They sign in with tenant `acme` → **Sign in as tenant
admin**. More in [Tenants and accounts](llm-gateway-howto.md#tenants-and-accounts).

Treat both passwords and every virtual key as secrets: don't paste them into
chats or commit them. The PoC's `keys/` and `artifacts/` are gitignored.

## Troubleshooting on a Mac

| Symptom | Cause | Fix |
|---|---|---|
| `cluster up` fails: `ip route replace default via 172.20.0.1fd95:…` | ocibnkctl before v2.4.1-3 on Docker Desktop: its networks are dual-stack | ocibnkctl v2.4.1-3 or later |
| `deploy cne` times out on `kube-multus-ds`; pods `ImagePullBackOff`, events `x509: certificate signed by unknown authority` | Netskope's CA isn't trusted on the nodes | `extra_ca_certs` ([step 3](#3-create-the-poc)), then `ocibnkctl destroy …` and rerun `e2e`: nodes read the CA at creation |
| `doctor`: `runtime memory … < 24 GiB floor` | Expected below 24 GB | Nothing: `e2e` runs `deploy shrink`. Under 18 GB, also `ai_metrics: false` |
| Pods `Pending`, `Insufficient memory` | Docker VM too small, or another cluster running | 18 GB for Docker, and only this cluster |
| Console: *"wrong username or password"* | Tenant `lab` (the default) instead of `demo`, the wrong button, or the other admin's password | [Step 6](#6-sign-in-as-tenant-demo) / [step 8](#8-sign-in-as-platform-admin); the table above |
| Console page dead after a while | The port-forward stopped | `./console.sh` again |
| `console.sh`: `localhost:3000 is already in use` | An earlier forward still runs | Use it, or stop it (`pkill -f 'port-forward svc/f5-aigateway-console'`) |
| `ask.sh`: `no virtual key` | The `llm-gateway` scenario hasn't run | `ocibnkctl scenario run llm-gateway` |

## Tear down

```bash
cd ~/lab/llm
ocibnkctl destroy --yolo --confirm-cluster llm
```

This removes the cluster containers and networks and restores
`~/.kube/config`. The PoC directory with its keys, journal and reports stays,
so `e2e` brings it back. Lower Docker Desktop's memory again afterwards if
you need it for something else.
