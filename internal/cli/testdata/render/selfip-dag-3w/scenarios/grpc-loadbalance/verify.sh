#!/bin/sh
# Verify the GRPCRoute control plane, and gRPC through the L4Route data plane.
. "$(dirname "$0")/../../lib/common.sh"

kit_check "grpcbin Deployment Available" \
	kubectl -n "$NAMESPACE" wait --for=condition=Available deployment/grpcbin --timeout=5m
kit_check "Gateway Programmed=True" \
	kubectl -n "$NAMESPACE" wait --for=condition=Programmed gateway/scn-grpc-gateway --timeout=5m
kit_eq "GRPCRoute Accepted=True" True "$(kubectl -n "$NAMESPACE" get grpcroute/scn-grpc-route \
	-o jsonpath='{.status.parents[0].conditions[?(@.type=="Accepted")].status}')"
kit_check "L4 Gateway Programmed=True" \
	kubectl -n "$NAMESPACE" wait --for=condition=Programmed gateway/scn-grpc-l4-gateway --timeout=5m
kit_eq "L4Route ResolvedRefs=True (plain-TCP backend bound)" True "$(kubectl -n "$NAMESPACE" get l4route/scn-grpc-l4-route \
	-o jsonpath='{.status.parents[0].conditions[?(@.type=="ResolvedRefs")].status}')"

bgp="FRR BGP table has $VIP/32 advertised by TMM"
bgp4="FRR BGP table has $L4_VIP/32 (L4 Gateway) advertised by TMM"
tool="grpcurl available on the edge client"
glist="grpcurl list via Gateway (informational, RST_STREAM expected)"
l4list="grpcurl list via L4Route Gateway returns grpcbin.GRPCBin (gRPC works over L4)"
l4unary="grpcurl unary grpcbin.GRPCBin/Index via L4Route returns 'gRPC testing server'"
if ! edge_enabled; then
	edge_skip "$bgp" "$tool" "$glist" "$bgp4" "$l4list" "$l4unary"
	kit_done "GRPCRoute + L4Route reconciled"
fi
edge_check_vip "$bgp" "$VIP"
edge_check_vip "$bgp4" "$L4_VIP"

# The netshoot client has no grpcurl: fetch the pinned release once and
# mount it in. An external client brings its own.
GRPCURL=grpcurl
if [ "$EDGE_DRIVER" = ocibnkctl ]; then
	case $(uname -m) in
	x86_64 | amd64) arch=x86_64 sha=$GRPCURL_SHA_X86_64 ;;
	aarch64 | arm64) arch=arm64 sha=$GRPCURL_SHA_ARM64 ;;
	*) arch= ;;
	esac
	dir=$KIT_CACHE/grpcurl-$GRPCURL_VERSION
	if [ -z "$arch" ]; then
		kit_fail "$tool" "no grpcurl release for $(uname -m)"
	elif (kit_fetch "https://github.com/fullstorydev/grpcurl/releases/download/v$GRPCURL_VERSION/grpcurl_${GRPCURL_VERSION}_linux_$arch.tar.gz" \
		"$sha" "$dir/grpcurl.tgz" && tar -xzf "$dir/grpcurl.tgz" -C "$dir" grpcurl) 2>&1; then
		EDGE_RUN_ARGS="-v $dir/grpcurl:/grpcurl:ro"
		GRPCURL=/grpcurl
		kit_pass "$tool" "$dir/grpcurl (SHA-256 verified)"
	else
		kit_fail "$tool" "download or checksum failed"
	fi
else
	kit_check "$tool" edge_client sh -c 'command -v grpcurl'
fi

# The GRPCRoute data plane is known to reset streams (docs/grpc-route-
# investigation.md); record what it does, don't require it.
kit_pass "$glist" "$(edge_client "$GRPCURL" -plaintext -max-time 10 "$VIP:$PORT" list 2>&1)"

grpc_l4() {
	out=$(edge_client "$GRPCURL" -plaintext -max-time 10 "$@" 2>&1) || return 1
}
if kit_retry 90 5 grpc_l4 "$L4_VIP:$L4_PORT" list; then kit_contains "$l4list" grpcbin.GRPCBin "$out"; else kit_fail "$l4list" "$out"; fi
if kit_retry 90 5 grpc_l4 -d '{}' "$L4_VIP:$L4_PORT" grpcbin.GRPCBin/Index; then
	kit_contains "$l4unary" "gRPC testing server" "$out"
else
	kit_fail "$l4unary" "$out"
fi

kit_done "gRPC load-balanced end-to-end via L4Route (TCP); GRPCRoute control plane reconciled but its L7 data plane RST_STREAMs (informational) — see Description"
