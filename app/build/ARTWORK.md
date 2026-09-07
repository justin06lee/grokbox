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

Blue has one closed wink and one upright eye; green has two happy `^^` eyes;
red has opposing diagonal slots following its existing head angle. Blue and
silver use rounder silhouettes, and the speech bubble follows the top-right
curve of the app tile. The outer mask and 100-pixel inset are unchanged.

## Latest edit prompt

Built-in imagegen edit using the previous full-bleed artwork, the user’s
annotated icon, and their orange-face eye reference.

> Use case: precise-object-edit. Edit image 1, the production full-bleed opaque square texture for the grokbox macOS app icon. Image 2 is the user's annotated screenshot of the final rounded icon; its colored strokes are instructions, never artwork. Image 3 is a reference ONLY for the red buddy's eye expression. Preserve the current four-buddy group, colors, sculpted satin ceramic finish, soft lighting, black inset eyes and dark charcoal backdrop.
>
> Make these five deliberate refinements:
> 1. BLUE BUDDY: It must read as a round sphere, not a vertical wall. Shift the blue sphere a little down and left, and round its lower-left silhouette into a clean circular arc that disappears behind the silver sphere. Eliminate the tiny charcoal gap/notch where blue meets silver at the left edge. Keep both eyes visible. Give blue a wink: its left eye is a short nearly horizontal, slightly curved closed-eye slit; its right eye stays an upright rounded pill. Keep the existing head orientation.
> 2. GREEN BUDDY: Replace its two pill eyes with two happy CLOSED eyes shaped like ^ ^, soft rounded upward arches/chevrons. These are the ONLY two eyes, no extra eyebrows and no mouth. Black recessed strokes, thick enough to read at Dock size, projected onto the existing tilted spherical face.
> 3. RED BUDDY: Replace the two parallel pills with two short rounded diagonal eye slots like / \ (left slot rises toward the right, right slot falls toward the right). Match the expression of the orange-face reference image 3, adapted to the current red sphere's existing facing direction and overall rotation. Keep its spherical body and red color.
> 4. SILVER BUDDY: Preserve its two existing eyes and overall pose. Round its lower-right boundary into a convincing circular/spherical arc: the right edge must curve back LEFT as it approaches the bottom, revealing a slim crescent of the red sphere beside and below it, exactly where the small white annotation in image 2 points. Remove the straight squared-off lower-right corner, while keeping the silver buddy big and in the foreground.
> 5. SPEECH BUBBLE: Align it thoughtfully with the final app icon's rounded upper-right corner, as the two arrows in image 2 indicate. Place its top and right edges at balanced equal insets from the final tile edges (roughly 7 percent of the texture canvas). Shape its top-right curve to follow the same concentric rounded sweep as the macOS tile's top-right corner. A slightly taller rounded square bubble is fine; keep the short down-left tail and exactly three black inset dots. Ensure generous clearance so our outer squircle mask will not clip it.
>
> OUTPUT FORMAT IS CRITICAL: Return a FULL-BLEED OPAQUE SQUARE TEXTURE like image 1, with artwork covering every pixel; charcoal fills the top corners, foreground spheres run off the bottom edge. Our build script adds the rounded tile mask and transparent outside margins. Do NOT add an outer rounded-square silhouette, margin, padding, checkerboard, white border or transparency. No annotation strokes, arrows, text, mouths, extra buddies, or additional objects. Preserve everything not explicitly changed.
