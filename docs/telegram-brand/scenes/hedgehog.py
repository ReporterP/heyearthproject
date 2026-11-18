"""The HiEarth hedgehog for the Telegram scenes (spec 12 §12.6).

Geometry = site/src/components/Hedgehog.jsx (viewBox 132x96); colours = hiearth.css tokens.
Drawing is supersampled (SS x) and downscaled per frame, so edges stay smooth in the GIF.
"""
import math
import re

from PIL import Image, ImageDraw

C = {
    'bg': (9, 12, 20),            # --bg
    'raised': (15, 20, 31),       # --bg-raised
    'body': (21, 27, 40),         # --bg-elevated
    'hover': (27, 34, 50),        # --bg-hover
    'line': (31, 39, 53),         # --line
    'line2': (44, 54, 72),        # --line-strong
    'fg': (238, 242, 247),        # --fg
    'accent': (34, 197, 94),      # --accent
    'spark': (74, 222, 128),      # --accent-fg
    'ink': (5, 46, 20),           # --accent-ink
    'info': (122, 167, 250),      # --info-fg
    'danger': (248, 113, 113),    # --danger-fg
}
W, H, SS = 640, 360, 4


def mix(a, b, t):
    """a over b with opacity t."""
    return tuple(round(a[i] * t + b[i] * (1 - t)) for i in range(3))


C['cheek'] = mix(C['danger'], C['body'], .45)

# --- the site hedgehog (Hedgehog.jsx) ---------------------------------------------------------
SPIKES = 'M18 58 L10 42 L26 45 L22 27 L38 35 L40 17 L53 29 L60 11 L69 27 L80 14 L83 32 L97 24 L96 42 L108 40'
BODY = 'M14 62 C14 38 46 28 72 30 C96 32 110 46 116 60 C108 72 90 76 68 76 C42 76 16 74 14 62 Z'
EYE_HAPPY = 'M91 51 Q96 45 101 51'
EYE_SLEEP = 'M91 50 H101'
BROW = 'M89 42 L100 45'
SMILE = 'M104 67 Q108 70 112 67'
DROP = 'M110 30 C110 30 105 37 105 40 A5 5 0 0 0 115 40 C115 37 110 30 110 30 Z'
LEGS = 'M44 76 V85 M62 77 V86 M86 76 V85'
SPARK_A = 'M30 4 L32 10 L38 12 L32 14 L30 20 L28 14 L22 12 L28 10 Z'
SPARK_B = 'M104 2 L105.5 7 L110 8.5 L105.5 10 L104 15 L102.5 10 L98 8.5 L102.5 7 Z'


# --- affine matrices (a, b, c, d, e, f): x' = a x + c y + e, y' = b x + d y + f -------------------
IDENT = (1, 0, 0, 1, 0, 0)


def mul(m, n):
    """m after n."""
    a, b, c, d, e, f = m
    a2, b2, c2, d2, e2, f2 = n
    return (a * a2 + c * b2, b * a2 + d * b2, a * c2 + c * d2, b * c2 + d * d2,
            a * e2 + c * f2 + e, b * e2 + d * f2 + f)


def T(tx, ty):
    return (1, 0, 0, 1, tx, ty)


def S(sx, sy=None, ox=0.0, oy=0.0):
    sy = sx if sy is None else sy
    return mul(T(ox, oy), mul((sx, 0, 0, sy, 0, 0), T(-ox, -oy)))


def R(deg, ox=0.0, oy=0.0):
    r = math.radians(deg)
    cs, sn = math.cos(r), math.sin(r)
    return mul(T(ox, oy), mul((cs, sn, -sn, cs, 0, 0), T(-ox, -oy)))


# --- SVG path sampling (absolute M L H V C Q A Z, as in Hedgehog.jsx) ---------------------------
_TOK = re.compile(r'[MLHVCQAZ]|-?\d*\.?\d+(?:e-?\d+)?')


