#!/usr/bin/env bash
# Rebuild docs/llm-gateway-demo.pptx after editing fill.py or run.json.
#
# base.pptx is F5-Template-Starter-FY26-Q3.potx saved as a presentation. This
# unpacks it, has fill.py write every slide and its speaker notes from scratch
# (masters, layouts and theme stay), and repacks it. run.json holds the facts
# of the verified runs; the deck states only those numbers.
#   docs/deck/build.sh
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
docs=$(dirname "$here")
work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
python3 -c 'import sys, zipfile; zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2])' "$here/base.pptx" "$work/u"
python3 "$here/fill.py" "$work/u"
# Unreferenced media from the base's old slides would otherwise ship along.
python3 - "$work/u" <<'PY'
import os, re, sys
u = sys.argv[1]
used = set()
for root, _, files in os.walk(u):
    for f in files:
        if f.endswith('.rels'):
            used |= set(os.path.basename(m) for m in re.findall(r'Target="([^"]+)"', open(os.path.join(root, f)).read()))
media = os.path.join(u, 'ppt', 'media')
for f in (os.listdir(media) if os.path.isdir(media) else []):
    if f not in used:
        os.remove(os.path.join(media, f))
PY
python3 "$here/pack.py" "$work/u" "$work/out.pptx"
cp "$work/out.pptx" "$docs/llm-gateway-demo.pptx"
echo "wrote $docs/llm-gateway-demo.pptx"
