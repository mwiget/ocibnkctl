.PHONY: all build build-linux-arm64 build-darwin-arm64 build-all install test clean tidy fmt vet smoke release release-linux-amd64 release-linux-arm64 release-darwin-arm64 exporter-image webhook-image telemetry-images

all: build-all

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BNK     := 2.3.0

LDFLAGS := -X 'github.com/mwiget/ocibnkctl/internal/version.Version=$(VERSION)' \
           -X 'github.com/mwiget/ocibnkctl/internal/version.Commit=$(COMMIT)' \
           -X 'github.com/mwiget/ocibnkctl/internal/version.BuildDate=$(DATE)' \
           -X 'github.com/mwiget/ocibnkctl/internal/version.BNKVersion=$(BNK)'

build:
	@mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/ocibnkctl ./cmd/ocibnkctl

build-linux-arm64:
	@mkdir -p bin
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
	    go build -trimpath -ldflags "$(LDFLAGS)" \
	    -o bin/ocibnkctl-linux-arm64 ./cmd/ocibnkctl

build-darwin-arm64:
	@mkdir -p bin
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 \
	    go build -trimpath -ldflags "$(LDFLAGS)" \
	    -o bin/ocibnkctl-darwin-arm64 ./cmd/ocibnkctl

build-all: build build-linux-arm64

# --- tmm telemetry images (exporter sidecar + injection webhook) ---------
# Built locally and imported into the k3s nodes by `deploy telemetry` (no
# registry behind them). IMG_ARCH must match the k3s nodes (default: host arch).
IMG_ARCH        ?= $(shell go env GOARCH)
EXPORTER_IMAGE  ?= tmm-stat-exporter:dev
WEBHOOK_IMAGE   ?= tmm-stat-webhook:dev

exporter-image:
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=$(IMG_ARCH) go build -trimpath -ldflags "$(LDFLAGS)" -o bin/tmm-stat-exporter ./cmd/tmm-stat-exporter
	docker build -f cmd/tmm-stat-exporter/Dockerfile -t $(EXPORTER_IMAGE) .

webhook-image:
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=$(IMG_ARCH) go build -trimpath -ldflags "$(LDFLAGS)" -o bin/tmm-stat-webhook ./cmd/tmm-stat-webhook
	docker build -f cmd/tmm-stat-webhook/Dockerfile -t $(WEBHOOK_IMAGE) .

# Build both telemetry images (deploy telemetry imports them into the nodes).
telemetry-images: exporter-image webhook-image

# --- release artifacts --------------------------------------------------
#
# Tagging convention. The binary is hard-pinned to BNK 2.3.0
# (BNKVersion=2.3.0, baked into ldflags here and in .goreleaser.yaml), so
# every release tag carries the `v2.3.0` prefix. Tool-level changes that
# need a fresh binary — bug fixes, new scenarios, doc bumps — get an
# incrementing suffix, NOT a new MAJOR.MINOR.PATCH:
#
#   v2.3.0       first cut for BNK 2.3.0
#   v2.3.0-1     next binary, same BNK release
#   v2.3.0-2     ...and so on
#
# This mirrors github.com/mwiget/dpubnkctl. Pushing any `v*` tag triggers
# the GitHub Actions goreleaser workflow (.github/workflows/release.yml),
# which is the canonical release path:
#
#   git tag v2.3.0-1 && git push origin v2.3.0-1
#
# The `make release` targets below are the manual fallback — they produce
# the same versioned, sha256-checksummed binaries locally. Run from a
# clean checkout of the tag so $(VERSION) resolves (e.g. to v2.3.0-1):
#
#   git checkout v2.3.0-1
#   make release
#   gh release upload v2.3.0-1 bin/ocibnkctl-v2.3.0-1-* --clobber
release-linux-amd64:
	@mkdir -p bin
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
	    go build -trimpath -ldflags "$(LDFLAGS)" \
	    -o bin/ocibnkctl-$(VERSION)-linux-amd64 ./cmd/ocibnkctl
	cd bin && sha256sum ocibnkctl-$(VERSION)-linux-amd64 \
	    > ocibnkctl-$(VERSION)-linux-amd64.sha256

release-linux-arm64:
	@mkdir -p bin
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
	    go build -trimpath -ldflags "$(LDFLAGS)" \
	    -o bin/ocibnkctl-$(VERSION)-linux-arm64 ./cmd/ocibnkctl
	cd bin && sha256sum ocibnkctl-$(VERSION)-linux-arm64 \
	    > ocibnkctl-$(VERSION)-linux-arm64.sha256

release-darwin-arm64:
	@mkdir -p bin
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 \
	    go build -trimpath -ldflags "$(LDFLAGS)" \
	    -o bin/ocibnkctl-$(VERSION)-darwin-arm64 ./cmd/ocibnkctl
	cd bin && sha256sum ocibnkctl-$(VERSION)-darwin-arm64 \
	    > ocibnkctl-$(VERSION)-darwin-arm64.sha256

