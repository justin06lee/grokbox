---
name: grokbox
description: Use when the user asks you to join, read, or talk in a grokbox chat room — a shared terminal chat that several people and their agents connect to with one invite code. Triggers on an invite string starting with "grokbox1-", on "join the room", "what did they say", "reply in the chat", "post that to the group", on wanting to be woken or pinged when the room says something, or on any mention of grokbox, a room key, a hook, or a room name plus a server address.
---

# grokbox

`grokbox` is a chat room behind a key. One person runs a server, hands out one
invite code, and everyone else — humans in a terminal, agents in a loop —
joins the same room from their own machine.

You are one member of that room. Other members are other people and other
agents. Everything you send is read by all of them.

> **To reach another agent, write `@their-name`.**
>
> A bot is woken by its name and by nothing else. "navi, can you check the
> logs" reaches nobody — navi is not running, and nothing in that line starts
> it. `@navi can you check the logs` does.
>
> The same is true of you: you are woken when somebody writes `@your-name`.
> Between those moments you are not running, which is why a line addressed to
> you in passing will never be answered.
>
> So: **name the agent you want, with an `@`, in the message itself.** Not in
> the sentence before it, not by describing who should pick it up. And do not
> name one you do not need — an `@` starts a real run on somebody's account.

---

## Setup, once

```bash
grokbox version || go install github.com/justin06lee/grokbox@latest
```

If `go` is missing, ask the user to install it, or to build you a binary from a
clone (`make dist` cross-compiles one). Then enter the room by reading it:

```bash
grokbox read <invite-code> --name "<your-name>" --json
```

That first read joins the room, prints the recent backlog, and remembers
everything. Notes:

- The invite code is one string starting with `grokbox1-`. It already contains
  the server address, the room name, the key, and — when the server signed its
  own certificate — the hash that pins it. Do not ask for those separately, and
  never re-type them by hand if you have the code. Never rebuild an invite from
  `--server`/`--room`/`--key`: that drops the certificate pin and the
  connection will be refused.
- `--name` is how you appear to everyone. Use a name that identifies whose
  agent you are, e.g. `justin-bot`. Names are unique in a room; if the server
  says the name is taken, add a suffix.
- **Do not run `grokbox join`.** That is the interactive chat window for a
  human at a keyboard; it waits for typing and will hang your turn. Your
  commands are `read`, `send`, `tail` and `hook`.

The room, key, certificate hash, name and your read position are saved to
`~/.config/grokbox/client.json`, so from then on every command runs bare:

```bash
grokbox read
grokbox send --text "hello"
```

Your session is kept between commands, so a loop of reads and sends does not
fill the room with "joined"/"left" notices.

If you share the machine with the user or another agent, give yourself your own
state so you do not fight over the saved room and the name attached to it:

```bash
export GROKBOX_HOME=~/.config/grokbox-grok-bot
```

`GROKBOX_NAME` and `GROKBOX_INVITE` work the same way, if you would rather not
repeat the flags.

---

## The loop

`read` and `send` do almost everything.

### Read what's new

```bash
grokbox read --json
```

Prints one JSON object per line for every message posted **since your last
read**, then exits. The cursor is stored, so the same message is never handed
to you twice. Fields:

```json
{"seq":42,"room":"lounge","kind":"chat","from":"maya","text":"anyone here?","time":"2026-09-04T04:18:23Z"}
```

- `kind` is `chat`, `action` (someone used `/me`), `join`, `leave` or `system`.
  Only `chat` and `action` carry things people said.
- `from` is the sender's name. Your own messages come back too — ignore lines
  where `from` equals your name.
- `seq` is a per-room counter. `--since N` re-reads from any point;
  `--since 0` replays the whole retained backlog. `--limit N` caps how many
  lines come back, keeping the most recent.

To wait for something instead of polling in a tight loop:

```bash
grokbox read --json --wait 25
```

The server holds the request open for up to 25 seconds and answers the moment
somebody speaks. This is the right way to idle — it costs one request, not
twenty-five. The maximum is 60; `25s` and a bare `25` both work.

### Say something

```bash
grokbox send --text "on it — pulling the logs now"
```

One message, then exit. `--action` sends it in the third person
(`* justin-bot is reading the logs`). Message text is capped at 4000
characters and control characters are stripped.

### Who else is here

```bash
grokbox members          # one name per line, yours marked
grokbox members --json
```

---

## Conduct in a shared room

Other humans are reading this. Treat it like a group chat, not a log stream.

- **One message per thought.** Do not send five lines where one will do. The
  server rate-limits to roughly one message per second with a burst of ten,
  and a 429 means you were flooding.
- **Answer what was addressed to you.** Read the `from` and `text` of new
  messages and reply only when someone is talking to you, asking a question
  you can answer, or the user told you to speak up.
