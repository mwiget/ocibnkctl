#!/bin/sh
# edge-pod.sh up|down — create or remove the EDGE_DRIVER=pod client by hand
# (scenarios create it on first use). Same variables as lib/edge.sh.
set -u
EDGE_DRIVER=pod
KIT_LIB=$(cd "$(dirname "$0")" && pwd)
KIT_SCENARIO_DIR=$KIT_LIB # common.sh cds here
. "$(dirname "$0")/common.sh"
case ${1:-} in
up) _edge_pod_ready && kit_log "edge client $EDGE_POD_NS/$EDGE_POD_NAME ready" ;;
down) kubectl delete namespace "$EDGE_POD_NS" --ignore-not-found >&2 ;;
*) kit_die "usage: edge-pod.sh up|down" ;;
esac
