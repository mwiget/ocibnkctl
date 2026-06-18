# Minimal image for the tmm-stat-exporter sidecar. Static CGO-free Go build, so
# a scratch base suffices. Build context is the repo root:
#
#   CGO_ENABLED=0 GOOS=linux GOARCH=$(go env GOARCH) go build -o bin/tmm-stat-exporter ./cmd/tmm-stat-exporter
#   docker build -f cmd/tmm-stat-exporter/Dockerfile -t tmm-stat-exporter:dev .
#
# GOARCH must match the k3s nodes or the pod gets "exec format error". The
# Makefile's exporter-image target defaults the arch to the host GOARCH.
FROM scratch
COPY bin/tmm-stat-exporter /tmm-stat-exporter
USER 65532:65532
ENTRYPOINT ["/tmm-stat-exporter"]
