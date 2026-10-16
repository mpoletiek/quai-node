# quai-node

One download that sets up and runs a [Quai Network](https://qu.ai) node.

`quai-node` carries a prebuilt [go-quai](https://github.com/dominant-strategies/go-quai)
and the [quai-dash](https://github.com/mpoletiek/quai-node-dashboard) web dashboard
for its platform. On first run it asks a few questions, unpacks them,
writes their configs and starts them, as one service if you want: it starts
at boot (or at login on macOS), and one start or stop covers the node and
the dashboard together.

```
$ ./quai-node
  quai-node setup ── network ── node or mine ── chain data ── dashboard ── service
…
Done.
The node is starting. It syncs from the network first, which takes a while.
Its progress:         quai-node status
Dashboard:            http://127.0.0.1:8090
```

## Install

Linux and macOS, in a terminal (as your own user, not root):

```sh
curl -fsSL https://raw.githubusercontent.com/mpoletiek/quai-node/main/install.sh | sh
```

Windows, in PowerShell opened as administrator (so the node can run as a
service):

```powershell
irm https://raw.githubusercontent.com/mpoletiek/quai-node/main/install.ps1 | iex
```

This downloads the quai-node build for your computer from the newest
release, checks it against that release's `SHA256SUMS`, and runs
`quai-node setup`. Setup asks its questions, then puts everything in one
folder (`~/.local/share/quai-node`, `~/Library/Application Support/quai-node`
or `%ProgramData%\quai-node`) and `quai-node` on your PATH. Run the same
line again to update: setup keeps your earlier answers, and
`| sh -s -- --apply` (on Windows, `$env:QUAI_NODE_ARGS='--apply'` first)
updates without asking. `QUAI_NODE_VERSION=v0.1.0` installs a given
release. To read the script before running it, download
[`install.sh`](install.sh) or [`install.ps1`](install.ps1) and run that.

To remove it:

```sh
curl -fsSL https://raw.githubusercontent.com/mpoletiek/quai-node/main/uninstall.sh | sh
```

```powershell
irm https://raw.githubusercontent.com/mpoletiek/quai-node/main/uninstall.ps1 | iex
```

This stops and removes the service and the programs. The chain data, the
node's key, logs and settings stay, so installing again picks up where the
node left off; `| sh -s -- --purge` (`$env:QUAI_NODE_ARGS='--purge'`)
deletes those too.

## Platforms

| | go-quai | quai-dash | services |
|---|---|---|---|
| Linux x86_64, arm64 | ✓ (static) | ✓ | systemd or OpenRC (a system service, run as you; sudo once) |
| macOS arm64, x86_64 | ✓ | ✓ (no peer map: it reads Linux `/proc`) | launchd LaunchAgent (at login, no admin) |
| Windows x86_64 | ✓ | not yet (quai-dash doesn't build for Windows) | Windows service (elevated terminal) |

## Commands

| | |
|---|---|
| `quai-node` | setup the first time, `status` after |
| `quai-node setup` | ask again and re-apply; `--yes` takes flags instead of asking, `--apply` re-applies an edited `quai-node.toml`, `--dry-run` shows the files and commands |
| `quai-node status` | running or not, height against the network's, peers |
| `quai-node run` | run in the foreground until Ctrl-C (no service) |
| `quai-node service install\|uninstall\|start\|stop\|restart\|status` | the quai-node service |
| `quai-node snapshot [restore]` | quikshot.dev's latest snapshot; `restore` replaces the chain data with it |
| `quai-node keepers status\|claim` | this node on the Keepers at quainode.org |
| `quai-node logs` | the end of the zone log |
| `quai-node uninstall [--purge]` | remove services and binaries; `--purge` also the chain data and node key |

Setup draws its questions as forms. Where the terminal reports no size
(some serial consoles and SSH setups) it asks line by line instead;
`QUAI_NODE_PLAIN=1` forces that.

Unattended setup, e.g. for provisioning:

```sh
quai-node setup --yes --network colosseum --data-dir /srv/quai \
  --mine --quai-address 0x00… --qi-address 0x0080… \
  --geoip online --location auto
```

The dashboard listens on this machine only unless you let other machines
open it (`--dashboard-public`: every interface, and it has no login). The
terminal version is `quai-dash tui`.

Setup puts the `quai-node` and `quai-dash` commands on your PATH (and
points quai-dash's default config at this node, unless you have your own):
a link in `~/.local/bin`
(Linux, macOS; setup says so if that directory isn't on your PATH), or the
home's `bin` folder added to your user PATH (Windows). A `--home` install
leaves the command alone. `uninstall` takes it off again.

### Chain data, snapshots and disk space

Setup reads [quikshot.dev](https://quikshot.dev)'s latest snapshot
manifest: its unpacked size (about 303 GB in October 2026) is the best
measure of the chain's size today, so setup warns, and `--yes` refuses
without `--ignore-space`, when the data directory hasn't room for it.

On mainnet cyprus1 it also offers to start from that snapshot instead of
syncing from the network (`--snapshot`, or `quai-node snapshot restore`
later). quikshot.dev is an independent community service, not affiliated
with the Quai Network team. The download (about 224 GB, zstd) runs over
16 connections at once, since one gets about 60 MB/s from quikshot.dev and
together they fill a gigabit line (about 35 minutes instead of an hour),
and streams, in order,
through its published SHA-256 into a staging directory beside the chain
data, so it needs the unpacked size free, not both; nothing is replaced
until the whole download has checked out, and a dropped connection resumes
where it stopped. The snapshot is Pebble databases, so the node switches to
`--node.db-engine pebble` once it is in place.

When the data directory already holds a chain, setup offers to keep it
(the default), to replace it with the snapshot, or to delete it and sync
from scratch (`--from-scratch`; the wizard asks again before deleting),
and shows its size, database engine and, while the node runs, its height.
Deleting removes the chain databases (`prime`, `region-*`, `zone-*`) only,
after stopping the node. A chain that stays
keeps the engine it was made with: setup reads it from the files, so
pointing setup at an existing LevelDB or Pebble chain just works. The
snapshot is never saved as an archive; the only thing a restore leaves
behind is `.snapshot-partial` in the data directory if it was killed
mid-download, which the next restore, or setup, removes.

### The P2P port

go-quai talks to peers on one port, TCP and UDP (4002 by default), and
asks the router to forward it over UPnP. Two Quai nodes behind one router
can't share that port: go-quai dials out from it too, and a router that
keeps source ports makes both nodes the same address to a peer, so it drops
one node's connections and that node finds no peers. Setup reads the
router's UPnP port mappings, and if another machine already uses the port
(or another program on this one does) it says which and recommends a free
port; `--yes` takes that port unless `--p2p-port` chose one. Otherwise the
port stays. A node elsewhere on the network that doesn't use UPnP doesn't
show up in the router's mappings, so setup can't see it.

### Keepers on quainode.org

On mainnet cyprus1, setup offers to report the node to the Keepers,
[quainode.org/nodes](https://quainode.org/nodes), under a tag
(`--keepers-tag`; `--no-keepers` stops it): go-quai's quaistats client logs
in to `keepers.quainode.org` with the tag and a generated secret (kept as
`keepers_tag` and `keepers_secret` in `quai-node.toml`, which is
owner-only) and sends stats every 15 s. The Keepers verify the node through
its RPC, so the RPC then listens on every interface and you open TCP 9200
to the internet: the node becomes a public RPC (net, web3 and quai APIs
only). `quai-node keepers status` shows its checks; `quai-node keepers
claim` signs the tag with the node's p2p key once it has logged in. The
node's page is `https://quainode.org/nodes/<tag>`, and
[quainode.org/nodes/status](https://quainode.org/nodes/status) shows the
service's own state. The Keepers take secrets of 16 to 128 characters (the
generated one has 26) and refuse some reserved tags: if `quai-node keepers
status` never sees the node log in although TCP 9200 is open, try another
tag. `claim` prints the Keepers' own answer.

The Keepers used to be on quaination.xyz. Installs that reported there
move over by themselves on the next `quai-node setup` or update: the tag
and secret (`quaination_tag` and `quaination_secret` in `quai-node.toml`)
are reused, saved under the new names, and go-quai reports to
`keepers.quainode.org` from then on. The tag is reserved on quainode.org
by the node's first login there; the roll on quaination.xyz is a separate
service and isn't carried over. The old `quai-node quaination` command and
`--quaination-tag` flag still work.

### The dashboard's map

The node doesn't know where its peers are, so quai-dash looks their IP
addresses up. Setup asks how:

- **a local database**, offered when a MaxMind GeoLite2 City database is
  in `/usr/share/GeoIP`, `/var/lib/GeoIP` or `~/.local/share/GeoIP`:
  nothing leaves the machine;
- **online**: ip-api.com receives the peers' IP addresses (`--geoip online`);
- **not at all** (`--geoip off`).

It also places this node: detected from the machine's public IP with one
lookup at ipinfo.io during setup (`--location auto`; behind a VPN that's
the VPN's exit), typed in (`--location LAT,LON`), or left off. The
position is rounded to a tenth of a degree, about 10 km.

## What goes where

Home: `~/.local/share/quai-node` (Linux), `~/Library/Application Support/quai-node`
(macOS), `%ProgramData%\quai-node` (Windows); `--home` or `QUAI_NODE_HOME`
for another.

```
quai-node.toml        what setup chose; the configs below are written from it
bin/                  go-quai, quai-dash, quai-node
VERSION, params/      go-quai reads these from its working directory
config/config.toml    go-quai's config (and its node key, private.key)
config/extra.toml     go-quai keys of your own, merged into config.toml
dash/config.toml      quai-dash's config
data/                 chain data (setup asks where)
nodelogs/             go-quai's logs
```

go-quai always runs with the home as its working directory: it reads
`VERSION` and `params/*.json` relative to it and writes `nodelogs/` there.

### Your own go-quai keys

`config/config.toml` is rewritten from `quai-node.toml` whenever setup runs:
updates, `setup --apply`, `snapshot restore` and `service install` or
`uninstall`. Edits made to it directly don't last. Put keys of your own
in `config/extra.toml` instead (setup leaves a commented one there):
quai-node merges it into `config.toml` on every rewrite. Use go-quai's own
tables and key names; `go-quai config --global.config-dir DIR`, run from a
folder holding a copy of the home's `VERSION`, prints them all with their
defaults.

```toml
[metrics]
enabled = true

[node]
max-peers = '100'
```

Then `quai-node setup --apply` to rewrite the config and restart the node.
Setup checks the file first and refuses a table go-quai doesn't have, or a
key quai-node writes itself (the network, zone, ports, data directory,
mining and the RPC). Change those in `quai-node.toml`.

## The service

One service, `quai-node`, runs `quai-node run --service`, which supervises
go-quai and quai-dash:

- go-quai exiting stops the service, and the service manager starts it
  again (systemd `Restart=on-failure`, OpenRC `supervise-daemon`, launchd
  `KeepAlive`, Windows restart-on-failure).
- quai-dash exiting restarts quai-dash alone, after 10 s, doubling to 5
  minutes while it keeps failing. The node runs on.
- A stop interrupts quai-dash, then go-quai, which gets 300 s to close
  its databases.

| | service file | its output |
|---|---|---|
| systemd | `/etc/systemd/system/quai-node.service` | `journalctl -u quai-node` |
| OpenRC | `/etc/init.d/quai-node` | `/var/log/quai-node/quai-node.log` (OpenRC opens it as root) |
| macOS | `~/Library/LaunchAgents/io.github.mpoletiek.quai-node.quai-node.plist` | `<home>/logs/quai-node.log` |
| Windows | service `quai-node` | `<home>\logs\quai-node.log` |

That output is small: quai-node's own lines, quai-dash's, and go-quai's
stderr, where a crash would show. go-quai's log proper is in
`<home>/nodelogs/` (one file per chain, rotated by go-quai); go-quai also
copies it to stdout, which the service drops. Setup lists these places.

## Building

A release binary is quai-node with a payload for one OS/arch embedded:

```sh
make release GOOS=linux GOARCH=amd64     # go-quai via zig cc; any host
make release GOOS=windows GOARCH=amd64   # likewise
make release GOOS=darwin GOARCH=arm64    # on a Mac
```

go-quai needs cgo (`ltcd/secp256k1_ltc` is C), so Linux and Windows builds
use `zig cc` as the cross compiler, and macOS builds run on a Mac for the
Apple SDK. `scripts/build-payload.sh` documents its knobs (`GO_QUAI_REF`,
`DASH_VERSION`, …). `.github/workflows/release.yml` builds all five
platforms on a `v*` tag.

## License

quai-node is MIT ([LICENSE](LICENSE)). Its release binaries bundle
go-quai, which is GPL-3.0 (its source is the tagged release named by
`quai-node version`), and quai-dash, which is MIT.
