"""Write the LLM gateway demo deck's slides into an unpacked F5 template (u/).

The base is F5-Template-Starter-FY26-Q3 (base.pptx, the template saved as a
presentation): its masters, layouts and theme stay; every slide and its
speaker notes are rewritten from scratch on each build. Seven slides and the
F5 end slide: title, why, what gets deployed, two ways to run it, proof,
know before you demo, get it. The source of every claim is
docs/llm-gateway-howto.md and the verified runs it names.

The building blocks (title_slide, page, cards, table, code, banner, rewrite)
are pons's docs/deck/fill.py, trimmed to what this deck uses.
"""
import json
import os
import re
import sys
from xml.sax.saxutils import escape

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from diagram import Slide, INK, GREY  # noqa: E402

U = sys.argv[1]
# Facts of the verified runs (run.json, written from the run reports): the
# deck states only these numbers.
RUN = json.load(open(os.path.join(os.path.dirname(os.path.abspath(__file__)), 'run.json')))

NS = ('xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" '
      'xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" '
      'xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"')
GRP = ('<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>'
       '<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/>'
       '<a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>')

X0, W = 457_200, 11_274_552
TOP = 1_400_000
CHIP = {'WHY': '0E41AA', 'HOW': '007A2E', 'PROOF': '933CCC', 'FIELD NOTES': 'A35A00', 'GET IT': '6B6B6B'}
BLUE, GREEN, PURPLE, AMBER, SLATE, RED = '0E41AA', '007A2E', '933CCC', 'A35A00', '6B6B6B', 'C00000'
HDR_FILL, LINE, ZEBRA = '222222', 'D0D0D0', 'F7F7F7'
REPO = 'github.com/mwiget/ocibnkctl'
GUIDE = 'docs/llm-gateway-howto.md'
LAYOUT_TITLE, LAYOUT_PAGE, LAYOUT_END = 'slideLayout35.xml', 'slideLayout16.xml', 'slideLayout46.xml'


def sld(shapes):
    return (f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>\n<p:sld {NS}>'
            f'<p:cSld><p:spTree>{GRP}{shapes}</p:spTree></p:cSld>'
            '<p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>')


def ph(id_, name, phattrs, paras, xfrm=None):
    """A placeholder; paras are (text, bold) or (text, bold, sz, color); xfrm overrides the layout."""
    body = ''
    for p in paras:
        t, b, sz, col = (*p, None, None)[:4]
        fill = f'<a:solidFill><a:srgbClr val="{col}"/></a:solidFill>' if col else ''
        body += (f'<a:p><a:r><a:rPr lang="en-US"{f" sz=\"{sz}\"" if sz else ""}{" b=\"1\"" if b else ""} dirty="0">'
                 f'{fill}</a:rPr><a:t>{escape(t)}</a:t></a:r></a:p>')
    sp = (f'<a:xfrm><a:off x="{xfrm[0]}" y="{xfrm[1]}"/><a:ext cx="{xfrm[2]}" cy="{xfrm[3]}"/></a:xfrm>'
          if xfrm else '')
    return (f'<p:sp><p:nvSpPr><p:cNvPr id="{id_}" name="{name}"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>'
            f'<p:nvPr><p:ph {phattrs}/></p:nvPr></p:nvSpPr><p:spPr>{sp}</p:spPr>'
            f'<p:txBody><a:bodyPr/><a:lstStyle/>{body}</p:txBody></p:sp>')


def title_slide(title, lines):
    return sld(ph(2, 'Title 1', 'type="title"', [(title, False, 5400)], xfrm=(X0, 900_000, W, 1_500_000))
               + ph(3, 'Text Placeholder 2', 'idx="1"', lines, xfrm=(X0, 2_600_000, W, 3_600_000)))


def page(title, sub, chip, d):
    d.box(X0 + W - 1_450_000, 130_000, 1_450_000, 260_000, [(chip, 900, True, 'FFFFFF')],
          fill=CHIP[chip], line=None, geom='roundRect')
    return sld(ph(3, 'Title 2', 'type="title"', [(title, False)])
               + ph(4, 'Text Placeholder 3', 'type="body" sz="quarter" idx="17"', [(sub, False)])
               + d.shapes())


