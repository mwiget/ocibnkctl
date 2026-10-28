#!/bin/sh
# Verify the iRule's DSSM counters equal the token usage the backend
# reported, per LB, per mode (streaming / non-streaming), per kind.
. "$(dirname "$0")/../../lib/common.sh"

LBS="llm-chat:$VIP_CHAT llm-code:$VIP_CODE llm-embed:$VIP_EMBED llm-rag:$VIP_RAG"
kit_check "stub-llm Deployment Available" \
	kubectl -n "$NAMESPACE" wait --for=condition=Available deployment/stub-llm --timeout=2m
for lb in $LBS; do
	kit_check "Gateway aitok-${lb%%:*}-gw Programmed=True" \
		kubectl -n "$NAMESPACE" wait --for=condition=Programmed "gateway/aitok-${lb%%:*}-gw" --timeout=3m
done
kit_eq "F5BigCneIrule aitok-counter exists" aitok-counter \
	"$(kubectl -n "$NAMESPACE" get f5-big-cne-irule/aitok-counter -o jsonpath='{.metadata.name}')"

served="traffic served: requests returned a usage block"
readable="DSSM/Redis readable (TMMTOK subtable scanned)"
counted="all 4 LBs have token counters in DSSM"
modes="both streaming and non-streaming counted (mode detection works)"
match="DSSM counters match backend usage exactly (per LB, per mode, incl. streaming)"
if ! edge_enabled; then
	for lb in $LBS; do edge_skip "FRR BGP table has ${lb#*:}/32 (LB ${lb%%:*})"; done
	edge_skip "$served" "$readable" "$counted" "$modes" "$match"
	kit_done "4 LBs and the counting iRule reconciled"
fi
for lb in $LBS; do edge_check_vip "FRR BGP table has ${lb#*:}/32 (LB ${lb%%:*})" "${lb#*:}"; done

work=$(mktemp -d) || kit_die "mktemp"
trap 'rm -rf "$work"' EXIT

# read_dssm FILE — the TMMTOK subtable as "vs|mode|kind count" lines. Keys
# look like ...TMMTOK<kind>|vs=<lb>|mode=<mode>|...; values end in S<count>.
read_dssm() {
	rcli='redis-cli --tls --cert /tls/dssm/mds/svr/tls.crt --key /tls/dssm/mds/svr/tls.key --cacert /tls/dssm/mds/svr/ca.crt -p 6379 -n 0'
	kubectl -n "$BNK_NAMESPACE" exec "$DSSM_POD" -- sh -c \
		"$rcli --scan --pattern '*$SUBTABLE*' | while read k; do v=\$($rcli GET \"\$k\" 2>/dev/null); [ -n \"\$v\" ] && echo \"\$k~~~\$v\"; done" \
		>"$work/raw" 2>&1 || return 1
	awk -v sub_="$SUBTABLE" -F'~~~' '
		{ i = index($1, sub_); if (!i || !match($2, /S[0-9]+$/)) next
		  member = substr($1, i + length(sub_)); n = split(member, part, "|")
		  vs = ""; mode = ""
		  for (j = 2; j <= n; j++) { split(part[j], kv, "="); if (kv[1] == "vs") vs = kv[2]; if (kv[1] == "mode") mode = kv[2] }
		  if (vs != "" && mode != "") sum[vs "|" mode "|" part[1]] += substr($2, RSTART + 1) }
		END { for (k in sum) print k, sum[k] }' "$work/raw" >"$1"
}
read_dssm "$work/before" || : >"$work/before"

# Drive the traffic, summing the backend-reported usage per bucket.
: >"$work/backend"
ok=0 total=0 round=0
while [ "$round" -lt "$ROUNDS" ]; do
	mode=non-streaming stream=
	[ $((round % 2)) -eq 1 ] && mode=streaming stream='"stream":true,"stream_options":{"include_usage":true},'
	for lb in $LBS; do
		name=${lb%%:*} total=$((total + 1))
		body=$(edge_curl ${stream:+-N} -H "Content-Type: application/json" -H "Host: $name.aitok.local" \
			-d "{\"model\":\"gpt-stub\",$stream\"messages\":[{\"role\":\"user\",\"content\":\"token counting request round $round for $name with several words to tokenize\"}]}" \
			"http://${lb#*:}:$PORT/v1/chat/completions" 2>/dev/null) || continue
		usage() { printf '%s\n' "$body" | sed -n "s/.*\"$1\": *\([0-9][0-9]*\).*/\1/p" | tail -1; }
		t=$(usage total_tokens)
		[ -n "$t" ] && [ "$t" -gt 0 ] || continue
		ok=$((ok + 1))
		printf '%s|%s|total %s\n%s|%s|prompt %s\n%s|%s|completion %s\n' \
			"$name" "$mode" "$t" "$name" "$mode" "$(usage prompt_tokens)" "$name" "$mode" "$(usage completion_tokens)" >>"$work/backend"
	done
	round=$((round + 1))
done
kit_count "$served" "$ok" "$total"

sleep 3 # let the iRule's table writes reach DSSM
if read_dssm "$work/after"; then kit_pass "$readable" "$(wc -l <"$work/after" | tr -d ' ') bucket(s)"; else kit_fail "$readable" "$(head -c 200 "$work/raw")"; fi

# This run's DSSM delta vs the backend's sums; the per-bucket breakdown goes
# to the log.
awk 'FILENAME == ARGV[1] { b[$1] = $2; next } FILENAME == ARGV[2] { a[$1] = $2; next } { be[$1] += $2 }
	END { for (k in a) if (a[k] - b[k] != 0) d[k] = a[k] - b[k]
	      for (k in be) all[k] = 1; for (k in d) all[k] = 1
	      # Only buckets the backend reported are compared; DSSM-only ones are listed.
	      # (k in be) first: referencing be[k] would create the element.
	      for (k in all) { inbe = (k in be); printf "%s dssm=%d backend=%d %s\n", k, d[k], be[k], (!inbe ? "extra" : d[k] == be[k] ? "ok" : "MISMATCH") } }' \
	"$work/before" "$work/after" "$work/backend" | sort >"$work/cmp"
kit_log "per-bucket (vs|mode|kind) dssm vs backend:"
sed 's/^/  /' "$work/cmp" >&2
lbs=$(awk '$1 ~ /\|total$/ && $2 != "dssm=0" { split($1, p, "|"); print p[1] }' "$work/cmp" | sort -u)
kit_eq "$counted" 4 "$(printf '%s\n' "$lbs" | grep -c .)"
seen=$(awk '$1 ~ /\|total$/ && $2 != "dssm=0" { split($1, p, "|"); print p[2] }' "$work/cmp" | sort -u | tr '\n' ' ')
kit_eq "$modes" "non-streaming streaming " "$seen"
mism=$(grep MISMATCH "$work/cmp" | tr '\n' ';')
if [ -z "$mism" ]; then kit_pass "$match" "$(wc -l <"$work/cmp" | tr -d ' ') bucket(s) equal"; else kit_fail "$match" "$mism"; fi

kit_done "4 LBs counting AI tokens via custom iRule -> DSSM/Redis; counters match backend usage (streaming + non-streaming)"
