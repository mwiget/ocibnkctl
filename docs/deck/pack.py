import os, sys, zipfile
src, out = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(out, 'w', zipfile.ZIP_DEFLATED) as z:
    z.write(os.path.join(src, '[Content_Types].xml'), '[Content_Types].xml')
    for root, _, files in os.walk(src):
        for f in sorted(files):
            p = os.path.join(root, f); a = os.path.relpath(p, src)
            if a != '[Content_Types].xml': z.write(p, a)
