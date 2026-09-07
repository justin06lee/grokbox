// The window. Everything it can ask for goes through API in api.go; the two
// events it listens to are the only things Go pushes at it.
//
// The shell is Grok Bot's: a 280pt room list with a search field, a bare
// header, grey bubbles for other people and a near-black one for you, and a
// pill composer. style.css records where each measurement came from.

import { Call, Events } from "/wails/runtime.js";

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
  members: $("members"),
  memberList: $("member-list"),
  membersBtn: $("members-btn"),
  modal: $("modal"),
  invite: $("invite"),
  joinName: $("join-name"),
  joinError: $("join-error"),
  joinGo: $("join-go"),
  toast: $("toast"),
  version: $("version"),
  meAvatar: $("me-avatar"),
  meName: $("me-name"),
};

let rooms = [];
let activeId = null;
let showMembers = false;
let filter = "";
let group = { from: null, at: 0, day: "" }; // what the last rendered row was

// ------------------------------------------------------------------- start

async function boot() {
  el.version.textContent = await call("Version");
  rooms = (await call("Rooms")) || [];
  renderRooms();
  const first = rooms.find((r) => r.mentioned) || rooms.find((r) => r.unread > 0) || rooms[0];
  if (first) await openRoom(first.id);
  else {
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
    renderHeader(room);
    renderMembers(room);
  } else if (activeId) {
    activeId = null;
    showEmpty(true);
  }
});

Events.On("grokbox:message", (e) => {
  const { room, message } = e.data || {};
  if (room !== activeId) return;
  const stick = nearBottom();
  addMessage(message);
  if (stick) scrollToEnd();
});

// ----------------------------------------------------------------- sidebar

function renderRooms() {
  el.rooms.replaceChildren();
  for (const r of rooms) {
    if (!matches(r)) continue;
    const btn = document.createElement("button");
    btn.className = "room" + (r.id === activeId ? " active" : "") + (r.connected ? "" : " offline");
    btn.onclick = () => openRoom(r.id);
    btn.oncontextmenu = (ev) => {
      ev.preventDefault();
      if (confirm(`Leave ${r.room}?\n\nYou will need the invite code to come back.`)) {
        call("Leave", r.id).catch((err) => toast(String(err)));
      }
    };
    btn.append(avatar(r.room), roomText(r));
    el.rooms.append(btn);
  }
}

// matches is what the search field does: room name or the line under it.
function matches(r) {
  if (!filter) return true;
  const hay = [r.room, r.name, r.last ? r.last.text : "", r.last ? r.last.from : ""];
  return hay.some((s) => (s || "").toLowerCase().includes(filter));
}

function roomText(r) {
  const box = document.createElement("span");
  box.className = "room-text";

  const top = document.createElement("div");
  top.className = "room-top";
  const name = document.createElement("span");
  name.className = "room-name";
  name.textContent = r.room;
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

// -------------------------------------------------------------------- room

async function openRoom(id) {
  try {
    const view = await call("Open", id);
    activeId = id;
    showEmpty(false);
    renderRooms();
    renderHeader(view);
    renderMembers(view);
    renderStream(view.history || []);
    el.input.focus();
  } catch (err) {
    toast(String(err));
  }
}

function renderHeader(r) {
  el.head.hidden = false;
  el.name.textContent = r.room;
  paint(el.headAvatar, r.room);
  el.input.placeholder = `Message ${r.room}`;

  // Grok Bot's header carries the name and nothing else. The only thing worth
  // interrupting that for is the room not being there.
  el.sub.className = "head-note" + (r.connected ? "" : " warn");
  el.sub.textContent = r.connected ? "" : r.note || "not connected";

  el.meName.textContent = r.name;
  paint(el.meAvatar, r.name);
}

// paint recolours an avatar span in place, keeping the element the ids point at.
function paint(span, name) {
  span.style.background = PALETTE[hash(name) % PALETTE.length];
  span.textContent = (name || "?").trim().charAt(0).toUpperCase();
  return span;
}

function renderMembers(r) {
  el.members.hidden = !showMembers;
  el.membersBtn.classList.toggle("on", showMembers);
  el.memberList.replaceChildren();
  for (const m of r.members || []) {
    const li = document.createElement("li");
    li.append(avatar(m.name, "small"));
    const name = document.createElement("span");
    name.textContent = m.name;
    li.append(name);
    if (m.name === r.name) {
      const you = document.createElement("span");
      you.className = "you";
      you.textContent = "you";
      li.append(you);
    }
    el.memberList.append(li);
  }
}

el.membersBtn.onclick = () => {
  showMembers = !showMembers;
  const room = rooms.find((r) => r.id === activeId);
  if (room) renderMembers(room);
};

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
    who.append(avatar(m.from, "tiny"));
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
    el.members.hidden = true;
    // Your name is the name you took in a room, so with no rooms there is
    // none to show — only which build this is.
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

document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && !el.modal.hidden) closeJoin();
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
});

// -------------------------------------------------------------- odds/ends

let toastTimer = null;
function toast(text) {
  el.toast.textContent = text.replace(/^Error:\s*/, "");
  el.toast.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (el.toast.hidden = true), 5000);
}

// The kinds that are somebody talking, as opposed to the room reporting on
// itself. proto has five; the other three read as notices.
const SAID = new Set(["chat", "action"]);

const PALETTE = ["#7FD1C0", "#E8785C", "#8FA9F5", "#F0C27B", "#C4A6E8", "#7FB2D1"];

function avatar(name, size) {
  const a = document.createElement("span");
  a.className = "avatar" + (size ? " " + size : "");
  return paint(a, name);
}

function hash(s) {
  let h = 0;
  for (let i = 0; i < (s || "").length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
  return h;
}

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

boot().catch((err) => toast(String(err)));
