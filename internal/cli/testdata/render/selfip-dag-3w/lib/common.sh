# common.sh — shared helpers for kit scenario scripts (POSIX sh).
#
# Source it as the first thing in apply.sh / verify.sh / cleanup.sh:
#
#   . "$(dirname "$0")/../../lib/common.sh"
#
# It changes into the scenario directory, loads scenario.env (the scenario's
# variables and their defaults; anything already set in the environment
# wins), and loads edge.sh and bnk.sh. Talks to whatever cluster kubectl
# points at (KUBECONFIG).
#
# verify.sh contract: each check prints ONE JSON line on stdout,
#   {"description":"...","ok":true|false,"got":"...","skipped":true}
# (via kit_pass / kit_fail / kit_skip / kit_check / kit_eq / kit_contains),
# then kit_done prints {"summary":"..."} and exits 0 when nothing failed,
# 1 otherwise; apply.sh exits 3 (kit_not_applicable) when the cluster
# lacks what the scenario needs. Everything else, logs included, goes to
# stderr. ocibnkctl
# parses those lines into its scenario report; a human just reads them.

# Preset both to source this from outside scenarios/<name>/ (lib/edge-pod.sh).
KIT_SCENARIO_DIR=${KIT_SCENARIO_DIR:-$(cd "$(dirname "$0")" && pwd)}
KIT_LIB=${KIT_LIB:-$(cd "$KIT_SCENARIO_DIR/../../lib" && pwd)}
# Where the caller ran from: relative paths they pass (KUBECONFIG, the
# platform's FAR_KEY / JWT_FILE) mean relative to here, not to the script.
KIT_CALLER_DIR=${KIT_CALLER_DIR:-$PWD}
case ${KUBECONFIG:-} in
'' | /*) ;;
*) KUBECONFIG=$KIT_CALLER_DIR/$KUBECONFIG && export KUBECONFIG ;;
esac
cd "$KIT_SCENARIO_DIR" || exit 2

KIT_FAILED=0
KIT_SKIPPED=0
# Where scenarios keep downloaded tools (e.g. grpcurl) between runs.
KIT_CACHE=${KIT_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/bnk-kit}
# Where scenarios keep what apply.sh hands to verify.sh (e.g. a generated
# API key), by scenario name. ocibnkctl points it at <PoC>/artifacts.
KIT_STATE=${KIT_STATE:-$KIT_CACHE/state}

kit_log() { printf '%s\n' "$*" >&2; }

# kit_die MSG — abort the script (exit 2): a setup error, not a failed check.
kit_die() {
	kit_log "error: $*"
	exit 2
}

# kit_not_applicable REASON — stop (exit 3): this cluster lacks what the
# scenario is about (another data-plane mode, no AI gateway, an older BNK).
# ocibnkctl reports the scenario as skipped, not failed.
kit_not_applicable() {
	kit_log "not applicable: $*"
	exit 3
}

# _kit_json DESC OK GOT [skipped] — one assertion line. Whitespace and
# control characters collapse to single spaces, like the Go side's oneLine.
_kit_json() {
	_KJ_D=$1 _KJ_OK=$2 _KJ_SKIP=${4:-} \
		_KJ_GOT=$(printf '%s' "${3:-}" | tr '\001-\037' ' ') \
		awk 'function esc(s) {
			gsub(/\\/, "\\\\", s); gsub(/"/, "\\\"", s)
			gsub(/  +/, " ", s); sub(/^ /, "", s); sub(/ $/, "", s)
			if (length(s) > 300) s = substr(s, 1, 300) "..."
			return s
		}
		BEGIN {
			printf "{\"description\":\"%s\",\"ok\":%s", esc(ENVIRON["_KJ_D"]), ENVIRON["_KJ_OK"]
			if (ENVIRON["_KJ_GOT"] != "") printf ",\"got\":\"%s\"", esc(ENVIRON["_KJ_GOT"])
			if (ENVIRON["_KJ_SKIP"] != "") printf ",\"skipped\":true"
			print "}"
		}'
}

# Assertion helpers return the check's outcome, so `if ! kit_check ...`
# can stop early when later checks depend on this one.
kit_pass() { _kit_json "$1" true "${2:-}"; }
kit_fail() {
	_kit_json "$1" false "${2:-}"
	KIT_FAILED=$((KIT_FAILED + 1))
	return 1
}
# kit_skip DESC WHY — a check that could not run here (e.g. no edge peer).
kit_skip() {
	_kit_json "$1" true "${2:-}" skipped
	KIT_SKIPPED=$((KIT_SKIPPED + 1))
}

# kit_check DESC CMD... — pass when CMD succeeds; its output is the "got".
kit_check() {
	_kc_d=$1
	shift
	if _kc_o=$("$@" 2>&1); then kit_pass "$_kc_d" "$_kc_o"; else kit_fail "$_kc_d" "$_kc_o"; fi
}

# kit_eq DESC WANT GOT
kit_eq() { if [ "$2" = "$3" ]; then kit_pass "$1" "$3"; else kit_fail "$1" "$3"; fi; }

# kit_count DESC OK TOTAL [DETAIL] — pass when OK == TOTAL; the "got" is
# "OK/TOTAL", plus DETAIL (e.g. the last error) when it fails.
kit_count() {
	if [ "$2" -eq "$3" ]; then
		kit_pass "$1" "$2/$3"
	else
		kit_fail "$1" "$2/$3${4:+ — $4}"
	fi
}

# kit_contains DESC NEEDLE HAYSTACK
kit_contains() {
	case $3 in
	*"$2"*) kit_pass "$1" "$3" ;;
	*) kit_fail "$1" "$3" ;;
	esac
}

# kit_done SUMMARY — print the summary line; exit 1 if any check failed.
kit_done() {
	_kd_s=$1
	[ "$KIT_SKIPPED" -gt 0 ] && _kd_s="$_kd_s ($KIT_SKIPPED check(s) skipped)"
	_KJ_S=$_kd_s awk 'BEGIN { s = ENVIRON["_KJ_S"]; gsub(/\\/, "\\\\", s); gsub(/"/, "\\\"", s); printf "{\"summary\":\"%s\"}\n", s }'
	[ "$KIT_FAILED" -eq 0 ] && exit 0
	exit 1
}

# kit_b64d — base64-decode stdin. Hosts disagree on the flag: GNU, busybox
# (Alpine, the BNK Forge runner image) and macOS 13+ take -d; busybox
# rejects --decode, which older macOS needs.
kit_b64d() {
	if [ -z "${_KIT_B64D:-}" ]; then
		if printf 'aA==' | base64 -d >/dev/null 2>&1; then _KIT_B64D=-d; else _KIT_B64D=--decode; fi
	fi
	base64 "$_KIT_B64D"
}

# kit_fetch URL SHA256 FILE — download URL to FILE unless FILE already has
# that checksum; aborts if the download does not match. curl, or wget where
# there is no curl (busybox systems).
kit_fetch() {
	_kf_sum() { if command -v sha256sum >/dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1; }
	[ -f "$3" ] && [ "$(_kf_sum "$3")" = "$2" ] && return 0
	mkdir -p "$(dirname "$3")" || kit_die "mkdir for $3"
	if command -v curl >/dev/null; then
		curl -fsSL -o "$3.part" "$1"
	else
		wget -q -O "$3.part" "$1"
	fi || { rm -f "$3.part"; kit_die "download $1"; }
	[ "$(_kf_sum "$3.part")" = "$2" ] || { rm -f "$3.part"; kit_die "checksum mismatch for $1"; }
	mv "$3.part" "$3"
}

# kit_retry SECONDS INTERVAL CMD... — rerun CMD until it succeeds or
# SECONDS have passed. Returns CMD's success.
kit_retry() {
	_kr_end=$(($(date +%s) + $1))
	_kr_iv=$2
	shift 2
	while :; do
		"$@" && return 0
		[ "$(date +%s)" -ge "$_kr_end" ] && return 1
		sleep "$_kr_iv"
	done
}

# kit_apply FILE... — kubectl apply each file; *.tmpl files are rendered
# first (lib/render.sh, from the environment). Aborts on failure.
kit_apply() {
	for _ka_f in "$@"; do
		kit_log "apply $_ka_f"
		case $_ka_f in
		*.tmpl) sh "$KIT_LIB/render.sh" "$_ka_f" | kubectl apply -f - >&2 ;;
		*) kubectl apply -f "$_ka_f" >&2 ;;
		esac || kit_die "apply $_ka_f failed"
	done
}

# kit_delete_namespace NS — delete NS (and wait); a missing one is fine.
kit_delete_namespace() { kubectl delete namespace "$1" --ignore-not-found >&2; }

# kit_copy_secret NAME FROM_NS TO_NS — copy a Secret's data into another
# namespace (a pod can only mount Secrets from its own namespace).
kit_copy_secret() {
	_kcs_data=$(kubectl -n "$2" get secret "$1" \
		-o go-template='{{range $k, $v := .data}}  {{$k}}: {{$v}}{{"\n"}}{{end}}') ||
		kit_die "read secret $2/$1"
	[ -n "$_kcs_data" ] || kit_die "secret $2/$1 has no data"
	printf 'apiVersion: v1\nkind: Secret\nmetadata:\n  name: %s\n  namespace: %s\ntype: Opaque\ndata:\n%s\n' \
		"$1" "$3" "$_kcs_data" | kubectl apply -f - >&2 || kit_die "copy secret $1 to $3"
}

# kit_require CAP... — preflight against the cluster itself (not poc.yaml).
# A capability the cluster does not have (crd, anycast-bgp, ai-gateway,
# gateway-settings) makes the scenario not applicable (kit_not_applicable);
# one it has but that is not working (a GatewayClass not Accepted, a missing
# Secret) is an error (kit_die):
#   crd:<name>             the CRD exists
#   gatewayclass:<name>    the GatewayClass exists and is Accepted
#   secret:<ns>/<name>     the Secret exists
#   anycast-bgp            the CNEInstance runs the anycast-bgp data plane:
#                          a NAD on TMM and TMM_MAPRES_ADDL_VETHS_ON_DP=FALSE,
#                          so net1 keeps its kernel IP for BGP
#   ai-gateway             the CNEInstance enables BNK's AI gateway
#                          (spec.aiGateway.enabled, BNK 2.4.1+)
#   legacy-spk             the CNE controller runs BNK 2.4's legacy SPK network
#                          model (USE_GATEWAY_SETTINGS=false, selfip-dag),
#                          the only one that reconciles F5SPKVlan / Vrf
#   gateway-settings       the CNE controller runs the Infra/GatewaySettings
#                          network model (USE_GATEWAY_SETTINGS=true), which
#                          address-less Gateways need
kit_require() {
	for _kq in "$@"; do
		case $_kq in
		crd:*)
			kubectl get crd "${_kq#crd:}" >/dev/null 2>&1 ||
				kit_not_applicable "precondition $_kq: CRD ${_kq#crd:} not installed"
			;;
		gatewayclass:*)
			_kq_s=$(kubectl get gatewayclass "${_kq#gatewayclass:}" \
				-o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}' 2>&1)
			[ "$_kq_s" = True ] || kit_die "precondition $_kq: not Accepted (${_kq_s:-absent})"
			;;
		secret:*)
			_kq_r=${_kq#secret:}
			kubectl -n "${_kq_r%%/*}" get secret "${_kq_r#*/}" >/dev/null 2>&1 ||
				kit_die "precondition $_kq: Secret not found"
			;;
		anycast-bgp)
			_kq_m=$(kubectl get cneinstances -A -o \
				jsonpath='{.items[0].spec.advanced.tmm.env[?(@.name=="TMM_MAPRES_ADDL_VETHS_ON_DP")].value}' 2>&1)
			_kq_n=$(kubectl get cneinstances -A -o jsonpath='{.items[0].spec.networkAttachments[*]}' 2>&1)
			[ "$_kq_m" = FALSE ] && [ -n "$_kq_n" ] ||
				kit_not_applicable "precondition anycast-bgp: needs a NAD on TMM and TMM_MAPRES_ADDL_VETHS_ON_DP=FALSE (got mapres=${_kq_m:-unset} networkAttachments=${_kq_n:-none}); otherwise mapres takes net1 and BGP has no source address"
			;;
		ai-gateway)
			[ "$(kubectl get cneinstances -A -o jsonpath='{.items[0].spec.aiGateway.enabled}' 2>&1)" = true ] ||
				kit_not_applicable "precondition ai-gateway: CNEInstance spec.aiGateway.enabled is not true (ocibnkctl: bnk.ai_gateway.enabled in poc.yaml, then deploy cne)"
			;;
		legacy-spk)
			_kq_g=$(kubectl get cneinstances -A -o \
				jsonpath='{.items[0].spec.advanced.cneController.env[?(@.name=="USE_GATEWAY_SETTINGS")].value}' 2>&1)
			[ "$_kq_g" = false ] ||
				kit_not_applicable "precondition legacy-spk: USE_GATEWAY_SETTINGS=${_kq_g:-unset}; only BNK 2.4's legacy SPK model (selfip-dag) reconciles F5SPKVlan, elsewhere the CRs are accepted and ignored"
			;;
		gateway-settings)
			_kq_g=$(kubectl get cneinstances -A -o \
				jsonpath='{.items[0].spec.advanced.cneController.env[?(@.name=="USE_GATEWAY_SETTINGS")].value}' 2>&1)
			[ "$_kq_g" = true ] ||
				kit_not_applicable "precondition gateway-settings: USE_GATEWAY_SETTINGS=${_kq_g:-unset}; the legacy SPK model (selfip-dag) has no Infra IPAM, so address-less Gateways never get an address"
			;;
		*) kit_die "kit_require: unknown capability $_kq" ;;
		esac
	done
}

# Load the scenario's variables and the other helpers last: edge.sh validates
# its settings with kit_die as it loads.
# shellcheck source=/dev/null
if [ -f ./scenario.env ]; then . ./scenario.env; fi
. "$KIT_LIB/edge.sh"
. "$KIT_LIB/bnk.sh"
