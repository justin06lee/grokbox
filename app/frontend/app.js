// The window. Everything it can ask for goes through API in api.go; the three
// events it listens to are the only things Go pushes at it.
//
// The shell is Grok Bot's: a 280pt room list with a search field, a bare
// header, grey bubbles for other people and a near-black one for you, a pill
// composer, a settings pane on the right, and a right-click menu on a room.
// style.css records where each measurement came from.

import { Call, Events } from "/wails/runtime.js";
import * as Avatar from "/avatar.js";

const call = (method, ...args) => Call.ByName("main.API." + method, ...args);

const $ = (id) => document.getElementById(id);
const el = {
  rooms: $("rooms"),
  search: $("search"),
  stream: $("stream"),
  head: $("room-head"),
  headAvatar: $("head-avatar"),
  name: $("room-name"),
  sub: $("room-sub"),
  empty: $("empty"),
  composer: $("composer"),
  input: $("input"),
  send: $("send"),
  memberList: $("member-list"),
  settingsBtn: $("settings-btn"),
  modal: $("modal"),
  invite: $("invite"),
  joinName: $("join-name"),
  joinError: $("join-error"),
  joinGo: $("join-go"),
  toast: $("toast"),
  version: $("version"),
  meAvatar: $("me-avatar"),
  meName: $("me-name"),
  hiddenRow: $("hidden-row"),
  hiddenLabel: $("hidden-label"),
  menu: $("menu"),
  pop: $("pop"),
  popShapes: $("pop-shapes"),
  popColors: $("pop-colors"),
  pane: $("pane"),
  paneAvatar: $("pane-avatar"),
  paneNick: $("pane-nick"),
  paneRealName: $("pane-realname"),
  paneYou: $("pane-you"),
  paneInvite: $("pane-invite"),
  paneFp: $("pane-fp"),
  paneNotify: $("pane-notify"),
  paneLeave: $("pane-leave"),
  ask: $("ask"),
  askTitle: $("ask-title"),
  askBody: $("ask-body"),
  askYes: $("ask-yes"),
  askNo: $("ask-no"),
};

let rooms = [];
let activeId = null;
let current = null; // the open room, as Open() described it
let profile = { login: "", photo: "", shape: "", color: "" };
let filter = "";
let showHidden = false;
let paneOpen = false;
let popTarget = null; // what the avatar picker is dressing
let popAnchor = null;
let avatarSaving = false;
let group = { from: null, at: 0, day: "" }; // what the last rendered row was

// look is the three things that decide how something is drawn.
const look = (r) => ({ shape: r.shape, color: r.color, photo: r.photo });

// ------------------------------------------------------------------- start

async function boot() {
  el.version.textContent = await call("Version");
  profile = (await call("Profile")) || profile;
  rooms = (await call("Rooms")) || [];
  renderRooms();
  const first = pick();
  if (first) {
    await openRoom(first.id);
  } else {
    showEmpty(true);
    // Nothing has asked for the keyboard, and the webview will hand it to the
    // first field it finds — which would light up the search box on a window
    // with nothing to search.
    el.search.blur();
  }
}

Events.On("grokbox:rooms", (e) => {
  rooms = e.data || [];
  renderRooms();
  const room = rooms.find((r) => r.id === activeId);
  if (room) {
    current = { ...current, ...room };
    renderHeader(current);
    renderPane();
  } else if (activeId) {
    // The room being read is gone — you left it. Land on the next one; the
    // empty state is for having no rooms at all, and showing it with rooms
    // still in the sidebar reads as though they went too.
    activeId = null;
    current = null;
    showNext();
  }
});

// pick is the room to land on with nothing else to go by: one that named you,
// else one with anything unread, else the top of the list.
function pick() {
  const visible = rooms.filter((r) => !r.hidden);
  return visible.find((r) => r.mentioned) || visible.find((r) => r.unread > 0) || visible[0];
}

