// Avatars, the way Grok Bot draws them: a coloured shape with two eyes.
//
// The eight shapes and eleven colours are the ones its own picker offers —
// the names came out of its bundle (`["blob","pebble","squircle","tablet",
// "wedge","hex","cloud","teardrop"]` and the eleven `{id,label,value}` swatches
// beside them). Grok Bot generates its silhouettes procedurally; these are
// drawn by hand to the same outlines.
//
// Palette and silhouettes match the picker reference. The neutral swatch
// follows the system theme; legacy saved "black" choices remain supported.
export const SHAPES = ["blob", "pebble", "squircle", "tablet", "wedge", "hex", "cloud", "teardrop"];
export const COLORS = {
  white: "var(--avatar-neutral)", brown: "#855c34", red: "#e91b36",
  orange: "#ff6900", yellow: "#ff9900", green: "#009e60",
  cyan: "#00a995", blue: "#1079df", violet: "#824de0",
  magenta: "#df2387", gray: "#7b7b7b", black: "#000000",
};
export const COLOR_ORDER = Object.keys(COLORS).filter((c) => c !== "black");
const DERIVABLE = COLOR_ORDER.filter((c) => c !== "white");

// A shared 100-unit canvas leaves room for the selection contour. Faces sit
// slightly above and right of centre, with two rounded, tilted eyes.
const ART = {
  blob: "M49 7C71 5 89 24 91 47C95 72 79 93 55 94C30 96 10 80 7 56C4 33 22 10 49 7Z",
  pebble: "M56 10C74 6 88 23 93 44C100 66 90 83 71 89C49 98 19 84 9 68C-2 48 18 20 39 14C45 12 50 11 56 10Z",
  squircle: "M49 10C82 10 90 13 91 44L90 68C89 86 84 91 63 91H37C14 91 10 85 10 64V38C10 16 16 10 49 10Z",
  tablet: "M35 20H65C83 20 97 32 97 51C97 69 84 81 65 81H35C15 81 3 69 3 52C3 34 15 20 35 20Z",
  wedge: "M43 12Q51 0 59 12L91 69Q102 88 82 89H18Q-1 89 10 69Z",
  hex: "M43 6Q50 2 57 6L84 22Q91 26 91 34V67Q91 75 84 79L57 95Q50 99 43 95L16 79Q9 75 9 67V34Q9 26 16 22Z",
  cloud: "M17 37C15 13 42 1 59 17C78 7 94 26 87 44C107 62 94 84 75 80C62 97 42 90 35 81C11 89-5 66 7 48Q11 41 17 37Z",
  teardrop: "M44 7Q50-1 56 7L82 40C106 71 86 96 60 98C31 101 11 81 12 60C12 42 30 23 44 7Z",
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
  const path = ART[shape];
  const eyeColor = color === "black" ? "#fff" : "var(--avatar-eyes)";
  const eye = (x, y) => `<rect x="${x}" y="${y}" width="7.5" height="16" rx="3.75" fill="${eyeColor}" transform="rotate(-18 ${x + 3.75} ${y + 8})"/>`;
  const pointed = shape === "wedge" || shape === "teardrop";
  return `<svg viewBox="-6 -6 112 112" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
    <path class="avatar-contour" d="${path}" fill="none" stroke="var(--avatar-outline)" stroke-width="17" stroke-linejoin="round"/>
    <path class="avatar-contour" d="${path}" fill="none" stroke="var(--avatar-surface, var(--bg-elevated))" stroke-width="11" stroke-linejoin="round"/>
    <path d="${path}" fill="${COLORS[color]}"/>
    ${eye(pointed ? 48 : 51, pointed ? 54 : 35)}${eye(shape === "cloud" ? 71 : pointed ? 72 : 77, pointed ? 50 : 31)}
  </svg>`;
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