def _arc(x1, y1, rx, ry, phi, fa, fs, x2, y2, n):
    p = math.radians(phi)
    cp, sp = math.cos(p), math.sin(p)
    dx, dy = (x1 - x2) / 2, (y1 - y2) / 2
    x1p, y1p = cp * dx + sp * dy, -sp * dx + cp * dy
    lam = x1p ** 2 / rx ** 2 + y1p ** 2 / ry ** 2
    if lam > 1:
        rx, ry = rx * math.sqrt(lam), ry * math.sqrt(lam)
    num = rx ** 2 * ry ** 2 - rx ** 2 * y1p ** 2 - ry ** 2 * x1p ** 2
    den = rx ** 2 * y1p ** 2 + ry ** 2 * x1p ** 2
    k = math.sqrt(max(0.0, num / den)) * (-1 if fa == fs else 1)
    cxp, cyp = k * rx * y1p / ry, -k * ry * x1p / rx
    cx = cp * cxp - sp * cyp + (x1 + x2) / 2
    cy = sp * cxp + cp * cyp + (y1 + y2) / 2
    ang = lambda ux, uy, vx, vy: math.atan2(ux * vy - uy * vx, ux * vx + uy * vy)
    t1 = ang(1, 0, (x1p - cxp) / rx, (y1p - cyp) / ry)
    dt = ang((x1p - cxp) / rx, (y1p - cyp) / ry, (-x1p - cxp) / rx, (-y1p - cyp) / ry)
    if not fs and dt > 0:
        dt -= 2 * math.pi
    elif fs and dt < 0:
        dt += 2 * math.pi
    out = []
    for i in range(1, n + 1):
        t = t1 + dt * i / n
        out.append((cx + rx * math.cos(t) * cp - ry * math.sin(t) * sp, cy + rx * math.cos(t) * sp + ry * math.sin(t) * cp))
    return out


def sample_path(d, steps=24):
    """→ [(points, closed)] for every subpath."""
    toks = _TOK.findall(d)
    i, cmd, cur, subs = 0, None, (0.0, 0.0), []
    pts = []

    def num():
        nonlocal i
        v = float(toks[i])
        i += 1
        return v

    while i < len(toks):
        if toks[i].isalpha():
            cmd = toks[i]
            i += 1
        if cmd == 'M':
            if pts:
                subs.append((pts, False))
            cur = (num(), num())
            pts = [cur]
            cmd = 'L'
        elif cmd == 'L':
            cur = (num(), num())
            pts.append(cur)
        elif cmd == 'H':
            cur = (num(), cur[1])
            pts.append(cur)
        elif cmd == 'V':
            cur = (cur[0], num())
            pts.append(cur)
        elif cmd == 'C':
            p1, p2, p3 = (num(), num()), (num(), num()), (num(), num())
            p0 = cur
            for s in range(1, steps + 1):
                t = s / steps
                a, b, c, e = (1 - t) ** 3, 3 * (1 - t) ** 2 * t, 3 * (1 - t) * t * t, t ** 3
                pts.append((a * p0[0] + b * p1[0] + c * p2[0] + e * p3[0], a * p0[1] + b * p1[1] + c * p2[1] + e * p3[1]))
            cur = p3
        elif cmd == 'Q':
            p1, p2 = (num(), num()), (num(), num())
            p0 = cur
            for s in range(1, steps + 1):
                t = s / steps
                a, b, c = (1 - t) ** 2, 2 * (1 - t) * t, t * t
                pts.append((a * p0[0] + b * p1[0] + c * p2[0], a * p0[1] + b * p1[1] + c * p2[1]))
            cur = p2
        elif cmd == 'A':
            rx, ry, phi, fa, fs, x, y = num(), num(), num(), num(), num(), num(), num()
            pts.extend(_arc(cur[0], cur[1], rx, ry, phi, int(fa), int(fs), x, y, steps))
            cur = (x, y)
        elif cmd == 'Z':
            subs.append((pts, True))
            pts = []
            cmd = None
    if pts:
        subs.append((pts, False))
    return subs


_CACHE = {}


def path(d):
    if d not in _CACHE:
        _CACHE[d] = sample_path(d)
    return _CACHE[d]


def ellipse_pts(cx, cy, rx, ry, n=48):
    return [(cx + rx * math.cos(2 * math.pi * i / n), cy + ry * math.sin(2 * math.pi * i / n)) for i in range(n)]


def round_rect_pts(x, y, w, h, r, n=8):
    r = min(r, w / 2, h / 2)
    out = []
    for cx, cy, a0 in ((x + w - r, y + r, -90), (x + w - r, y + h - r, 0), (x + r, y + h - r, 90), (x + r, y + r, 180)):
        for i in range(n + 1):
            a = math.radians(a0 + 90 * i / n)
            out.append((cx + r * math.cos(a), cy + r * math.sin(a)))
    return out