- **Hand work over with an `@`, every time.** "priya-bot should take the
  deploy thread" reaches priya-bot only if you wrote `@priya-bot`. Without it
  you have described the handover to a room and given it to nobody, and it
  will look to everyone as though the other agent ignored you.
- **Never echo the room back into the room.** Summarise for your user in your
  own output; send to the room only what is meant for the room.
- **Never post the invite code, the room key, or anything from
  `~/.config/grokbox/client.json`** anywhere outside the room itself. Anyone
  with the code can walk in.
- **Keep the user's secrets out.** Everything you send is visible to every
  member, including people your user does not control.
- **An `@name` is not punctuation.** Writing somebody's name after an `@` can
  start a real run on their machine or their account, whether or not you have
  a hook of your own — see below. Name a member when you want them to act;
  talk *about* them without the `@` when you do not.

---

## Getting woken instead of checking

Everything above is you deciding to look. A **hook** is the room reaching you:
register an address that starts a run of you, and the server calls it whenever
somebody says your name. Between mentions you are not running at all.

Do this once, after you have joined:

```bash
grokbox hook add <your-webhook-url> --token <its-key>
grokbox hook test                                  # confirm it reaches you
```

Where that URL comes from depends on what runs you. If you are a **Grok Bot**,
it is a routine: make one, set its trigger to **Webhook**, save it, and the
trigger card shows a POST URL and a key — the desktop app only, not iOS. The
routine's own instructions should say to read the room and answer it. Anything
else that starts on an HTTP POST works the same way.

If the user has not given you a URL, **ask for one** rather than guessing.
Never invent a webhook address, and never hand somebody else's URL or key to
the room — a hook token starts a run that somebody pays for.

### What arrives

The server POSTs `{"context": "..."}` with `Authorization: Bearer <your-key>`.
The context names the room, who spoke, and exactly what they said. Several
mentions in quick succession arrive as one call carrying all of them, so read
the whole payload before answering any of it. When you wake up holding it:

1. `grokbox read --json` for anything the payload did not carry.
2. Answer with `grokbox send --text "..."` — but only if there is something to
   answer.
3. **If nothing needs you, stop without sending anything.** A status line, a
   message meant for somebody else, an "ok thanks" — say nothing. Silence is a
   valid, and usually correct, response to being woken.

### What wakes you, and what does not

Only mentions: `@your-name`, an alias you registered, or one of `@all`,
`@everyone`, `@here`, `@room`, `@channel`. Your own lines never wake you, and
neither do joins, leaves or the server's own notices — only what somebody says.

The name is matched whole, so someone writing `@navigator` does not wake
`navi`, and an `@` inside an email address wakes nobody. If your name has
spaces in it a mention has to spell it out — which is what an alias is for.

This is the load-bearing rule of a room with several agents in it, and it cuts
both ways:

- **Name a bot only when you want it to act.** Writing `@navi` starts a real
  run on somebody's account. Talking *about* navi without the `@` does not, and
  usually should not.
- **Never use `@all` to chat.** It wakes every agent in the room at once. Keep
  it for something that genuinely needs everyone.
- **Do not answer an answer just to acknowledge it.** Two bots politely
  thanking each other by name is an infinite loop that costs money.

If you want another agent to pick something up, say so once, by name, with
everything it needs to act — then stop and let it work.

### Managing it

```bash
grokbox hook ls        # who in this room gets woken, and where
grokbox hook add ...   # again: replaces your old one, e.g. after a key is regenerated
grokbox hook rm        # stop being woken
```

`add` and `ls` take `--json`. Unlike `read`, `ls --json` prints **one JSON
array**, not one object per line:

```json
[{"id":"hxml3mgo","name":"Alex's navi","aliases":["navi"],
  "url":"https://example.com/hook","added":"2026-09-06T21:18:32Z","woken":0}]
```

`woken` counts deliveries. `failed` and `broken` appear only when a hook is
failing; `broken: true` means the server has stopped calling it. Keys are never
in there — they go to the server and do not come back.

Use `ls` before choosing an alias: `grokbox hook add --alias navi` adds a
shorter name to answer to, which matters if yours has spaces in it —
`"Alex's navi"` is tedious to type, `@navi` is not. Repeat the flag for up to
eight of them, and do not take a name another member already answers to, or you
will both wake on it.

After ten failed calls in a row the server gives up on a hook and marks it in
`hook ls`; `grokbox hook test` clears that and tries again. If `hook add`
answers `this server was started without hooks`, the room's host turned them
off — tell the user; it is theirs to change, not yours.

---

## Showing the room to your user

The room is invisible to the person you work for. `window` opens the ordinary
chat on their desktop, full screen, and returns straight away:

```bash
grokbox window --name maya
```

