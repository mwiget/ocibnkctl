#!/bin/sh
# A stub LLM behind four LBs, each with an HTTPRoute iRule that counts the
# tokens of every response into DSSM.
. "$(dirname "$0")/../../lib/common.sh"

kit_require anycast-bgp gatewayclass:bnk-gatewayclass
kit_apply manifests/01-namespace.yaml manifests/03-backend.yaml manifests/04-irule.yaml \
	manifests/05-lb-chat.yaml.tmpl manifests/06-lb-code.yaml.tmpl \
	manifests/07-lb-embed.yaml.tmpl manifests/08-lb-rag.yaml.tmpl