# --- canvas + pen ------------------------------------------------------------------------------
class Canvas:
    def __init__(self, bg=C['bg']):
        self.img = Image.new('RGB', (W * SS, H * SS), bg)
        self.d = ImageDraw.Draw(self.img)

    def frame(self):
        return self.img.resize((W, H), Image.LANCZOS)


class Pen:
    """Draws in local units through an affine matrix onto a Canvas (in output pixels x SS)."""

    def __init__(self, cv, m=IDENT):
        self.cv, self.m = cv, m

    def t(self, *ms):
        m = self.m
        for n in ms:
            m = mul(m, n)
        return Pen(self.cv, m)

    def pt(self, p):
        a, b, c, d, e, f = self.m
        return ((a * p[0] + c * p[1] + e) * SS, (b * p[0] + d * p[1] + f) * SS)

    def unit(self):
        a, b, c, d, _, _ = self.m
        return math.sqrt(abs(a * d - b * c)) * SS

    def fill(self, pts, color):
        if len(pts) > 2:
            self.cv.d.polygon([self.pt(p) for p in pts], fill=color)

    def stroke(self, pts, color, w=3, closed=False):
        px = [self.pt(p) for p in pts]
        lw = max(1, round(w * self.unit()))
        if closed:
            px = px + px[:2]
        self.cv.d.line(px, fill=color, width=lw, joint='curve')
        if not closed:
            r = lw / 2
            for x, y in (px[0], px[-1]):
                self.cv.d.ellipse([x - r, y - r, x + r, y + r], fill=color)

    def shape(self, pts, fill=None, stroke=None, w=3, closed=True):
        if fill is not None:
            self.fill(pts, fill)
        if stroke is not None:
            self.stroke(pts, stroke, w, closed)

    def path(self, d, fill=None, stroke=None, w=3):
        for pts, closed in path(d):
            self.shape(pts, fill, stroke, w, closed)

    def circle(self, cx, cy, r, fill=None, stroke=None, w=3):
        self.shape(ellipse_pts(cx, cy, r, r), fill, stroke, w)

    def capsule(self, p0, p1, r, fill=C['body'], stroke=C['fg'], w=3):
        """An outlined limb: a thick stroke in the outline colour, the fill inside it."""
        self.stroke([p0, p1], stroke, 2 * r + w)
        self.stroke([p0, p1], fill, 2 * r - w * .2)


# --- the hedgehog ------------------------------------------------------------------------------
def draw_hedgehog(pen, mood='idle', breath=0.0, blink=False, eye_dx=0.0, brow=None, drop_dy=None, spikes_dy=0.0,
                  smile=None, brow_path=BROW):
    """mood: idle | happy | sleep | worried. breath 0..1 (scaleY up to 1.025 from the feet line).
    brow: None or the brow's vertical offset (0 = the site's worried brow). drop_dy: None or the drop offset.
    smile: None = by mood (happy), or True/False."""
    g = pen.t(S(1, 1 + .025 * breath, 66, 76))
    g.t(T(0, spikes_dy)).path(SPIKES, stroke=C['accent'], w=4)
    g.path(BODY, fill=C['body'], stroke=C['fg'])
    g.circle(117, 61, 4.2, fill=C['fg'])
    g.circle(103, 62, 4, fill=C['cheek'])
    e = g.t(T(eye_dx, 0))
    if mood == 'happy':
        e.path(EYE_HAPPY, stroke=C['fg'])
    elif mood == 'sleep':
        e.path(EYE_SLEEP, stroke=C['fg'])
    else:
        e.t(S(1, .1 if blink else 1, 96, 50)).circle(96, 50, 3.4, fill=C['fg'])
    if smile or (smile is None and mood == 'happy'):
        g.path(SMILE, stroke=C['fg'])
    if brow is not None:
        g.t(T(0, brow)).path(brow_path, stroke=C['fg'])
    if drop_dy is not None:
        g.t(T(0, drop_dy)).path(DROP, fill=C['info'])
    pen.path(LEGS, stroke=C['fg'])


def spark(pen, which, s, color=C['spark']):
    """A site spark (A = left, B = right) scaled by s around its centre (twinkle)."""
    if s <= .02:
        return
    cx, cy = (30, 12) if which == 'A' else (104, 8.5)
    pen.t(S(s, s, cx, cy)).path(SPARK_A if which == 'A' else SPARK_B, fill=color)


def ground(pen, x0=26, x1=106, y=87):
    pen.stroke([(x0, y), (x1, y)], C['line'], 2)
