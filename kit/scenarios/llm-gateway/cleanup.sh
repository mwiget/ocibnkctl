#!/bin/sh
# Delete the mock LLM and the setup Job. The tenant and its objects stay: the
# AI gateway API owns them (delete them in the console or with the API).
. "$(dirname "$0")/../../lib/common.sh"

kit_delete_namespace "$NAMESPACE"
