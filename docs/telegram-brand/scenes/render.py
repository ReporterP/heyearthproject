"""Renders the hedgehog scenes for the Telegram bot and channel (spec 12 §12.6).

    python docs/telegram-brand/scenes/render.py            # writes server/backend/cmd/api/tgscenes/*.gif
    python docs/telegram-brand/scenes/render.py --check    # checks the written files
    python docs/telegram-brand/scenes/render.py --sheet out.png [--at 0.3]   # one frame of each, for review
"""
import math
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from PIL import Image  # noqa: E402

from hedgehog import (C, Canvas, Pen, R, S, T, W, H, draw_hedgehog, ellipse_pts, ground, mix,  # noqa: E402
                      round_rect_pts, spark)

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..', '..'))
OUT = os.path.join(ROOT, 'server', 'backend', 'cmd', 'api', 'tgscenes')
SLUGS = ['wave', 'coin', 'piggy', 'gift', 'alarm', 'wallet', 'sleep', 'news', 'support']
FRAME_MS = 40
K = 2.7  # px per hedgehog unit


def pen_at(cv, cx=64.0, cy=50.0):
    """Hedgehog units → output px with the point (cx, cy) of the scene in the middle of the frame."""
    return Pen(cv).t(T(W / 2 - cx * K, H / 2 + 6 - cy * K), S(K))


def wave01(t, n=1):
    """0..1..0, n times per loop."""
    return .5 - .5 * math.cos(2 * math.pi * n * t)


def ease_io(x):
    return .5 - .5 * math.cos(math.pi * max(0.0, min(1.0, x)))


def bump(x, a, b):
    """0 outside [a, b], a sine bump inside."""
    if x <= a or x >= b:
        return 0.0
    return math.sin(math.pi * (x - a) / (b - a))


def blink_at(t, at=.8):
    return at <= t < at + .035


def twinkle(t, phase):
    x = (t + phase) % 1.0
    return max(0.0, math.sin(math.pi * min(1.0, x / .55))) if x < .55 else 0.0


def coin(pen, cx, cy, r, flip):
    """flip = cos of the turn: 1 face, -1 back, 0 edge-on."""
    sx = max(.12, abs(flip))
    p = pen.t(S(sx, 1, cx, cy))
    face = flip >= 0
    p.circle(cx, cy, r, fill=C['accent'] if face else mix(C['accent'], C['ink'], .55), stroke=C['ink'], w=1.4)
    p.circle(cx, cy, r * .62, stroke=C['ink'] if face else C['accent'], w=1.6)


# --- scenes: each takes t in [0, 1) and returns an RGB frame -------------------------------------
def scene_wave(t):
    cv = Canvas()
    p = pen_at(cv, 66, 46)
    ground(p)
    sh = (100, 50)
    a = math.radians(-62 + 24 * math.sin(2 * math.pi * 2 * t))
    paw = (sh[0] + 30 * math.cos(a), sh[1] + 30 * math.sin(a))
    p.capsule(sh, paw, 5.2)
    draw_hedgehog(p, 'idle', breath=wave01(t), blink=blink_at(t, .82), smile=True)
    # motion marks by the paw at the ends of the swing
    s = abs(math.sin(2 * math.pi * 2 * t))
    if s > .75:
        k = (s - .75) / .25
        col = mix(C['spark'], C['bg'], k)
        side = 1 if math.sin(2 * math.pi * 2 * t) > 0 else -1
        for i, rr in enumerate((9, 13)):
            ang = math.atan2(paw[1] - sh[1], paw[0] - sh[0]) + side * .55
            c0 = (paw[0] + rr * math.cos(ang - .25), paw[1] + rr * math.sin(ang - .25))
            c1 = (paw[0] + rr * math.cos(ang + .25), paw[1] + rr * math.sin(ang + .25))
            p.stroke([c0, c1], col, 2.2)
    return cv.frame()


def scene_coin(t):
    cv = Canvas()
    x = (t * 2) % 1.0  # two hops per loop
    jump = math.sin(math.pi * x / .6) if x < .6 else 0.0
    squash = .06 * bump(x, .6, .78)
    p0 = pen_at(cv, 64, 38)
    ground(p0)
    p = p0.t(T(0, -13 * jump), S(1 + squash, 1 - squash, 66, 86))
    draw_hedgehog(p, 'happy', breath=0)
    # the coin flies higher than the hedgehog and turns
    cy = 2 - 22 * math.sin(math.pi * x / .6) if x < .6 else 2.0
    coin(p0, 64, cy - 13 * jump * .2, 10, math.cos(2 * math.pi * 2 * t))
    spark(p0, 'A', twinkle(t, 0))
    spark(p0, 'B', twinkle(t, .5))
    return cv.frame()