// showNext opens that room, or shows the empty state when there is none left.
// Opening a room publishes the room list again, which lands back here — the
// flag is what stops that from starting a second open.
let switching = false;
function showNext() {
  if (switching) return;
  const room = pick();
  if (!room) {
    showEmpty(true);
    return;
  }
  switching = true;
  openRoom(room.id).finally(() => (switching = false));
}

Events.On("grokbox:message", (e) => {
  const { room, message } = e.data || {};
  if (room !== activeId) return;
  const stick = nearBottom();
  addMessage(message);
  if (stick) scrollToEnd();
});

Events.On("grokbox:profile", (e) => {
  profile = e.data || profile;
  renderMe();
});

// ----------------------------------------------------------------- sidebar

function renderRooms() {
  el.rooms.replaceChildren();
  for (const r of rooms) {
    if (r.hidden && !showHidden) continue;
    if (!matches(r)) continue;
    const btn = document.createElement("button");
    btn.className =
      "room" + (r.id === activeId ? " active" : "") + (r.connected ? "" : " offline") + (r.hidden ? " dimmed" : "");
    btn.dataset.id = r.id;
    btn.onclick = () => openRoom(r.id);
    btn.oncontextmenu = (ev) => {
      ev.preventDefault();
      roomMenu(r, ev.clientX, ev.clientY);
    };
    btn.append(Avatar.node(r.room, look(r)), roomText(r));
    el.rooms.append(btn);
  }

  const hidden = rooms.filter((r) => r.hidden).length;
  el.hiddenRow.hidden = hidden === 0;
  el.hiddenLabel.textContent = showHidden ? `Hide ${hidden} again` : `Hidden rooms (${hidden})`;
}

// matches is what the search field does: room name or the line under it.
function matches(r) {
  if (!filter) return true;
  const hay = [r.title, r.room, r.name, r.last ? r.last.text : "", r.last ? r.last.from : ""];
  return hay.some((s) => (s || "").toLowerCase().includes(filter));
}

function roomText(r) {
  const box = document.createElement("span");
  box.className = "room-text";

  const top = document.createElement("div");
  top.className = "room-top";
  const name = document.createElement("span");
  name.className = "room-name";
  name.textContent = r.title || r.room;
  const when = document.createElement("span");
  when.className = "room-when";
  when.textContent = r.last ? shortWhen(new Date(r.last.time)) : "";
  top.append(name, when);

  const bottom = document.createElement("div");
  bottom.className = "room-top";
  const last = document.createElement("span");
  last.className = "room-last";
  last.textContent = r.last
    ? (SAID.has(r.last.kind) ? `${r.last.from}: ${r.last.text}` : r.last.text)
    : r.connected
      ? "no messages yet"
      : r.note || "connecting…";
  bottom.append(last);

  if (r.unread > 0) {
    const pill = document.createElement("span");
    pill.className = "pill" + (r.mentioned ? " mention" : "");
    pill.textContent = r.unread > 99 ? "99+" : String(r.unread);
    bottom.append(pill);
  }

  box.append(top, bottom);
  return box;
}

el.search.addEventListener("input", () => {
  filter = el.search.value.trim().toLowerCase();
  renderRooms();
});

el.hiddenRow.onclick = () => {
  showHidden = !showHidden;
  renderRooms();
};

// renderMe draws the footer: your picture, and the name you go by.
function renderMe() {
  const name = current ? current.name : profile.login || "you";
  Avatar.paint(el.meAvatar, name, profile);
  el.meAvatar.classList.add("big");
  el.meName.textContent = current ? current.name : profile.login || "Not in a room";
}

// -------------------------------------------------------------------- room

async function openRoom(id) {
  try {
    const view = await call("Open", id);
    activeId = id;
    current = view;
    showEmpty(false);
    renderRooms();
    renderHeader(view);
    renderStream(view.history || []);
    if (paneOpen) await loadPane();
    else renderPane();
    el.input.focus();
  } catch (err) {
    toast(String(err));
  }
}

