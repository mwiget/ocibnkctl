# runner.Dockerfile — the "ocibnkctl-tools-runner" image
#
# Published to ghcr.io/mwiget/ocibnkctl-tools-runner and pinned (by digest) from
# the bnkctl-index artifact manifest (tools/ocibnkctl/bnkforge.artifact.json)
# that BNK Forge deploys as a container-runner module. The image carries
# ocibnkctl plus the tools it shells out to (docker CLI, kubectl, helm, git) and
# runs non-root (uid 1000), as BNK Forge's container engine requires — it refuses
# to start a root image (see the BNK Forge authoring guide §12).
#
# The binary is NOT built here: it is downloaded from the matching GitHub release
# and checksum-verified, so the release tag must already be published (goreleaser
# runs on tag push) BEFORE this image is built.
#
# Build (single-arch, matching the published image) via the Makefile:
#   make runner-image RUNNER_VERSION=2.3.1-10 PUSH=1
# or multi-arch directly:
#   docker buildx build --platform linux/amd64,linux/arm64 \
#     --build-arg OCIBNKCTL_VERSION=2.3.1-10 \
#     -t ghcr.io/mwiget/ocibnkctl-tools-runner:2.3.1-10 -f runner.Dockerfile --push .
#
# See docs/RELEASE.md for the full release → image → bnkctl-index → BNK Forge chain.

FROM alpine:3.21

# OCIBNKCTL_VERSION is the release tag WITHOUT the leading "v" (e.g. 2.3.1-10).
# TARGETARCH is supplied automatically by buildx (amd64 / arm64).
ARG OCIBNKCTL_VERSION
ARG TARGETARCH

RUN apk add --no-cache docker-cli kubectl helm git ca-certificates make openssl python3

# Download + checksum-verify the released ocibnkctl binary for the target arch.
RUN cd /tmp \
    && wget -q "https://github.com/mwiget/ocibnkctl/releases/download/v${OCIBNKCTL_VERSION}/ocibnkctl_${OCIBNKCTL_VERSION}_linux_${TARGETARCH}.tar.gz" \
    && wget -q "https://github.com/mwiget/ocibnkctl/releases/download/v${OCIBNKCTL_VERSION}/checksums.txt" \
    && grep "ocibnkctl_${OCIBNKCTL_VERSION}_linux_${TARGETARCH}.tar.gz" checksums.txt | sha256sum -c - \
    && tar -xzf "ocibnkctl_${OCIBNKCTL_VERSION}_linux_${TARGETARCH}.tar.gz" -C /usr/local/bin ocibnkctl \
    && rm -f /tmp/ocibnkctl_* /tmp/checksums.txt \
    && /usr/local/bin/ocibnkctl version

# Non-root runner (uid 1000 matches the BNK Forge workspace owner) + the /state
# mount point the artifact manifest declares as the persistent workspace.
RUN adduser -D -u 1000 -h /home/runner runner \
    && mkdir -p /state \
    && chown runner:runner /state

ENV HOME=/home/runner
USER runner
