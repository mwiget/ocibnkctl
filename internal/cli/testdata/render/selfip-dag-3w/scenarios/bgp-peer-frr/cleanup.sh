#!/bin/sh
# Nothing to remove: the BGP peering is platform configuration (deploy cne)
# and the edge router is shared infrastructure, not owned by this scenario.
. "$(dirname "$0")/../../lib/common.sh"

kit_log "bgp-peer-frr owns no objects; nothing to clean up"