function renderHeader(r) {
  el.head.hidden = false;
  el.name.textContent = r.title || r.room;
  Avatar.paint(el.headAvatar, r.room, look(r));
  el.headAvatar.classList.add("small");
  el.input.placeholder = `Message ${r.title || r.room}`;

  // Grok Bot's header carries the name and nothing else. The only thing worth
  // interrupting that for is the room not being there.
  el.sub.className = "head-note" + (r.connected ? "" : " warn");
  el.sub.textContent = r.connected ? "" : r.note || "not connected";
  renderMe();
}

// ------------------------------------------------------------ settings pane

el.settingsBtn.onclick = () => togglePane();
$("pane-close").onclick = () => togglePane(false);

async function togglePane(want) {
  paneOpen = want === undefined ? !paneOpen : want;
  el.settingsBtn.classList.toggle("on", paneOpen);
  el.pane.hidden = !paneOpen;
  if (paneOpen) await loadPane();
}

// loadPane fills the pane and fetches the one thing not already in hand: the
// invite code, which is rebuilt from the live connection so it always carries
// the certificate the room is actually pinned to.
async function loadPane() {
  renderPane();
  if (!activeId) return;
  try {
    const [code, fp] = await Promise.all([call("Invite", activeId), call("Fingerprint", activeId)]);
    el.paneInvite.value = code;
    el.paneFp.textContent = fp || "not pinned";
  } catch (err) {
    el.paneInvite.value = "";
    el.paneFp.textContent = String(err).replace(/^Error:\s*/, "");
  }
}

function renderPane() {
  if (!current) return;
  Avatar.paint(el.paneAvatar, current.room, look(current));
  el.paneAvatar.classList.add("huge");
  if (document.activeElement !== el.paneNick) el.paneNick.value = current.nickname || "";
  el.paneNick.placeholder = current.room;
  el.paneRealName.textContent = current.nickname
    ? `Really called ${current.room}, on ${short(current.server)}`
    : `On ${short(current.server)}`;
  el.paneYou.value = current.name;
  el.paneNotify.checked = !current.quiet;

  el.memberList.replaceChildren();
  for (const m of current.members || []) {
    const li = document.createElement("li");
    li.append(Avatar.node(m.name, {}, "small"));
    const name = document.createElement("span");
    name.textContent = m.name;
    li.append(name);
    if (m.name === current.name) {
      const you = document.createElement("span");
      you.className = "you";
      you.textContent = "you";
      li.append(you);
    }
    el.memberList.append(li);
  }
}

el.paneNick.addEventListener("change", () => {
  if (activeId) call("SetNickname", activeId, el.paneNick.value).catch(oops);
});
el.paneNick.addEventListener("keydown", (e) => {
  if (e.key === "Enter") {
    e.preventDefault();
    el.paneNick.blur();
  }
});

el.paneNotify.addEventListener("change", () => {
  if (activeId) call("SetQuiet", activeId, !el.paneNotify.checked).catch(oops);
});

$("pane-copy").onclick = () => copy(el.paneInvite.value, "Invite code copied");

el.paneLeave.onclick = async () => {
  if (!current) return;
  const { id, room } = current;
  if (!(await confirmLeave(room))) return;
  try {
    await call("Leave", id);
    togglePane(false);
  } catch (err) {
    oops(err);
  }
};

$("pane-avatar-btn").onclick = (e) => {
  if (!current) return;
  openPop(e.currentTarget, { kind: "room", id: current.id, name: current.room, look: look(current) });
};

// --------------------------------------------------------- the context menu

function icon(d) {
  return `<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${d}</svg>`;
}

