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

## Current expressions and silhouettes

Blue's closed wink and green's two happy eyes are wide, plump curved beans
with rounded ends and a shallow lower notch. Red has matching bean slots
at opposite diagonal tilts. Blue's open eye and silver's two eyes keep the
original shape. The round silhouettes, bubble placement, outer mask, and
100-pixel inset are unchanged.

## Latest edit prompts

Built-in imagegen, using the previous full-bleed artwork and the user's
annotated screenshot. The second edit refines the red eye rotation.

> Precise eye-shape edit. Image 1 is the current full-bleed production artwork. Image 2 is the user's annotated screenshot: painted shapes and arrows are instructions ONLY and must not appear in the final image. Change ONLY the five eye shapes specified below, keeping the rest of image 1 pixel-faithful: the four spherical bodies, silhouettes, placement, silver buddy and both of its eyes, blue's open right eye, colors, speech bubble, backdrop, lighting and framing.
>
> GREEN: replace its two pointed chevron-shaped closed eyes with two matching plump curved BEAN / crescent shapes following the two cream-colored shapes drawn above the head in image 2. The cream is just sketch ink: the actual eyes must remain BLACK recessed sockets. These are low, wide happy closed-eye arches, with a smooth convex domed top, a very shallow concave lower edge, fat rounded ends, and absolutely NO sharp ^ peak or deep V notch. Each eye should be roughly twice as wide as tall; both are identical in size and thickness, projected naturally onto the green sphere at the existing eye positions. A soft rounded upside-down smile, like a chunky kidney bean rotated horizontally. No extra eyebrows or eyes.
>
> BLUE: make only its closed left wink a matching plump horizontal happy bean with a gently domed upper edge and a shallow concave lower edge, as the black shape drawn over blue's eye in image 2 indicates. Thicker and fuller than the existing thin slit. Keep the open right eye unchanged.
>
> RED: make its two eyes an EXACT MATCH in unrotated shape, length, width, end radius and thickness: a pair of rounded bean-shaped black inset eye slots from the same shape family as blue's open eye and silver's eyes, with only a subtle organic bean curvature. They should not look like one wide eye and one narrow eye. Rotate the LEFT bean about +45 degrees from vertical so it reads /, and the RIGHT bean about -45 degrees from vertical so it reads backslash. Equal and opposite diagonal tilt, symmetric angles, same size. Preserve their existing centers and the red sphere's overall face placement. Softly rounded ends, no pointed corners.
>
> Match the existing sculpted ceramic finish and subtle rim highlights around all edited sockets. Keep all eyes BLACK. Do NOT include the cream guide shapes above green, any arrows, handwriting, labels, mouths or new objects.
> OUTPUT: same full-bleed OPAQUE SQUARE texture as image 1, with charcoal filling the top corners and foreground spheres cropped by the bottom canvas edge. NO rounded outer tile, margin, padding, transparency, checkerboard, border or mockup; our build script applies the native macOS mask and transparent padding.

> Edit exactly ONE shape: the RIGHTMOST black eye on the RED sphere in the lower-right corner. Rotate that eye COUNTERCLOCKWISE by an additional 25 degrees around its center. Its long axis should go from upper-left to lower-right at a strong 45-degree diagonal, like the descending arm of an upside-down V. It must be the mirror-angle counterpart of red's left eye, which slopes the other way. The current right eye is too upright. Make the two red eye sockets equally long and equally thick, rounded capsule/bean shapes. Do not move their centers. This is a visible geometric rotation, not a tiny adjustment. Leave every other pixel of the supplied artwork unchanged. Preserve all other eyes, spheres, shading, speech bubble, full-bleed square format, and colors. No added borders, annotations, texture or framing.
