# Minimal image for the tmm-stat-webhook (mutating admission webhook). Static
# CGO-free Go build; reads its TLS serving cert from a mounted secret at /tls.
#
#   CGO_ENABLED=0 GOOS=linux GOARCH=$(go env GOARCH) go build -o bin/tmm-stat-webhook ./cmd/tmm-stat-webhook
#   docker build -f cmd/tmm-stat-webhook/Dockerfile -t tmm-stat-webhook:dev .
FROM scratch
COPY bin/tmm-stat-webhook /tmm-stat-webhook
USER 65532:65532
ENTRYPOINT ["/tmm-stat-webhook"]
