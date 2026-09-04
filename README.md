<div align="center">

<img src="assets/grokbox.svg" alt="grokbox" width="330" />

# grokbox

**A chat room behind a key.**<br>
*One person runs the server, hands out one code, and everyone else joins from their own machine.*

</div>

---

grokbox is a single Go binary that is both the server and the client. You start
a server, it prints one invite code, and anyone you give that code to can join
the room from their own terminal — down the table or across the internet.

The room is designed to hold people **and** the agents working for them. A
human gets a chat window with a pinned input line; an agent gets `read`,
`send` and `tail` with a saved cursor and JSON output, so a loop of reads never
sees the same message twice. Both are ordinary members of the same room.

## Install

```bash
go install github.com/justin06lee/grokbox@latest
```

Or from a clone — `make` builds it, installs it, and checks it runs from
anywhere:

```bash
make
```

For people who would rather not install Go, `make dist` cross-compiles
binaries for macOS, Linux and Windows into `./dist`.

## Host a room

```console
$ grokbox serve

grokbox v0.1.0 — listening on :7777
reachable at http://192.168.1.24:7777

  room    lounge
  key     nsfq-z29d-wkb4-dz3x
  invite  grokbox1-eyJzIjoiaHR0cDovLzE5Mi4xNjguMS4yNDo3Nzc3IiwiciI6ImxvdW5nZSIsImsiOiJuc2ZxLXoyOWQtd2tiNC1kejN4In0

  hand out the invite, then everyone runs:

      grokbox join grokbox1-eyJzIjoiaHR0cDovLzE5Mi4xNjgu… --name <their-name>
```

The invite code carries the address, the room name and the key in one string,
so there is exactly one thing to share. Keys and transcripts are kept under
`~/.config/grokbox/server`, which means the code you handed out yesterday still
works after a restart.

Useful flags:

| Flag | What it does |
|---|---|
| `--addr :7777` | Address to listen on. |
| `--room lounge` | Room to host. Repeat it for several rooms; `--room lounge=my-key` sets the key yourself. |
| `--advertise URL` | The public address to put in invites, when the server sits behind a tunnel or proxy. |
| `--open` | Let joiners create rooms that do not exist yet; the first one in sets the key. |
| `--store ""` | Keep everything in memory, so keys and history vanish on exit. |
| `--history 200` | Messages retained and replayed per room. |
| `--idle 90s` | How long a member can go unheard from before the room drops them. |
| `--tls-cert / --tls-key` | Serve HTTPS directly instead of behind a proxy. |

## Join a room

```bash
grokbox join grokbox1-eyJzIjoi… --name maya
```

That opens the chat: the transcript scrolls, and your input line stays pinned
to the bottom no matter who says what while you are typing. Inside it:

| Command | |
|---|---|
| `/me <text>` | Say something in the third person. |
| `/who` | Who is in the room right now. |
| `/invite` | Print the invite code, to pass on to somebody else. |
| `/clear` | Clear the screen. |
| `/quit` | Leave. `ctrl-c` does the same. |

After the first join, the room is remembered — `grokbox join` on its own goes
back to it.

## Letting people in from outside your network

The address `serve` prints is a LAN address; it works for everyone on the same
wifi and nobody else. To let people further away in, expose the port and tell
grokbox what the outside world calls it:

```bash
# a quick tunnel
cloudflared tunnel --url http://localhost:7777
grokbox serve --advertise https://something.trycloudflare.com

# a machine you already own
grokbox serve --addr :7777 --advertise https://chat.example.com

# a private network, no tunnel needed
tailscale ip -4                     # -> 100.x.y.z
grokbox serve --advertise http://100.x.y.z:7777
```

Anything that can forward plain HTTP will do: grokbox streams over
server-sent events, not WebSocket, so it survives proxies that do not know what
a WebSocket upgrade is.

Over plain HTTP the room key crosses the network in the clear. Put the server
behind HTTPS — a tunnel, a reverse proxy, or `--tls-cert`/`--tls-key` — for
anything you would mind a stranger on the same coffee-shop wifi reading.

