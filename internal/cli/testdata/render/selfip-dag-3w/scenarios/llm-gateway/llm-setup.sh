#!/bin/sh
# llm-setup.sh — configure the BNK 2.4.1 AI gateway for a demo, through the
# same REST API the console uses (f5ingress's AI gateway API, Service
# f5-cne-controller-api:8443). Each step is idempotent: an object that
# already exists (HTTP 409) is reused, so the script can be re-run.
#
#   tenant → provider key → model → principal → route → virtual key
#
# Inputs (env):
#   ADMIN_PASSWORD  password of the local admin (Secret f5-aigateway-admin)
#   TENANT          tenant name                         (default: demo)
#   VIP             Gateway address the route listens on (default: 203.0.113.140)
#   PROVIDER_URL    OpenAI-compatible base URL incl. /v1
#                   (default: the in-cluster mock, http://mock-llm.llm-demo.svc:8080/v1)
#   PROVIDER_KEY    upstream API key (default: sk-mock; the mock ignores it)
#   MODEL           model name clients ask for            (default: gpt-4o-mini)
#   API             API base URL (default: the in-cluster Service)
#
# Output: progress lines, then a final `VKEY=<token>` line. The token is shown
# once by the API and cannot be read back, so the caller must keep it.
set -eu

API=${API:-http://f5-cne-controller-api.default.svc.cluster.local:8443}
TENANT=${TENANT:-demo}
VIP=${VIP:-203.0.113.140}
PROVIDER_URL=${PROVIDER_URL:-http://mock-llm.llm-demo.svc:8080/v1}
PROVIDER_KEY=${PROVIDER_KEY:-sk-mock}
MODEL=${MODEL:-gpt-4o-mini}
T=/api/v1/tenants/$TENANT

# field NAME JSON — the first "NAME":"value" string in JSON (no jq in the image).
field() { printf '%s' "$2" | sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p" | head -n 1; }

# call METHOD PATH [BODY] — prints the body, then the status on its own line.
call() {
	if [ -n "${3:-}" ]; then
		curl -sS -X "$1" -H "authorization: Bearer $TOKEN" -H 'content-type: application/json' \
			-w '\n%{http_code}' -d "$3" "$API$2"
	else
		curl -sS -X "$1" -H "authorization: Bearer $TOKEN" -w '\n%{http_code}' "$API$2"
	fi
}

# step LABEL METHOD PATH BODY [OWNED_PATH] — create the object. A 409 means
# the name is taken; models and routes live in one namespace shared by every
# tenant, so a 409 only counts as "already ours" when OWNED_PATH, a
# tenant-scoped GET, finds it under this tenant.
step() {
	out=$(call "$2" "$3" "$4")
	code=$(printf '%s' "$out" | tail -n 1)
	BODY=$(printf '%s' "$out" | sed '$d')
	case $code in
	200|201) echo "  $1: created" ;;
	409)
		if [ -n "${5:-}" ] && [ "$(call GET "$5" | tail -n 1)" != 200 ]; then
			echo "  $1: FAILED — the name is taken by another tenant" >&2
			exit 1
		fi
		echo "  $1: already exists"
		;;
	*) echo "  $1: FAILED (HTTP $code) $BODY" >&2; exit 1 ;;
	esac
}

echo "== AI gateway API at $API"
login=$(curl -sS -X POST -H 'content-type: application/json' \
	-d "{\"username\":\"admin\",\"password\":\"$ADMIN_PASSWORD\"}" "$API/api/v1/auth/login")
TOKEN=$(field access_token "$login")
[ -n "$TOKEN" ] || { echo "  login FAILED: $login" >&2; exit 1; }
echo "  login: ok (admin)"

step "tenant $TENANT" POST /api/v1/tenants "{\"name\":\"$TENANT\",\"displayName\":\"$TENANT\"}"

# A provider key is an AIProvider + AICredential + Secret named pk-<hex>. Its
# name is not unique, so reuse an existing "demo" key rather than add another.
existing=$(call GET "$T/provider-keys" | sed '$d')
PK=$(printf '%s' "$existing" | sed -n 's/.*"ref":"\(pk-[0-9a-f]*\)","name":"demo".*/\1/p' | head -n 1)
if [ -n "$PK" ]; then
	echo "  provider key: already exists ($PK)"
else
	step "provider key" POST "$T/provider-keys" \
		"{\"name\":\"demo\",\"vendor\":\"OpenAI\",\"value\":\"$PROVIDER_KEY\",\"enabled\":true,\"openAI\":{\"baseURL\":\"$PROVIDER_URL\"}}"
	PK=$(field ref "$BODY")
fi

# The model's object name (id) is tenant-qualified so tenants don't collide;
# name is what clients put in "model".
MODEL_ID=$TENANT-$(printf '%s' "$MODEL" | tr -c 'a-z0-9-\n' '-')
step "model $MODEL" POST "$T/models" \
	"{\"id\":\"$MODEL_ID\",\"name\":\"$MODEL\",\"displayName\":\"$MODEL\",\"deployments\":[{\"providerRef\":\"$PK\",\"upstream\":\"$MODEL\"}]}" \
	"$T/models/$MODEL_ID"
step "principal demo-app" POST "$T/governance/principals" \
	"{\"id\":\"demo-app\",\"name\":\"demo-app\",\"kind\":\"service\",\"teamId\":\"default\"}"
# The route creates the Gateway, HTTPRoute, AppPolicy and AIGatewayProfile,
# all named after it, so it is tenant-qualified too.
ROUTE=$TENANT-llm
step "route $ROUTE on $VIP:80" POST "$T/routes" \
	"{\"name\":\"$ROUTE\",\"gatewayClassName\":\"bnk-gatewayclass\",\"address\":\"$VIP\",\"httpPort\":80}" \
	"$T/routes/$ROUTE"

# A virtual key is new on every run: the token is shown only at creation.
step "virtual key" POST "$T/virtual-keys" \
	"{\"name\":\"demo-$(date +%s)\",\"principalId\":\"demo-app\",\"modelScope\":[\"$MODEL\"],\"providerKeys\":[]}"
VKEY=$(field token "$BODY")
[ -n "$VKEY" ] || { echo "  virtual key: no token in response" >&2; exit 1; }
echo "VKEY=$VKEY"
