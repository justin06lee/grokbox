#!/usr/bin/env python3
"""Package the grokbox icons: python3 app/build/icon.py

Writes icon.svg / icon.png (the app icon) and tray.svg / tray.png (the menu
bar one). The PNGs are what main.go embeds, and they are committed, so this
only needs running when the art changes. Needs rsvg-convert for the PNGs.
The app artwork is icon-artwork.png; this script supplies the native icon
mask and transparent padding. See ARTWORK.md for its source and prompt.

The canvas is 1024 but the shape is 824 centred in it, matching the installed
ChatGPT icon's visible width. Filling the canvas makes an icon look oversized
in the Dock. A superellipse approximates the continuous macOS corners.
"""
import math

CANVAS = 1024
BODY = 824          # Matches ChatGPT.app's icon footprint.
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


def render():
    squircle = superellipse(CANVAS / 2, CANVAS / 2, BODY / 2, BODY / 2, 5.0, steps=512)
    return f'''<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 {CANVAS} {CANVAS}" width="{CANVAS}" height="{CANVAS}" role="img" aria-label="grokbox: four bot buddies and a speech bubble">
  <defs>
    <clipPath id="body"><path d="{squircle}"/></clipPath>
  </defs>
  <image x="{OFF}" y="{OFF}" width="{BODY}" height="{BODY}"
         xlink:href="icon-artwork.png" clip-path="url(#body)"/>
</svg>
'''


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