const ICON = {
  unread: icon('<path d="M18 8a6 6 0 1 0-12 0c0 7-3 8-3 8h18s-3-1-3-8"/><path d="M10.3 21a1.9 1.9 0 0 0 3.4 0"/>'),
  settings: icon('<path d="M4 7h10M18 7h2M4 17h2M10 17h10"/><circle cx="16" cy="7" r="2.2"/><circle cx="8" cy="17" r="2.2"/>'),
  copy: icon('<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V6a2 2 0 0 1 2-2h9"/>'),
  hide: icon('<path d="M3 3l18 18M10.6 10.7a2 2 0 0 0 2.7 2.9"/><path d="M9.4 5.2A9.7 9.7 0 0 1 12 5c5 0 9 4.5 9 7a11 11 0 0 1-2.2 3.3M6.3 6.7C3.9 8.3 3 10.6 3 12c0 2.5 4 7 9 7 1.4 0 2.7-.3 3.8-.9"/>'),
  show: icon('<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/>'),
  paint: icon('<circle cx="12" cy="12" r="9"/><circle cx="9" cy="9.5" r="1.3" fill="currentColor" stroke="none"/><circle cx="15" cy="9.5" r="1.3" fill="currentColor" stroke="none"/>'),
  leave: icon('<path d="M6 7V5a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2v-2"/><path d="M3 12h11M11 8l4 4-4 4"/>'),
};

function roomMenu(r, x, y) {
  openMenu(x, y, [
    { icon: ICON.unread, label: "Mark as Unread", run: () => call("MarkUnread", r.id).catch(oops) },
    {
      icon: ICON.paint,
      label: "Change picture",
      run: () => {
        const row = [...el.rooms.children].find((c) => c.dataset.id === r.id);
        openPop(row || el.rooms, { kind: "room", id: r.id, name: r.room, look: look(r) }, x, y);
      },
    },
    {
      icon: ICON.settings,
      label: "Room settings",
      run: async () => {
        if (r.id !== activeId) await openRoom(r.id);
        togglePane(true);
      },
    },
    "-",
    {
      icon: ICON.copy,
      label: "Copy invite code",
      run: () => call("Invite", r.id).then((code) => copy(code, "Invite code copied")).catch(oops),
    },
    { icon: ICON.copy, label: "Copy room ID", run: () => copy(r.id, "Room ID copied") },
    "-",
    {
      icon: r.hidden ? ICON.show : ICON.hide,
      label: r.hidden ? "Show in sidebar" : "Hide from sidebar",
      run: () => call("SetHidden", r.id, !r.hidden).catch(oops),
    },
    {
      icon: ICON.leave,
      label: "Leave room",
      danger: true,
      run: async () => {
        if (await confirmLeave(r.room)) call("Leave", r.id).catch(oops);
      },
    },
  ]);
}

function openMenu(x, y, items) {
  el.menu.replaceChildren();
  for (const it of items) {
    if (it === "-") {
      const hr = document.createElement("div");
      hr.className = "menu-sep";
      el.menu.append(hr);
      continue;
    }
    const b = document.createElement("button");
    b.type = "button";
    b.className = "menu-item" + (it.danger ? " danger" : "");
    b.innerHTML = it.icon;
    const label = document.createElement("span");
    label.textContent = it.label;
    b.append(label);
    b.onclick = () => {
      closeMenu();
      it.run();
    };
    el.menu.append(b);
  }
  place(el.menu, x, y);
}

// place puts a floating panel at a point, folding it back inside the window
// when it would hang off an edge.
function place(node, x, y) {
  node.hidden = false;
  node.style.left = "0px";
  node.style.top = "0px";
  const r = node.getBoundingClientRect();
  node.style.left = Math.max(8, Math.min(x, window.innerWidth - r.width - 8)) + "px";
  node.style.top = Math.max(8, Math.min(y, window.innerHeight - r.height - 8)) + "px";
}

function closeMenu() {
  el.menu.hidden = true;
}

// ------------------------------------------------------- the avatar picker