def card(d, x, y, w, h, head, color, lines, sz=1100, head_sz=1250):
    hh = head_sz * 230
    d.box(x, y, w, h, fill='FFFFFF', line=color, lw=9525, layer='bg')
    d.box(x, y, w, 60_000, fill=color, line=None, layer='bg')
    d.text(x + 150_000, y + 140_000, w - 300_000, hh, [(head, head_sz, True, color)])
    d.text(x + 150_000, y + 190_000 + hh, w - 300_000, h - 250_000 - hh,
           [('• ' + t, sz, False, INK) for t in lines])


def cards(d, y, h, items, gap=180_000, sz=1100, head_sz=1250, x=X0, w_total=W):
    w = (w_total - gap * (len(items) - 1)) / len(items)
    for i, (head, color, lines) in enumerate(items):
        card(d, x + i * (w + gap), y, w, h, head, color, lines, sz=sz, head_sz=head_sz)


def table(d, x, y, cols, rows, sz=1050, rh=300_000, hdr_h=320_000, ok_words=('OK', 'PASS')):
    xs = [x]
    for c in cols[:-1]:
        xs.append(xs[-1] + c)
    for j, h in enumerate(rows[0]):
        d.box(xs[j], y, cols[j], hdr_h, [(h, sz, True, 'FFFFFF')], fill=HDR_FILL, line=None, align='l', ins=60_000)
    for i, r in enumerate(rows[1:]):
        yy = y + hdr_h + i * rh
        for j, t in enumerate(r):
            ok = bool(ok_words) and any(t.startswith(w) for w in ok_words)
            d.box(xs[j], yy, cols[j], rh, [(t, sz, j == 0 or ok, GREEN if ok else INK)],
                  fill=ZEBRA if i % 2 else 'FFFFFF', line=LINE, lw=6350, align='l', ins=60_000)
    return y + hdr_h + (len(rows) - 1) * rh


def code(d, x, y, w, h, lines, sz=1050):
    """A code panel in Courier New; comment lines green (a:latin after a:solidFill, or PowerPoint drops the colour)."""
    d.box(x, y, w, h, fill='FFFFFF', line='A6A6A6', layer='bg')
    d.text(x + 150_000, y + 120_000, w - 300_000, h - 240_000,
           [(t or ' ', sz, False, GREEN if t.startswith('#') else INK) for t in lines])
    d.layers['fg'][-1] = d.layers['fg'][-1].replace('</a:solidFill></a:rPr>',
                                                    '</a:solidFill><a:latin typeface="Courier New"/></a:rPr>')


def banner(d, y, head, body, color=INK, h=600_000, sz=1300):
    d.box(X0, y, W, h, fill=color, line=None, geom='roundRect', layer='bg')
    d.text(X0 + 200_000, y + 60_000, W - 400_000, h - 120_000,
           [(head, sz, True, 'FFFFFF'), (body, sz - 200, False, 'E6E6E6')], anchor='ctr')


SLIDES = []


def add(xml, notes, layout=LAYOUT_PAGE):
    SLIDES.append((xml, layout, notes))


# ============================ 1. title ============================
add(title_slide('LLM gateway demo on BNK 2.4.1', [
    ('One machine, one command: TMM governs LLM traffic', True, 2600),
    ('Virtual keys, model scope and per-request metering, with the console, on a laptop or lab box. '
     'No GPU, no LLM API key', False, 1800),
    ('Run it by hand or let a coding agent run it: the PoC tells the agent how (AGENTS.md)', False, 1800),
    (' ', False, 1200),
    (f'Marcel Wiget · F5 · {RUN["date"]} · {REPO}', False, 1400, GREY)]),
    'BNK 2.4.1 ships an AI gateway inside TMM. This is a way to show it to a customer on any '
    'machine with Docker, in about half an hour, without GPUs, without an LLM API key and '
    'without a shared lab. One tool, ocibnkctl, builds the cluster, installs BNK with F5’s own '
    'operator and proves the gateway end to end; a coding agent can drive the whole thing.',
    LAYOUT_TITLE)

