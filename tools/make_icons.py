#!/usr/bin/env python3
"""WG Companion icon set v2: glass ring + state core + per-state glyph.
Color is never the only signal: each state also changes shape (glyph)."""
import math, os
from PIL import Image, ImageDraw

SS = 8
RING = (226, 232, 255, 235)
INK = (10, 14, 26, 235)          # glyph ink on bright cores
DIM = (139, 147, 167, 255)       # off gray
STATES = {
    "off":     ((139, 147, 167, 90), "dot"),      # hollow-ish core, tiny dot
    "on":      ((52, 211, 153, 255), "flow"),     # green, flow chevrons
    "ready":   ((45, 212, 191, 255), "flow"),
    "error":   ((248, 113, 113, 255), "bang"),    # red, exclamation
    "blocked": ((251, 191, 36, 255), "bar"),      # amber, stop bar
    "busy0":   ((90, 162, 255, 255), None),       # blue, rotating highlight
    "busy1":   ((90, 162, 255, 255), None),
    "busy2":   ((90, 162, 255, 255), None),
    "busy3":   ((90, 162, 255, 255), None),
}
BUSY_STEP = {"busy0": 0, "busy1": 1, "busy2": 2, "busy3": 3}

def draw(name, core, glyph, size):
    s = size * SS
    img = Image.new("RGBA", (s, s), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    c = s / 2
    w_ring = s * 0.075
    r_ring = s * 0.44
    r_core = s * 0.27

    # glass ring
    d.ellipse([c - r_ring, c - r_ring, c + r_ring, c + r_ring],
              outline=RING, width=int(w_ring))
    # rotating highlight for busy states
    if name in BUSY_STEP:
        a0 = BUSY_STEP[name] * 90
        d.arc([c - r_ring, c - r_ring, c + r_ring, c + r_ring],
              start=a0, end=a0 + 110, fill=(255, 255, 255, 255), width=int(w_ring * 1.15))
    # core
    d.ellipse([c - r_core, c - r_core, c + r_core, c + r_core], fill=core)

    u = s / 64.0  # glyph unit
    if glyph == "dot":
        r = 3.4 * u
        d.ellipse([c - r, c - r, c + r, c + r], fill=(226, 232, 255, 200))
    elif glyph == "flow":
        lw = int(4.6 * u)
        for off in (-7.5 * u, 1.5 * u):
            d.line([(c + off, c - 7 * u), (c + off + 6 * u, c), (c + off, c + 7 * u)],
                   fill=INK, width=lw, joint="curve")
    elif glyph == "bang":
        w = 5.4 * u
        d.line([(c, c - 9 * u), (c, c + 2.5 * u)], fill=INK, width=int(w))
        r = 3.1 * u
        d.ellipse([c - r, c + 7 * u - r, c + r, c + 7 * u + r], fill=INK)
    elif glyph == "bar":
        w = 5.4 * u
        d.line([(c - 8.5 * u, c), (c + 8.5 * u, c)], fill=INK, width=int(w))
    return img.resize((size, size), Image.LANCZOS)

os.makedirs("icons", exist_ok=True)
for name, (core, glyph) in STATES.items():
    for sz in (16, 32, 48):
        draw(name, core, glyph, sz).save(f"icons/{name}{sz}.png")
    if name in ("off", "on"):
        draw(name, core, glyph, 128).save(f"icons/{name}128.png")

sizes = [16, 24, 32, 48, 64, 128, 256]
imgs = [draw("on", *STATES["on"], sz) for sz in sizes]
imgs[-1].save("app.ico", format="ICO", sizes=[(i.width, i.height) for i in imgs])
print("ok")