function openPop(anchor, target, x, y) {
  if (popAnchor === anchor && !el.pop.hidden) return closePop();
  closeMenu();
  closePop(false);
  popAnchor = anchor;
  popTarget = target;
  anchor.setAttribute("aria-expanded", "true");
  renderPop();
  el.pop.hidden = false;
  const bounds = el.pop.getBoundingClientRect();
  if (x === undefined) {
    const r = anchor.getBoundingClientRect();
    x = r.left + (r.width - bounds.width) / 2;
    y = r.bottom + 8;
    if (y + bounds.height > innerHeight - 8) y = r.top - bounds.height - 8;
  }
  place(el.pop, x, y);
  (el.pop.querySelector(".pop-shape.on") || el.pop.querySelector(".pop-tab")).focus({ preventScroll: true });
}

function closePop(restoreFocus = true) {
  el.pop.hidden = true;
  popAnchor?.setAttribute("aria-expanded", "false");
  if (restoreFocus) popAnchor?.focus({ preventScroll: true });
  popTarget = null;
  popAnchor = null;
}

function renderPop() {
  if (!popTarget) return;
  const focused = document.activeElement?.dataset.choice;
  const chosen = popTarget.look || {};
  const derived = Avatar.derive(popTarget.name);
  const shapeNow = chosen.shape || derived.shape;
  const colorNow = chosen.color || derived.color;
  el.pop.setAttribute("aria-busy", String(avatarSaving));

  el.popShapes.replaceChildren();
  for (const shape of Avatar.SHAPES) {
    const selected = !chosen.photo && shape === shapeNow;
    const b = document.createElement("button");
    b.type = "button";
    b.className = "pop-shape" + (selected ? " on" : "");
    b.title = shape;
    b.dataset.choice = shape;
    b.setAttribute("aria-label", shape);
    b.setAttribute("aria-pressed", String(selected));
    b.innerHTML = Avatar.svg(popTarget.name, { shape, color: colorNow });
    b.onclick = () => choose({ shape });
    el.popShapes.append(b);
  }

  el.popColors.replaceChildren();
  for (const color of Avatar.COLOR_ORDER) {
    const selected = !chosen.photo && color === colorNow;
    const b = document.createElement("button");
    b.type = "button";
    b.className = "pop-color" + (selected ? " on" : "");
    b.title = color === "white" ? "Neutral" : color;
    b.dataset.choice = color;
    b.setAttribute("aria-label", b.title);
    b.setAttribute("aria-pressed", String(selected));
    b.style.background = Avatar.COLORS[color];
    b.onclick = () => choose({ color });
    el.popColors.append(b);
  }
  for (const button of el.pop.querySelectorAll("button")) button.disabled = avatarSaving;
  if (focused && !avatarSaving) {
    [...el.pop.querySelectorAll("[data-choice]")].find((b) => b.dataset.choice === focused)?.focus({ preventScroll: true });
  }
}

// choose applies one half of a look and leaves the other alone, so clicking a
// colour does not throw away the shape you just picked.
async function choose(part) {
  if (!popTarget || avatarSaving) return;
  const target = popTarget;
  const derived = Avatar.derive(target.name);
  const now = target.look || {};
  const shape = part.shape || now.shape || derived.shape;
  const color = part.color || now.color || derived.color;
  avatarSaving = true;
  target.look = { shape, color, photo: "" };
  renderPop();
  try {
    if (target.kind === "room") await call("SetAvatar", target.id, shape, color);
    else await call("SetProfileAvatar", shape, color);
  } catch (err) {
    target.look = now;
    oops(err);
  } finally {
    avatarSaving = false;
    renderPop();
    if (popTarget === target) el.pop.querySelector(`[data-choice="${part.shape ? shape : color}"]`)?.focus({ preventScroll: true });
  }
}