# ============================ 2. why ============================
d = Slide()
cards(d, TOP, 3_100_000, sz=1500, head_sz=1700, items=[
    ('What the field needs', BLUE, [
        'show BNK’s AI gateway, not slides about it',
        'no GPUs, no provider account, no shared lab to book',
        'repeatable: the same demo every time, on any box',
        'something a customer can run after the meeting']),
    ('What 2.4.1 ships', GREEN, [
        'the gateway inside TMM: virtual keys, model scope, OpenAI-style API',
        'one record per request: tokens, cost, latency',
        'a web console per tenant',
        'FLO installs it all from one CNEInstance field']),
    ('What this adds', AMBER, [
        'ocibnkctl init --profile llm-gateway',
        'a mock LLM and the setup, as a scenario',
        'checks that prove what TMM enforced',
        'a guide, and AGENTS.md for an agent']),
])
banner(d, TOP + 3_300_000, 'Everything runs F5’s 2.4.1 release images from repo.f5.com',
       'Nothing is built from source: FLO v2.30.0-0.11.4, f5-aigateway-ui v0.11.0, clickhouse-server 24.8, '
       'installed from the 2.4.1 release manifest. The only add-ons are the mock LLM and the test client.',
       color=INK, h=700_000)
add(page('A real AI gateway demo, without a lab', 'The field needs to show it; 2.4.1 ships it; ocibnkctl makes it one command', 'WHY', d),
    'Field engineers need to show the AI gateway working, not describe it, and without '
    'booking GPU labs or using a provider account. BNK 2.4.1 ships the gateway inside TMM with '
    'a console, installed by FLO from one field on the CNEInstance. ocibnkctl adds a profile, a '
    'mock LLM, the configuration and the checks. Everything that runs is F5’s 2.4.1 release.')

# ============================ 3. what gets deployed ============================
d = Slide()
Y = TOP + 150_000
# edge client
d.box(X0, Y + 800_000, 2_000_000, 1_100_000, [('Client, outside the cluster', 1100, True, INK),
                                              ('edge FRR netns · curl', 1000, False, GREY)],
      fill='F7F7F7', line=SLATE)
# cluster frame
CX, CW = X0 + 2_600_000, W - 2_600_000
d.box(CX, Y, CW, 3_350_000, [], fill=None, line=SLATE, dash='dash', layer='bg')
d.text(CX + 120_000, Y + 50_000, 5_000_000, 300_000,
       [('k3s cluster in Docker containers · BNK 2.4.1 via FLO', 1000, True, GREY)])
# TMM
d.box(CX + 250_000, Y + 600_000, 3_000_000, 1_700_000,
      [('TMM', 1500, True, 'FFFFFF'), ('virtual key → model scope', 1050, False, 'FFFFFF'),
       ('→ provider key → meter', 1050, False, 'FFFFFF'), ('203.0.113.140:80', 1000, True, 'FFFFFF')],
      fill=GREEN, line=None, geom='roundRect')
# mock
d.box(CX + 4_000_000, Y + 600_000, 2_300_000, 1_000_000,
      [('mock-llm', 1300, True, INK), ('OpenAI API · no GPU, no key', 950, False, GREY)],
      fill='FFFFFF', line=INK)
# clickhouse
d.box(CX + 6_550_000, Y + 600_000, 2_000_000, 1_000_000,
      [('ClickHouse', 1300, True, INK), ('one record / request', 950, False, GREY)],
      fill='FFFFFF', line=INK)
# controller + console
d.box(CX + 250_000, Y + 2_600_000, 3_000_000, 600_000,
      [('f5-cne-controller · AI gateway API', 1100, True, INK)], fill='FFFFFF', line=BLUE)
d.box(CX + 4_000_000, Y + 2_600_000, 2_300_000, 600_000,
      [('console (Polaris AI)', 1100, True, INK)], fill='FFFFFF', line=BLUE)
d.box(CX + 6_550_000, Y + 2_600_000, 2_000_000, 600_000,
      [('you: localhost:3000', 1100, True, BLUE)], fill='EAF0FA', line=BLUE)
# arrows
d.arrow(X0 + 2_000_000, Y + 1_350_000, CX + 250_000, Y + 1_350_000, GREEN, lw=28575)
d.text(X0 + 2_020_000, Y + 1_000_000, 580_000, 300_000, [('BGP /32', 900, True, GREEN)])
d.arrow(CX + 3_250_000, Y + 1_100_000, CX + 4_000_000, Y + 1_100_000, GREEN, lw=28575)
d.path([(CX + 3_250_000, Y + 1_950_000), (CX + 7_550_000, Y + 1_950_000), (CX + 7_550_000, Y + 1_600_000)],
       PURPLE, dash='dash')
