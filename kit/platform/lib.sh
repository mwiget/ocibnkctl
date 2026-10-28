# lib.sh — helpers for kit/platform/<phase>/install.sh. Sourced after
# kit/lib/common.sh and platform.env.

# plat_abs PATH — PATH made absolute against the caller's directory.
plat_abs() {
	case $1 in
	/*) printf '%s\n' "$1" ;;
	*) printf '%s\n' "$KIT_CALLER_DIR/$1" ;;
	esac
}

# plat_b64 — base64 on one line, on GNU and BSD alike.
plat_b64() { base64 | tr -d '\n'; }

# plat_render TEMPLATE [VAR=value ...] — render a kit template (lib/render.sh)
# with the given variables on top of the environment.
plat_render() {
	_pr_t=$1
	shift
	env "$@" sh "$KIT_LIB/render.sh" "$_pr_t"
}

# plat_work — a private (0700) scratch directory, removed on exit. Secrets
# (the FAR credential, the JWT) only ever sit in files here.
plat_work() {
	[ -n "${PLAT_WORK:-}" ] && return 0
	PLAT_WORK=$(umask 077 && mktemp -d) || kit_die "mktemp"
	trap 'rm -rf "$PLAT_WORK"' EXIT
}

# far_init — read the FAR tarball into $PLAT_WORK: dockerconfig.json (the
# image-pull secret), registry-user and registry-pass (helm registry login).
# The tarball holds either a literal .dockerconfigjson (older packs) or a GCP
# service-account key, plain or base64 (cne_pull_64.json): that becomes the
# GAR "_json_key_base64:<key>" auth for $REGISTRY.
far_init() {
	plat_work
	[ -s "$PLAT_WORK/dockerconfig.json" ] && return 0
	_fi_tgz=$(plat_abs "$FAR_KEY")
	[ -f "$_fi_tgz" ] || kit_die "FAR tarball not found at $_fi_tgz (FAR_KEY)"
	mkdir "$PLAT_WORK/far" && tar -xzf "$_fi_tgz" -C "$PLAT_WORK/far" || kit_die "unpack $_fi_tgz"
	_fi_f=$(find "$PLAT_WORK/far" -type f | head -1)
	[ -n "$_fi_f" ] || kit_die "$_fi_tgz holds no files"
	if grep -q '"auths"' "$_fi_f"; then
		cp "$_fi_f" "$PLAT_WORK/dockerconfig.json"
		# "auth": base64("<user>:<password>")
		sed -n 's/.*"auth" *: *"\([^"]*\)".*/\1/p' "$_fi_f" | head -1 | base64 --decode >"$PLAT_WORK/auth" ||
			kit_die "decode the auth in $_fi_tgz"
		sed 's/:.*//' "$PLAT_WORK/auth" | tr -d '\n' >"$PLAT_WORK/registry-user"
		sed 's/^[^:]*://' "$PLAT_WORK/auth" >"$PLAT_WORK/registry-pass"
		return 0
	fi
	if tr -d ' \n\r' <"$_fi_f" | base64 --decode 2>/dev/null | grep -q service_account; then
		tr -d ' \n\r' <"$_fi_f" | base64 --decode >"$PLAT_WORK/sa.json"
	elif grep -q service_account "$_fi_f"; then
		cp "$_fi_f" "$PLAT_WORK/sa.json"
	else
		kit_die "$_fi_tgz: neither a dockerconfigjson nor a (base64) GCP service-account key"
	fi
	_fi_auth=$( (printf '_json_key_base64:' && plat_b64 <"$PLAT_WORK/sa.json") | plat_b64)
	printf '{"auths":{"%s":{"auth":"%s"}}}' "$REGISTRY" "$_fi_auth" >"$PLAT_WORK/dockerconfig.json"
	printf '_json_key' >"$PLAT_WORK/registry-user"
	cp "$PLAT_WORK/sa.json" "$PLAT_WORK/registry-pass"
}

# registry_login — helm registry login to $REGISTRY with the FAR credential
# (password on stdin).
registry_login() {
	far_init
	helm registry login "$REGISTRY" --username "$(cat "$PLAT_WORK/registry-user")" \
		--password-stdin <"$PLAT_WORK/registry-pass" >&2 || kit_die "helm registry login $REGISTRY"
}

