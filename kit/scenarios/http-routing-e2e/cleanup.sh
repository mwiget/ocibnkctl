#!/bin/sh
# Remove the http-routing-e2e workload. The GatewayClass stays: it is
# platform infrastructure other scenarios use.
. "$(dirname "$0")/../../lib/common.sh"

kit_delete_namespace "$NAMESPACE"