d.text(CX + 4_100_000, Y + 1_970_000, 2_400_000, 260_000, [('a record per request, refused ones too', 850, False, PURPLE)])
d.arrow(CX + 1_750_000, Y + 2_600_000, CX + 1_750_000, Y + 2_300_000, BLUE)
d.arrow(CX + 4_000_000, Y + 2_900_000, CX + 3_250_000, Y + 2_900_000, BLUE)
d.arrow(CX + 6_550_000, Y + 2_900_000, CX + 6_300_000, Y + 2_900_000, BLUE)
d.text(X0, Y + 3_500_000, W, 600_000, [(
    'The llm-gateway scenario configures it through the AI gateway API, the calls the console makes: '
    'tenant demo, provider key → mock, model gpt-4o-mini, route demo-llm (Gateway, HTTPRoute, AppPolicy, '
    'AIGatewayProfile), a virtual key scoped to that model.', 1100, False, GREY)])
add(page('What gets deployed', 'Stock BNK 2.4.1 on k3s in Docker; FLO installs the gateway; the scenario adds a mock and a client', 'HOW', d),
    'Left, a client outside the cluster, reaching the gateway address over BGP the way a '
    'customer’s app would through a router. In the cluster, TMM checks the virtual key and the '
    'model scope, swaps in the provider key, forwards to the mock LLM and writes one record per '
    'request to ClickHouse. The controller holds the AI gateway API; the console is its UI. '
    'Everything in the cluster frame is installed by FLO from the 2.4.1 release, except the mock.')

# ============================ 4. two ways to run it ============================
d = Slide()
HALF = (W - 300_000) / 2
d.text(X0, TOP, HALF, 350_000, [('By hand: four steps, ~30 min the first time', 1500, True, GREEN)])
code(d, X0, TOP + 400_000, HALF, 2_050_000, [
    'ocibnkctl init llmdemo --profile llm-gateway',
    'cd llmdemo',
    '# your F5 FAR key + license JWT',
    'cp …/f5-far-auth-key.tgz keys/',
    'cp …/license.jwt keys/.jwt',
    'ocibnkctl doctor && ocibnkctl validate',
    'ocibnkctl e2e --yolo \\',
    '  --confirm-cluster llmdemo --with-scenarios',
], sz=1150)
d.text(X0 + HALF + 300_000, TOP, HALF, 350_000, [('With a coding agent: ask in plain words', 1500, True, BLUE)])
code(d, X0 + HALF + 300_000, TOP + 400_000, HALF, 2_050_000, [
    '# in the PoC directory: it reads AGENTS.md',
    'cd llmdemo && claude',
    '',
    '> Deploy the LLM gateway demo in this',
    '  PoC end to end and prove it works.',
    '  Check the host first and tell me what',
    '  you are about to run before anything',
    '  destructive.',
], sz=1150)
cards(d, TOP + 2_650_000, 1_450_000, sz=1400, head_sz=1500, items=[
    ('The profile seeds poc.yaml', GREEN, [
        'ai_gateway on, with metrics; anycast-bgp data plane',
        'scenarios: bgp-peer-frr, llm-gateway']),
    ('AGENTS.md tells the agent', BLUE, [
        'what to run and check, what to report',
        'which files are secret; the --confirm gate is yours']),
])
add(page('Two ways to run it', 'The same ocibnkctl commands, typed by you or run by an agent that reads AGENTS.md', 'HOW', d),
    'Left, the manual path: a profile seeds the PoC, you drop in your F5 keys, and one e2e '
    'command builds the cluster, installs BNK and runs the demo scenario. Right, the same with '
    'a coding agent: started in the PoC directory, it reads AGENTS.md, which says what to run, '
    'what to check and report, and which files are secret. You keep the destructive gate.')

# ============================ 5. proof ============================
d = Slide()
rows = [['Check, from a client outside the cluster', RUN['host_label'], RUN['ref_label']]]
for a in RUN['assertions']:
    rows.append([a['description'], 'OK' if a['gx10'] else 'FAIL', 'OK' if a['ref'] else 'FAIL'])
