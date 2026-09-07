#!/usr/bin/env python3
"""Draw the grokbox icons. Run it from app/build:  python3 icon.py

Writes icon.svg / icon.png (the app icon) and tray.svg / tray.png (the menu
bar one). The PNGs are what main.go embeds, and they are committed, so this
only needs running when the art changes. Needs rsvg-convert for the PNGs.

Two things here are not guesses. The canvas is 1024 but the shape is 824
centred in it: that 9.8% margin is Apple's icon grid, and an icon that fills
its canvas instead renders visibly larger than every neighbour in the dock.
And the corners are a superellipse rather than a rounded rect, because the
macOS squircle has continuous curvature and an `rx` gives itself away next to
a real one.
"""
import math

CANVAS = 1024
BODY = 824          # Apple's icon grid
OFF = (CANVAS - BODY) / 2


def superellipse(cx, cy, a, b, n, steps=256, rot=0.0):
    """A rounded blob. n=2 is an ellipse, n=5 is close to Apple's squircle."""
    pts = []
    for i in range(steps):
        t = 2 * math.pi * i / steps
        ct, st = math.cos(t), math.sin(t)
        x = a * math.copysign(abs(ct) ** (2 / n), ct)
        y = b * math.copysign(abs(st) ** (2 / n), st)
        if rot:
            r = math.radians(rot)
            x, y = x * math.cos(r) - y * math.sin(r), x * math.sin(r) + y * math.cos(r)
        pts.append((cx + x, cy + y))
    d = "M %.2f %.2f " % pts[0] + " ".join("L %.2f %.2f" % p for p in pts[1:]) + " Z"
    return d


def capsule(cx, cy, w, h, rot):
    """An eye: a rounded slot, tilted."""
    r = w / 2
    return (
        f'<rect x="{cx - w/2:.2f}" y="{cy - h/2:.2f}" width="{w:.2f}" height="{h:.2f}" '
        f'rx="{r:.2f}" transform="rotate({rot} {cx:.2f} {cy:.2f})"/>'
    )


def buddy(cx, cy, w, h, fill, eyes=True, eye_scale=1.0, tilt=16, eye_fill="#15151A", extra=""):
    """One of the little ones. Body plus two tilted slot eyes."""
    body = superellipse(cx, cy, w / 2, h / 2, 2.9)
    out = [f'<path d="{body}" fill="{fill}"/>{extra}']
    if eyes:
        ew = 0.115 * w * eye_scale
        eh = 0.30 * h * eye_scale
        gap = 0.235 * w
        drop = -0.01 * h
        out.append(f'<g fill="{eye_fill}">')
        out.append(capsule(cx - gap, cy + drop, ew, eh, -tilt))
        out.append(capsule(cx + gap, cy + drop - 0.03 * h, ew, eh, -tilt))
        out.append("</g>")
    return "\n    ".join(out)


def bubble(x, y, w, h, r, fill, dot, tail):
    """A speech bubble with three dots. tail is (tipx, tipy)."""
    tx, ty = tail
    bx, by = x + w * 0.22, y + h
    cx_, cy_ = x + w * 0.44, y + h
    parts = [
        f'<path d="M {x + r} {y} H {x + w - r} A {r} {r} 0 0 1 {x + w} {y + r} '
        f'V {y + h - r} A {r} {r} 0 0 1 {x + w - r} {y + h} '
        f'H {cx_:.1f} L {tx:.1f} {ty:.1f} L {bx:.1f} {by:.1f} '
        f'H {x + r} A {r} {r} 0 0 1 {x} {y + h - r} '
        f'V {y + r} A {r} {r} 0 0 1 {x + r} {y} Z" fill="{fill}"/>'
    ]
    dy = y + h / 2
    for i, dx in enumerate((0.27, 0.5, 0.73)):
        parts.append(f'<circle cx="{x + w * dx:.1f}" cy="{dy:.1f}" r="{h * 0.093:.1f}" fill="{dot}"/>')
    return "\n    ".join(parts)