for (const tab of el.pop.querySelectorAll(".pop-tab")) {
  tab.onclick = async () => {
    const kind = tab.dataset.tab;
    if (kind === "bot" || !popTarget || avatarSaving) return;
    const target = popTarget;
    try {
      if (kind === "shuffle") {
        const derived = Avatar.derive(target.name);
        const current = { shape: target.look?.shape || derived.shape, color: target.look?.color || derived.color };
        const pairs = Avatar.SHAPES.flatMap((shape) => Avatar.COLOR_ORDER
          .filter((color) => shape !== current.shape || color !== current.color)
          .map((color) => ({ shape, color })));
        await choose(pairs[Math.floor(Math.random() * pairs.length)]);
      } else {
        avatarSaving = true;
        renderPop();
        if (kind === "upload") {
          await call("PickPhoto", target.kind === "room" ? target.id : "");
        } else if (kind === "reset") {
          if (target.kind === "room") await call("SetAvatar", target.id, "", "");
          else await call("ClearProfile");
        }
        if (popTarget === target) closePop();
      }
    } catch (err) {
      oops(err);
    } finally {
      avatarSaving = false;
      renderPop();
    }
  };
}

// Keep Tab inside the picker; Escape returns to the button that opened it.
el.pop.addEventListener("keydown", (e) => {
  if (e.key !== "Tab") return;
  const buttons = [...el.pop.querySelectorAll("button:not(:disabled)")];
  if (!buttons.length) { e.preventDefault(); return; }
  const first = buttons[0], last = buttons[buttons.length - 1];
  if (e.shiftKey && document.activeElement === first) {
    e.preventDefault(); last.focus();
  } else if (!e.shiftKey && document.activeElement === last) {
    e.preventDefault(); first.focus();
  }
});
window.addEventListener("resize", () => closePop(false));

$("foot-me").onclick = (e) =>
  openPop(e.currentTarget, {
    kind: "profile",
    name: current ? current.name : profile.login || "you",
    look: { shape: profile.shape, color: profile.color, photo: profile.photo },
  });

// One click anywhere else puts both floating things away.
document.addEventListener("mousedown", (e) => {
  if (!el.menu.hidden && !el.menu.contains(e.target)) closeMenu();
  if (!el.pop.hidden && !el.pop.contains(e.target) && !e.target.closest("#foot-me,#pane-avatar-btn")) closePop(false);
});

// ------------------------------------------------------------------ stream

function renderStream(history) {
  el.stream.replaceChildren();
  group = { from: null, at: 0, day: "" };
  for (const m of history) addMessage(m);
  scrollToEnd();
}

// addMessage appends one message, folding it into the block above when the
// same person said it a moment ago — the thing that makes a transcript read
// like a conversation instead of a log.
function addMessage(m) {
  const when = new Date(m.time);
  const day = when.toDateString();

  // Grok Bot marks a break in the conversation with one grey line carrying the
  // day and the clock: at a new day, or after an hour of nobody saying
  // anything.
  if (day !== group.day || when - group.at > 60 * 60 * 1000) {
    const d = document.createElement("div");
    d.className = "day";
    d.textContent = `${dayLabel(when)} ${clock(when)}`;
    el.stream.append(d);
    group = { from: null, at: 0, day };
  }

  if (m.kind === "system" || m.kind === "join" || m.kind === "leave") {
    const s = document.createElement("div");
    s.className = "system";
    s.textContent = m.text;
    el.stream.append(s);
    group = { from: null, at: when, day };
    return;
  }

  const same = m.from === group.from && when - group.at < 5 * 60 * 1000 && m.kind !== "action";
  const row = document.createElement("div");
  row.className = "row" + (m.mine ? " mine" : "") + (same ? " same" : "") + (m.kind === "action" ? " action" : "");

  if (!same && !m.mine && m.kind !== "action") {
    const who = document.createElement("div");
    who.className = "who";
    who.append(Avatar.node(m.from, {}, "tiny"));
    const name = document.createElement("span");
    name.textContent = m.from;
    const t = document.createElement("time");
    t.textContent = clock(when);
    who.append(name, t);
    row.append(who);
  }

  const bubble = document.createElement("div");
  bubble.className = "bubble" + (m.mention ? " mention" : "");
  bubble.title = clock(when);
  if (m.kind === "action") bubble.append("· " + m.from + " ");
  bubble.append(...withMentions(m.text));
  row.append(bubble);
  el.stream.append(row);

  group = { from: m.kind === "action" ? null : m.from, at: when, day };
}

