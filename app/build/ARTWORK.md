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

## Current artwork

Green's happy bean eyes are larger for readability at Dock sizes. Blue keeps
its rounded wink, red keeps the matched diagonal beans, and silver keeps its
original eyes. Shadows are slightly softer, and the speech bubble sits farther
up and left with more clearance from the tile's right edge. The spherical
silhouettes, outer mask, and 100-pixel transparent inset are unchanged.

## Latest edit prompt

Built-in imagegen edit using the previous full-bleed artwork and the user's
Dock screenshot showing the crowded speech bubble.

> Precise production app-icon edit of image 1. Image 2 is the user's small Dock screenshot showing that the speech bubble is crowded against the outer right corner. Make ONLY these three refinements:
> 1. Enlarge BOTH GREEN happy bean eyes by about 30 percent in width and height, scaling each about its existing center. Keep their plump low curved arch/bean shape, rounded ends and shallow concave bottom; do not turn them into sharp chevrons, eyebrows or open pill eyes. They must be noticeably bigger and equally sized, readable at small Dock sizes.
> 2. Move the ENTIRE ivory speech bubble LEFT by about 110 pixels and UP by about 25 pixels on this 1254x1254 texture. Current bubble spans roughly x=900..1195, y=108..365 including tail. Target spans roughly x=790..1085, y=83..340. Preserve its current size, rounded shape, exactly three black dots, and short down-left tail. The important change is a broad dark charcoal gap to the RIGHT of the bubble, so it sits comfortably inside the icon's upper-right corner rather than hugging it. Do not enlarge the bubble or move it closer to the right edge. Our outer mask is applied later.
> 3. Reduce shadow intensity just a little, around 15 to 20 percent: soften the cast shadow under the bubble and dark contact shadows between buddies, and lift the deepest body shading slightly. Retain the three-dimensional spherical ceramic finish, saturated blue/green/coral colors, pearl silver foreground buddy, fine highlights and recessed black eyes. Avoid flat vector shading, washed-out colors, or added texture.
> Preserve ALL other features: positions and round silhouettes of the four buddies, blue wink and open eye, red's matching diagonally opposed bean slots, white buddy's eyes, charcoal background, and the overall composition.
> OUTPUT remains the same FULL-BLEED OPAQUE SQUARE artwork texture. Charcoal fills the top corners, foreground spheres extend through the bottom canvas edge. No outer rounded icon frame, no extra margin or transparent padding, no checkerboard, no border, no text, arrows or new objects.