# jwt_type — tst or prod for the JWT in JWT_FILE: by the jku host, then a
# TST- subject, then a synthetic "tst" claim (test fixtures). Only the
# header and claims are read.
jwt_type() {
	_jt_f=$(plat_abs "$JWT_FILE")
	[ -f "$_jt_f" ] || { echo unknown; return 0; }
	_jt_seg() { # decode JWT segment N (base64url, unpadded)
		_jt_s=$(tr -d ' \n\r' <"$_jt_f" | cut -d. -f"$1" | tr '_-' '/+')
		case $((${#_jt_s} % 4)) in 2) _jt_s="$_jt_s==" ;; 3) _jt_s="$_jt_s=" ;; esac
		printf '%s' "$_jt_s" | base64 --decode 2>/dev/null
	}
	_jt_h=$(_jt_seg 1 | tr 'A-Z' 'a-z')
	_jt_c=$(_jt_seg 2)
	case $_jt_h in
	*product-tst.apis.f5networks.net* | *product-s-tst.apis.f5networks.net*) echo tst; return 0 ;;
	*product.apis.f5.com* | *product-s.apis.f5.com*) echo prod; return 0 ;;
	esac
	case $(printf '%s' "$_jt_c" | tr 'a-z' 'A-Z') in
	*'"SUB":"TST-'* | *'"SUB": "TST-'* | *'"TST":TRUE'* | *'"TST":"TRUE"'* | *'"TST":"TST"'*) echo tst ;;
	*) echo prod ;;
	esac
}

# cne_vars — the CNEInstance's per-mode variables (platform/cne/
# cne-instance.yaml), from DATAPLANE_MODE:
#   standby     no NAD; mapres TRUE (BNK's stock HA shape)
#   selfip-dag  DAG NAD; mapres TRUE (net1 becomes interface 1.1 for the
#               F5SPKVlan); legacy SPK network model (USE_GATEWAY_SETTINGS=false)
#   anycast-bgp bnk-bgp NAD; mapres FALSE (net1 keeps its kernel IP for BGP)
cne_vars() {
	NAD_NAME= MAPRES_ON_DP=TRUE USE_GATEWAY_SETTINGS=true
	case $DATAPLANE_MODE in
	standby) ;;
	selfip-dag) NAD_NAME=f5-tmm-dag USE_GATEWAY_SETTINGS=false ;;
	anycast-bgp) NAD_NAME=bnk-bgp MAPRES_ON_DP=FALSE ;;
	*) kit_die "DATAPLANE_MODE=$DATAPLANE_MODE: want anycast-bgp, selfip-dag or standby" ;;
	esac
	export NAD_NAME MAPRES_ON_DP USE_GATEWAY_SETTINGS INSTANCE_NAME MANIFEST_VERSION DEPLOYMENT_SIZE \
		METRIC_SUBSYSTEM LOGGING_SUBSYSTEM AIGW_ENABLED AIGW_METRICS AIGW_CONSOLE_URL
}

# render_cne — the CNEInstance for this environment, on stdout.
render_cne() {
	cne_vars
	sh "$KIT_LIB/render.sh" "$KIT_LIB/../platform/cne/cne-instance.yaml"
}

# apply_quota_retry MANIFEST QUOTA SECONDS — kubectl apply, retrying while
# the API refuses with "status unknown for quota QUOTA" (FLO / CWC create the
# ResourceQuota guarding a singleton just before its first object).
apply_quota_retry() {
	_aqr_end=$(($(date +%s) + $3))
	while :; do
		_aqr_out=$(kubectl apply -f "$1" 2>&1) && { printf '%s\n' "$_aqr_out" >&2; return 0; }
		case $_aqr_out in *"status unknown for quota"*"$2"* | *"$2"*"status unknown for quota"*) ;; *)
			printf '%s\n' "$_aqr_out" >&2
			return 1
			;;
		esac
		[ "$(date +%s)" -ge "$_aqr_end" ] && { kit_log "quota $2 status never populated"; return 1; }
		kit_log "quota $2 status not yet computed, retrying in 5s ..."
		sleep 5
	done
}