end = table(d, X0, TOP, [6_574_552, 2_350_000, 2_350_000], rows, sz=1050, rh=330_000)
cards(d, end + 150_000, 4_000_000 - (end - TOP) - 150_000, sz=1050, head_sz=1200, items=[
    ('The agent run (gx10)', PURPLE, RUN['agent_lines']),
    ('Timings', GREEN, RUN['timing_lines']),
])
add(page('Proof: every check green, on arm64, run by an agent',
         f'{RUN["host_label"]}: a fresh clone, driven by Claude Code from AGENTS.md; {RUN["ref_label"]} as reference', 'PROOF', d),
    'Each row is an assertion the llm-gateway scenario makes, sending real requests from '
    'outside the cluster. On gx10, an arm64 box, Claude Code ran everything from a fresh '
    'clone, reading only AGENTS.md and the prompt shown on the previous slide. '
    + RUN['notes'])

# ============================ 6. know before you demo ============================
d = Slide()
cards(d, TOP, 2_150_000, sz=1350, head_sz=1500, items=[
    ('Console sign-in is per tenant', BLUE, [
        'tenant demo, user admin',
        'password: Secret aitenant-demo-admin',
        'f5-aigateway-admin is the API’s platform admin; not for the tenant page']),
    ('Reach it on localhost:3000', BLUE, [
        'sign-in redirects go to localhost:3000 (FLO’s default)',
        'kubectl port-forward svc/f5-aigateway-console 3000:3000',
        'remote box: ssh -L 3000:… then the port-forward']),
    ('A new route can stick (2.4.1)', AMBER, [
        '“AI profile … is not prepared yet”, never retried',
        'TMM gets no virtual server',
        'the scenario restarts f5-cne-controller once']),
])
cards(d, TOP + 2_350_000, 1_450_000, sz=1350, head_sz=1500, items=[
    ('Branding', SLATE, ['the shipped console says “Polaris AI”: F5’s f5-aigateway-ui image']),
    ('Public docs', RED, ['clouddocs BNK 2.4 covers 2.4.0; the AI gateway is not documented there yet']),
    ('Reused hosts', SLATE, ['old PoCs’ docker networks can hold the edge subnet: set edge_octet']),
])
add(page('Know before you demo', 'What surprised us, so it doesn’t surprise you in front of a customer', 'FIELD NOTES', d),
    'Five things to know. Sign-in is per tenant, with a password in a per-tenant Secret, not '
    'the platform admin. Use localhost:3000. A new route can get stuck in 2.4.1; the demo '
    'handles it, but if you add routes in the console, a controller restart unsticks them. The '
    'console is branded Polaris AI. And the public docs don’t describe the AI gateway yet, so '
    'present it as a shipped preview, not as documented procedure.')

# ============================ 7. get it ============================
d = Slide()
cards(d, TOP, 2_550_000, sz=1450, head_sz=1700, items=[
    ('Get it', GREEN, [
        REPO,
        f'guide: {GUIDE}',
        'release binaries: macOS arm64, Linux amd64 and arm64',
        'needs Docker, kubectl, helm; 10 cores, 24 GB']),
    ('Bring', BLUE, [
        'your F5 FAR image-pull key',
        'a BNK license JWT',
        'nothing else: no GPU, no provider key',
        'smaller hosts: the small-host profile']),
    ('Read more', SLATE, [
        'clouddocs.f5.com/bigip-next-for-kubernetes/2.4',
        'F5 internal: PDR “BNK as LLM Gateway”',
        'Jira BIPNXTSPPM-2960',
        'AGENTS.md in every PoC']),
])
banner(d, TOP + 2_850_000, 'Post your questions and results in the Teams channel',
       'Issues and pull requests on the repo are welcome. ocibnkctl is a field tool, not a supported F5 product.',
       color=GREEN, h=750_000)
add(page('Get it', f'{REPO} · {GUIDE}', 'GET IT', d),
    'The repo is public. The guide has both paths, the console tour, troubleshooting and '
    'references, marked public or F5-internal. Bring your F5 image-pull key and a license; '
    'nothing else. This is a field tool, not a supported F5 product.')

# ============================ end ============================
add(sld(''), 'Thank you.', LAYOUT_END)


