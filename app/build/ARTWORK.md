# App icon artwork

`icon-artwork.png` is the full-bleed source artwork: four sculpted bot buddies
in pearl silver, jade green, blue, and coral, against a charcoal background.
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

## Current artwork

Green's happy bean eyes are larger for readability at Dock sizes. Blue keeps
its rounded wink, red keeps the matched diagonal beans, and silver keeps its
original eyes. Shadows are slightly softer. The speech bubble, its three dots,
tail, and cast shadow have been removed, restoring the charcoal background. The spherical
silhouettes, outer mask, and 100-pixel transparent inset are unchanged.

## Latest edit prompt

Built-in imagegen edit using the previous full-bleed artwork.

> Use case: precise-object-edit. Edit target: the supplied Grok Box app icon artwork. Remove ONLY the entire ivory speech bubble in the upper right, including its three black dots, its tail, and its cast shadow. Seamlessly fill that area with the same surrounding dark charcoal textured background. Preserve all four bot buddies exactly: their positions, sizes, silhouettes, pearl silver, jade green, blue, coral colors, inset eyes and facial expressions, ceramic texture, highlights and shading. Preserve the original square full-bleed opaque composition and framing. Do not move or enlarge any characters. No bubble, dots, new objects, text, borders, added margins, outer rounded mask, transparency, or checkerboard. The only intended change is removal of the speech bubble and its shadow.
