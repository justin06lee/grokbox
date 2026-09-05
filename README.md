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

It is meant to be run where everyone can reach it — a VM, a box with a public
address — so the connection is HTTPS from the start, with a certificate the
server signs itself and the invite pins. That takes no domain, no certificate
authority and no renewal.

## Install

```bash
go install github.com/justin06lee/grokbox@latest
```

Or from a clone — `make` builds it, installs it, and checks it runs from
anywhere:

```bash
make
```

For people who would rather not install Go, `make dist` cross-compiles binaries
for macOS, Linux and Windows into `./dist`; `gh release create v0.2.0 dist/*`
puts them where others can download them.

## Host a room

```console
$ grokbox serve

grokbox v0.2.0 — listening on :7777
reachable at https://203.0.113.9:7777   (public — anyone with the invite can reach it)

  room    lounge
  key     nsfq-z29d-wkb4-dz3x
  invite  grokbox1-eyJzIjoiaHR0cHM6Ly8yMDMuMC4xMTMuOTo3Nzc3IiwiciI6ImxvdW5nZSIs…

  hand out the invite, then everyone runs:

      grokbox join grokbox1-eyJzIjoiaHR0cHM6Ly8yMDMuMC4xMTMu… --name <their-name>

  tls     self-signed, pinned by the invite (xqCkVZng6zxPfflLSNs7hdazyVr0CgZ5lG49EXcryzY)
  store   ~/.config/grokbox/server
```

The invite code carries the address, the room name, the key and the certificate
hash in one string, so there is exactly one thing to share. All of it is kept
under `~/.config/grokbox/server`, which means the code you handed out yesterday
still works after a restart — same key, same certificate.

It also says how far the address it printed actually goes. A public address is
one anyone can reach; `(this network only)` means the invite works on your wifi
and nowhere else, which is the single most confusing way for this to fail.

Lost the code? `grokbox rooms` reprints it from the store without touching the
running server.

Useful flags:

| Flag | What it does |
|---|---|
| `--addr :7777` | Address to listen on. |
| `--room lounge` | Room to host. Repeat it for several rooms; `--room lounge=my-key` sets the key yourself. |
| `--key KEY` | The key for the first room, instead of a generated one. |
| `--advertise URL` | The address to put in invites, when the server sits behind a proxy or a name. |
| `--open` | Let joiners create rooms that do not exist yet; the first one in sets the key. |
| `--store ""` | Keep everything in memory, so keys and history vanish on exit. |
| `--history 200` | Messages retained and replayed per room. |
| `--idle 90s` | How long a member can go unheard from before the room drops them. |
| `--quiet` | Print nothing but errors — no banner, no join log. |
| `--tls-cert / --tls-key` | Use a real certificate instead of a self-signed one. Invites then pin nothing, because it verifies on its own. |
| `--tls=false` | Serve plain HTTP. Only when something in front is already terminating TLS. |

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

## Running it where everyone can reach it

Put it on a machine with a public address — a VM, a cheap box, anything — and
there is nothing to configure:

```bash
grokbox serve
```

It works out its own public address (directly from the interface on most
providers, and from the instance metadata service on AWS, GCP, Azure and
DigitalOcean, where the public address is mapped in front of a private one),
signs a certificate, and prints an invite that anyone can use from anywhere.

To keep it running after you close the ssh session, and after a reboot:

```bash
make service                    # installs a systemd unit, Linux
sudo grokbox rooms --store /var/lib/grokbox/server   # the invites, any time later
journalctl -u grokbox -f        # its log
```

`sudo` on that second one because the service runs under a systemd
`DynamicUser`, which owns its state directory.

Settings for the service live in `/etc/grokbox.env` — `GROKBOX_ADDR`,
`GROKBOX_ADVERTISE`, `GROKBOX_ROOM`.

### If you have a domain

Point it at the box and either bring your own certificate, or let whatever is
already terminating TLS keep doing it:

```bash
grokbox serve --advertise https://chat.example.com \
              --tls-cert /etc/letsencrypt/live/chat.example.com/fullchain.pem \
              --tls-key  /etc/letsencrypt/live/chat.example.com/privkey.pem

# or behind a reverse proxy that already speaks HTTPS
grokbox serve --addr 127.0.0.1:7777 --tls=false --advertise https://chat.example.com
```

With a real certificate the invite carries no fingerprint, because the
certificate proves itself.

### Why the certificate is in the invite

A room on a bare IP has no domain, so no certificate authority will vouch for
it — and the usual answer, plain HTTP, would put the room key in the clear on
every join. So the server signs its own certificate and the invite carries the
hash of it.

