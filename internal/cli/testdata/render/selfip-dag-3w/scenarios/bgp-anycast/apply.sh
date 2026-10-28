#!/bin/sh
# Nothing to apply: the anycast data plane is how `deploy cne` set the
# cluster up. Only check that it is in that mode.
. "$(dirname "$0")/../../lib/common.sh"

kit_require anycast-bgp
