#!/bin/sh
# render.sh — render a kit template from environment variables.
#
#   NAD_NAME=bnk-bgp DEPLOYMENT_SIZE=Small ... sh kit/lib/render.sh kit/platform/cne/cne-instance.yaml
#
# Same rules as the Go renderer (kit/kit.go), which ocibnkctl uses on the
# same files:
#
#   ${NAME}       replaced by the value of environment variable NAME
#   #@if NAME     on a line of its own: keep the lines up to the matching
#   #@end         #@end only when NAME is non-empty (blocks nest)
#
# Every NAME the template uses must be set (an empty value is fine);
# an unset one is an error. $NAME without braces and ${NAME:-x} pass
# through untouched. POSIX sh + awk only (works with BSD awk on macOS);
# set AWK to pick a specific awk.
set -eu

if [ $# -ne 1 ]; then
	echo "usage: render.sh <template>" >&2
	exit 2
fi

exec "${AWK:-awk}" '
function fail(msg) { printf "render.sh: %s: line %d: %s\n", FILENAME, FNR, msg > "/dev/stderr"; failed = 1; exit 1 }
function kept(   i) { for (i = 1; i <= depth; i++) if (!keep[i]) return 0; return 1 }
{
	line = $0
	sub(/\r$/, "", line)
	if (line ~ /^[ \t]*#@if[ \t]+[A-Z_][A-Z0-9_]*[ \t]*$/) {
		name = line
		sub(/^[ \t]*#@if[ \t]+/, "", name)
		sub(/[ \t]*$/, "", name)
		if (!(name in ENVIRON)) fail("#@if " name ": variable not set")
		keep[++depth] = (ENVIRON[name] != "")
		next
	}
	if (line ~ /^[ \t]*#@end[ \t]*$/) {
		if (depth == 0) fail("#@end without #@if")
		depth--
		next
	}
	if (!kept()) next
	out = ""
	rest = $0
	while (match(rest, /[$][{][A-Z_][A-Z0-9_]*[}]/)) {
		name = substr(rest, RSTART + 2, RLENGTH - 3)
		if (!(name in ENVIRON)) fail("${" name "}: variable not set")
		out = out substr(rest, 1, RSTART - 1) ENVIRON[name]
		rest = substr(rest, RSTART + RLENGTH)
	}
	print out rest
}
END {
	if (!failed && depth != 0) { printf "render.sh: %s: %d #@if block(s) not closed by #@end\n", FILENAME, depth > "/dev/stderr"; exit 1 }
}' "$1"
