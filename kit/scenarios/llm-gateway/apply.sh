#!/bin/sh
# The BNK AI gateway end to end: a mock LLM, then llm-setup.sh as a Job
# against the gateway API (tenant, provider, model, principal, route on VIP,
# a virtual key scoped to MODEL). Saves the virtual key to KEY_FILE.
. "$(dirname "$0")/../../lib/common.sh"

kit_require ai-gateway anycast-bgp
kit_apply manifests/01-mock-llm.yaml.tmpl
kubectl -n "$NAMESPACE" wait --for=condition=Available deployment/mock-llm --timeout=3m >&2 ||
	kit_die "mock-llm not Available"

# The Job reads the platform admin password and the provider key from Secrets
# in its own namespace. Neither value is ever on a command line or in the
# output: they only pass through 0600 files and pipes.
umask 077
work=$(mktemp -d) || kit_die "mktemp"
trap 'rm -rf "$work"' EXIT
kubectl -n "$FLO_NAMESPACE" get secret "$PLATFORM_ADMIN_SECRET" -o jsonpath='{.data.password}' |
	base64 --decode >"$work/password" && [ -s "$work/password" ] ||
	kit_die "read $FLO_NAMESPACE/$PLATFORM_ADMIN_SECRET (FLO creates it when the AI gateway is enabled)"
printf sk-mock >"$work/api-key"
secret() {
	kubectl -n "$NAMESPACE" create secret generic "$1" --from-file="$2=$work/$2" --dry-run=client -o yaml |
		kubectl apply -f - >&2 || kit_die "apply Secret $1"
}
secret llm-admin password
secret llm-provider api-key
kubectl -n "$NAMESPACE" create configmap llm-setup --from-file=llm-setup.sh=manifests/llm-setup.sh \
	--dry-run=client -o yaml | kubectl apply -f - >&2 || kit_die "apply llm-setup ConfigMap"

# A Job's pod template is immutable: replace any previous run.
kubectl -n "$NAMESPACE" delete job llm-setup --ignore-not-found --wait=true >&2
kit_apply manifests/02-setup-job.yaml.tmpl
job_done() {
	st=$(kubectl -n "$NAMESPACE" get job llm-setup -o jsonpath='{.status.succeeded}/{.status.failed}')
	case $st in 1/* | */1) return 0 ;; esac
	return 1
}
kit_retry 180 3 job_done
logs=$(kubectl -n "$NAMESPACE" logs job/llm-setup 2>&1)
# The key goes to KEY_FILE, never to the log.
printf '%s\n' "$logs" | sed "s|^VKEY=.*|VKEY=<saved to $KEY_FILE>|" | sed 's/^/  /' >&2
case $st in 1/*) ;; *) kit_die "llm-setup Job ${st:+did not succeed ($st)}" ;; esac
key=$(printf '%s\n' "$logs" | sed -n 's/^VKEY=\([^[:space:]]*\)[[:space:]]*$/\1/p' | tail -1)
[ -n "$key" ] || kit_die "llm-setup printed no virtual key"
mkdir -p "$(dirname "$KEY_FILE")" && printf '%s\n' "$key" >"$KEY_FILE" || kit_die "write $KEY_FILE"
