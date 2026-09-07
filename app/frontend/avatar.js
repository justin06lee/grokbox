// Avatars, the way Grok Bot draws them: a coloured shape with two eyes.
//
// The eight shapes and eleven colours are the ones its own picker offers —
// the names came out of its bundle (`["blob","pebble","squircle","tablet",
// "wedge","hex","cloud","teardrop"]` and the eleven `{id,label,value}` swatches
// beside them). Grok Bot generates its silhouettes procedurally; these are
// drawn by hand to the same outlines.
//
// The colours are its token values with the chroma taken down to 78% in OKLCH,
// which is the transform that turns the raw #00BCA6 in its source into the
// #5cc0b0 that actually appears on screen. Sampled from three of its avatars,
// consistent to a rounding error.

export const SHAPES = ["blob", "pebble", "squircle", "tablet", "wedge", "hex", "cloud", "teardrop"];

export const COLORS = {
  black: "#000000",
  brown: "#906b4b",
  red: "#ef5656",
  orange: "#f27d48",
  yellow: "#f6a654",
  green: "#58c985",
  cyan: "#54bdab",
  blue: "#428cea",
  violet: "#916de9",
  magenta: "#f15b9f",
  gray: "#7b7b7b",
};

// The order the swatches sit in, which is the order Grok Bot lists them.
export const COLOR_ORDER = Object.keys(COLORS);

// Black is a fine thing to choose and a poor thing to be given, so the shape a
// name lands on never picks it.
const DERIVABLE = COLOR_ORDER.filter((c) => c !== "black");

// Every path is drawn in a 100×100 box. `eyes` is where that shape wants its
// face: the centre, and how far apart, since a wedge has less room at the top
// than a tablet has in the middle.
const ART = {
  blob: {
    d: "M50 4C74.5 4 96 24 96 49.5C96 75.5 75 96 50 96C24.5 96 4 75 4 50C4 24.5 25 4 50 4Z",
    eyes: { y: 50, gap: 20, scale: 1 },
  },
  pebble: {
    d: "M50 8C77 8 97 25 97 50C97 76 78 92 50 92C22 92 3 76 3 50C3 25 23 8 50 8Z",
    eyes: { y: 50, gap: 20, scale: 0.96 },
  },
  squircle: {
    d: "M50 2C87 2 98 13 98 50C98 87 87 98 50 98C13 98 2 87 2 50C2 13 13 2 50 2Z",
    eyes: { y: 50, gap: 20, scale: 1 },
  },
  tablet: {
    d: "M36 14H64A36 36 0 0 1 64 86H36A36 36 0 0 1 36 14Z",
    eyes: { y: 50, gap: 20, scale: 0.94 },
  },
  wedge: {
    d: "M50 7C55 7 59 9 61 13L93 74C97 82 92 93 83 93H17C8 93 3 82 7 74L39 13C41 9 45 7 50 7Z",
    eyes: { y: 62, gap: 18, scale: 0.9 },
  },
  hex: {
    d: "M43 6C47.5 3.5 52.5 3.5 57 6L85 22C89.5 24.6 92 28.9 92 34V66C92 71.1 89.5 75.4 85 78L57 94C52.5 96.5 47.5 96.5 43 94L15 78C10.5 75.4 8 71.1 8 66V34C8 28.9 10.5 24.6 15 22Z",
    eyes: { y: 50, gap: 19, scale: 0.96 },
  },
  cloud: {
    d: "M28 33C30 20 39 11 51 11C63 11 72 20 74 33C86 35 95 44 95 56C95 74 76 89 50 89C24 89 5 74 5 56C5 44 15 35 28 33Z",
    eyes: { y: 56, gap: 19, scale: 0.92 },
  },
  teardrop: {
    d: "M50 4C50 4 88 44 88 62C88 81 71 95 50 95C29 95 12 81 12 62C12 44 50 4 50 4Z",
    eyes: { y: 62, gap: 19, scale: 0.95 },
  },
};

// fnv1a is the hash Grok Bot uses to pick a bot's look from its name. Same
// function, so a room called the same thing lands on the same shape.
function fnv1a(s) {
  let h = 2166136261;
  for (let i = 0; i < (s || "").length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}

// derive is what a room looks like before anybody has dressed it. Both indexes
// are forced back to unsigned first: Math.imul answers a signed 32-bit int, and
// a negative remainder would index past the front of the array and paint the
// shape with `fill="undefined"`, which renders black.
export function derive(name) {
  const h = fnv1a(name || "?");
  const mixed = (((h >>> 8) ^ Math.imul(h, 2654435769)) >>> 0);
  return {
    shape: SHAPES[h % SHAPES.length],
    color: DERIVABLE[mixed % DERIVABLE.length],
  };
}

// svg draws one avatar. `look` may name a shape and a colour; whatever it
// leaves out is derived from the name.
export function svg(name, look = {}) {
  const d = derive(name);
  const shape = ART[look.shape] ? look.shape : d.shape;
  const color = COLORS[look.color] ? look.color : d.color;
  const art = ART[shape];
  const { y, gap, scale } = art.eyes;
  const w = 6.4 * scale;
  const h = 14.5 * scale;
  const eye = (cx) =>
    `<rect x="${(cx - w / 2).toFixed(2)}" y="${(y - h / 2).toFixed(2)}" width="${w.toFixed(2)}" height="${h.toFixed(2)}" rx="${(w / 2).toFixed(2)}" fill="#fff"/>`;
  return (
    `<svg viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">` +
    `<path d="${art.d}" fill="${COLORS[color]}"/>` +
    eye(50 - gap / 2) +
    eye(50 + gap / 2) +
    `</svg>`
  );
}

// paint fills an element with an avatar: a photo when there is one, the shape
// otherwise. It works in place so the ids the window holds stay valid.
export function paint(el, name, look = {}) {
  el.classList.add("avatar");
  if (look.photo) {
    el.style.background = "";
    el.innerHTML = `<img src="${look.photo}" alt="" />`;
    return el;
  }
  el.style.background = "";
  el.innerHTML = svg(name, look);
  return el;
}

// node is paint(), for when there is nothing to paint into yet.
export function node(name, look = {}, size) {
  const el = document.createElement("span");
  if (size) el.classList.add(size);
  return paint(el, name, look);
}