def piggy(p, squash=0.0):
    q = p.t(T(10, 0), S(1 + squash * .6, 1 - squash, 152, 84))
    for x0 in (138, 146, 158, 166):
        q.stroke([(x0, 76), (x0, 84)], C['fg'], 3)
    q.path('M170 62 Q178 58 175 52 Q172 48 170 54', stroke=C['fg'], w=2.4)  # tail
    q.shape(ellipse_pts(152, 64, 21, 15), fill=C['hover'], stroke=C['fg'])
    q.shape([(140, 53), (143, 42), (149, 51)], fill=C['hover'], stroke=C['fg'], w=2.6)  # ear
    q.shape(ellipse_pts(130, 66, 5.5, 6.5), fill=mix(C['danger'], C['hover'], .5), stroke=C['fg'], w=2.6)  # snout
    q.circle(128.6, 64.4, 1.1, fill=C['fg'])
    q.circle(128.6, 68, 1.1, fill=C['fg'])
    q.circle(140, 59, 2.2, fill=C['fg'])  # eye
    q.stroke([(147, 49.6), (158, 49.6)], C['bg'], 3.4)  # slot
    return q


def scene_piggy(t):
    cv = Canvas()
    p = pen_at(cv, 100, 48)
    ground(p, 26, 186)
    draw_hedgehog(p, 'happy', breath=wave01(t))
    fall = (t - .12) / .3  # the coin falls 0.12..0.42, lands in the slot
    sq = .07 * bump(t, .42, .6)
    if 0 <= fall <= 1:
        cy = -14 + (52 + 14) * fall * fall
        coin(p, 162.5, cy, 8, .3)
    piggy(p, sq)
    for which, ph in (('A', .55), ('B', .75)):
        s = bump(t, ph - .15, ph + .25)
        if which == 'A':
            spark(p.t(T(122, 26)), 'A', s)
        else:
            spark(p.t(T(76, 22)), 'B', s)
    return cv.frame()


def scene_gift(t):
    cv = Canvas()
    p = pen_at(cv, 84, 48)
    ground(p, 26, 160)
    draw_hedgehog(p, 'happy', breath=wave01(t), smile=True)
    lift = bump(t, .45, .8)
    # box
    p.shape(round_rect_pts(124, 61, 28, 25, 2), fill=C['hover'], stroke=C['fg'])
    p.stroke([(138, 62), (138, 85)], C['accent'], 4)
    # sparks out of the box while the lid is up
    if lift > .05:
        spark(p.t(T(98, 30)), 'A', lift)
        spark(p.t(T(48, 26)), 'B', bump(t, .52, .86))
    lid = p.t(T(0, -9 * lift), R(-10 * lift, 122, 61))
    lid.path('M132 52 Q124 43 130 41 Q135 41 138 52 Q141 41 146 41 Q152 43 144 52', stroke=C['accent'], w=3)
    lid.shape(round_rect_pts(121, 52, 34, 9, 2), fill=C['hover'], stroke=C['fg'])
    lid.stroke([(138, 53), (138, 60)], C['accent'], 4)
    return cv.frame()


def alarm_clock(p, ang, ring):
    q = p.t(R(ang, 140, 48))
    q.stroke([(130, 60), (125, 68)], C['fg'], 3)
    q.stroke([(150, 60), (155, 68)], C['fg'], 3)
    for cx in (128, 152):
        q.circle(cx, 33, 6.2, fill=C['accent'], stroke=C['fg'], w=2.6)
    q.stroke([(140, 28), (140, 31)], C['fg'], 3)
    q.circle(140, 48, 16, fill=C['raised'], stroke=C['fg'])
    q.stroke([(140, 48), (140, 38)], C['fg'], 2.6)
    q.stroke([(140, 48), (147, 52)], C['fg'], 2.6)
    q.circle(140, 48, 1.8, fill=C['accent'])
    if ring > .05:
        col = mix(C['spark'], C['bg'], ring)
        for sgn in (-1, 1):
            cx = 140 + sgn * 27
            for dy in (-6, 0, 6):
                q.stroke([(cx, 40 + dy), (cx + sgn * 5, 40 + dy * 1.4)], col, 2.2)


