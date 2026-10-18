"""DrawingML shapes for the pons deck's network diagrams (EMU throughout)."""
from xml.sax.saxutils import escape

EBPF, INGRESS, EGRESS = '933CCC', '0E41AA', '009639'
INK, GREY, PALE, NODE = '222222', '6B6B6B', 'E6E6E6', 'F7F7F7'
K = 1_000_000  # a million EMU is a little over an inch; handy for layout


class Slide:
    """Collects shapes in three layers: node backgrounds, traffic, then boxes on top."""

    def __init__(self):
        self.n, self.layers = 10, {'bg': [], 'line': [], 'fg': []}

    def _id(self):
        self.n += 1
        return self.n

    def box(self, x, y, w, h, lines=(), fill=None, line=INK, lw=9525, geom='rect',
            sz=900, color=INK, bold=False, dash=None, anchor='ctr', align='ctr', ins=36000, layer='fg'):
        """A rectangle with centred text; lines are strings or (text, sz, bold, color)."""
        fill_x = f'<a:solidFill><a:srgbClr val="{fill}"/></a:solidFill>' if fill else '<a:noFill/>'
        dash_x = f'<a:prstDash val="{dash}"/>' if dash else ''
        line_x = (f'<a:ln w="{lw}"><a:solidFill><a:srgbClr val="{line}"/></a:solidFill>{dash_x}</a:ln>'
                  if line else '<a:ln><a:noFill/></a:ln>')
        paras = ''
        for ln in lines:
            t, s, b, c = (ln, sz, bold, color) if isinstance(ln, str) else ln
            paras += (f'<a:p><a:pPr algn="{align}"/><a:r><a:rPr lang="en-US" sz="{s}"'
                      f'{" b=\"1\"" if b else ""} dirty="0"><a:solidFill><a:srgbClr val="{c}"/></a:solidFill>'
                      f'</a:rPr><a:t>{escape(t)}</a:t></a:r></a:p>')
        if not paras:
            paras = '<a:p><a:endParaRPr lang="en-US" dirty="0"/></a:p>'
        i = self._id()
        self.layers[layer].append(
            f'<p:sp><p:nvSpPr><p:cNvPr id="{i}" name="Shape {i}"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr>'
            f'<p:spPr><a:xfrm><a:off x="{int(x)}" y="{int(y)}"/><a:ext cx="{int(w)}" cy="{int(h)}"/></a:xfrm>'
            f'<a:prstGeom prst="{geom}"><a:avLst/></a:prstGeom>{fill_x}{line_x}</p:spPr>'
            f'<p:txBody><a:bodyPr wrap="square" lIns="{ins}" rIns="{ins}" tIns="{ins // 2}" bIns="{ins // 2}" '
            f'anchor="{anchor}"><a:normAutofit/></a:bodyPr><a:lstStyle/>{paras}</p:txBody></p:sp>')

    def text(self, x, y, w, h, lines, sz=900, color=INK, bold=False, align='l', anchor='t'):
        self.box(x, y, w, h, lines, line=None, sz=sz, color=color, bold=bold,
                 align=align, anchor=anchor, ins=0)

    def arrow(self, x1, y1, x2, y2, color, lw=19050, dash=None, head=True):
        flip = (' flipH="1"' if x2 < x1 else '') + (' flipV="1"' if y2 < y1 else '')
        dash_x = f'<a:prstDash val="{dash}"/>' if dash else ''
        head_x = '<a:tailEnd type="triangle" w="med" len="med"/>' if head else ''
        i = self._id()
        self.layers['line'].append(
            f'<p:cxnSp><p:nvCxnSpPr><p:cNvPr id="{i}" name="Line {i}"/><p:cNvCxnSpPr/><p:nvPr/></p:nvCxnSpPr>'
            f'<p:spPr><a:xfrm{flip}><a:off x="{int(min(x1, x2))}" y="{int(min(y1, y2))}"/>'
            f'<a:ext cx="{int(abs(x2 - x1))}" cy="{int(abs(y2 - y1))}"/></a:xfrm>'
            f'<a:prstGeom prst="line"><a:avLst/></a:prstGeom>'
            f'<a:ln w="{lw}"><a:solidFill><a:srgbClr val="{color}"/></a:solidFill>{dash_x}{head_x}</a:ln>'
            f'</p:spPr></p:cxnSp>')

    def path(self, pts, color, dash=None):
        """A polyline with the arrowhead on its last segment."""
        for (a, b), last in zip(zip(pts, pts[1:]), [False] * (len(pts) - 2) + [True]):
            self.arrow(*a, *b, color, dash=dash, head=last)

    # --- building blocks ---

    def iface(self, x, y, w, h, name):
        self.box(x, y, w, h, [name], fill='FFFFFF', sz=800)

    def hook(self, x, y, w, h, lines):
        self.box(x, y, w, h, [(t, 800, i == 0, 'FFFFFF') for i, t in enumerate(lines)],
                 fill=EBPF, line=None, geom='roundRect', ins=18000)

    def legend(self, x, y):
        self.hook(x, y, 900000, 220000, ['eBPF (tcx)'])
        x += 1050000
        for label, color, dash in (('ingress', INGRESS, None), ('egress', EGRESS, None),
                                   ('VXLAN', EGRESS, 'dash')):
            self.arrow(x, y + 110000, x + 380000, y + 110000, color, dash=dash)
            self.text(x + 430000, y + 10000, 700000, 200000, [label], sz=900, anchor='ctr')
            x += 1150000

    def network(self, x, y, w, label):
        self.box(x, y, w, 340000, [label], fill='FFFFFF', line=INK, sz=900, geom='roundRect')

    @staticmethod
    def slim_geom(x, y, w, h):
        """Where a slim node's uplink and pod veth sit: (uplink centre x, veth top y)."""
        return x + 300000, y + h - 760000 - 110000

    def slim_node(self, x, y, w, h, kind):
        """A narrow node: uplink and one pod. kind is 'tmm' or 'rev'."""
        self.box(x, y, w, h, fill=NODE, line='A6A6A6', geom='roundRect', layer='bg')
        self.text(x, y + 230000, w, 400000,
                  [('TMM node' if kind == 'tmm' else 'revenue node', 900, True, INK)], align='ctr')
        self.iface(x + 60000, y - 130000, 480000, 260000, 'uplink')
        if kind != 'tmm':
            self.hook(x + 580000, y - 190000, w - 640000, 380000, ['steer_return', 'on cni-pons'])
        pod_y, pod_h = y + h - 760000, 650000
        if kind == 'tmm':
            self.box(x + 60000, pod_y, w - 120000, pod_h, [('TMM', 1000, True, 'FFFFFF')], fill=INK, line=None, anchor='b')
        else:
            self.box(x + 60000, pod_y, w - 120000, pod_h, [('tenant pod', 900, True, INK)],
                     fill='FFFFFF', line='A6A6A6', anchor='b')
        self.iface(x + 90000, pod_y - 110000, 420000, 220000, 'veth')
        self.hook(x + 540000, pod_y - 190000, w - 600000, 380000,
                  ['steer_tmm'] if kind == 'tmm' else ['steer_egress'])

    def shapes(self):
        return ''.join(''.join(self.layers[k]) for k in ('bg', 'line', 'fg'))