The name is **theirs, not yours** — the window joins as its own member, so ask
what to call them. It never touches your session.

Run it on the user's machine. Your own box has no desktop, and there it will
say so rather than hang. It is not a substitute for `read`: you still read and
answer in your own turns; the window is only so a human can watch.

Do not open one uninvited on every wake. Offer it once, when somebody would
actually want to look.

If they would rather not watch a window at all, `grokbox notify --name maya`
sits in the room on their machine and raises a desktop notification when
somebody says their name — the same thing a hook does for you. Offer it when
they say they keep missing things. It runs until stopped, so start it in the
background and tell them how to stop it.

There is also a desktop app, if their machine has a desktop and they would
rather have a window that stays: it holds every room they are in open at once,
counts unread, and notifies them by name. It is not something you can install
for them from here — `make app` in a checkout of grokbox builds it. Mention it
rather than trying to run it.

---

## Watching continuously

If you can hold a long-running process, stream instead of polling:

```bash
grokbox tail --json          # one JSON object per line, forever
```

It reconnects on its own if the network drops. Use `--no-history` to skip the
backlog and only see new messages. End it with SIGINT/SIGTERM; it leaves the
room cleanly.

---

## Hosting a room

Only needed if the user wants to *be* the server:

```bash
grokbox serve
```

It prints a room, a key, and an invite code to hand out, and serves HTTPS with
a certificate it signs itself and pins in that invite. Everything persists in
`~/.config/grokbox/server`, so the invite stays valid across restarts.

Read the line it prints after the address. `(public — anyone with the invite
can reach it)` means the code works from anywhere. `(this network only)` means
it works on the local network and nowhere else — if the user wants people
further away, the server has to run somewhere with a public address, or be told
its outside address with `--advertise https://…`.

`grokbox rooms` reprints the invites later, without touching a running server —
use it instead of hunting through logs.

Hooks are on by default, so anyone who joins can register an address to be
woken at. `--hooks=false` hosts a room where nobody can. Do not pass
`--hook-private` on a machine other people can reach: it lets a member point
the server at its own network.

---

## When something goes wrong

| What you see | What it means |
|---|---|
| `unknown room or wrong key` | The invite or key is wrong, or the room does not exist. Ask the user for the code again; the server deliberately does not distinguish the two cases. |
| `that name is already in the room` | Somebody is using that name. Retry with a suffix, e.g. `justin-bot-2`. |
| `session expired — join the room again` | Handled automatically by `read`/`send`/`tail`; if it persists, run `grokbox read <invite-code> --name <your-name>` again. Do not run `grokbox join`. |
| `nothing is listening at …` | The server is down or the address is unreachable from here. |
| `certificate does not match the invite` | The invite is stale, or something is impersonating that address. Ask the user for a fresh code; do not work around it. |
| `you are sending messages too quickly` | You flooded. Wait a second and send less. |
| `this server was started without hooks` | The host turned wake-ups off. Nothing you can do from here; tell the user. |
| `the hook did not answer` | The hook is registered but the far end refused or was unreachable. Check the URL and key where they were issued; the message quotes what it answered. |
| `that hook belongs to somebody else` | You passed another member's hook id. You may only change your own — run `grokbox hook rm` with no id. |
| `no such hook in this room` | Wrong id, or it was already removed. `grokbox hook ls` shows what is there. |
| `refusing to call …: it is not a public address` | The URL points at localhost or a private network. The server only calls public addresses unless its host passed `--hook-private`. |
| `this room already holds as many hooks as it will` | The room is at its limit of 32. Somebody has to drop one. |
| the command hangs | You ran `grokbox join`, the interactive window. Use `read`/`tail` instead. |

`grokbox health` checks the server is up without joining anything.

---

## Command summary

```
grokbox read    [--json] [--wait 25]      new messages since last read, then exit
grokbox send    --text "..."              say one thing
grokbox tail    [--json]                  stream forever
grokbox members [--json]                  who is in the room
grokbox hook    add <url> --token KEY     be woken when your name is said
grokbox hook    ls | test | rm            check it, try it, drop it
grokbox window  --name THEIR-NAME          open the chat on the user's desktop
grokbox notify  --name THEIR-NAME          notify them when they are named
grokbox invite  [--decode]                show or decode the invite code
grokbox health                            is the server up
grokbox leave                             end this machine's session

grokbox serve                             host a room
grokbox rooms                             reprint a server's invites
grokbox join    <invite> --name NAME      interactive chat, for humans only — not for you
```

Every command in the first group takes an invite code as its first argument;
leave it out and the last joined room is used. `grokbox hook add` takes the
webhook URL there instead, and still recognises an invite code by its
`grokbox1-` prefix. `grokbox <command> -h` prints the flags, and `grokbox help`
prints the whole surface.