def scene_alarm(t):
    cv = Canvas()
    p = pen_at(cv, 86, 46)
    ground(p, 26, 164)
    env = max(bump(t, 0, .28), bump(t, .5, .78))
    ang = 9 * env * math.sin(2 * math.pi * 14 * t)
    jump = .6 * max(bump(t, .02, .16), bump(t, .52, .66))
    hp = p.t(T(0, -4 * jump))
    draw_hedgehog(hp, 'idle', breath=wave01(t), blink=False, brow=0, brow_path='M90 40 Q95 37 100 40')
    alarm_clock(p, ang, env)
    return cv.frame()


def moth(p, x, y, flap, op):
    """A small grey moth: two wings beating (x-scale), a body."""
    wing = mix(C['fg'], C['bg'], .5 * op)
    k = .3 + .7 * abs(math.sin(flap))
    for sgn in (-1, 1):
        p.t(S(k, 1, x, y)).shape(ellipse_pts(x + sgn * 5, y - 2, 5, 3.6), fill=wing)
        p.t(S(k, 1, x, y)).shape(ellipse_pts(x + sgn * 3.6, y + 2.6, 3, 2.2), fill=wing)
    p.shape(ellipse_pts(x, y, 1.6, 4.2), fill=mix(C['fg'], C['bg'], .85 * op))


def scene_wallet(t):
    cv = Canvas()
    p = pen_at(cv, 88, 46)
    ground(p, 26, 170)
    draw_hedgehog(p, 'worried', breath=wave01(t), eye_dx=1.2, brow=0, drop_dy=5 * wave01(t))
    # an open wallet: body, the empty dark slot on top, the clasp tab on the right
    p.shape(round_rect_pts(120, 58, 42, 28, 5), fill=C['raised'], stroke=C['fg'])
    p.shape(round_rect_pts(125, 55.5, 32, 6, 3), fill=C['bg'], stroke=C['fg'], w=2.4)
    p.shape(round_rect_pts(150, 66, 16, 12, 4), fill=C['hover'], stroke=C['fg'], w=2.6)
    p.circle(157, 72, 2.4, fill=C['accent'])
    p.stroke([(126, 79), (146, 79)], C['line2'], 1.8)
    # a moth flies out of it: nothing else inside
    ph = t
    op = min(1.0, ph / .12) * (1 - max(0.0, ph - .7) / .3)
    mx = 141 + 22 * ph + 4 * math.sin(ph * 2 * math.pi * 2)
    my = 54 - 46 * ph
    moth(p, mx, my, t * 2 * math.pi * 7, op)
    return cv.frame()


def zee(p, x, y, s, op):
    col = mix(C['fg'], C['bg'], op)
    p.stroke([(x, y), (x + s, y), (x, y + s), (x + s, y + s)], col, 2.2)


def scene_sleep(t):
    cv = Canvas()
    p = pen_at(cv, 70, 44)
    ground(p)
    draw_hedgehog(p, 'sleep', breath=wave01(t))
    for i, s in enumerate((9, 7.5, 6)):
        ph = (t + i / 3) % 1.0
        op = min(1.0, ph / .25) * (1 - ph)
        zee(p, 104 + 16 * ph, 34 - 30 * ph, s, op)
    return cv.frame()


def newspaper(p, fold):
    q = p.t(R(-5, 124, 60))
    left = [(100, 38), (124, 38), (124, 82), (100, 82)]
    q.shape(left, fill=C['fg'])
    q.shape(round_rect_pts(103, 42, 18, 6, 1), fill=C['body'])
    for y in (52, 57, 62, 67, 72, 77):
        q.stroke([(104, y), (120, y)], C['line2'], 1.6)
    r = q.t(S(1 - .45 * fold, 1, 124, 60))
    r.shape([(124, 38), (148, 38), (148, 82), (124, 82)], fill=mix(C['fg'], C['line2'], .9))
    r.shape(round_rect_pts(127, 42, 18, 12, 1), fill=C['accent'])
    for y in (59, 64, 69, 74):
        r.stroke([(128, y), (144, y)], C['line2'], 1.6)
    q.stroke([(124, 38), (124, 82)], C['line2'], 1.4)


