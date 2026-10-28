#!/bin/sh
# Copy CWC's client certs and auth token into the scenario namespace, then
# start the curl probe that mounts them.
. "$(dirname "$0")/../../lib/common.sh"

kit_require "secret:$CWC_NAMESPACE/cwc-license-client-certs" "secret:$CWC_NAMESPACE/cwc-auth-token"
kit_apply manifests/01-namespace.yaml
kit_copy_secret cwc-license-client-certs "$CWC_NAMESPACE" "$NAMESPACE"
kit_copy_secret cwc-auth-token "$CWC_NAMESPACE" "$NAMESPACE"
kit_apply manifests/02-probe.yaml
