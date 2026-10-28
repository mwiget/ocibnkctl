#!/bin/sh
. "$(dirname "$0")/../../lib/common.sh"

kubectl -n "$BNK_NAMESPACE" delete routingconfig scn-rc-tenant --ignore-not-found >&2
kubectl -n "$BNK_NAMESPACE" delete routingtemplate scn-rt-tenant --ignore-not-found >&2
