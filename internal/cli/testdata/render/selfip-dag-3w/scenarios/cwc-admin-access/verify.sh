#!/bin/sh
# Verify the CWC admin API needs both mTLS and the bearer token.
. "$(dirname "$0")/../../lib/common.sh"

if ! kit_check "probe Deployment Available" \
	kubectl -n "$NAMESPACE" wait --for=condition=Available deployment/probe --timeout=2m; then
	kit_done "probe never became available"
fi
probe=$(kubectl -n "$NAMESPACE" get pod -l app=probe --field-selector=status.phase=Running \
	-o jsonpath='{.items[0].metadata.name}')
if [ -z "$probe" ]; then
	kit_fail "probe pod Running" "none found"
	kit_done "no probe pod"
fi

# cwc_get [TOKEN_EXPR] — GET /status over mTLS from the probe, printing the
# body and a final __HTTP_CODE__=<code> line. TOKEN_EXPR is evaluated in the
# pod (so the real token never leaves it); empty sends no Authorization.
cwc_get() {
	auth=
	[ -n "${1:-}" ] && auth="-H \"Authorization: Bearer $1\""
	kubectl -n "$NAMESPACE" exec "$probe" -- sh -c \
		"curl -sS --max-time 8 -w '\n__HTTP_CODE__=%{http_code}' --cacert /certs/ca-root-cert \
		--cert /certs/client-cert --key /certs/client-key $auth $CWC_URL" 2>&1
}
authorized() {
	body=$(cwc_get '$(cat /token/token)')
	case $body in *"__HTTP_CODE__=200"*) ;; *) return 1 ;; esac
	case $body in *LicenseStatus*) ;; *) return 1 ;; esac
	case $body in *DigitalAssetID*) return 0 ;; esac
	return 1
}
if kit_retry 90 5 authorized; then
	kit_pass "Authenticated GET /status returns HTTP 200 with license JSON" "$body"
else
	kit_fail "Authenticated GET /status returns HTTP 200 with license JSON" "$body"
fi

# Rejected = anything but 200 (a 4xx, or the connection refused after mTLS).
rejected() {
	case $2 in
	*"__HTTP_CODE__=200"*) kit_fail "$1" "$2" ;;
	*) kit_pass "$1" "$2" ;;
	esac
}
rejected "Unauthenticated GET /status is rejected (no token in header)" "$(cwc_get)"
rejected "Bogus token is rejected (constant-time compare)" "$(cwc_get not-the-real-token)"

kit_done "CWC admin API gated by mTLS + bearer token; authenticated request 200, unauthenticated and bogus-token both rejected"