## For agents

An agent should never run `grokbox join` — that is the interactive window and
it waits for typing. It uses three commands instead:

```bash
grokbox read <invite> --name grok-bot --json   # first contact: joins, prints the backlog
grokbox read --json --wait 25                  # what's new since last time (long-polls)
grokbox send --text "on it"                    # say one thing
```

`read` remembers where it stopped, so consecutive reads never repeat
themselves, and the session is kept between commands, so a polling loop does
not fill the room with "joined"/"left" notices. `--wait` holds the request open
until somebody speaks, which costs one request instead of a busy loop.
`grokbox tail --json` streams forever for agents that can hold a process open.

The repo ships a skill teaching all of this — including how to behave in a room
with other people in it — at [`skills/grokbox/SKILL.md`](skills/grokbox/SKILL.md):

```bash
bmo add justin06lee/grokbox/skills/grokbox        # or copy the folder into your skills dir
```

## Commands

```
grokbox serve   [flags]                     host rooms and print their invites
grokbox join    [invite] --name NAME        open the interactive chat
grokbox send    [invite] --name NAME TEXT   say one thing and exit
grokbox read    [invite] --name NAME        print what has been said since last time
grokbox tail    [invite] --name NAME        stream messages as they arrive
grokbox members [invite] --name NAME        list who is in the room
grokbox invite  [invite] [--decode]         show or decode an invite code
grokbox health  [invite]                    check that a server is up
grokbox leave   [invite]                    end this machine's session
grokbox version
```

Every client command takes the invite code as its first argument; leave it out
and the last room this machine joined is used. `--server`, `--room` and `--key`
spell out the same thing the long way, and `GROKBOX_INVITE`, `GROKBOX_NAME`,
`GROKBOX_SERVER`, `GROKBOX_ROOM` and `GROKBOX_KEY` set them from the
environment. `--no-save` keeps a room out of the config file entirely.

## How it works

The server is plain HTTP with a JSON body on every route.

| Route | |
|---|---|
| `POST /v1/join` | Room name, key and display name in, session token and backlog out. |
| `POST /v1/send` | One message, bearer token required. |
| `GET /v1/messages?since=N&wait=S` | Everything after cursor `N`; `wait` long-polls for up to 60s. |
| `GET /v1/stream?since=N` | The same messages as server-sent events. |
| `GET /v1/members`, `POST /v1/leave`, `GET /v1/health` | |

Every message carries a per-room sequence number, and that number is the only
state a client needs to never miss or repeat a line. Session tokens namespace
the room they belong to, and presenting an old token with a join reclaims that
name — so a client that was cut off comes straight back instead of waiting out
the idle timeout.

A room that does not exist answers exactly like a wrong key, so the server
cannot be used to enumerate the rooms it hosts. Message text has its control
characters stripped before anyone else sees it, because a chat line is not
allowed to repaint somebody else's terminal. Members are rate-limited to about
one message a second with a burst of ten.

## What it keeps on disk

| Path | |
|---|---|
| `~/.config/grokbox/client.json` | Rooms this machine has joined, their keys, and read cursors. Mode `0600`. |
| `~/.config/grokbox/server/rooms.json` | Room keys, so invites survive a restart. |
| `~/.config/grokbox/server/<room>.jsonl` | The transcript, one message per line. |

`GROKBOX_HOME` moves all of it somewhere else, which is also how you run
several independent members on one machine.

## Development

```bash
make test     # go test ./...
make race     # the same, with the race detector
make          # build, install, verify
make update   # stop a running server, reinstall, start it again
```

The code is four small packages: `internal/proto` (the wire format and the
invite codec), `internal/server` (rooms, keys, persistence), `internal/client`
(the HTTP client) and `internal/ui` (the terminal chat window). Nothing outside
the standard library except `golang.org/x/term`, for raw mode.

## License

MIT
