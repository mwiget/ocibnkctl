#!/bin/sh
# Prepare every TMM's OcNOS for BGP: deploy cne already configured each TMM
# to peer the edge router; write imish's passwd.conf and re-issue the
# redistribute statements so kernel/connected routes (and any Gateway VIPs)
# enter BGP — OcNOS only redistributes when that is re-issued at runtime.
. "$(dirname "$0")/../../lib/common.sh"

kit_require anycast-bgp
[ -n "$(bnk_tmm_pods)" ] || kit_die "no Running f5-tmm pods: deploy BNK first"
bnk_ocnos_passwd || kit_die "could not write passwd.conf on every TMM"
for _ in 1 2 3; do
	bnk_ocnos_redistribute
	sleep "$REDISTRIBUTE_PAUSE"
done
kit_log "BGP redistribution triggered on $(bnk_tmm_pods | wc -l | tr -d ' ') TMM(s)"
