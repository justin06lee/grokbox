# App icon artwork

`icon-artwork.png` is the full-bleed source artwork: four sculpted bot buddies
in pearl silver, jade green, blue, and coral, with an ivory speech bubble.
It was created with the built-in imagegen tool using the user's group sketch
and the installed Grok Bot icon as visual references.

`icon.py` wraps the artwork in a continuous-corner mask, centered at 824×824
inside a transparent 1024×1024 canvas. This matches the visible width of the
installed ChatGPT icon. Keep that 100-pixel inset when changing the art:
filling the canvas makes grokbox look oversized in the Dock.

Regenerate the committed app and template menu bar assets with:

```sh
python3 app/build/icon.py
```

This needs `rsvg-convert`. The script writes `icon.svg`, `icon.png`, `tray.svg`,
and `tray.png`. The SVG wrapper references the adjacent `icon-artwork.png`;
keep them together. Normal app builds consume the committed PNGs and do not
need Python, an image generator, or an SVG renderer. `make app` creates all
macOS icon resolutions, installs the app, and opens it.

## Art direction

The first generation used the Grok Bot reference's rounded silver dome,
paired inset diagonal pill eyes, fine rim highlights, and soft studio lighting.
The user's sketch supplied the four-buddy composition and speech bubble.
The original generated exterior contained a painted checkerboard, so the
production source uses opaque full-bleed artwork. Transparency and icon
geometry are supplied by the SVG wrapper, independently of generation.

## Final production prompt

Built-in imagegen edit, using the first generated group icon as its reference:

> Precise production artwork edit: return ONLY the interior artwork of this app icon as a FULL-BLEED OPAQUE SQUARE IMAGE. This is a texture that our macOS app build will clip into a squircle and add its own transparent padding. So remove the checkerboard and remove ALL outer margin, padding, drop shadow and rounded tile corners. ZOOM/CROP to the dark tile to fill the entire image edge-to-edge, and extend the dark charcoal color into all FOUR corners to make a perfectly solid square. Every pixel of the output canvas must be artwork, zero checkerboard or white backdrop. Keep the exact four buddy composition, identities, silver/green/blue/coral colors, beautiful sculpted finish, all eight black pill eyes, and ivory three-dot speech bubble from the reference. Preserve positions and proportions inside the tile. The silver buddy at lower left and coral buddy at lower right run cleanly off the bottom edge; the charcoal tile fills every top and side corner. No outer roundness, no icon mockup framing, no margin, no border. A square 1024x1024 full bleed texture of just the artwork.

Final refinement, using that full-bleed output as the reference:

> Precise object edit of this full-bleed square app-icon texture. Change ONLY the speech bubble: shrink it by about 15 percent and move it left so there is a clear dark charcoal gap of about 8 percent of the entire canvas width between its rightmost edge and the right canvas edge. Keep its top at about the same height, and preserve the three inset black dots, ivory material and short down-left tail. Keep EVERYTHING ELSE pixel-faithful: all four blue/green/silver/coral sculpted buddy domes, every eye, lighting, arrangement, scale and cropping. Preserve the full-bleed opaque square format, including the charcoal top corners; do not add rounded outer corners, margins, background, text or checkerboard. Only the bubble needs more breathing room so it will survive a rounded macOS icon mask.