That is not the weaker version of HTTPS, it is a stronger one. The client knows
exactly which certificate to expect *before it connects*, because the
fingerprint came with the invite you were handed. There is no first contact to
be impersonated on and no authority that can be persuaded to issue a second
certificate for the same name. A client whose invite has a fingerprint will
refuse anything else, and a client whose invite has none will not accept a
self-signed certificate at all.

The certificate lives in the store beside the room keys, so restarts do not
invalidate the invites you have handed out.

### Still just on your wifi

That works too and needs none of the above — `serve` says `(this network
only)`, and everyone on the same network can join. It is only when somebody
leaves the building that they need an address that goes further.

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
bmo add justin06lee/grokbox/skills/grokbox grok       # or claude, codex, cursor, …
bmo add justin06lee/grokbox/skills/grokbox everyone   # every harness on the machine
```

Or copy `skills/grokbox/` into whatever directory your agent reads skills from.

## Commands

```
grokbox join    [invite] --name NAME        open the interactive chat
grokbox send    [invite] --name NAME TEXT   say one thing and exit
grokbox read    [invite] --name NAME        print what has been said since last time
grokbox tail    [invite] --name NAME        stream messages as they arrive
grokbox members [invite] --name NAME        list who is in the room
grokbox invite  [invite] [--decode]         show or decode an invite code
grokbox health  [invite]                    check that a server is up
grokbox leave   [invite]                    end this machine's session

grokbox serve   [flags]                     host rooms and print their invites
grokbox rooms   [flags]                     reprint a server's invites, without starting it
grokbox version
```

Every command in the first group takes the invite code as its first argument; leave it out
and the last room this machine joined is used. `--server`, `--room` and `--key`
spell out the same thing the long way — but an invite rebuilt from parts has no
certificate hash in it, so prefer the code itself. `--no-save` keeps a room out
of the config file entirely.

Everything can come from the environment instead:

| | |
|---|---|
| `GROKBOX_INVITE`, `GROKBOX_NAME` | Which room, and who you are in it. |
| `GROKBOX_SERVER`, `GROKBOX_ROOM`, `GROKBOX_KEY` | The long way round. |
| `GROKBOX_ADDR`, `GROKBOX_ADVERTISE` | For `serve`. |
| `GROKBOX_HOME` | Where grokbox keeps everything, client and server alike. |

## How it works

The server speaks HTTPS, with a JSON body on every route.

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

Nothing streams over WebSocket, deliberately: server-sent events and a long
poll are both ordinary HTTP requests, so a room works through any proxy that
can forward one.

A room that does not exist answers exactly like a wrong key, so the server
cannot be used to enumerate the rooms it hosts, and keys are compared in
constant time. Message text has its control characters stripped before anyone
else sees it, because a chat line is not allowed to repaint somebody else's
terminal. Members are rate-limited to about one message a second with a burst
of ten, a room holds at most 64 of them, and a session that goes unheard from
for 90 seconds is dropped.

## What it keeps on disk

| Path | |
|---|---|
| `~/.config/grokbox/client.json` | Rooms this machine has joined, their keys, and read cursors. Mode `0600`. |
| `~/.config/grokbox/server/rooms.json` | Room keys, so invites survive a restart. |
| `~/.config/grokbox/server/cert.pem`, `key.pem` | The self-signed certificate the invites pin. Mode `0600`. |
| `~/.config/grokbox/server/server.json` | The advertised address, so `grokbox rooms` can rebuild invites. |
| `~/.config/grokbox/server/<room>.jsonl` | The transcript, one message per line. |

`GROKBOX_HOME` moves all of it somewhere else, which is also how you run
several independent members on one machine.

## Development

```bash
make            # build, install to $PATH, verify it runs from anywhere
make build      # just the binary, into ./bin
make install    # just the install step
make update     # stop a running server, reinstall, start it again
make service    # run it under systemd, surviving reboots (Linux)
make unservice  # remove that unit again
make test       # go test ./...
make race       # the same, with the race detector
make fmt vet    # gofmt -w, go vet
make dist       # cross-compiled binaries into ./dist
make clean      # remove bin and dist
```

The code is four small packages: `internal/proto` (the wire format, the invite
codec and the fingerprint), `internal/server` (rooms, keys, certificates,
persistence), `internal/client` (the HTTP client and the certificate pinning)
and `internal/ui` (the terminal chat window). Nothing outside the standard
library except `golang.org/x/term`, for raw mode.

## License

MIT