// withMentions bolds @names, the way the terminal client does. Whether a
// message names *you* is decided in Go and arrives as m.mention — this is
// only the typography.
function withMentions(text) {
  const out = [];
  const re = /@[A-Za-z0-9](?:[A-Za-z0-9._-]{0,31})/g;
  let at = 0;
  for (const match of text.matchAll(re)) {
    if (match.index > at) out.push(text.slice(at, match.index));
    const b = document.createElement("span");
    b.className = "at";
    b.textContent = match[0];
    out.push(b);
    at = match.index + match[0].length;
  }
  if (at < text.length) out.push(text.slice(at));
  return out;
}

function nearBottom() {
  return el.stream.scrollHeight - el.stream.scrollTop - el.stream.clientHeight < 120;
}

function scrollToEnd() {
  el.stream.scrollTop = el.stream.scrollHeight;
}

// ---------------------------------------------------------------- composer

el.composer.hidden = true;

function showEmpty(on) {
  el.empty.classList.toggle("show", on);
  el.stream.hidden = on;
  el.composer.hidden = on;
  el.head.hidden = on;
  if (on) {
    togglePane(false);
    el.meAvatar.style.visibility = "hidden"; // keep the slot, lose the disc
    el.meName.textContent = "Not in a room";
  } else {
    el.meAvatar.style.visibility = "";
  }
}

el.input.addEventListener("input", () => {
  el.input.style.height = "auto";
  el.input.style.height = Math.min(el.input.scrollHeight, 160) + "px";
  el.send.disabled = el.input.value.trim() === "";
});

el.input.addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey) {
    e.preventDefault();
    el.composer.requestSubmit();
  }
});

el.composer.addEventListener("submit", async (e) => {
  e.preventDefault();
  const text = el.input.value.trim();
  if (!text || !activeId) return;
  el.input.value = "";
  el.input.style.height = "auto";
  el.send.disabled = true;
  try {
    await call("Send", activeId, text);
  } catch (err) {
    el.input.value = text; // put it back rather than losing what was typed
    el.input.dispatchEvent(new Event("input"));
    toast(String(err));
  }
});

// ------------------------------------------------------------ joining

function openJoin() {
  el.modal.hidden = false;
  el.joinError.hidden = true;
  el.invite.value = "";
  el.joinName.value = rooms.length ? rooms[0].name : "";
  el.invite.focus();
}

const closeJoin = () => (el.modal.hidden = true);

$("add-room").onclick = openJoin;
$("empty-join").onclick = openJoin;
$("foot-join").onclick = openJoin;
$("composer-add").onclick = openJoin;
$("join-cancel").onclick = closeJoin;
$("join-close").onclick = closeJoin;

$("join-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  el.joinGo.disabled = true;
  el.joinError.hidden = true;
  try {
    const room = await call("Join", el.invite.value, el.joinName.value);
    closeJoin();
    await openRoom(room.id);
  } catch (err) {
    el.joinError.textContent = String(err).replace(/^Error:\s*/, "");
    el.joinError.hidden = false;
  } finally {
    el.joinGo.disabled = false;
  }
});

// ------------------------------------------------------------ asking first

