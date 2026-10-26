# go-quai patches

quai-node builds go-quai from source (`scripts/build-payload.sh`) and applies
each `*.patch` here, in order, for a bug its users hit that go-quai hasn't
fixed yet. A patch that doesn't apply to a new go-quai stops the build, so
it gets looked at again: updated, or deleted once go-quai has the fix.
`quai-node version` lists the patches its go-quai was built with.

This file is for go-quai's developers ([dominant-strategies/go-quai][repo]).
Each section below can be pasted into an upstream issue as it stands: what
users see, why, how to reproduce it, and the fix. Line references are to
go-quai [v0.56.1][v0.56.1], unpatched.

| Issue | Patch | In quai-node since | Reported upstream |
|---|---|---|---|
| [Chain databases share one process's file handles](#chain-databases-share-one-processs-file-handles) | `0001-share-file-handles-between-chain-databases.patch` | v0.1.6 | not yet |
| [An unreadable Pebble database is taken for LevelDB](#an-unreadable-pebble-database-is-taken-for-leveldb) | none | | not yet |
| [`--node.validate-indexer` and `--node.reindex` only open LevelDB](#--nodevalidate-indexer-and---nodereindex-only-open-leveldb) | none | | not yet |

Update the last column with the issue's link when one is filed, and delete a
section (and its patch) once go-quai ships the fix.

## Chain databases share one process's file handles

**What users see.** On macOS, a node with a mainnet chain of a few hundred
GB stops starting: the zone never comes up (nothing answers on its RPC
port), and the logs show `too many open files`. It worked while the chain
was smaller. Raising the limit in launchd doesn't help. A user reported it
with a 285 GB chain on Pebble.

**Why.** One go-quai process opens a chain database each for prime, the
region and the zone, and gives every one of them half of the process's file
handles:

- `MakeDatabaseHandles` returns half the process's allowance
  ([`cmd/utils/flags.go:1158`][handles], [`:1167`][half]).
- `SetQuaiConfig` stores that number in the config each chain gets
  ([`cmd/utils/flags.go:1540`][setcfg]), and each chain's backend opens its
  database with all of it ([`quai/backend.go:114`][backend]).

So three databases are promised 150% of the handles. A database only uses
its full share once it's big enough: Pebble writes 2 MiB tables, so a 285 GB
chain has about 140,000 files. Then the last database to open runs out.

macOS gets there first. `fdlimit.Maximum` on darwin caps the process at
`OPEN_MAX` (10,240), whatever the hard limit allows
([`common/fdlimit/fdlimit_darwin.go:60`][max], [`:66`][cap]). Each database
is promised 5,120 handles, and the three need 15,360. Linux hits the same
error later: under a 65,536 limit each database is promised 32,768, which
is 98,304 in total.

The next issue makes the failure harder to read: out of handles, the zone's
Pebble database is taken for LevelDB.

**To reproduce.** On macOS, start go-quai on a mainnet data directory of
roughly 250 GB or more (the quikshot.dev snapshot, with
`--node.db-engine pebble`). Watch the open files grow
(`lsof -p <pid> | wc -l`) to about 10,240, after which the zone fails to
open its database.

**Fix (the patch).**

- `MakeDatabaseHandles(dbs int)` still leaves half the handles for
  networking, and splits the other half between the `dbs` databases.
- `SetQuaiConfig` passes the number of chain databases in the process:
  prime, one per region, and one per zone, from
  `common.GetHierarchySizeForExpansionNumber`.
- `MakeChainDatabase`, which opens a single database, passes 1.
- On darwin, `Maximum` caps at `kern.maxfilesperproc`, as Go's runtime does.
  It falls back to 10,240 only if that can't be read. `Raise` already
  re-reads the limit macOS actually applied, so it needs no change.

**Status.** In quai-node since v0.1.6, built for every platform. The
reporter hasn't yet confirmed on their Mac that the node now starts.

## An unreadable Pebble database is taken for LevelDB

**What users see.** A Pebble chain that runs out of file handles at startup
fails as if it were LevelDB, so the error points at the wrong thing.

**Why.** `hasPreexistingDb` decides Pebble if `filepath.Glob(".../OPTIONS*")`
matches anything ([`core/rawdb/database.go:241`][glob]). `filepath.Glob`
returns an error only for a malformed pattern; it ignores I/O errors, such
as `EMFILE` when listing the directory. With no handles left, there's no
match, and the database is reported as LevelDB.

**To reproduce.** Start a Pebble node with a file limit lower than its
databases need (see the previous issue), or call `hasPreexistingDb` on a
Pebble directory with `RLIMIT_NOFILE` exhausted.

**Suggested fix.** List the directory with `os.ReadDir` and return its
error, so a database that can't be read fails as unreadable instead of
being misdetected.

## `--node.validate-indexer` and `--node.reindex` only open LevelDB

**What users see.** On a Pebble chain (`--node.db-engine pebble`, or any
node restored from a Pebble snapshot), both flags stop go-quai at startup
with `Error opening the zone db for reindexing`. The underlying error is
`db.engine choice was leveldb but found pre-existing pebble database`. So a
Pebble node can neither check its Qi UTXO address index nor rebuild it.

**Why.** `ReIndexChainIndexer` and `ValidateChainIndexer` open the zone
database with `Type: "leveldb"`
([`cmd/utils/hierarchical_coordinator.go:1427`][reindex],
[`:1453`][validate]). `openKeyValueDatabase` refuses a type that differs
from the database on disk
([`core/rawdb/database.go:270`][mismatch]).

**To reproduce.** `go-quai start --node.db-engine pebble
--node.validate-indexer` on a Pebble data directory.

**Suggested fix.** Open with `Type: ""`, so the engine on disk is used, or
pass the configured `--node.db-engine`. A build with `Type: ""` validated
the Qi UTXO index of the quikshot.dev Pebble snapshot of October 24, 2026
(zone height 10,782,844): all 856,463 UTXOs were found in the index.

[repo]: https://github.com/dominant-strategies/go-quai
[v0.56.1]: https://github.com/dominant-strategies/go-quai/tree/v0.56.1
[handles]: https://github.com/dominant-strategies/go-quai/blob/v0.56.1/cmd/utils/flags.go#L1158
[half]: https://github.com/dominant-strategies/go-quai/blob/v0.56.1/cmd/utils/flags.go#L1167
[setcfg]: https://github.com/dominant-strategies/go-quai/blob/v0.56.1/cmd/utils/flags.go#L1540
[backend]: https://github.com/dominant-strategies/go-quai/blob/v0.56.1/quai/backend.go#L114
[max]: https://github.com/dominant-strategies/go-quai/blob/v0.56.1/common/fdlimit/fdlimit_darwin.go#L60
[cap]: https://github.com/dominant-strategies/go-quai/blob/v0.56.1/common/fdlimit/fdlimit_darwin.go#L66
[glob]: https://github.com/dominant-strategies/go-quai/blob/v0.56.1/core/rawdb/database.go#L241
[reindex]: https://github.com/dominant-strategies/go-quai/blob/v0.56.1/cmd/utils/hierarchical_coordinator.go#L1427
[validate]: https://github.com/dominant-strategies/go-quai/blob/v0.56.1/cmd/utils/hierarchical_coordinator.go#L1453
[mismatch]: https://github.com/dominant-strategies/go-quai/blob/v0.56.1/core/rawdb/database.go#L270
