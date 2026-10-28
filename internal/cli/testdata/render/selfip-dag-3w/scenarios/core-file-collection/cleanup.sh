#!/bin/sh
# Deliberately leaves core collection ON. Toggling coreCollection.enabled
# back to false makes FLO UPDATE every managed CR (F5Tmm, CNEController,
# Afm, Cwc, IPAMController, Observer, DSSM, Rabbitmq, ...) with
# spec.crashagentConfig: null, which the API server rejects; the CNEInstance
# then sits at Reconciled=False and FLO stops propagating ANY later change
# (e.g. networkAttachments, so TMM loses net1 and BGP never comes up).
# Restarting FLO does not help. CoreMond is a small hostPath DaemonSet, and
# re-applying this scenario is idempotent, so leaving it on is the safe choice.
. "$(dirname "$0")/../../lib/common.sh"

kit_log "core-file-collection leaves core collection enabled on purpose (see cleanup.sh)"