def render():
    o = OFF
    squircle = superellipse(CANVAS / 2, CANVAS / 2, BODY / 2, BODY / 2, 5.0, steps=512)

    # Local coordinates inside the 824 body, translated by OFF at draw time.
    # The two behind sit high enough that the one in front never cuts through
    # an eye — a half-covered eye reads as a mistake at every size.
    # The one in front runs off the bottom of the frame on purpose: cropped by
    # the edge it fills the tile, and a group with air all round it reads small
    # next to its neighbours even when the canvas is the right size.
    front = (o + 412, o + 596, 436, 436)          # the one in front
    left = (o + 208, o + 342, 274, 274)           # peeking behind, left
    right = (o + 616, o + 330, 258, 258)          # peeking behind, right

    svg = f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {CANVAS} {CANVAS}" width="{CANVAS}" height="{CANVAS}" role="img" aria-label="grokbox">
  <defs>
    <linearGradient id="bg" x1="0" y1="0" x2="0.35" y2="1">
      <stop offset="0" stop-color="#1B1B21"/>
      <stop offset="1" stop-color="#08080B"/>
    </linearGradient>
    <linearGradient id="front" x1="0.15" y1="0" x2="0.8" y2="1">
      <stop offset="0" stop-color="#FBFBFD"/>
      <stop offset="0.55" stop-color="#DCDCE3"/>
      <stop offset="1" stop-color="#9C9CAA"/>
    </linearGradient>
    <linearGradient id="teal" x1="0.2" y1="0" x2="0.8" y2="1">
      <stop offset="0" stop-color="#8FE0CE"/>
      <stop offset="1" stop-color="#4FA894"/>
    </linearGradient>
    <linearGradient id="coral" x1="0.2" y1="0" x2="0.8" y2="1">
      <stop offset="0" stop-color="#F58A6E"/>
      <stop offset="1" stop-color="#C4523A"/>
    </linearGradient>
    <clipPath id="body"><path d="{squircle}"/></clipPath>
    <!-- The one in front needs to read as in front; on a black ground only a
         shadow says so. -->
    <filter id="lift" x="-30%" y="-30%" width="160%" height="160%">
      <feDropShadow dx="0" dy="10" stdDeviation="26" flood-color="#000000" flood-opacity="0.55"/>
    </filter>
  </defs>

  <path d="{squircle}" fill="url(#bg)"/>

  <g clip-path="url(#body)">
    {buddy(*left, "url(#teal)", eye_scale=0.92, tilt=14)}
    {buddy(*right, "url(#coral)", eye_scale=0.92, tilt=14)}
    {bubble(o + 510, o + 44, 240, 150, 51, "#F4F4F8", "#15151A", (o + 562, o + 228))}
    <g filter="url(#lift)">{buddy(*front, "url(#front)", tilt=17)}</g>
  </g>
</svg>
'''
    return svg


def main():
    import os
    import subprocess

    here = os.path.dirname(os.path.abspath(__file__))
    for name, svg, size in (("icon", render(), 1024), ("tray", tray(), 64)):
        svg_path = os.path.join(here, name + ".svg")
        png_path = os.path.join(here, name + ".png")
        with open(svg_path, "w") as f:
            f.write(svg)
        subprocess.run(
            ["rsvg-convert", "-w", str(size), "-h", str(size), svg_path, "-o", png_path],
            check=True,
        )
        print("wrote", svg_path, "and", png_path)




def tray():
    """The menu bar icon: one of them, alone, as a template image.

    Only alpha matters — macOS recolours it for light and dark menu bars — so
    the eyes are holes, not dark fills.
    """
    S = 64
    head = superellipse(S / 2, S / 2 + 1, 23, 23, 2.9)
    eye_l = capsule(S / 2 - 10.5, S / 2 + 0.5, 6.0, 15.5, -16)
    eye_r = capsule(S / 2 + 10.5, S / 2 - 1.0, 6.0, 15.5, -16)
    return f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {S} {S}" width="{S}" height="{S}" role="img" aria-label="grokbox">
  <mask id="face">
    <rect width="{S}" height="{S}" fill="black"/>
    <path d="{head}" fill="white"/>
    <g fill="black">
      {eye_l}
      {eye_r}
    </g>
  </mask>
  <rect width="{S}" height="{S}" fill="black" mask="url(#face)"/>
</svg>
'''


if __name__ == "__main__":
    main()