def scene_news(t):
    cv = Canvas()
    p = pen_at(cv, 80, 48)
    ground(p, 26, 150)
    x = (t * 2) % 1.0
    eye = 2.6 * (x / .8) if x < .8 else 2.6 * (1 - (x - .8) / .2)
    draw_hedgehog(p, 'idle', breath=wave01(t), eye_dx=eye - 1.8, blink=blink_at(t, .9))
    newspaper(p, bump(t, .55, .8))
    return cv.frame()


def scene_support(t):
    cv = Canvas()
    p = pen_at(cv, 66, 48)
    ground(p)
    draw_hedgehog(p, 'idle', breath=wave01(t), blink=blink_at(t, .7), smile=True)
    p.path('M86 46 Q84 16 66 22', stroke=C['fg'], w=3.4)  # headband over the head
    p.path('M89 62 Q96 74 108 71', stroke=C['fg'], w=2.4)  # mic boom
    p.circle(110, 70.5, 2.8, fill=C['accent'], stroke=C['fg'], w=1.6)
    p.shape(round_rect_pts(82, 45, 9, 15, 3), fill=C['accent'], stroke=C['fg'], w=2.6)  # ear cup
    for i, rr in enumerate((6, 10)):
        op = bump((t * 2 + i * .22) % 1.0, 0, .6)
        if op > .03:
            pts = [(110 + rr * math.cos(a), 70.5 + rr * math.sin(a)) for a in [math.radians(d) for d in range(-38, 39, 6)]]
            p.stroke(pts, mix(C['spark'], C['bg'], op), 2.2)
    return cv.frame()


SCENES = {s: globals()['scene_' + s] for s in SLUGS}
LOOP_FRAMES = {s: 60 for s in SLUGS} | {'sleep': 80}


def palette_for(frames):
    """One palette for all frames: quantise a strip of sampled frames together."""
    pick = frames[:: max(1, len(frames) // 8)][:8]
    strip = Image.new('RGB', (W, H * len(pick)))
    for i, f in enumerate(pick):
        strip.paste(f, (0, H * i))
    return strip.quantize(colors=96, method=Image.Quantize.MEDIANCUT)


def render(slug):
    n = LOOP_FRAMES[slug]
    frames = [SCENES[slug](i / n) for i in range(n)]
    pal = palette_for(frames)
    q = [f.quantize(palette=pal, dither=Image.Dither.NONE) for f in frames]
    os.makedirs(OUT, exist_ok=True)
    path = os.path.join(OUT, slug + '.gif')
    q[0].save(path, save_all=True, append_images=q[1:], duration=FRAME_MS, loop=0, optimize=False, disposal=1)
    return path


def check():
    bad = []
    for slug in SLUGS:
        path = os.path.join(OUT, slug + '.gif')
        if not os.path.exists(path):
            bad.append(f'{slug}: missing')
            continue
        size = os.path.getsize(path)
        im = Image.open(path)
        frames, durs = 0, set()
        try:
            while True:
                frames += 1
                durs.add(im.info.get('duration'))
                im.seek(im.tell() + 1)
        except EOFError:
            pass
        im = Image.open(path)
        if im.size != (W, H):
            bad.append(f'{slug}: size {im.size}')
        if not 50 <= frames <= 80:
            bad.append(f'{slug}: {frames} frames')
        if durs != {FRAME_MS}:
            bad.append(f'{slug}: durations {durs}')
        if im.info.get('loop') != 0:
            bad.append(f'{slug}: loop {im.info.get("loop")}')
        if size > 1_048_576:
            bad.append(f'{slug}: {size} bytes')
        print(f'{slug:8} {frames:3} frames  {size / 1024:6.0f} KB')
    if bad:
        print('FAIL\n' + '\n'.join(bad))
        return 1
    print(f'{len(SLUGS)} scenes OK')
    return 0


def sheet(out, at):
    cols = 3
    img = Image.new('RGB', (W * cols, H * math.ceil(len(SLUGS) / cols)), C['line'])
    for i, slug in enumerate(SLUGS):
        img.paste(SCENES[slug](at), ((i % cols) * W, (i // cols) * H))
    img.save(out)
    print(out)


if __name__ == '__main__':
    args = sys.argv[1:]
    if '--check' in args:
        sys.exit(check())
    if '--sheet' in args:
        at = float(args[args.index('--at') + 1]) if '--at' in args else .3
        sheet(args[args.index('--sheet') + 1], at)
        sys.exit(0)
    only = [a for a in args if a in SLUGS] or SLUGS
    for slug in only:
        print(render(slug))