release: release-linux-amd64 release-linux-arm64 release-darwin-arm64

install: build
	install -m 0755 bin/ocibnkctl $(HOME)/.local/bin/ocibnkctl

test:
	go test ./...

tidy:
	go mod tidy

fmt:
	gofmt -w .

vet:
	go vet ./...

# Smoke target runs both layers:
#   Layer B — Go unit tests (`go test ./...`) catch package-level
#             regressions before the CLI binary even runs.
#   Layer A — exercises the built binary against a tmp PoC, no cluster
#             required. Each step has a pass/fail check so a regression
#             breaks the build instead of silently scrolling by.
SMOKE_DIR := /tmp/ocibnkctl-smoke-make
smoke: test build
	@echo "=== Layer A smoke (CLI) ==="
	@set -eu; \
	echo "[1] version"; ./bin/ocibnkctl version >/dev/null; \
	echo "[2] init smoke (tmp)"; rm -rf $(SMOKE_DIR); \
	  ./bin/ocibnkctl init smoke --dir $(SMOKE_DIR) --no-git >/dev/null; \
	echo "[3] poc skeleton landed"; \
	  for f in poc.yaml AGENTS.md CLAUDE.md .gitignore keys/.gitkeep \
	           artifacts/.gitkeep journal; do \
	    test -e $(SMOKE_DIR)/$$f || { echo "MISSING $$f"; exit 1; }; \
	  done; \
	echo "[4] validate (expect missing-keys error)"; \
	  if ./bin/ocibnkctl validate --poc $(SMOKE_DIR) >/tmp/smoke-validate.log 2>&1; then \
	    echo "validate should have failed (returned 0)"; exit 1; \
	  fi; \
	  grep -q "far_key_ref file" /tmp/smoke-validate.log || { echo "FAR error missing"; exit 1; }; \
	  grep -q "jwt_ref file"     /tmp/smoke-validate.log || { echo "JWT error missing"; exit 1; }; \
	echo "[5] touch fake keys + validate clean"; \
	  touch $(SMOKE_DIR)/keys/f5-far-auth-key.tgz $(SMOKE_DIR)/keys/.jwt; \
	  ./bin/ocibnkctl validate --poc $(SMOKE_DIR) | grep -q "OK"; \
	echo "[6] e2e --dry-run lists all phases (incl. conditional deploy-shrink) with auto-filled gates"; \
	  ./bin/ocibnkctl e2e --poc $(SMOKE_DIR) --dry-run > /tmp/smoke-e2e.log 2>&1; \
	  for ph in validate cluster-up deploy-prereqs deploy-flo deploy-shrink deploy-cne; do \
	    grep -q "$$ph" /tmp/smoke-e2e.log || { echo "phase $$ph missing"; exit 1; }; \
	  done; \
	  grep -q -- "--confirm-cluster smoke" /tmp/smoke-e2e.log || { echo "confirm-cluster not auto-filled"; exit 1; }; \
	  grep -q -- "--confirm-deploy smoke"  /tmp/smoke-e2e.log || { echo "confirm-deploy not auto-filled"; exit 1; }; \
	echo "[7] --yolo without --confirm-cluster errors"; \
	  if ./bin/ocibnkctl e2e --poc $(SMOKE_DIR) --yolo >/tmp/smoke-yolo.log 2>&1; then \
	    echo "missing-confirm gate broken (returned 0)"; exit 1; \
	  fi; \
	  grep -q "confirm-cluster is required" /tmp/smoke-yolo.log \
	    || { echo "missing-confirm error message missing"; exit 1; }; \
	echo "[8] doctor reports a resource floor (min baseline or small-host floor)"; \
	  ./bin/ocibnkctl doctor 2>&1 | grep -qE "min baseline|small-host floor" \
	    || { echo "doctor resource-floor line missing"; exit 1; }; \
	echo "[9] tmm_dataplane_mode=anycast-bgp validates with single-host caveat"; \
	  cp $(SMOKE_DIR)/poc.yaml $(SMOKE_DIR)/poc.yaml.bak; \
	  sed -i.tmp 's/^    demo_mode: true/    demo_mode: true\n    tmm_dataplane_mode: anycast-bgp/' $(SMOKE_DIR)/poc.yaml; \
	  ./bin/ocibnkctl validate --poc $(SMOKE_DIR) > /tmp/smoke-aa.log 2>&1; \
	  grep -q "anycast-bgp" /tmp/smoke-aa.log || { echo "anycast-bgp caveat warning missing"; exit 1; }; \
	  grep -q "OK" /tmp/smoke-aa.log || { echo "anycast-bgp should validate clean"; exit 1; }; \
	  mv $(SMOKE_DIR)/poc.yaml.bak $(SMOKE_DIR)/poc.yaml; rm -f $(SMOKE_DIR)/poc.yaml.tmp; \
	echo "PASS"

clean:
	rm -rf bin/