// ask puts a question on screen and resolves to what was clicked.
//
// It exists because window.confirm does not work here: WKWebView hands the
// call to the host, and Wails has no panel to show for it, so confirm()
// returns false without ever asking. Anything gated on one silently does
// nothing — which is what "Leave room" did. alert() and prompt() go the same
// way; when something needs to say or ask, it comes through here or a toast.
let askClose = null;

function ask({ title, body, confirm: label = "OK", danger = false }) {
  askClose?.(false); // a second question replaces the first rather than stacking
  el.askTitle.textContent = title;
  el.askBody.textContent = body;
  el.askYes.textContent = label;
  el.askYes.classList.toggle("danger", danger);
  el.ask.hidden = false;
  el.askYes.focus();

  return new Promise((resolve) => {
    askClose = (answer) => {
      el.ask.hidden = true;
      el.askYes.onclick = el.askNo.onclick = null;
      el.ask.onmousedown = null;
      askClose = null;
      resolve(answer);
    };
    el.askYes.onclick = () => askClose(true);
    el.askNo.onclick = () => askClose(false);
    // Clicking the dimmed backdrop is a cancel, the way it is on the join sheet.
    el.ask.onmousedown = (e) => {
      if (e.target === el.ask) askClose(false);
    };
  });
}

// confirmLeave is the one question the app asks, from the two places that ask
// it: the settings pane and the right-click menu.
const confirmLeave = (room) =>
  ask({
    title: `Leave ${room}?`,
    body: "You will need the invite code to come back.",
    confirm: "Leave",
    danger: true,
  });

document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") {
    if (askClose) return askClose(false);
    if (!el.pop.hidden) return closePop();
    if (!el.menu.hidden) return closeMenu();
    if (!el.modal.hidden) return closeJoin();
    if (paneOpen) return togglePane(false);
  }
  if (askClose) {
    // While a question is up it is the only thing the keyboard talks to.
    if (e.key === "Enter") {
      e.preventDefault();
      askClose(true);
    }
    return;
  }
  // ⌘N is what every chat app uses for "new conversation", and there is no
  // menu bar item competing for it here.
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "n") {
    e.preventDefault();
    openJoin();
  }
  // ⌘F puts the cursor in the room search, the way it does in Grok Bot.
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "f") {
    e.preventDefault();
    el.search.focus();
    el.search.select();
  }
  // ⌘I opens the settings pane, the way ⌘I opens an inspector everywhere else.
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "i") {
    e.preventDefault();
    if (activeId) togglePane();
  }
});

// -------------------------------------------------------------- odds/ends

async function copy(text, said) {
  if (!text) return;
  try {
    await call("Copy", text);
    toast(said);
  } catch (err) {
    oops(err);
  }
}

const oops = (err) => toast(String(err));

let toastTimer = null;
function toast(text) {
  el.toast.textContent = String(text).replace(/^Error:\s*/, "");
  el.toast.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (el.toast.hidden = true), 4000);
}

// The kinds that are somebody talking, as opposed to the room reporting on
// itself. proto has five; the other three read as notices.
const SAID = new Set(["chat", "action"]);

function clock(d) {
  return d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

function dayLabel(d) {
  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  if (d.toDateString() === today.toDateString()) return "Today";
  if (d.toDateString() === yesterday.toDateString()) return "Yesterday";
  return d.toLocaleDateString([], { weekday: "long", month: "short", day: "numeric" });
}

// shortWhen is the room list's right-hand column: a clock today, a weekday
// this week, a date before that.
function shortWhen(d) {
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return clock(d);
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  if (d.toDateString() === yesterday.toDateString()) return "Yesterday";
  if (now - d < 7 * 24 * 60 * 60 * 1000) return d.toLocaleDateString([], { weekday: "short" });
  return d.toLocaleDateString([], { month: "numeric", day: "numeric", year: "2-digit" });
}

// short is a server URL with the parts nobody reads taken off.
function short(server) {
  return (server || "").replace(/^https?:\/\//, "").replace(/\/$/, "");
}

boot().catch((err) => toast(String(err)));