# ============================ write ============================
def notes_xml(text):
    return (f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>\n<p:notes {NS}>'
            f'<p:cSld><p:spTree>{GRP}'
            '<p:sp><p:nvSpPr><p:cNvPr id="2" name="Slide Image Placeholder 1"/><p:cNvSpPr><a:spLocks noGrp="1" noRot="1" noChangeAspect="1"/></p:cNvSpPr>'
            '<p:nvPr><p:ph type="sldImg"/></p:nvPr></p:nvSpPr><p:spPr/></p:sp>'
            '<p:sp><p:nvSpPr><p:cNvPr id="3" name="Notes Placeholder 2"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>'
            '<p:nvPr><p:ph type="body" idx="1"/></p:nvPr></p:nvSpPr><p:spPr/>'
            f'<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" dirty="0"/><a:t>{escape(text)}</a:t></a:r></a:p></p:txBody></p:sp>'
            '</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:notes>')


REL = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships'
RELS_HEAD = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>\n'
             '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">')


def rewrite():
    """Drop every slide and note part, then write SLIDES as slide1..N with their notes."""
    ct_path, rels_path, pres_path = f'{U}/[Content_Types].xml', f'{U}/ppt/_rels/presentation.xml.rels', f'{U}/ppt/presentation.xml'
    ct, rels, pres = open(ct_path).read(), open(rels_path).read(), open(pres_path).read()
    for sub in ('ppt/slides', 'ppt/slides/_rels', 'ppt/notesSlides', 'ppt/notesSlides/_rels'):
        os.makedirs(f'{U}/{sub}', exist_ok=True)
        for f in os.listdir(f'{U}/{sub}'):
            if os.path.isfile(f'{U}/{sub}/{f}'):
                os.remove(f'{U}/{sub}/{f}')
    ct = re.sub(r'<Override PartName="/ppt/(slides|notesSlides)/[^"]+"[^>]*/>', '', ct)
    rels = re.sub(r'<Relationship Id="[^"]+" Type="[^"]+/slide" Target="slides/[^"]+"/>', '', rels)
    nm = re.search(r'Target="notesMasters/(notesMaster\d+\.xml)"', rels).group(1)
    ids = []
    for i, (xml, layout, notes) in enumerate(SLIDES, 1):
        open(f'{U}/ppt/slides/slide{i}.xml', 'w').write(xml)
        open(f'{U}/ppt/slides/_rels/slide{i}.xml.rels', 'w').write(
            RELS_HEAD
            + f'<Relationship Id="rId1" Type="{REL}/slideLayout" Target="../slideLayouts/{layout}"/>'
            + f'<Relationship Id="rId2" Type="{REL}/notesSlide" Target="../notesSlides/notesSlide{i}.xml"/></Relationships>')
        open(f'{U}/ppt/notesSlides/notesSlide{i}.xml', 'w').write(notes_xml(notes))
        open(f'{U}/ppt/notesSlides/_rels/notesSlide{i}.xml.rels', 'w').write(
            RELS_HEAD
            + f'<Relationship Id="rId1" Type="{REL}/notesMaster" Target="../notesMasters/{nm}"/>'
            + f'<Relationship Id="rId2" Type="{REL}/slide" Target="../slides/slide{i}.xml"/></Relationships>')
        ct = ct.replace('</Types>',
                        f'<Override PartName="/ppt/slides/slide{i}.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>'
                        f'<Override PartName="/ppt/notesSlides/notesSlide{i}.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml"/></Types>')
        rid = f'rId{1000 + i}'
        rels = rels.replace('</Relationships>', f'<Relationship Id="{rid}" Type="{REL}/slide" Target="slides/slide{i}.xml"/></Relationships>')
        ids.append(f'<p:sldId id="{256 + i}" r:id="{rid}"/>')
    # base.pptx carries an empty, self-closing list; a template with slides, the long form.
    pres = re.sub(r'<p:sldIdLst/>|<p:sldIdLst>.*?</p:sldIdLst>',
                  lambda _: '<p:sldIdLst>' + ''.join(ids) + '</p:sldIdLst>', pres, count=1, flags=re.S)
    open(ct_path, 'w').write(ct)
    open(rels_path, 'w').write(rels)
    open(pres_path, 'w').write(pres)


rewrite()
