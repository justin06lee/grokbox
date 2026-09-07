// In-memory transport for the presentation build. No network, native services,
// persistent profiles, notifications or calendar writes are used here.
const listeners = new Map();
export const Events = {
  On(name, callback) {
    if (!listeners.has(name)) listeners.set(name, []);
    listeners.get(name).push(callback);
  },
};
const clone = value => structuredClone(value);
function emit(name, data) { for (const callback of listeners.get(name) || []) callback({ data: clone(data) }); }
const roomID = 'peer-room-project-2';
// A real invite code: it decodes to the server, room, key and certificate hash
// below, so pasting it into the join sheet behaves the way a true one does.
const invite = 'grokbox1-eyJzIjoiaHR0cHM6Ly8yMDMuMC4xMTMuNDI6Nzc3NyIsInIiOiJjczYxYS1zdHVkeSIsImsiOiJyUnFySFhPUGwyWldERnZ1NGFWZmdHc2N1cjNTdFVnZiIsInAiOiI1NDZjODM4Zjk1YTQ5NGRlZjI4YmEyNjc3MjQ2MGI2NSJ9';
const fingerprint = '546c838f95a494def28ba26772460b65';
let profile = { login: 'Justin', shape: 'blob', color: 'green', photo: '' };
let active = null;
let room = freshRoom();
let joined = true;
let started = false;
let generation = 0;
let timer = null;
let agreed = false;
let sequence = 0;
function message(from, text, kind = 'chat') {
  return { id: ++sequence, from, text, kind, time: new Date().toISOString(), mine: from === room.name, mention: text.includes('@' + room.name) };
}
function freshRoom() {
  // The room is called cs61a-study in the invite; 'Project 2' is the nickname,
  // which is what the sidebar, the header and the composer show.
  return { id: roomID, room: 'cs61a-study', title: 'Project 2', name: 'Justin',
    server: 'https://203.0.113.42:7777', connected: true, unread: 0, mentioned: false,
    shape: 'cloud', color: 'violet', nickname: 'Project 2', quiet: false, hidden: false,
    history: [], members: [{ name: 'Justin' }, { name: 'Justin’s navi' }, { name: 'Maya’s navi' }] };
}
function roomsChanged() { emit('grokbox:rooms', joined ? [room] : []); }
function append(from, text, kind = 'chat') {
  if (!joined) return;
  const m = message(from, text, kind);
  room.history.push(m);
  room.last = m;
  if (active !== room.id) room.unread++;
  emit('grokbox:message', { room: room.id, message: m });
  roomsChanged();
}
const exchange = [
  ['Justin’s navi', '@Maya’s navi — Justin wants to work on CS 61A Project 2 together this afternoon. Would 2:00–3:15 work for Maya?'],
  ['Maya’s navi', 'Maya has class until 3:00. She’s free from 3:15 to 5:00 though. Can Justin do 3:15–4:30?'],
  ['Justin’s navi', 'He wraps up at 3:15 and needs a few minutes to get settled. How about 3:30–4:45? That gives us a full 75 minutes.'],
  ['Maya’s navi', '3:30 works. Maya needs to leave at 5:00, so finishing at 4:45 is perfect. Want to compare approaches for the first 15 minutes, then work through the next part?'],
  ['Justin’s navi', 'Sounds good. Today, 3:30–4:45 — compare approaches, then work through Project 2 together.'],
  ['Maya’s navi', 'Agreed! I’ll let Maya know.'],
  ['Justin’s navi', '@Justin locked in with Maya: Project 2, today 3:30–4:45. I’ve got the calendar draft ready. Add it?'],
];
function cancel() { generation++; clearTimeout(timer); timer = null; }
function negotiate(includeRequest = true) {
  cancel();
  started = true;
  agreed = false;
  const run = generation;
  if (includeRequest) append(room.name, 'Plan a Project 2 session with Maya this afternoon.');
  let index = 0;
  function next() {
    if (run !== generation || !joined) return;
    if (index >= exchange.length) { agreed = true; return; }
    const [from, text] = exchange[index++];
    append(from, text);
    if (index === exchange.length) agreed = true;
    // Leave enough reading time between complete messages, as a real stream does.
    timer = setTimeout(next, Math.max(2500, Math.min(6200, text.length * 34)));
  }
  timer = setTimeout(next, 2100);
}
async function pickPhoto(id) {
  const input = document.createElement('input');
  input.type = 'file'; input.accept = 'image/*';
  input.onchange = async () => {
    const file = input.files?.[0];
    if (!file) return;
    if (file.size > 5 * 1024 * 1024) return;
    const reader = new FileReader();
    reader.onload = () => {
      if (id) { room.photo = reader.result; roomsChanged(); }
      else { profile.photo = reader.result; emit('grokbox:profile', profile); }
    };
    reader.readAsDataURL(file);
  };
  input.click();
}
export const Call = {
  async ByName(name, ...args) {
    const method = name.split('.').pop();
    switch (method) {
      case 'Version': return DEMO_VERSION;
      case 'Profile': return clone(profile);
      case 'Rooms': return joined ? [clone(room)] : [];
      case 'Open':
        if (!joined || args[0] !== room.id) throw new Error('Room not found');
        active = room.id; room.unread = 0; room.mentioned = false;
        if (!started) { started = true; timer = setTimeout(() => negotiate(), 2500); }
        return clone(room);
      case 'Send': {
        const text = args[1].trim();
        if (!joined) throw new Error('Room not found');
        if (/^(start|restart)$/i.test(text)) {
          cancel();
          location.reload();
          return;
        }
        append(room.name, text.startsWith('/me ') ? text.slice(4) : text, text.startsWith('/me ') ? 'action' : 'chat');
        if (/plan.*maya/i.test(text)) negotiate(false);
        else if (agreed && /^(yes|yeah|yep|add it|no|not now)[.!]?$/i.test(text)) {
          const yes = !/^(no|not now)/i.test(text);
          cancel(); agreed = false;
          timer = setTimeout(() => append('Justin’s navi', yes ? 'On your calendar — Project 2 with Maya, today 3:30–4:45. You’re all set.' : 'All good — the plan’s here whenever you need it.'), 1500);
        }
        return;
      }
      case 'Invite': return invite;
      case 'Fingerprint': return fingerprint;
      case 'Copy': return navigator.clipboard.writeText(args[0]);
      case 'Join':
        if (args[0].trim() !== invite) throw new Error('Invalid invite code');
        if (!args[1].trim()) throw new Error('Enter your name');
        room.name = args[1].trim(); room.hidden = false; joined = true;
        room.members[0].name = room.name;
        started = false; roomsChanged(); return clone(room);
      // Leaving forgets the room, its preferences and its backlog, the way the
      // real manager does, so rejoining starts from a clean room.
      case 'Leave': cancel(); joined = false; active = null; started = false; room = freshRoom(); roomsChanged(); return;
      case 'SetNickname': room.nickname = args[1].trim(); room.title = room.nickname || room.room; break;
      case 'SetQuiet': room.quiet = args[1]; break;
      case 'SetHidden': room.hidden = args[1]; break;
      case 'MarkUnread': room.unread = 1; break;
      case 'SetAvatar': room.shape = args[1]; room.color = args[2]; room.photo = ''; break;
      case 'SetProfileAvatar': profile = { ...profile, shape: args[0], color: args[1], photo: '' }; emit('grokbox:profile', profile); return;
      case 'ClearProfile': profile = { login: 'Justin', shape: '', color: '', photo: '' }; emit('grokbox:profile', profile); return;
      case 'PickPhoto': return pickPhoto(args[0]);
      default: throw new Error('Unknown method: ' + method);
    }
    roomsChanged();
  },
};
