# LLM gateway on a MacBook Pro

How to run the BNK 2.4.1 AI/LLM gateway demo on one Apple-silicon MacBook
Pro with Docker Desktop: a k3s cluster with BNK and TMM, F5's console, a mock
LLM, virtual keys and request metering. No GPU or LLM API key needed.

Measured on 2026-10-21 on a **MacBook Pro, Apple M4 Pro, 12 cores, 24 GB,
macOS 26.7**, Docker Desktop (engine 29.8), on the F5 network behind
Netskope, with ocibnkctl v2.4.1-3. The general guide, with every option and
the API, is [llm-gateway-howto.md](llm-gateway-howto.md).

## Contents

- [What it needs](#what-it-needs)
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

v2.4.1-3 or later: earlier releases fail on Docker Desktop (see
[Troubleshooting](#troubleshooting-on-a-mac)).

```bash
V=2.4.1-3
curl -sSL "https://github.com/mwiget/ocibnkctl/releases/download/v$V/ocibnkctl_${V}_darwin_arm64.tar.gz" | tar xz ocibnkctl
install -m 0755 ocibnkctl ~/.local/bin/
ocibnkctl version                       # ocibnkctl 2.4.1-3 ...
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
(and `demo-password.sh`, `admin-password.sh` for steps 6 and 8):

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

The profile puts `ask.sh` in the PoC directory (ocibnkctl after v2.4.1-3;
on v2.4.1-3, copy the scripts from `internal/embedded/profiles/llm-gateway/`
in the repo). It sends one prompt as `demo-app`, with its virtual key, through
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
read its admin password with
`kubectl get secret aitenant-acme-admin -o jsonpath='{.data.password}' | base64 -d`,
and hand them that. They sign in with tenant `acme` → **Sign in as tenant
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