# wait_condition KIND/NAME NAMESPACE CONDITION SECONDS — poll until the
# condition is True; fail at once on False. Prints the message on failure.
wait_condition() {
	_wc_end=$(($(date +%s) + $4))
	while :; do
		_wc_s=$(kubectl ${2:+-n "$2"} get "$1" -o jsonpath="{.status.conditions[?(@.type==\"$3\")].status}" 2>/dev/null)
		case $_wc_s in
		True) return 0 ;;
		False)
			kit_log "$1 $3=False: $(kubectl ${2:+-n "$2"} get "$1" -o jsonpath="{.status.conditions[?(@.type==\"$3\")].message}" 2>/dev/null)"
			return 1
			;;
		esac
		[ "$(date +%s)" -ge "$_wc_end" ] && return 1
		sleep 5
	done
}

# wait_tmm_net1 NAD SECONDS — wait until a TMM pod's Multus network-status
# names NAD (net1 attached). A warning, not a failure, on timeout.
wait_tmm_net1() {
	_wtn() {
		kubectl -n "$BNK_NAMESPACE" get pods -l app=f5-tmm \
			-o jsonpath='{range .items[*]}{.metadata.annotations.k8s\.v1\.cni\.cncf\.io/network-status}{end}' 2>/dev/null |
			grep -qF "$1"
	}
	kit_retry "$2" 5 _wtn "$1" ||
		kit_log "warning: TMM net1 ($1) not seen within $2s; net1-bound config may not apply until it is up"
}

# wait_tmm_ready WORKERS SECONDS — the f5-tmm DaemonSet runs WORKERS pods,
# all updated and available.
wait_tmm_ready() {
	_wtr() {
		set -- $(kubectl -n "$BNK_NAMESPACE" get daemonset f5-tmm -o jsonpath='{.status.desiredNumberScheduled} {.status.updatedNumberScheduled} {.status.numberAvailable}' 2>/dev/null) 0 0 0
		_wtr_why=
		if [ "$1" -lt "$_wtr_w" ]; then _wtr_why="f5-tmm scheduled on $1 of $_wtr_w app=f5-tmm worker(s)"
		elif [ "$2" -lt "$1" ]; then _wtr_why="$2 of $1 TMM pod(s) updated"
		elif [ "$3" -lt "$1" ]; then _wtr_why="$3 of $1 TMM pod(s) available"
		fi
		[ -z "$_wtr_why" ]
	}
	_wtr_w=$1
	[ "$_wtr_w" -ge 1 ] 2>/dev/null || _wtr_w=1
	kit_retry "$2" 10 _wtr && return 0
	kit_log "TMM not ready after $2s: $_wtr_why — pods: $(kubectl -n "$BNK_NAMESPACE" get pods -l app=f5-tmm \
		-o jsonpath='{range .items[*]}{.metadata.name} {.status.phase} {.status.conditions[?(@.type=="PodScheduled")].message}; {end}' 2>&1)"
	return 1
}

# wait_workloads NAMESPACE SECONDS — every DaemonSet, Deployment and
# StatefulSet in NAMESPACE has as many available pods as it wants.
wait_workloads() {
	_wwl() {
		_wwl_bad=$(kubectl get daemonset,deployment,statefulset -n "$1" -o jsonpath='{range .items[*]}{.kind}/{.metadata.name} {.status.desiredNumberScheduled}{.spec.replicas} {.status.numberAvailable}{.status.availableReplicas}{"\n"}{end}' 2>&1) ||
			return 1
		_wwl_bad=$(printf '%s\n' "$_wwl_bad" | awk 'NF >= 2 && $2 ~ /^[0-9]+$/ && ($3 + 0) < $2 { printf "%s %d/%d; ", $1, $3, $2 }')
		[ -z "$_wwl_bad" ]
	}
	kit_retry "$2" 10 _wwl "$1" && return 0
	kit_log "workload(s) in $1 not available after $2s: $_wwl_bad"
	return 1
}

# node_exec NODE CMD... — run CMD on NODE via NODE_EXEC (e.g. docker exec).
node_exec() {
	_ne_n=$1
	shift
	# shellcheck disable=SC2086 # NODE_EXEC is a command prefix
	$NODE_EXEC "$_ne_n" "$@"
}

# plat_state FILE — path under KIT_STATE where an install records what it
# rendered or resolved (ocibnkctl: <PoC>/artifacts).
plat_state() {
	mkdir -p "$KIT_STATE" && printf '%s\n' "$KIT_STATE/$1"
}
