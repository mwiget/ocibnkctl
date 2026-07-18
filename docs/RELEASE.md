# Releasing ocibnkctl (and shipping it to BNK Forge)

ocibnkctl reaches users two ways:

1. **Standalone binaries** — a GitHub release (built by goreleaser on tag push).
2. **A BNK Forge container-runner module** — the `ocibnkctl-tools-runner` image
   wraps the release binary and is pinned by digest from the
   [`bnkctl-index`](https://github.com/mwiget/bnkctl-index) catalog repo, which
   BNK Forge syncs and deploys.

This is the full chain for cutting a new version. The version is **git-tag
driven** — `internal/version.Version` defaults to `dev` and is stamped from the
tag by goreleaser / the Makefile `LDFLAGS`; there is no version constant to edit.

```
ocibnkctl tag  ──goreleaser──▶  GitHub release (binaries + checksums)
      │
      └── runner.Dockerfile downloads that binary ──▶  ghcr.io/mwiget/ocibnkctl-tools-runner@sha256:…
                                                              │
                                    bnkctl-index pins the digest ──▶ artifact/pack + blueprint bump
                                                              │
                                              BNK Forge syncs source + reimports blueprint release
```

## Tag naming

Tags are `v2.3.1-N` — BNK is hard-pinned to 2.3.1, and `N` is the ocibnkctl
packaging counter (`git tag --sort=-creatordate | head`). Bump `N` for every
release.

## Step 1 — tag + publish the binaries

From a clean `main` that has the changes you want to ship:

```bash
git checkout main && git pull
git tag -a v2.3.1-10 -m "ocibnkctl v2.3.1-10"
git push origin v2.3.1-10          # release.yml runs goreleaser
gh run watch                        # wait for the release job to finish
gh release view v2.3.1-10           # confirm the tar.gz + checksums.txt assets exist
```

The runner image (next step) downloads the binary **from this release**, so the
assets must be live before you build it.

## Step 2 — build + push the runner image

`runner.Dockerfile` is checked in; the Makefile wraps the buildx call. It
downloads + checksum-verifies the released binary, so pass the same version:

```bash
make runner-image RUNNER_VERSION=2.3.1-10 PUSH=1
# capture the pushed manifest digest for the artifact manifest:
docker buildx imagetools inspect ghcr.io/mwiget/ocibnkctl-tools-runner:2.3.1-10 \
  --format '{{.Manifest.Digest}}'
```

The image is single-platform `linux/amd64` by default (matching prior releases);
build multi-arch with `RUNNER_PLATFORM=linux/amd64,linux/arm64` if needed.
`ghcr.io` push auth comes from your `docker login ghcr.io`.

## Step 3 — bump `bnkctl-index` (pin the new digest)

In the [`bnkctl-index`](https://github.com/mwiget/bnkctl-index) checkout, per its
own "Updating" note and the BNK Forge authoring guide's **bump-on-any-edit** rule
(§9.1 / §10 of *How to write CI container runner modules and blueprints for BNK
Forge*):

- `tools/ocibnkctl/bnkforge.artifact.json` — bump `version`, set the new
  `container_image.digest`, update the `wraps ocibnkctl v…` description.
- `tools/ocibnkctl/bnkforge.pack.json` — bump `module.version` to match.
- `blueprints/k3s-bnk-demo/forge-blueprint.json` — bump `blueprint.version` and
  re-pin the `tools/ocibnkctl` module `version`.

The catalog row is a content hash, so an unbumped edit is a `version_conflict` on
sync — always bump. Commit + push.

## Step 4 — sync into BNK Forge

On the BNK Forge instance (operator/admin token), re-sync the module source, then
re-sync + re-import the blueprint release so new projects pick up the version:

```bash
curl -sk -X POST "$BNK/api/module-sources/<id>/sync"                 -H "Authorization: Bearer $TOKEN"
curl -sk -X POST "$BNK/api/blueprint-catalog/sources/<id>/sync"      -H "Authorization: Bearer $TOKEN"
curl -sk -X POST "$BNK/api/blueprint-catalog/releases/<rel>/import"  -H "Authorization: Bearer $TOKEN"
```

Existing project modules stay pinned to the version they were created with;
upgrading one is an explicit operator action (**Change Version** in the module
menu / `POST /api/project-modules/<id>/change-version`). See the BNK Forge repo's
runbook (`docs/RUNNER_MODULE_UPDATE.md`) for the receiving side.
